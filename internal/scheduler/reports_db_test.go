package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pingoo/internal/agent"
	"pingoo/internal/demo"
	"pingoo/internal/llm"
	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/secret"
	"pingoo/internal/signals"
	"pingoo/internal/store"
	"pingoo/internal/syncer"
	"pingoo/internal/testdb"
)

// 自动报告的整条链路：给谁排队、AI 用文字回答时自动整理、报告里不混演示数据、到了用量上限就停。
func TestAutoReports(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	// 假 AI：正常请求一律用文字回答（格式不对），只有“整理格式”的请求才返回 JSON
	var repairs atomic.Int32
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		content := "这一期整体平稳，没有需要特别处理的事情。"
		if len(req.Messages) > 1 && strings.Contains(req.Messages[0].Content, "整理成指定的 JSON 格式") {
			repairs.Add(1)
			content = `{"title":"本期平稳","lede":"没有大变化。","stories":[],"reviews":[],"next":[]}`
			if strings.Contains(req.Messages[1].Content, `"headline"`) {
				content = `{"headline":"整体平稳","lede":"没有大变化。","items":[],"todos":[]}`
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5}})
	}))
	defer ai.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.New(pool, secret.New("0123456789abcdef0123456789abcdef"))
	// 用户打开了自动生成、有积分；平台有一个指向假 AI 的默认模型
	uid := testdb.User(t, pool, "owner@pingoo.test")
	if _, err := pool.Exec(ctx, `UPDATE users SET auto_ai=true WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	ctx = store.WithUser(ctx, uid)
	if _, err := st.SaveModel(ctx, store.AIModel{Name: "fake", Model: "fake", BaseURL: ai.URL, APIKey: "k", PriceIn: 1, PriceOut: 1, Multiplier: 1, Enabled: true, IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddCredits(ctx, uid, 100000, "admin", "测试"); err != nil {
		t.Fatal(err)
	}
	sy := syncer.New(st, log)
	m := metrics.New(st, sy, log)
	ag := agent.New(st, m, sy, log)
	det := &signals.Detector{M: m}
	if err := demo.Seed(ctx, st, det); err != nil {
		t.Fatal(err)
	}
	// 一个早就添加、已同步的真实网站 + 一个刚添加的真实网站
	oldID, _ := st.CreateSite(ctx, store.Site{Name: "老网站", GAProperty: "properties/1", Timezone: "Asia/Shanghai"})
	newID, _ := st.CreateSite(ctx, store.Site{Name: "新网站", GAProperty: "properties/2", Timezone: "Asia/Shanghai"})
	if _, err := pool.Exec(ctx, `UPDATE sites SET created_at = now() - interval '75 days' WHERE id=$1`, oldID); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().AddDate(0, 0, -1)
	_ = st.SaveSyncState(ctx, store.SyncState{SiteID: oldID, Source: "ga", LastSynced: &yesterday, BackfilledFrom: &yesterday, Status: "ok"})
	old, _ := st.Site(ctx, oldID)

	s := &Scheduler{Store: st, Sync: sy, Detector: det, Agent: ag, Log: log}
	drain := func() []string {
		s.reportQ = make(chan reportJob, 64)
		s.reportsQ.Range(func(k, _ any) bool { s.reportsQ.Delete(k); return true })
		s.reports(ctx)
		var out []string
		for len(s.reportQ) > 0 {
			j := <-s.reportQ
			who := "全站"
			if j.siteID != nil {
				who = fmt.Sprintf("site%d", *j.siteID)
			}
			out = append(out, fmt.Sprintf("%s/%s/%s", j.kind, who, period.D(j.start)))
		}
		sort.Strings(out)
		return out
	}
	now := time.Now().In(old.Location())
	ws, we := agent.WeeklyPeriod(now)
	ms, _ := agent.MonthlyPeriod(now)
	if !reportReady([]store.Site{old}, map[int64]map[string]store.SyncState{oldID: {"ga": {LastSynced: &yesterday}}}, we, now) {
		t.Skip("现在正好卡在周一凌晨、上周的报告还没到生成时间，跳过")
	}
	want := []string{
		fmt.Sprintf("monthly/site%d/%s", oldID, period.D(ms)), "monthly/全站/" + period.D(ms),
		fmt.Sprintf("weekly/site%d/%s", oldID, period.D(ws)), "weekly/全站/" + period.D(ws),
	}
	if got := drain(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("应该只给早就添加的真实网站补上一期（新网站 %d 和演示网站都不排）:\n got  %v\n want %v", newID, got, want)
	}

	// 生成全站周报：AI 先用文字回答，整理一次后成功；报告里只有真实网站
	rid, err := ag.GenerateReport(ctx, nil, "weekly", ws, we, "standard")
	rp, _ := st.Report(ctx, rid)
	var snap agent.ReportSnapshot
	_ = json.Unmarshal(rp.Snapshot, &snap)
	if err != nil || rp.Status != "done" || repairs.Load() != 1 {
		t.Fatalf("格式不对时应该整理一次后成功: status=%s err=%q 整理次数=%d (%v)", rp.Status, rp.Error, repairs.Load(), err)
	}
	if len(snap.Rows) != 2 || snap.Rows[0].Name != "老网站" || snap.Rows[1].Name != "新网站" {
		t.Errorf("全站报告不应该包含演示网站: %+v", snap.Rows)
	}
	if got := drain(); len(got) != 3 || strings.Contains(strings.Join(got, " "), "weekly/全站") {
		t.Errorf("全站周报已经有了，不应该再排: %v", got)
	}

	// 到了每日用量上限：不再排任何报告，手动生成也会被拦住
	_ = st.SetSetting(ctx, store.KeyAIDailyCap, "10")
	if got := drain(); len(got) != 0 {
		t.Errorf("到了每日用量上限不应该再排报告: %v", got)
	}
	if _, err := ag.GenerateReport(ctx, nil, "monthly", ms, ms.AddDate(0, 1, -1), "standard"); err != agent.ErrBudget {
		t.Errorf("到上限后应该返回 ErrBudget，得到 %v", err)
	}
	if have, failed, _, _ := st.ReportAttempts(ctx, nil, "monthly", ms); have != 0 || failed != 0 {
		t.Errorf("被上限拦住的不应该留下报告记录: have=%d failed=%d", have, failed)
	}
	_ = st.SetSetting(ctx, store.KeyAIDailyCap, "")

	// 没有自己的网站时（演示网站在共享的演示账号下）不自动生成，但手动生成仍然可用
	if _, err := pool.Exec(ctx, `DELETE FROM sites WHERE NOT demo`); err != nil {
		t.Fatal(err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM reports`)
	if got := drain(); len(got) != 0 {
		t.Errorf("只有演示网站时不应该自动生成报告: %v", got)
	}
	rid, _ = ag.GenerateReport(ctx, nil, "weekly", ws, we, "standard")
	if rp, _ := st.Report(ctx, rid); rp.Status != "done" {
		t.Errorf("没有网站时手动生成应该可用: %s %s", rp.Status, rp.Error)
	}
}
