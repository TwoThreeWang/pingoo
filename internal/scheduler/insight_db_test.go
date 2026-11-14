package scheduler

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"pingoo/internal/agent"
	"pingoo/internal/secret"
	"pingoo/internal/store"
	"pingoo/internal/testdb"
)

// 同步完要不要自动写洞察：最近没来过的用户不写；连续失败 3 次后不再自动重试，手动生成成功一次就恢复。
func TestInsightDue(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	st := store.New(pool, secret.New("0123456789abcdef0123456789abcdef"))
	s := &Scheduler{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	uid := testdb.User(t, pool, "owner@pingoo.test")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE users SET auto_ai=true, credits=100 WHERE id=$1`, uid)
	uctx := store.WithUser(ctx, uid)
	id, err := st.CreateSite(uctx, store.Site{Name: "A", GAProperty: "properties/1", Timezone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	site, _ := st.SiteAny(ctx, id)

	if s.insightDue(ctx, site) {
		t.Error("从没打开过 Pingoo（没有登录会话）的用户不应该提前写洞察")
	}
	exec(`INSERT INTO sessions(id,user_id,expires_at,last_seen_at) VALUES('s1',$1,now()+interval '1 day',now()-interval '5 days')`, uid)
	if s.insightDue(ctx, site) {
		t.Error("5 天没打开过的用户不应该提前写洞察")
	}
	exec(`UPDATE sessions SET last_seen_at=now()-interval '1 day' WHERE id='s1'`)
	if !s.insightDue(ctx, site) {
		t.Fatal("最近来过、开了自动生成、有积分：应该写洞察")
	}

	fail := func(ago string) {
		exec(`INSERT INTO insights(user_id,site_id,range_key,period_start,period_end,status,error,created_at)
			VALUES($1,$2,'d7',current_date-7,current_date-1,'error','接口出错',now()-$3::interval)`, uid, id, ago)
	}
	fail("3 hours")
	fail("2 hours")
	if n := st.InsightFailStreak(uctx, &id, "d7"); n != 2 || !s.insightDue(ctx, site) {
		t.Errorf("失败 2 次还会自动重试: streak=%d", n)
	}
	fail("1 hour")
	if n := st.InsightFailStreak(uctx, &id, "d7"); n != agent.InsightMaxFails || s.insightDue(ctx, site) {
		t.Errorf("连续失败 3 次后不再自动重试: streak=%d", n)
	}
	// 别的时间范围、别的网站的失败不算在这个网站头上
	if n := st.InsightFailStreak(uctx, &id, "d28"); n != 0 {
		t.Errorf("别的时间范围不受影响: %d", n)
	}
	if n := st.InsightFailStreak(uctx, nil, "d7"); n != 0 {
		t.Errorf("全部网站的简报单独计数: %d", n)
	}

	// 站长手动生成成功了一次（20 小时内不用再写）；过了有效期之后恢复自动生成，之前的失败不再算
	exec(`INSERT INTO insights(user_id,site_id,range_key,period_start,period_end,status,content,created_at)
		VALUES($1,$2,'d7',current_date-7,current_date-1,'done','{}',now())`, uid, id)
	if n := st.InsightFailStreak(uctx, &id, "d7"); n != 0 || s.insightDue(ctx, site) {
		t.Errorf("成功之后重新计数，而且 20 小时内不重复写: streak=%d", n)
	}
	exec(`UPDATE insights SET created_at=now()-interval '21 hours' WHERE status='done'`)
	exec(`UPDATE insights SET created_at=created_at-interval '21 hours' WHERE status='error'`)
	if !s.insightDue(ctx, site) {
		t.Error("成功过一次之后，洞察过期了应该恢复自动生成")
	}
}
