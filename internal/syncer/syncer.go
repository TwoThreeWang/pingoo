// Package syncer 把 GA 和 Search Console 的数据按天同步到本地数据库。
//
// 规则：
//   - 保留多久看网站主人的套餐：明细（页面、来源、搜索词…）DetailDays 天，每日总数 TotalDays 天。
//   - 新网站先回填到套餐的保留期；以后升级套餐、保留期变长时，下一次同步会自动往前补。
//   - 之后每天同步“昨天”，并重新拉取 GA 最近 3 天、GSC 最近 5 天（这些数据还会被 Google 修正）。
//   - 每段日期“先删后插”，保证重复同步结果一致。
package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"pingoo/internal/google"
	"pingoo/internal/period"
	"pingoo/internal/store"
)

const (
	gaResyncDays  = 3
	gscResyncDays = 5
)

type Syncer struct {
	Store *store.Store
	Log   *slog.Logger

	mu      sync.Mutex
	running map[int64]bool
}

func New(st *store.Store, log *slog.Logger) *Syncer {
	return &Syncer{Store: st, Log: log, running: map[int64]bool{}}
}

var ErrNoGoogle = errors.New("还没有配置 Google 服务账号")

// Client 读取服务账号配置并返回 Google 客户端。
func (s *Syncer) Client(ctx context.Context) (*google.Client, error) {
	sa, err := s.Store.Setting(ctx, store.KeyGoogleSA)
	if err != nil {
		return nil, err
	}
	if sa == "" {
		return nil, ErrNoGoogle
	}
	return google.Cached(ctx, sa)
}

// Running 报告某个网站是否正在同步。
func (s *Syncer) Running(siteID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[siteID]
}

// SyncSite 同步一个网站的 GA 和 GSC 数据。同一个网站同一时间只会有一个同步在跑。
func (s *Syncer) SyncSite(ctx context.Context, site store.Site) error {
	if site.Demo {
		return nil
	}
	s.mu.Lock()
	if s.running[site.ID] {
		s.mu.Unlock()
		return nil
	}
	s.running[site.ID] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.running, site.ID); s.mu.Unlock() }()

	gc, err := s.Client(ctx)
	if err != nil {
		return err
	}
	var errs []string
	if site.GAProperty != "" {
		if err := s.syncGA(ctx, gc, site); err != nil {
			errs = append(errs, "GA: "+err.Error())
		}
	}
	if site.GSCSite != "" {
		if err := s.syncGSC(ctx, gc, site); err != nil {
			errs = append(errs, "Search Console: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "；"))
	}
	return nil
}

func yesterdayIn(loc *time.Location) time.Time {
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
}

type chunk struct{ from, to time.Time }

func chunks(from, to time.Time, size int) []chunk {
	var out []chunk
	for a := from; !a.After(to); a = a.AddDate(0, 0, size) {
		b := a.AddDate(0, 0, size-1)
		if b.After(to) {
			b = to
		}
		out = append(out, chunk{a, b})
	}
	return out
}

// plan 决定这次要同步的日期范围。
func plan(st store.SyncState, yesterday time.Time, backfill, resync int) (from time.Time, backfilling bool) {
	if st.LastSynced == nil || st.BackfilledFrom == nil {
		return yesterday.AddDate(0, 0, -(backfill - 1)), true
	}
	last := time.Date(st.LastSynced.Year(), st.LastSynced.Month(), st.LastSynced.Day(), 0, 0, 0, 0, yesterday.Location())
	from = last.AddDate(0, 0, 1)
	if r := yesterday.AddDate(0, 0, -(resync - 1)); r.Before(from) {
		from = r
	}
	return from, false
}

// window 是一个网站该有数据的日期范围（按网站主人现在的套餐）：明细从 detail 开始，每日总数从 total 开始。
type window struct{ detail, total, yesterday time.Time }

func (s *Syncer) window(ctx context.Context, site store.Site) window {
	p := s.Store.PlanOf(ctx, site.UserID)
	y := yesterdayIn(site.Location())
	return window{detail: y.AddDate(0, 0, -(p.DetailDays - 1)), total: y.AddDate(0, 0, -(p.TotalDays - 1)), yesterday: y}
}

// localDay 把从 date 列读出来的日期（UTC 零点）换成网站当地那一天的零点。
func localDay(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// split 把一段日期按 w.detail 分成两段：之前的只要每日总数，之后的明细和总数都要。不需要的那段返回 ok=false。
func (w window) split(from, to time.Time) (totals chunk, totalsOK bool, full chunk, fullOK bool) {
	if from.Before(w.total) {
		from = w.total
	}
	if from.Before(w.detail) {
		end := w.detail.AddDate(0, 0, -1)
		if to.Before(end) {
			end = to
		}
		totals, totalsOK = chunk{from, end}, !end.Before(from)
		from = w.detail
	}
	full, fullOK = chunk{from, to}, !to.Before(from)
	return
}

// ========== GA ==========

func (s *Syncer) syncGA(ctx context.Context, gc *google.Client, site store.Site) error {
	w := s.window(ctx, site)
	st, err := s.Store.SyncState(ctx, site.ID, "ga")
	if err != nil {
		return err
	}
	from, backfilling := plan(st, w.yesterday, int(w.yesterday.Sub(w.detail).Hours()/24)+1, gaResyncDays)
	if !backfilling && syncedTo(st, w.yesterday) {
		// 今天已经同步过了，这是当天后面的刷新：最近 3 天早上那一次已经重拉过，这里只管昨天；
		// 而且先花一次很小的查询比对昨天的每日总数，GA 那边没变就什么都不写。
		// 昨天的数一般过几个小时就定了，之后的刷新基本都停在这一步，不会把同样的几千行明细删了再写一遍。
		from = w.yesterday
		if same, err := s.gaUnchanged(ctx, gc, site, w.yesterday); err == nil && same {
			s.skipRun(ctx, &st, "昨天的数据没有变化，这次没有重新拉取")
			from = w.yesterday.AddDate(0, 0, 1)
		}
	}
	if !from.After(w.yesterday) {
		runID := s.Store.StartRun(ctx, site.ID, "ga")
		st.Status, st.LastError = "running", ""
		if backfilling {
			bf := from
			st.BackfilledFrom = &bf
			st.Progress = 0
		}
		_ = s.Store.SaveSyncState(ctx, st)
		total := 0
		fail := func(err error) error {
			st.Status, st.LastError = "error", err.Error()
			_ = s.Store.SaveSyncState(ctx, st)
			s.Store.FinishRun(ctx, runID, "error", total, err.Error())
			return err
		}
		// 很久没同步（比如网站暂停了一阵）时，明细保留期以前的日子只补每日总数
		tc, tok, fc, fok := w.split(from, w.yesterday)
		if tok {
			n, err := s.gaTotals(ctx, gc, site, tc.from, tc.to)
			if err != nil {
				return fail(err)
			}
			total += n
		}
		if fok {
			cs := chunks(fc.from, fc.to, 7)
			for i, c := range cs {
				n, err := s.gaChunk(ctx, gc, site, c.from, c.to)
				if err != nil {
					return fail(err)
				}
				total += n
				last := c.to
				st.LastSynced = &last
				st.Progress = (i + 1) * 100 / len(cs)
				_ = s.Store.SaveSyncState(ctx, st)
			}
		}
		last := w.yesterday
		st.LastSynced, st.Status, st.Progress = &last, "ok", 100
		_ = s.Store.SaveSyncState(ctx, st)
		s.Store.FinishRun(ctx, runID, "ok", total, fmt.Sprintf("%s – %s", period.D(from), period.D(w.yesterday)))
		s.Log.Info("GA 同步完成", "site", site.Name, "from", period.D(from), "to", period.D(w.yesterday), "rows", total)
	}
	s.checkAdSense(ctx, gc, site)
	if err := s.extendGA(ctx, gc, site, w); err != nil {
		s.Log.Warn("GA 往前补数据失败，下次同步时再试", "site", site.Name, "err", err)
	}
	return nil
}

// syncedTo 表示这个数据源已经同步到 day（一般是昨天）了：再来的同步就是当天后面的刷新。
func syncedTo(st store.SyncState, day time.Time) bool {
	return st.LastSynced != nil && st.BackfilledFrom != nil && !localDay(*st.LastSynced, day.Location()).Before(day)
}

// skipRun 记下“检查过了，没有变化”：同步状态的时间更新一下（下一次刷新从现在算起），同步记录里留一行说明。
func (s *Syncer) skipRun(ctx context.Context, st *store.SyncState, msg string) {
	runID := s.Store.StartRun(ctx, st.SiteID, st.Source)
	st.Status, st.LastError, st.Progress = "ok", "", 100
	_ = s.Store.SaveSyncState(ctx, *st)
	s.Store.FinishRun(ctx, runID, "ok", 0, msg)
}

// gaUnchanged 比对某一天 GA 现在给的每日总数和库里存的是不是一样（访客、新访客、会话、认真阅读的会话、浏览量）。
// 只要一次不带维度的查询。广告收入不比：它定下来得更晚，第二天早上重拉 3 天时会补上。
func (s *Syncer) gaUnchanged(ctx context.Context, gc *google.Client, site store.Site, day time.Time) (bool, error) {
	qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rep, err := gc.RunReport(qctx, site.GAProperty, google.ReportRequest{
		Ranges:  []google.DateRange{{StartDate: period.D(day), EndDate: period.D(day)}},
		Metrics: []string{"activeUsers", "newUsers", "sessions", "engagedSessions", "screenPageViews"}})
	if err != nil {
		return false, err
	}
	var got, have [5]int
	if len(rep.Rows) > 0 && len(rep.Rows[0].Metrics) >= len(got) {
		for i := range got {
			got[i] = int(rep.Rows[0].Metrics[i])
		}
	}
	err = s.Store.Pool.QueryRow(ctx, `SELECT active_users,new_users,sessions,engaged_sessions,page_views FROM ga_daily WHERE site_id=$1 AND date=$2`,
		site.ID, day).Scan(&have[0], &have[1], &have[2], &have[3], &have[4])
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	return got == have, nil
}

// gscFirstChange 在一段日期里找出第一个“Search Console 现在给的每日总数和库里不一样”的日子（点击、曝光）。
// 没有不一样的返回 ok=false。只要一次按日期汇总的查询。
func (s *Syncer) gscFirstChange(ctx context.Context, gc *google.Client, site store.Site, from, to time.Time) (day time.Time, ok bool, err error) {
	daily, err := gc.SearchAnalytics(ctx, site.GSCSite, period.D(from), period.D(to), []string{"date"}, nil, 0)
	if err != nil {
		return day, false, err
	}
	type tot struct{ clicks, impr int }
	got, have := map[string]tot{}, map[string]tot{}
	for _, r := range daily {
		got[r.Keys[0]] = tot{int(r.Clicks), int(r.Impressions)}
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT date, clicks, impressions FROM gsc_daily WHERE site_id=$1 AND date BETWEEN $2 AND $3`, site.ID, from, to)
	if err != nil {
		return day, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var d time.Time
		var t tot
		if err := rows.Scan(&d, &t.clicks, &t.impr); err != nil {
			return day, false, err
		}
		have[period.D(d)] = t
	}
	if err := rows.Err(); err != nil {
		return day, false, err
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if got[period.D(d)] != have[period.D(d)] {
			return d, true, nil
		}
	}
	return day, false, nil
}

// adSenseTTL 比同步间隔长得多：同步停了一阵后过期，就不再给出可能过时的提示。
const adSenseTTL = 72 * time.Hour

// checkAdSense 记下网站的 GA4 媒体资源有没有关联 AdSense，网站页在还没有广告数据时据此提示。
// 已经有广告数据的网站不用查（页面直接显示收益）。查询失败只记日志，不覆盖原来的状态。
func (s *Syncer) checkAdSense(ctx context.Context, gc *google.Client, site store.Site) {
	if site.GAProperty == "" || s.Store.HasAdData(ctx, site.ID) {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	links, err := gc.AdSenseLinks(qctx, site.GAProperty)
	if err != nil {
		if !errors.Is(err, google.ErrNoEndpoint) {
			s.Log.Warn("查 AdSense 关联失败，下次同步再试", "site", site.Name, "err", err)
		}
		return
	}
	s.Store.SetAdSenseStatus(ctx, site.ID, len(links) > 0, adSenseTTL)
}

// extendGA 往前补数据：第一次同步、或者升级套餐后保留期变长时，把明细补到 w.detail、每日总数补到 w.total。
// “已经有哪天开始的数据”记在 sync_state 里：ga 记明细，ga_hist 记每日总数（清理过期数据时会跟着往后挪）。
func (s *Syncer) extendGA(ctx context.Context, gc *google.Client, site store.Site, w window) error {
	loc := site.Location()
	st, err := s.Store.SyncState(ctx, site.ID, "ga")
	if err != nil || st.BackfilledFrom == nil {
		return err
	}
	cov := localDay(*st.BackfilledFrom, loc)
	if w.detail.Before(cov) {
		for _, c := range chunks(w.detail, cov.AddDate(0, 0, -1), 7) {
			if _, err := s.gaChunk(ctx, gc, site, c.from, c.to); err != nil {
				return err
			}
		}
		d := w.detail
		st.BackfilledFrom = &d
		if err := s.Store.SaveSyncState(ctx, st); err != nil {
			return err
		}
		cov = w.detail
		s.Log.Info("GA 明细已往前补到套餐的保留期", "site", site.Name, "from", period.D(w.detail))
	}
	hist, err := s.Store.SyncState(ctx, site.ID, "ga_hist")
	if err != nil {
		return err
	}
	tcov := cov
	if hist.BackfilledFrom != nil && localDay(*hist.BackfilledFrom, loc).Before(tcov) {
		tcov = localDay(*hist.BackfilledFrom, loc)
	}
	if !w.total.Before(tcov) {
		return nil
	}
	to := tcov.AddDate(0, 0, -1)
	rows, err := s.gaTotals(ctx, gc, site, w.total, to)
	if err != nil {
		return err
	}
	t := w.total
	hist.Status, hist.Progress, hist.BackfilledFrom, hist.LastSynced, hist.LastError = "ok", 100, &t, &to, ""
	if err := s.Store.SaveSyncState(ctx, hist); err != nil {
		return err
	}
	s.Log.Info("GA 每日总数已往前补到套餐的保留期", "site", site.Name, "from", period.D(w.total), "to", period.D(to), "rows", rows)
	return nil
}

// gaTotals 只同步一段日期的每日总数（ga_daily）。一天只有一行，一年也只要两次查询。
func (s *Syncer) gaTotals(ctx context.Context, gc *google.Client, site store.Site, from, to time.Time) (int, error) {
	rng := []google.DateRange{{StartDate: period.D(from), EndDate: period.D(to)}}
	t, ai, err := gaDailyTable(func(dims, metrics []string) (*google.Report, error) {
		return gc.RunReport(ctx, site.GAProperty, google.ReportRequest{Ranges: rng, Dimensions: dims, Metrics: metrics})
	}, site.ID, site.Location())
	if err != nil {
		return 0, err
	}
	s.logAdErr(site, "每日广告收益", ai.err)
	if ai.impressions > 0 {
		s.Store.SetSiteCurrency(ctx, site.ID, ai.currency)
	}
	return s.replace(ctx, site.ID, from, to, []table{t})
}

// logAdErr 记下广告数据没查到的原因（同步照常完成，广告当作 0）。
func (s *Syncer) logAdErr(site store.Site, what string, err error) {
	if err != nil {
		s.Log.Warn("GA 广告数据没查到，这次按 0 记", "site", site.Name, "what", what, "err", err)
	}
}

// adMetrics 是网站上的广告收益（GA4 关联 AdSense 等广告平台后才有数据，没关联时都是 0）。
var adMetrics = []string{"totalAdRevenue", "publisherAdImpressions", "publisherAdClicks"}

// adInfo 是一段日期里广告数据的概况：有没有广告展示、金额的货币。
type adInfo struct {
	impressions float64
	currency    string
	err         error // 广告数据没查到的原因：不影响其他数据，但要记日志，否则“没收益”和“查询失败”分不清
}

// gaDailyTable 查一段日期的每日汇总（访客、新访客、回头客、会话、浏览量、停留，以及广告收入、展示、点击），整理成 ga_daily 的行。
// 广告数据单独查一次：查不到（比如接口报错）不影响其他数据，当作 0。
func gaDailyTable(run func(dims, metrics []string) (*google.Report, error), id int64, loc *time.Location) (table, adInfo, error) {
	t := table{name: "ga_daily", cols: []string{"site_id", "date", "active_users", "new_users", "returning_users", "sessions", "engaged_sessions", "page_views", "engagement_seconds",
		"ad_revenue", "ad_impressions", "ad_clicks"}}
	var ai adInfo
	daily, err := run([]string{"date"}, []string{"activeUsers", "newUsers", "sessions", "engagedSessions", "screenPageViews", "userEngagementDuration"})
	if err != nil {
		return t, ai, err
	}
	ret, err := run([]string{"date", "newVsReturning"}, []string{"activeUsers"})
	if err != nil {
		return t, ai, err
	}
	ads := map[string][]float64{}
	if rep, err := run([]string{"date"}, adMetrics); err == nil {
		ai.currency = rep.Currency
		for _, r := range rep.Rows {
			ads[r.Dims[0]] = r.Metrics
			ai.impressions += r.Metrics[1]
		}
	} else {
		ai.err = err
	}
	returning := map[string]float64{}
	for _, r := range ret.Rows {
		if r.Dims[1] == "returning" {
			returning[r.Dims[0]] = r.Metrics[0]
		}
	}
	for _, r := range daily.Rows {
		d, ok := parseGADate(r.Dims[0], loc)
		if !ok {
			continue
		}
		m := r.Metrics
		a := ads[r.Dims[0]]
		if len(a) < 3 {
			a = []float64{0, 0, 0}
		}
		t.rows = append(t.rows, []any{id, d, int(m[0]), int(m[1]), int(returning[r.Dims[0]]), int(m[2]), int(m[3]), int(m[4]), m[5], a[0], int(a[1]), int(a[2])})
	}
	return t, ai, nil
}

type table struct {
	name string
	cols []string
	rows [][]any
}

func parseGADate(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("20060102", s, loc)
	return t, err == nil
}

func (s *Syncer) gaChunk(ctx context.Context, gc *google.Client, site store.Site, from, to time.Time) (int, error) {
	loc := site.Location()
	rng := []google.DateRange{{StartDate: period.D(from), EndDate: period.D(to)}}
	run := func(dims, metrics []string) (*google.Report, error) {
		return gc.RunReport(ctx, site.GAProperty, google.ReportRequest{Ranges: rng, Dimensions: dims, Metrics: metrics})
	}
	id := site.ID
	var tables []table

	// 每日汇总
	t, ai, err := gaDailyTable(run, id, loc)
	if err != nil {
		return 0, err
	}
	tables = append(tables, t)
	s.logAdErr(site, "每日广告收益", ai.err)

	// 每个页面的广告收益：这段日期有广告展示才查（没放广告的网站不多花一次查询），查不到不影响其他数据
	ads := table{name: "ga_daily_ads", cols: []string{"site_id", "date", "path", "revenue", "impressions", "clicks"}}
	if ai.impressions > 0 {
		s.Store.SetSiteCurrency(ctx, id, ai.currency)
		rep, err := run([]string{"date", "pagePath"}, adMetrics)
		s.logAdErr(site, "页面广告收益", err)
		if err == nil {
			for _, r := range rep.Rows {
				d, ok := parseGADate(r.Dims[0], loc)
				if !ok || r.Dims[1] == gaOther || (r.Metrics[0] <= 0 && r.Metrics[1] <= 0) {
					continue
				}
				ads.rows = append(ads.rows, []any{id, d, r.Dims[1], r.Metrics[0], int(r.Metrics[1]), int(r.Metrics[2])})
			}
		}
	}
	tables = append(tables, ads)

	// 页面：同一路径可能对应多个标题，取浏览量最多的标题
	pages, err := run([]string{"date", "pagePath", "pageTitle"}, []string{"screenPageViews", "activeUsers", "userEngagementDuration"})
	if err != nil {
		return 0, err
	}
	type pagg struct {
		views, users int
		eng          float64
		title        string
		titleViews   int
		titles       int // 这一天这个页面出现过几种标题
	}
	pm := map[[2]string]*pagg{}
	for _, r := range pages.Rows {
		if r.Dims[1] == gaOther {
			continue // GA 把超出表格上限的页面合并成了“(other)”，无法归到具体页面
		}
		k := [2]string{r.Dims[0], r.Dims[1]}
		a := pm[k]
		if a == nil {
			a = &pagg{}
			pm[k] = a
		}
		v := int(r.Metrics[0])
		a.titles++
		a.views += v
		a.users += int(r.Metrics[1])
		a.eng += r.Metrics[2]
		if v >= a.titleViews {
			a.title, a.titleViews = r.Dims[2], v
		}
	}
	t = table{name: "ga_daily_pages", cols: []string{"site_id", "date", "path", "title", "page_views", "users", "engagement_seconds"}}
	for k, a := range pm {
		d, ok := parseGADate(k[0], loc)
		if !ok {
			continue
		}
		title := a.title
		if a.titles > dynamicTitles {
			title = "" // 标题跟着搜索词、筛选条件变的页面（如 /search），存空标题，页面上显示路径
		}
		t.rows = append(t.rows, []any{id, d, k[1], title, a.views, a.users, a.eng})
	}
	t.rows = trimLongTail(t.rows, 4)
	tables = append(tables, t)

	simple := []struct {
		name    string
		dims    []string
		metrics []string
		cols    []string
		ints    int // 前几个指标转成整数
	}{
		{"ga_daily_landing", []string{"date", "landingPage", "sessionDefaultChannelGroup", "sessionSource"}, []string{"sessions", "engagedSessions", "newUsers", "screenPageViews"},
			[]string{"site_id", "date", "path", "channel", "source", "sessions", "engaged_sessions", "new_users", "page_views"}, 4},
		{"ga_daily_sources", []string{"date", "sessionDefaultChannelGroup", "sessionSource", "sessionMedium", "sessionCampaignName"},
			[]string{"sessions", "engagedSessions", "newUsers", "screenPageViews", "userEngagementDuration"},
			[]string{"site_id", "date", "channel", "source", "medium", "campaign", "sessions", "engaged_sessions", "new_users", "page_views", "engagement_seconds"}, 4},
		{"ga_daily_geo", []string{"date", "country"}, []string{"sessions", "engagedSessions", "activeUsers"},
			[]string{"site_id", "date", "country", "sessions", "engaged_sessions", "users"}, 3},
		{"ga_daily_tech", []string{"date", "deviceCategory", "browser"}, []string{"sessions", "engagedSessions", "screenPageViews", "userEngagementDuration", "activeUsers"},
			[]string{"site_id", "date", "device", "browser", "sessions", "engaged_sessions", "page_views", "engagement_seconds", "users"}, -1},
		{"ga_hourly", []string{"date", "hour"}, []string{"sessions", "activeUsers"},
			[]string{"site_id", "date", "hour", "sessions", "users"}, 2},
		{"ga_daily_lang", []string{"date", "language"}, []string{"sessions", "engagedSessions"},
			[]string{"site_id", "date", "language", "sessions", "engaged_sessions"}, 2},
	}
	for _, sp := range simple {
		rep, err := run(sp.dims, sp.metrics)
		if err != nil {
			return 0, err
		}
		t := table{name: sp.name, cols: sp.cols}
		seen := map[string]bool{}
		for _, r := range rep.Rows {
			d, ok := parseGADate(r.Dims[0], loc)
			if !ok {
				continue
			}
			if sp.name == "ga_daily_landing" && r.Dims[1] == gaOther {
				continue
			}
			if sp.name == "ga_hourly" {
				// 数据量超过 GA 的表格上限时，会把一部分合并成小时为“(other)”的一行，无法归到某个小时，只能跳过
				if h, err := strconv.Atoi(r.Dims[1]); err != nil || h < 0 || h > 23 {
					continue
				}
			}
			key := strings.Join(r.Dims, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			row := []any{id, d}
			for _, v := range r.Dims[1:] {
				if sp.name == "ga_hourly" {
					h, _ := strconv.Atoi(v)
					row = append(row, int16(h))
					continue
				}
				row = append(row, v)
			}
			for i, m := range r.Metrics {
				if sp.name == "ga_daily_tech" {
					if i == 3 {
						row = append(row, m)
					} else {
						row = append(row, int(m))
					}
					continue
				}
				if sp.ints < 0 || i < sp.ints {
					row = append(row, int(m))
				} else {
					row = append(row, m)
				}
			}
			t.rows = append(t.rows, row)
		}
		if sp.name == "ga_daily_landing" {
			t.rows = trimLongTail(t.rows, 5) // 第 5 列是访问次数
		}
		tables = append(tables, t)
	}
	return s.replace(ctx, id, from, to, tables)
}

// 大网站每天有上万个只被看过一两次的页面，它们占了九成的行数，却几乎不影响任何分析，还会让查询慢上十几倍。
// 所以某一天超过 longTailKeep 行时，只保留当天前 longTailKeep 名，以及其余浏览（访问）≥ longTailMin 的行。
// 同一天里一个页面出现超过 dynamicTitles 种标题，就认为它的标题是动态生成的（搜索页每天几百种；
// 剧集页按集数变，一天一般不超过这个数）。
const dynamicTitles = 30

const (
	longTailKeep = 2000
	longTailMin  = 3
)

// trimLongTail 按日期裁掉长尾。rows 的第 2 列（下标 1）是日期，valIdx 是用来排序的数值列。
func trimLongTail(rows [][]any, valIdx int) [][]any {
	byDay := map[time.Time][]int{}
	for i, r := range rows {
		d := r[1].(time.Time)
		byDay[d] = append(byDay[d], i)
	}
	keep := make([]bool, len(rows))
	trimmed := false
	for _, idx := range byDay {
		if len(idx) <= longTailKeep {
			for _, i := range idx {
				keep[i] = true
			}
			continue
		}
		trimmed = true
		sort.Slice(idx, func(a, b int) bool { return num(rows[idx[a]][valIdx]) > num(rows[idx[b]][valIdx]) })
		for rank, i := range idx {
			keep[i] = rank < longTailKeep || num(rows[i][valIdx]) >= longTailMin
		}
	}
	if !trimmed {
		return rows
	}
	out := rows[:0:0]
	for i, r := range rows {
		if keep[i] {
			out = append(out, r)
		}
	}
	return out
}

func num(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return 0
}

// replace 在一个事务里删掉这段日期的旧数据，再批量写入新数据。
// gaOther 是 GA 在数据量超过表格上限时，把放不下的小项合并成的那一行。
const gaOther = "(other)"

func (s *Syncer) replace(ctx context.Context, siteID int64, from, to time.Time, tables []table) (int, error) {
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	total := 0
	for _, t := range tables {
		if _, err := tx.Exec(ctx, `DELETE FROM `+t.name+` WHERE site_id=$1 AND date BETWEEN $2 AND $3`, siteID, from, to); err != nil {
			return 0, err
		}
		if len(t.rows) == 0 {
			continue
		}
		n, err := tx.CopyFrom(ctx, pgx.Identifier{t.name}, t.cols, pgx.CopyFromRows(t.rows))
		if err != nil {
			return 0, fmt.Errorf("写入 %s 失败: %w", t.name, err)
		}
		total += int(n)
	}
	return total, tx.Commit(ctx)
}

// ========== Search Console ==========

func pathOf(page string) string {
	u, err := url.Parse(page)
	if err != nil || u.Path == "" {
		return "/"
	}
	return u.Path
}

func (s *Syncer) syncGSC(ctx context.Context, gc *google.Client, site store.Site) error {
	w := s.window(ctx, site)
	st, err := s.Store.SyncState(ctx, site.ID, "gsc")
	if err != nil {
		return err
	}
	from, backfilling := plan(st, w.yesterday, int(w.yesterday.Sub(w.detail).Hours()/24)+1, gscResyncDays)
	if !backfilling && syncedTo(st, w.yesterday) {
		// 当天后面的刷新：Search Console 的数据是陆续出来的（晚两三天），所以不是只看昨天，
		// 而是比对最近 5 天的每日总数，从第一个有变化的日子开始重拉；都没变就什么都不写。
		if day, changed, err := s.gscFirstChange(ctx, gc, site, from, w.yesterday); err == nil {
			if changed {
				from = day
			} else {
				s.skipRun(ctx, &st, "最近几天的搜索数据没有变化，这次没有重新拉取")
				from = w.yesterday.AddDate(0, 0, 1)
			}
		}
	}
	if !from.After(w.yesterday) {
		runID := s.Store.StartRun(ctx, site.ID, "gsc")
		st.Status, st.LastError = "running", ""
		if backfilling {
			bf := from
			st.BackfilledFrom = &bf
			st.Progress = 0
		}
		_ = s.Store.SaveSyncState(ctx, st)
		total := 0
		fail := func(err error) error {
			st.Status, st.LastError = "error", err.Error()
			_ = s.Store.SaveSyncState(ctx, st)
			s.Store.FinishRun(ctx, runID, "error", total, err.Error())
			return err
		}
		tc, tok, fc, fok := w.split(from, w.yesterday)
		if tok {
			n, err := s.gscRange(ctx, gc, site, tc.from, tc.to, false)
			if err != nil {
				return fail(err)
			}
			total += n
		}
		if fok {
			cs := chunks(fc.from, fc.to, 30)
			for i, c := range cs {
				n, err := s.gscRange(ctx, gc, site, c.from, c.to, true)
				if err != nil {
					return fail(err)
				}
				total += n
				last := c.to
				st.LastSynced = &last
				st.Progress = (i + 1) * 100 / len(cs)
				_ = s.Store.SaveSyncState(ctx, st)
			}
		}
		last := w.yesterday
		st.LastSynced, st.Status, st.Progress = &last, "ok", 100
		_ = s.Store.SaveSyncState(ctx, st)
		s.Store.FinishRun(ctx, runID, "ok", total, fmt.Sprintf("%s – %s", period.D(from), period.D(w.yesterday)))
		s.Log.Info("Search Console 同步完成", "site", site.Name, "rows", total)
	}
	if err := s.extendGSC(ctx, gc, site, w); err != nil {
		s.Log.Warn("Search Console 往前补数据失败，下次同步时再试", "site", site.Name, "err", err)
	}
	return nil
}

// extendGSC 和 extendGA 一样：gsc 记明细（页面、搜索词）从哪天开始，gsc_hist 记每日总数从哪天开始。
func (s *Syncer) extendGSC(ctx context.Context, gc *google.Client, site store.Site, w window) error {
	loc := site.Location()
	st, err := s.Store.SyncState(ctx, site.ID, "gsc")
	if err != nil || st.BackfilledFrom == nil {
		return err
	}
	cov := localDay(*st.BackfilledFrom, loc)
	if w.detail.Before(cov) {
		for _, c := range chunks(w.detail, cov.AddDate(0, 0, -1), 30) {
			if _, err := s.gscRange(ctx, gc, site, c.from, c.to, true); err != nil {
				return err
			}
		}
		d := w.detail
		st.BackfilledFrom = &d
		if err := s.Store.SaveSyncState(ctx, st); err != nil {
			return err
		}
		cov = w.detail
	}
	hist, err := s.Store.SyncState(ctx, site.ID, "gsc_hist")
	if err != nil {
		return err
	}
	tcov := cov
	if hist.BackfilledFrom != nil && localDay(*hist.BackfilledFrom, loc).Before(tcov) {
		tcov = localDay(*hist.BackfilledFrom, loc)
	}
	if !w.total.Before(tcov) {
		return nil
	}
	to := tcov.AddDate(0, 0, -1)
	rows := 0
	for _, c := range chunks(w.total, to, 90) {
		n, err := s.gscRange(ctx, gc, site, c.from, c.to, false)
		if err != nil {
			return err
		}
		rows += n
	}
	t := w.total
	hist.Status, hist.Progress, hist.BackfilledFrom, hist.LastSynced, hist.LastError = "ok", 100, &t, &to, ""
	if err := s.Store.SaveSyncState(ctx, hist); err != nil {
		return err
	}
	s.Log.Info("Search Console 每日总数已往前补到套餐的保留期", "site", site.Name, "from", period.D(w.total), "rows", rows)
	return nil
}

// gscRange 同步一段日期：每日总数一定要；details 为 true 时再加页面和搜索词（搜索词按 7 天一段拉，避免单次结果过大）。
func (s *Syncer) gscRange(ctx context.Context, gc *google.Client, site store.Site, from, to time.Time, details bool) (int, error) {
	loc := site.Location()
	parse := func(k string) (time.Time, bool) {
		d, err := time.ParseInLocation("2006-01-02", k, loc)
		return d, err == nil
	}
	daily, err := gc.SearchAnalytics(ctx, site.GSCSite, period.D(from), period.D(to), []string{"date"}, nil, 0)
	if err != nil {
		return 0, err
	}
	t := table{name: "gsc_daily", cols: []string{"site_id", "date", "clicks", "impressions", "pos_x_impr"}}
	for _, r := range daily {
		if d, ok := parse(r.Keys[0]); ok {
			t.rows = append(t.rows, []any{site.ID, d, int(r.Clicks), int(r.Impressions), r.Position * r.Impressions})
		}
	}
	tables := []table{t}
	if details {
		pages, err := gc.SearchAnalytics(ctx, site.GSCSite, period.D(from), period.D(to), []string{"date", "page"}, nil, 0)
		if err != nil {
			return 0, err
		}
		t := table{name: "gsc_daily_pages", cols: []string{"site_id", "date", "page", "path", "clicks", "impressions", "pos_x_impr"}}
		for _, r := range pages {
			if d, ok := parse(r.Keys[0]); ok {
				t.rows = append(t.rows, []any{site.ID, d, r.Keys[1], pathOf(r.Keys[1]), int(r.Clicks), int(r.Impressions), r.Position * r.Impressions})
			}
		}
		tables = append(tables, t)
	}
	total, err := s.replace(ctx, site.ID, from, to, tables)
	if err != nil || !details {
		return total, err
	}
	for _, qc := range chunks(from, to, 7) {
		rows, err := gc.SearchAnalytics(ctx, site.GSCSite, period.D(qc.from), period.D(qc.to), []string{"date", "query", "page"}, nil, 0)
		if err != nil {
			return total, err
		}
		t := table{name: "gsc_daily_queries", cols: []string{"site_id", "date", "query", "page", "path", "clicks", "impressions", "pos_x_impr"}}
		for _, r := range rows {
			if d, ok := parse(r.Keys[0]); ok {
				t.rows = append(t.rows, []any{site.ID, d, r.Keys[1], r.Keys[2], pathOf(r.Keys[2]), int(r.Clicks), int(r.Impressions), r.Position * r.Impressions})
			}
		}
		n, err := s.replace(ctx, site.ID, qc.from, qc.to, []table{t})
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
