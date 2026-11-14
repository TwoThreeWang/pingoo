package syncer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pingoo/internal/google"
	"pingoo/internal/secret"
	"pingoo/internal/store"
	"pingoo/internal/testdb"
)

// fakeGA 模拟 GA Data API 的 runReport：请求里的日期范围内每天返回一行，指标全是 2。
func fakeGA(t *testing.T, calls *atomic.Int32) *google.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasSuffix(r.URL.Path, ":runReport") {
			http.NotFound(w, r)
			return
		}
		var req struct {
			DateRanges []struct{ StartDate, EndDate string } `json:"dateRanges"`
			Dimensions []struct{ Name string }               `json:"dimensions"`
			Metrics    []struct{ Name string }               `json:"metrics"`
			Offset     int                                   `json:"offset"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		type val struct {
			Value string `json:"value"`
		}
		type row struct {
			DimensionValues []val `json:"dimensionValues"`
			MetricValues    []val `json:"metricValues"`
		}
		var rows []row
		from, _ := time.Parse("2006-01-02", req.DateRanges[0].StartDate)
		to, _ := time.Parse("2006-01-02", req.DateRanges[0].EndDate)
		for d := from; !d.After(to) && req.Offset == 0; d = d.AddDate(0, 0, 1) {
			x := row{}
			for _, dim := range req.Dimensions {
				v := "x"
				switch dim.Name {
				case "date":
					v = d.Format("20060102")
				case "hour":
					v = "3"
				case "newVsReturning":
					v = "returning"
				case "pagePath", "landingPage":
					v = "/a"
				}
				x.DimensionValues = append(x.DimensionValues, val{v})
			}
			for range req.Metrics {
				x.MetricValues = append(x.MetricValues, val{"2"})
			}
			rows = append(rows, x)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": rows, "rowCount": len(rows)})
	}))
	t.Cleanup(srv.Close)
	return google.NewWithHTTP(srv.Client(), google.Endpoints{Data: srv.URL, Admin: srv.URL, GSC: srv.URL})
}

// 首次同步按套餐回填：免费版明细 30 天、每日总数 90 天；升级到专业版后，下一次同步自动往前补到 180 / 395 天。
func TestSyncGABackfillsByPlan(t *testing.T) {
	pool := testdb.Open(t)
	uid := testdb.User(t, pool, "owner@pingoo.test")
	ctx := store.WithUser(context.Background(), uid)
	st := store.New(pool, secret.New("0123456789abcdef0123456789abcdef"))
	s := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	id, err := st.CreateSite(ctx, store.Site{Name: "A", GAProperty: "properties/1", Timezone: "America/Los_Angeles"})
	if err != nil {
		t.Fatal(err)
	}
	site, _ := st.Site(ctx, id)
	var calls atomic.Int32
	gc := fakeGA(t, &calls)
	count := func(q string) (n int) {
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return
	}
	check := func(detail, total int) {
		t.Helper()
		if n := count(`SELECT count(*) FROM ga_daily`); n != total {
			t.Errorf("每日总数应该有 %d 天，实际 %d", total, n)
		}
		if n := count(`SELECT (max(date) - min(date)) + 1 FROM ga_daily`); n != total {
			t.Errorf("每日总数应该连续覆盖 %d 天，实际跨度 %d", total, n)
		}
		if n := count(`SELECT count(DISTINCT date) FROM ga_daily_pages`); n != detail {
			t.Errorf("页面明细应该有 %d 天，实际 %d", detail, n)
		}
		if n := count(`SELECT count(*) FROM ga_daily WHERE returning_users = 2 AND active_users = 2`); n != total {
			t.Errorf("每日总数的各项指标应该都写进去了，只有 %d 行是对的", n)
		}
	}
	cfg := st.Plans(ctx)
	free, pro := cfg.Get(store.PlanFree), cfg.Get(store.PlanPro)
	if err := s.syncGA(ctx, gc, site); err != nil {
		t.Fatal(err)
	}
	check(free.DetailDays, free.TotalDays)
	yesterday := yesterdayIn(site.Location())
	hist, _ := st.SyncState(ctx, id, "ga_hist")
	ga, _ := st.SyncState(ctx, id, "ga")
	if hist.LastSynced == nil || ga.LastSynced == nil || ga.Status != "ok" || ga.LastSynced.Format("2006-01-02") != yesterday.Format("2006-01-02") {
		t.Fatalf("同步状态不对: ga=%+v hist=%+v", ga, hist)
	}

	// 当天再同步一次：先比对昨天的每日总数，GA 那边没变就不重拉，只花 1 次查询
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	day := func(n int) string { return yesterday.AddDate(0, 0, -n).Format("2006-01-02") } // n 天前（0 是昨天）
	before := calls.Load()
	if err := s.syncGA(ctx, gc, site); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load() - before; n != 1 {
		t.Errorf("昨天的数据没变时应该只有 1 次比对查询，实际 %d 次", n)
	}
	if n := count(`SELECT count(*) FROM sync_runs WHERE source='ga' AND status='ok' AND rows=0 AND message LIKE '%没有变化%'`); n != 1 {
		t.Errorf("跳过的刷新应该在同步记录里留一行说明: %d", n)
	}

	// 昨天的数在 GA 那边变了：只重拉昨天这一天，前天的不动
	exec(`UPDATE ga_daily SET sessions = 99 WHERE date = ANY($1::date[])`, []string{day(0), day(1)})
	before = calls.Load()
	if err := s.syncGA(ctx, gc, site); err != nil {
		t.Fatal(err)
	}
	// 1 次比对 + 一段日期 11 次查询：每日汇总 2 次 + 广告合计 + 页面 + 页面广告（这段日期有广告展示才查）+ 6 张明细表
	if n := calls.Load() - before; n != 12 {
		t.Errorf("昨天有变化时应该是 1 次比对 + 11 次查询，实际 %d 次", n)
	}
	if a, b := count(`SELECT sessions FROM ga_daily WHERE date = '`+day(0)+`'`), count(`SELECT sessions FROM ga_daily WHERE date = '`+day(1)+`'`); a != 2 || b != 99 {
		t.Errorf("当天后面的刷新只应该重写昨天: 昨天 %d（应为 2），前天 %d（应为 99）", a, b)
	}

	// 第二天的第一次同步（库里只同步到前天）：照旧重拉最近 3 天，把 GA 修正过的数补齐
	exec(`UPDATE sync_state SET last_synced_date = $1::date WHERE source = 'ga'`, day(1))
	exec(`UPDATE ga_daily SET sessions = 99 WHERE date = ANY($1::date[])`, []string{day(1), day(2), day(3)})
	before = calls.Load()
	if err := s.syncGA(ctx, gc, site); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load() - before; n != 11 {
		t.Errorf("每天第一次同步不比对，直接重拉（11 次查询），实际 %d 次", n)
	}
	if n := count(`SELECT count(*) FROM ga_daily WHERE sessions = 99`); n != 1 {
		t.Errorf("每天第一次同步应该重写最近 3 天（只剩 3 天前那一行没动），还有 %d 行没重写", n)
	}
	exec(`UPDATE ga_daily SET sessions = 2 WHERE sessions = 99`)
	if n := count(`SELECT count(*) FROM ga_daily WHERE ad_revenue = 2 AND ad_impressions = 2`); n != free.TotalDays {
		t.Errorf("每日总数里应该有广告收入: %d", n)
	}
	if n := count(`SELECT count(DISTINCT date) FROM ga_daily_ads`); n != free.DetailDays {
		t.Errorf("页面广告收入是明细，应该有 %d 天: %d", free.DetailDays, n)
	}
	if s2, _ := st.Site(ctx, id); s2.Currency != "" {
		t.Logf("货币: %s", s2.Currency)
	}
	check(free.DetailDays, free.TotalDays)

	// 升级到专业版：明细和每日总数都往前补到专业版的保留期
	if err := st.SetPlan(context.Background(), uid, store.PlanPro, 30); err != nil {
		t.Fatal(err)
	}
	if err := s.syncGA(ctx, gc, site); err != nil {
		t.Fatal(err)
	}
	check(pro.DetailDays, pro.TotalDays)
	if ga, _ := st.SyncState(ctx, id, "ga"); ga.BackfilledFrom.Format("2006-01-02") != yesterday.AddDate(0, 0, -(pro.DetailDays-1)).Format("2006-01-02") {
		t.Errorf("明细的起点应该挪到专业版保留期的开头: %v", ga.BackfilledFrom)
	}
}

// 还没有广告数据的网站，同步时记下 GA4 有没有关联 AdSense；已经有广告数据的不再查；查询失败不覆盖原来的状态。
func TestCheckAdSense(t *testing.T) {
	pool := testdb.Open(t)
	uid := testdb.User(t, pool, "owner@pingoo.test")
	ctx := store.WithUser(context.Background(), uid)
	st := store.New(pool, secret.New("0123456789abcdef0123456789abcdef"))
	s := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	id, err := st.CreateSite(ctx, store.Site{Name: "A", GAProperty: "properties/1", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	site, _ := st.Site(ctx, id)

	var reply atomic.Value
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/properties/1/adSenseLinks" {
			http.NotFound(w, r)
			return
		}
		body := reply.Load().(string)
		if body == "fail" {
			http.Error(w, `{"error":{"message":"denied"}}`, http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	gc := google.NewWithHTTP(srv.Client(), google.Endpoints{AdminAlpha: srv.URL})

	reply.Store(`{}`)
	s.checkAdSense(ctx, gc, site)
	if got := st.AdSenseStatus(ctx, id); got != store.AdSenseNone {
		t.Fatalf("没关联时应记为 none，实际 %q", got)
	}
	reply.Store(`{"adsenseLinks":[{"adClientCode":"ca-pub-1"}]}`)
	s.checkAdSense(ctx, gc, site)
	if got := st.AdSenseStatus(ctx, id); got != store.AdSenseLinked {
		t.Fatalf("关联后应记为 linked，实际 %q", got)
	}
	reply.Store("fail")
	s.checkAdSense(ctx, gc, site)
	if got := st.AdSenseStatus(ctx, id); got != store.AdSenseLinked {
		t.Fatalf("查询失败不能覆盖原来的状态，实际 %q", got)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ga_daily(site_id,date,ad_impressions) VALUES($1,current_date,5)`, id); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	s.checkAdSense(ctx, gc, site)
	if calls.Load() != before {
		t.Error("已经有广告数据的网站不用再查关联")
	}
	// 没配 v1alpha 地址的客户端（测试里的假 GA）：直接跳过，不发请求
	s.checkAdSense(ctx, google.NewWithHTTP(srv.Client(), google.Endpoints{}), store.Site{ID: 999, GAProperty: "properties/1"})
}

// fakeGSC 模拟 Search Console 的 searchAnalytics：请求的日期范围内每天返回一行（点击 2、曝光 4），并记下每次请求的范围和维度。
func fakeGSC(t *testing.T, reqs *[]string) *google.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			StartDate, EndDate string
			Dimensions         []string
			StartRow           int
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		*reqs = append(*reqs, req.StartDate+".."+req.EndDate+" "+strings.Join(req.Dimensions, ","))
		type row struct {
			Keys        []string `json:"keys"`
			Clicks      float64  `json:"clicks"`
			Impressions float64  `json:"impressions"`
			Position    float64  `json:"position"`
		}
		var rows []row
		from, _ := time.Parse("2006-01-02", req.StartDate)
		to, _ := time.Parse("2006-01-02", req.EndDate)
		for d := from; !d.After(to) && req.StartRow == 0; d = d.AddDate(0, 0, 1) {
			x := row{Clicks: 2, Impressions: 4, Position: 3}
			for _, dim := range req.Dimensions {
				x.Keys = append(x.Keys, map[string]string{"date": d.Format("2006-01-02"), "page": "https://a.example/a", "query": "q"}[dim])
			}
			rows = append(rows, x)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": rows})
	}))
	t.Cleanup(srv.Close)
	return google.NewWithHTTP(srv.Client(), google.Endpoints{Data: srv.URL, Admin: srv.URL, GSC: srv.URL})
}

// Search Console 当天后面的刷新：比对最近 5 天的每日总数，都没变就不重拉；有变化就从第一个有变化的日子开始重拉。
func TestSyncGSCRefreshOnlyChangedDays(t *testing.T) {
	pool := testdb.Open(t)
	uid := testdb.User(t, pool, "owner@pingoo.test")
	ctx := store.WithUser(context.Background(), uid)
	st := store.New(pool, secret.New("0123456789abcdef0123456789abcdef"))
	s := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	id, err := st.CreateSite(ctx, store.Site{Name: "A", GSCSite: "https://a.example/", Timezone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	site, _ := st.Site(ctx, id)
	var reqs []string
	gc := fakeGSC(t, &reqs)
	if err := s.syncGSC(ctx, gc, site); err != nil {
		t.Fatal(err)
	}
	yesterday := yesterdayIn(site.Location())
	day := func(n int) string { return yesterday.AddDate(0, 0, -n).Format("2006-01-02") }
	clicks := func(n int) (c int) {
		if err := pool.QueryRow(ctx, `SELECT clicks FROM gsc_daily WHERE date=$1::date`, day(n)).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return
	}

	// 当天再同步：最近 5 天没有变化，只花一次按日期汇总的查询
	reqs = nil
	if err := s.syncGSC(ctx, gc, site); err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0] != day(4)+".."+day(0)+" date" {
		t.Fatalf("没变化时应该只比对一次最近 5 天的每日总数: %v", reqs)
	}

	// 两天前的数变了（库里的比 Search Console 现在给的少）：只从那天开始重拉，更早的不动
	if _, err := pool.Exec(ctx, `UPDATE gsc_daily SET clicks = 1 WHERE date = $1::date`, day(2)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE gsc_daily_pages SET clicks = 77 WHERE date = $1::date`, day(3)); err != nil {
		t.Fatal(err)
	}
	reqs = nil
	if err := s.syncGSC(ctx, gc, site); err != nil {
		t.Fatal(err)
	}
	want := day(2) + ".." + day(0)
	if len(reqs) != 4 || reqs[1] != want+" date" || reqs[2] != want+" date,page" || reqs[3] != want+" date,query,page" {
		t.Fatalf("应该只重拉有变化的那天到昨天: %v", reqs)
	}
	var kept int
	_ = pool.QueryRow(ctx, `SELECT clicks FROM gsc_daily_pages WHERE date = $1::date`, day(3)).Scan(&kept)
	if clicks(2) != 2 || kept != 77 {
		t.Errorf("有变化的那天要重写（%d，应为 2），更早的明细不动（%d，应为 77）", clicks(2), kept)
	}
}
