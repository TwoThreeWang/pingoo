package store

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"pingoo/internal/secret"
	"pingoo/internal/testdb"
)

// newStore 打开一个全新的测试库，并建好 1 号用户。
func newStore(t *testing.T) *Store {
	pool := testdb.Open(t)
	testdb.User(t, pool, "owner@pingoo.test")
	return New(pool, secret.New("0123456789abcdef0123456789abcdef"))
}

// userCtx 是 newStore 建好的 1 号用户的 context。
func userCtx() context.Context {
	return WithUser(context.Background(), 1)
}

func count(t *testing.T, s *Store, q string, args ...any) (n int) {
	t.Helper()
	if err := s.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return
}

func exec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if _, err := s.Pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestSessionSlidingRenewal(t *testing.T) {
	s := newStore(t)
	ctx := userCtx()
	ttl := 30 * 24 * time.Hour
	if err := s.CreateSession(ctx, 1, "a", "ua", "1.1.1.1", ttl); err != nil {
		t.Fatal(err)
	}
	// 刚登录：有效，但不用续期
	if uid, renewed, err := s.TouchSession(ctx, "a"); err != nil || uid != 1 || renewed != 0 {
		t.Fatalf("刚登录不应该续期: uid=%v renewed=%v err=%v", uid, renewed, err)
	}
	// 用掉了一半有效期：续到“现在 + 30 天”，并告诉调用方重新下发 cookie
	exec(t, s, `UPDATE sessions SET expires_at = now() + interval '15 days' WHERE id='a'`)
	if uid, renewed, err := s.TouchSession(ctx, "a"); err != nil || uid != 1 || renewed != ttl {
		t.Fatalf("应该续期 30 天: uid=%v renewed=%v err=%v", uid, renewed, err)
	}
	if n := count(t, s, `SELECT count(*) FROM sessions WHERE id='a' AND expires_at > now() + interval '29 days'`); n != 1 {
		t.Fatal("过期时间没有延后")
	}
	// 紧接着再来一次：不重复续
	if _, renewed, _ := s.TouchSession(ctx, "a"); renewed != 0 {
		t.Fatalf("刚续过不应该再续: %v", renewed)
	}
	// 已经过期的会话：无效，也不会被“续活”
	exec(t, s, `UPDATE sessions SET expires_at = now() - interval '1 minute' WHERE id='a'`)
	if uid, _, _ := s.TouchSession(ctx, "a"); uid != 0 {
		t.Fatal("过期的会话应该无效")
	}
}

func TestSiteRoundTripAndReset(t *testing.T) {
	s := newStore(t)
	ctx := userCtx()
	id, err := s.CreateSite(ctx, Site{Name: "A", GAProperty: "properties/1", GSCSite: "sc-domain:a.com", Timezone: "UTC", SignalLevel: "high"})
	if err != nil {
		t.Fatal(err)
	}
	site, err := s.Site(ctx, id)
	if err != nil || site.SignalLevel != "high" || time.Since(site.CreatedAt) > time.Minute {
		t.Fatalf("site=%+v err=%v", site, err)
	}
	site.SignalLevel = "low"
	if err := s.UpdateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Site(ctx, id); got.SignalLevel != "low" {
		t.Fatalf("灵敏度没存上: %q", got.SignalLevel)
	}

	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	seed := func() {
		exec(t, s, `INSERT INTO ga_daily(site_id,date,active_users,new_users,returning_users,sessions,engaged_sessions,page_views,engagement_seconds) VALUES($1,$2,1,1,0,1,1,1,1)`, id, day)
		exec(t, s, `INSERT INTO ga_daily_pages(site_id,date,path,title,page_views,users,engagement_seconds) VALUES($1,$2,'/a','T',1,1,1)`, id, day)
		exec(t, s, `INSERT INTO gsc_daily(site_id,date,clicks,impressions,pos_x_impr) VALUES($1,$2,1,1,1)`, id, day)
		for _, src := range []string{"ga", "ga_hist", "gsc"} {
			if err := s.SaveSyncState(ctx, SyncState{SiteID: id, Source: src, LastSynced: &day, BackfilledFrom: &day, Status: "ok"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.UpsertSignal(ctx, Signal{SiteID: id, Kind: "traffic_drop", Severity: "warn", Title: "t", EventDate: day}, "k"); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	if err := s.ResetSiteData(ctx, id, true, false); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT count(*) FROM ga_daily`)+count(t, s, `SELECT count(*) FROM ga_daily_pages`)+count(t, s, `SELECT count(*) FROM signals`) != 0 {
		t.Error("换 GA 后旧的 GA 数据和信号应该清掉")
	}
	if count(t, s, `SELECT count(*) FROM gsc_daily`) != 1 || count(t, s, `SELECT count(*) FROM sync_state WHERE source='gsc'`) != 1 || count(t, s, `SELECT count(*) FROM sync_state`) != 1 {
		t.Error("只换 GA 时 Search Console 的数据和同步状态应该保留，GA（含历史回填进度）的同步状态应该清掉")
	}
}

func TestReportAttemptsCleanupPrune(t *testing.T) {
	s := newStore(t)
	ctx := userCtx()
	start := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	if have, failed, _, err := s.ReportAttempts(ctx, nil, "weekly", start); err != nil || have != 0 || failed != 0 {
		t.Fatalf("have=%d failed=%d err=%v", have, failed, err)
	}
	id, _ := s.CreateReport(ctx, nil, "weekly", start, start.AddDate(0, 0, 6))
	_ = s.FinishReport(ctx, id, nil, nil, "", "m", 0, 0, "boom")
	if have, failed, last, _ := s.ReportAttempts(ctx, nil, "weekly", start); have != 0 || failed != 1 || time.Since(last) > time.Minute {
		t.Fatalf("失败一次: have=%d failed=%d", have, failed)
	}
	_, _ = s.CreateReport(ctx, nil, "weekly", start, start.AddDate(0, 0, 6))
	if have, failed, _, _ := s.ReportAttempts(ctx, nil, "weekly", start); have != 1 || failed != 1 {
		t.Fatalf("生成中的也算已有: have=%d failed=%d", have, failed)
	}

	// 过期的会话和缓存
	_ = s.CreateSession(ctx, 1, "live", "ua", "1.1.1.1", time.Hour)
	_ = s.CreateSession(ctx, 1, "dead", "ua", "1.1.1.1", -time.Hour)
	s.CacheSet(ctx, "totals:7:d7", 1, time.Hour)
	s.CacheSet(ctx, "totals:70:d7", 1, time.Hour)
	s.CacheSet(ctx, "stale", 1, -time.Hour)
	if n := s.Cleanup(ctx); n != 2 {
		t.Errorf("应该清掉 1 个过期会话和 1 条过期缓存，实际 %d", n)
	}
	s.CacheDeletePrefix(ctx, "totals:7:")
	var v int
	if s.CacheGet(ctx, "totals:7:d7", &v) || !s.CacheGet(ctx, "totals:70:d7", &v) {
		t.Error("按前缀删缓存不应该误删别的网站（7 和 70）")
	}

	// 历史记录的清理
	siteID, _ := s.CreateSite(ctx, Site{Name: "A", Timezone: "UTC"})
	exec(t, s, `INSERT INTO sync_runs(site_id,source,started_at) VALUES($1,'ga',now()-interval '100 days'),($1,'ga',now()-interval '10 days')`, siteID)
	exec(t, s, `INSERT INTO ledger(user_id,kind,model,prompt_tokens,completion_tokens,created_at) VALUES(1,'chat','m',1,1,now()-interval '500 days'),(1,'chat','m',300,200,now()),
		(1,'redeem','',0,0,now()-interval '500 days')`)
	exec(t, s, `INSERT INTO insights(user_id,site_id,range_key,period_start,period_end,status,created_at) VALUES
		(1,$1,'d28',current_date,current_date,'done',now()-interval '60 days'),
		(1,$1,'d28',current_date,current_date,'done',now()-interval '40 days'),
		(1,$1,'d28',current_date,current_date,'done',now()-interval '1 day'),
		(1,$1,'d7',current_date,current_date,'done',now()-interval '90 days')`, siteID)
	// 同步来的数据按主人的套餐保留（多留 2 天余量）：免费版明细 30 天、每日总数 90 天；提醒按所有套餐里最长的明细
	for _, days := range []int{1, 32, 33, 92, 93} {
		exec(t, s, `INSERT INTO ga_daily_pages(site_id,date,path,title,page_views,users,engagement_seconds) VALUES($1,current_date - $2::int,'/p','T',1,1,1)`, siteID, days)
		exec(t, s, `INSERT INTO gsc_daily_queries(site_id,date,query,page,path) VALUES($1,current_date - $2::int,'q','https://a.com/p','/p')`, siteID, days)
		exec(t, s, `INSERT INTO ga_daily(site_id,date,active_users,new_users,returning_users,sessions,engaged_sessions,page_views,engagement_seconds) VALUES($1,current_date - $2::int,1,1,0,1,1,1,1)`, siteID, days)
		exec(t, s, `INSERT INTO signals(site_id,kind,severity,title,event_date,dedupe_key) VALUES($1,'k','info','t',current_date - $2::int,$3)`, siteID, days, fmt.Sprint("k", days))
	}
	exec(t, s, `INSERT INTO sync_state(site_id,source,backfilled_from) VALUES($1,'ga',current_date - 100),($1,'ga_hist',current_date - 300)`, siteID)
	// 1 条同步记录 + 1 条 AI 用量（充值记录保留）+ 2 条被更新的洞察取代的旧洞察 + 明细 2×3 + 每日总数 1
	if n := s.Prune(ctx); n != 4+6+1 {
		t.Errorf("应该清掉 11 行，实际 %d", n)
	}
	if count(t, s, `SELECT count(*) FROM insights`) != 2 || count(t, s, `SELECT count(*) FROM insights WHERE range_key='d7'`) != 1 {
		t.Error("每个时间范围最新的一份洞察要留着，哪怕它很旧")
	}
	if count(t, s, `SELECT count(*) FROM ga_daily_pages`) != 2 || count(t, s, `SELECT count(*) FROM gsc_daily_queries`) != 2 ||
		count(t, s, `SELECT count(*) FROM ga_daily`) != 4 || count(t, s, `SELECT count(*) FROM signals`) != 5 {
		t.Error("免费版：明细留 32 天、每日总数留 92 天；提醒按最长的套餐留")
	}
	if count(t, s, `SELECT count(*) FROM sync_state WHERE source='ga' AND backfilled_from = current_date - 32`) != 1 ||
		count(t, s, `SELECT count(*) FROM sync_state WHERE source='ga_hist' AND backfilled_from = current_date - 92`) != 1 {
		t.Error("删完要把“最早有哪天的数据”挪到保留期的起点，升级时同步才知道要往前补")
	}
	if n := s.Prune(ctx); n != 0 {
		t.Errorf("再清一次不应该还有可删的: %d", n)
	}
	// 专业版刚到期 3 天（宽限期内）：数据仍按专业版保留；到期 8 天后按免费版删
	exec(t, s, `UPDATE users SET plan='pro', plan_expires_at=now() - interval '3 days' WHERE id=1`)
	exec(t, s, `INSERT INTO ga_daily_pages(site_id,date,path,title,page_views,users,engagement_seconds) VALUES($1,current_date - 100,'/p','T',1,1,1)`, siteID)
	if s.Prune(ctx); count(t, s, `SELECT count(*) FROM ga_daily_pages`) != 3 {
		t.Error("宽限期内不应该按免费版删")
	}
	exec(t, s, `UPDATE users SET plan_expires_at=now() - interval '8 days' WHERE id=1`)
	if s.Prune(ctx); count(t, s, `SELECT count(*) FROM ga_daily_pages`) != 2 {
		t.Error("宽限期过了应该按免费版删")
	}
	if got := s.TokensSince(ctx, time.Now().Add(-time.Hour)); got != 500 {
		t.Errorf("TokensSince = %d", got)
	}
}

func TestMutedSignals(t *testing.T) {
	s := newStore(t)
	ctx := userCtx()
	id, _ := s.CreateSite(ctx, Site{Name: "A", Timezone: "UTC"})
	today := time.Now()
	_ = s.UpsertSignal(ctx, Signal{SiteID: id, Kind: "near_top", Severity: "chance", Title: "a", EventDate: today}, "1")
	_ = s.UpsertSignal(ctx, Signal{SiteID: id, Kind: "traffic_drop", Severity: "warn", Title: "b", EventDate: today}, "2")
	if sigs, err := s.OpenSignals(ctx, 0, 14); err != nil || len(sigs) != 2 {
		t.Fatalf("sigs=%d err=%v", len(sigs), err)
	}
	_ = s.SetSetting(ctx, KeySignalsMuted, "near_top, low_ctr")
	sigs, err := s.OpenSignals(ctx, id, 14)
	if err != nil || len(sigs) != 1 || sigs[0].Kind != "traffic_drop" {
		t.Fatalf("关掉的类型不应该返回: %+v err=%v", sigs, err)
	}
	if s.DailyTokenCap(ctx) != 0 {
		t.Error("没设置时每日上限应该是 0（不限）")
	}
}

// 套餐卡密：开通送当月积分；同档续期只延长；有效期内升级按价格折算剩余天数；每 30 天发一次积分，不重复发。
func TestPlanCodesAndMonthlyCredits(t *testing.T) {
	s := newStore(t)
	ctx := userCtx()
	cfg := DefaultPlans()
	b, p := cfg.Plans[PlanBasic], cfg.Plans[PlanPro]
	b.Price, p.Price = 10, 30
	cfg.Plans[PlanBasic], cfg.Plans[PlanPro] = b, p
	if err := s.SavePlans(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	redeem := func(plan string, days int) RedeemResult {
		t.Helper()
		codes, err := s.CreateCodes(ctx, 1, 0, plan, days, "t", nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Redeem(ctx, 1, codes[0])
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	daysLeft := func(e time.Time) float64 { return time.Until(e).Hours() / 24 }
	if s.PlanOf(ctx, 1).ID != PlanFree {
		t.Fatal("新用户是免费版")
	}
	res := redeem(PlanBasic, 30)
	if res.Plan != PlanBasic || res.Granted != 500 || math.Abs(daysLeft(res.Expires)-30) > 0.01 || count(t, s, `SELECT credits FROM users WHERE id=1`) != 500 {
		t.Fatalf("开通基础版：%+v", res)
	}
	res = redeem(PlanBasic, 30)
	if res.Granted != 0 || math.Abs(daysLeft(res.Expires)-60) > 0.01 {
		t.Fatalf("续期只延长、不再送积分：%+v", res)
	}
	// 基础版剩 60 天（价值 60×10），加 30 天专业版（30×30）→ 按专业版价格折成 (600+900)/30 = 50 天
	res = redeem(PlanPro, 30)
	if res.Plan != PlanPro || res.Granted != 2000 || math.Abs(daysLeft(res.Expires)-50) > 0.01 {
		t.Fatalf("升级专业版：%+v（剩 %.2f 天）", res, daysLeft(res.Expires))
	}
	// 专业版有效期内用基础版卡密：30 天基础版折成 10 天专业版
	res = redeem(PlanBasic, 30)
	if res.Plan != PlanPro || math.Abs(daysLeft(res.Expires)-60) > 0.01 {
		t.Fatalf("低档卡密折算进高档：%+v", res)
	}
	if s.PlanOf(ctx, 1).Sites != 20 {
		t.Error("专业版 20 个网站")
	}
	// 每月积分：还没满 30 天不发；满了发一次，同时调两次也只发一次
	if n := s.GrantMonthly(ctx); n != 0 {
		t.Errorf("刚发过，不应该再发: %d", n)
	}
	exec(t, s, `UPDATE users SET plan_credits_at = now() - interval '31 days' WHERE id=1`)
	before := count(t, s, `SELECT credits FROM users WHERE id=1`)
	if n := s.GrantMonthly(ctx) + s.GrantMonthly(ctx); n != 1 || count(t, s, `SELECT credits FROM users WHERE id=1`) != before+2000 {
		t.Errorf("满 30 天应该只发一次 2000 积分: n=%d", n)
	}
	// 到期：功能回到免费版，数据保留按原套餐再算 7 天
	exec(t, s, `UPDATE users SET plan_expires_at = now() - interval '1 day' WHERE id=1`)
	u, _ := s.User(ctx, 1)
	if u.PlanID(time.Now()) != PlanFree || u.RetentionPlanID(time.Now()) != PlanPro || s.GrantMonthly(ctx) != 0 {
		t.Error("到期后功能按免费版、数据宽限期内按专业版，也不再发积分")
	}
	// 超出网站数的按列表顺序暂停
	free := s.PlanOf(ctx, 1)
	a, _ := s.CreateSite(ctx, Site{Name: "A", Timezone: "UTC"})
	b2, _ := s.CreateSite(ctx, Site{Name: "B", Timezone: "UTC"})
	sa, _ := s.Site(ctx, a)
	sb, _ := s.Site(ctx, b2)
	if free.Paused(sa) || !free.Paused(sb) {
		t.Errorf("免费版只保留排在前面的 1 个网站: rank a=%d b=%d", sa.Rank, sb.Rank)
	}
	if err := s.SavePlans(ctx, PlanConfig{Currency: "$", Plans: map[string]Plan{PlanFree: {Name: "免费版", Sites: 1, DetailDays: 30, TotalDays: 20}}}); err == nil {
		t.Error("缺档或者每日总数比明细短的设置应该拒绝")
	}
}

// 半夜自动整理只碰被撑大的表：删掉九成的行之后表文件不会变小，Compact(ctx, 0) 应该挑中它，而且不丢数据。
func TestCompactOnlyBloated(t *testing.T) {
	s := newStore(t)
	ctx := userCtx()
	siteID, _ := s.CreateSite(ctx, Site{Name: "A", Timezone: "UTC"})
	exec(t, s, `INSERT INTO ga_daily_pages(site_id,date,path,title,page_views,users,engagement_seconds)
		SELECT $1, current_date - (i % 20), '/p/' || i, repeat('标题', 20), 1, 1, 1 FROM generate_series(1, 20000) i`, siteID)
	// 每 1000 行留 1 行（包括最后一行）：空位夹在中间，普通 VACUUM 只能标记、不能把文件截短，只有 VACUUM FULL 收得回来
	exec(t, s, `DELETE FROM ga_daily_pages WHERE substr(path, 4)::int % 1000 <> 0`)
	exec(t, s, `VACUUM ANALYZE ga_daily_pages`)
	// 只检查挑表和数据：同一个库里别的测试包在并行跑，它们打开的事务会让 VACUUM 暂时回收不了刚删的行，大小不稳定
	got := s.Compact(ctx, 0)
	if len(got) != 1 || got[0].Table != "ga_daily_pages" {
		t.Fatalf("应该只整理被撑大的 ga_daily_pages: %+v", got)
	}
	if count(t, s, `SELECT count(*) FROM ga_daily_pages`) != 20 {
		t.Error("整理不能丢数据")
	}
}

func TestLedgerPage(t *testing.T) {
	s := newStore(t)
	ctx := userCtx()
	const total = 125
	for i := 0; i < total; i++ {
		exec(t, s, `INSERT INTO ledger(user_id,kind,model,credits) VALUES(1,'chat','m',$1)`, i)
	}
	// 另一个用户的流水不能混进来
	testdb.User(t, s.Pool, "other@pingoo.test")
	exec(t, s, `INSERT INTO ledger(user_id,kind,credits) VALUES(2,'chat',999)`)

	var seen []int64
	before := int64(0)
	pages := 0
	for {
		rows, more, err := s.LedgerPage(ctx, before, 50)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for i, r := range rows {
			if i > 0 && r.ID >= rows[i-1].ID {
				t.Fatalf("同一页必须按 id 倒序：%d 在 %d 之后", r.ID, rows[i-1].ID)
			}
			if r.Credits == 999 {
				t.Fatal("串到了别的用户的流水")
			}
			seen = append(seen, r.ID)
		}
		if !more {
			if len(rows) != total%50 {
				t.Errorf("最后一页应有 %d 条，实际 %d", total%50, len(rows))
			}
			break
		}
		if len(rows) != 50 {
			t.Errorf("非最后一页应有 50 条，实际 %d", len(rows))
		}
		before = rows[len(rows)-1].ID
	}
	if pages != 3 {
		t.Errorf("125 条按 50 分页应为 3 页，实际 %d 页", pages)
	}
	if len(seen) != total {
		t.Errorf("翻完应覆盖全部 %d 条，实际 %d", total, len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] >= seen[i-1] {
			t.Fatal("跨页也必须严格倒序、不重叠")
		}
	}
}
