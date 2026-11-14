package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pingoo/internal/agent"
	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/store"
)

// rangeKey 从请求里读日期范围：?range=d28 这类预设，或者 ?from=2026-10-01&to=2026-10-31 自选起止日期
// （也可以直接写成 ?range=2026-10-01_2026-10-31）。不合法时返回空，由调用方用默认范围。
func rangeKey(r *http.Request) string {
	q := r.URL.Query()
	k := q.Get("range")
	if f, t := q.Get("from"), q.Get("to"); f != "" && t != "" {
		k = f + "_" + t
	}
	if !period.Valid(k) {
		return ""
	}
	return k
}

// compareMode 读 ?cmp=yoy（和去年同期对比）；其他值一律按“和上一期对比”。
func compareMode(r *http.Request) string {
	if r.URL.Query().Get("cmp") == period.CompareYoY {
		return period.CompareYoY
	}
	return ""
}

// planFor 返回当前用户的套餐（没登录按免费版）。
func (s *Server) planFor(r *http.Request) store.Plan {
	if u := currentUser(r); u != nil {
		return s.Store.PlanOf(r.Context(), u.ID)
	}
	return s.Store.Plans(r.Context()).Get(store.PlanFree)
}

// withCompare 按 ?cmp= 设置对比期；套餐的每日总数保留得不够和去年同期比时，退回“比上一期”。
func (s *Server) withCompare(r *http.Request, rg period.Range) (period.Range, bool) {
	can := s.planFor(r).CanYoY(len(rg.Dates()))
	if !can {
		return rg.WithCompare(""), false
	}
	return rg.WithCompare(compareMode(r)), true
}

// retentionNote 在所选范围（或上一期）早于套餐保留的每日数据时，说明为什么前面是空的。
func (s *Server) retentionNote(r *http.Request, rg period.Range, loc *time.Location) string {
	p := s.planFor(r)
	keep := time.Now().In(loc).AddDate(0, 0, -p.TotalDays)
	if period.D(rg.Sequential().PrevStart) >= period.D(keep) {
		return ""
	}
	return fmt.Sprintf("%s保留最近 %d 天的每日数据，更早的日子没有数据，和上一期的对比可能不完整。", p.Name, p.TotalDays)
}

// ========== 全部网站概览 ==========

type overviewData struct {
	Ranges   []struct{ Key, Label string }
	Range    period.Range
	O        metrics.Overview
	NoSites  bool
	NoGoogle bool
	Sort     string
	AIReady  bool
	MaxAlert int
	Today    string // 自选日期最晚能选到哪天
	CanYoY   bool   // 套餐能不能和去年同期比
	Note     string // 所选范围超出套餐保留期时的说明
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sites, err := s.Store.Sites(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key := rangeKey(r)
	if key == "" {
		key = "d7"
	}
	loc := s.loc()
	rg, canYoY := s.withCompare(r, period.Resolve(key, time.Now(), loc))
	d := overviewData{Ranges: period.Keys, Range: rg, NoSites: len(sites) == 0, Sort: r.URL.Query().Get("sort"), MaxAlert: 4,
		Today: period.D(time.Now().In(loc).AddDate(0, 0, -1)), CanYoY: canYoY, Note: s.retentionNote(r, rg, loc)}
	if d.Sort == "" {
		d.Sort = "attention"
	}
	sa, _ := s.Store.Setting(ctx, store.KeyGoogleSA)
	d.NoGoogle = sa == ""
	d.AIReady = s.aiStatus(r, "diagnose").Ready
	if !d.NoSites {
		d.O = s.M.Overview(ctx, sites, rg, loc)
		sortCards(d.O.Cards, d.Sort)
		// 页头的日期说明跟着已同步的数据走
		d.Range = rg.EndAt(d.O.GALatest)
	}
	s.page(w, r, "overview", "全部网站", "overview", d)
}

var statusRank = map[string]int{"anomaly": 0, "down": 1, "up": 2, "flat": 3, "empty": 4}

func sortCards(cs []metrics.SiteCard, by string) {
	less := func(a, b metrics.SiteCard) bool {
		switch by {
		case "visitors":
			return a.Visitors > b.Visitors
		case "growth":
			return a.Delta > b.Delta
		case "decline":
			return a.Delta < b.Delta
		}
		if statusRank[a.Status] != statusRank[b.Status] {
			return statusRank[a.Status] < statusRank[b.Status]
		}
		da, db := a.Delta, b.Delta
		if da < 0 {
			da = -da
		}
		if db < 0 {
			db = -db
		}
		return da > db
	}
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && less(cs[j], cs[j-1]); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

// loc 是“全部网站”页面用的时区：后台设置的默认时区。
func (s *Server) loc() *time.Location {
	l, err := time.LoadLocation(s.Store.DefaultTZ(context.Background()))
	if err != nil {
		return time.Local
	}
	return l
}

type insightView struct {
	SiteID   int64
	Range    period.Range
	Insight  *store.Insight
	Content  agent.InsightContent
	Running  bool
	NoAI     bool
	AIMsg    string // AI 用不了的原因（没有模型、积分不足）
	Error    string
	Todos    []store.Todo
	PollURL  string
	RegenURL string
	Manual   bool // 这个时间范围不自动生成，需要站长点“生成洞察”
	Outdated bool // 已有的洞察是按另一段日期生成的（例如昨天生成的“今天”）
}

func (s *Server) loadInsight(r *http.Request, site *store.Site, rg period.Range) insightView {
	ctx := r.Context()
	v := insightView{Range: rg}
	var sid *int64
	key := agent.InsightKey(store.UserID(ctx), site, rg.Key)
	if site != nil {
		sid = &site.ID
		v.SiteID = site.ID
		v.PollURL = fmt.Sprintf("/sites/%d/part/insight?range=%s", site.ID, rg.Key)
	} else {
		v.PollURL = "/part/overview-insight?range=" + rg.Key
	}
	v.RegenURL = v.PollURL
	st := s.aiStatus(r, "insight")
	v.NoAI, v.AIMsg = !st.Ready, st.Msg
	ins, err := s.Store.LatestInsight(ctx, sid, rg.Key)
	if err == nil {
		v.Insight = &ins
		if ins.Status == "done" {
			_ = json.Unmarshal(ins.Content, &v.Content)
			if site != nil {
				v.Todos, _ = s.Store.Todos(ctx, site.ID, "insight", ins.ID)
			}
		}
		if ins.Status == "error" {
			v.Error = ins.Error
			if s.Store.InsightFailStreak(ctx, sid, rg.Key) >= agent.InsightMaxFails {
				v.Error += fmt.Sprintf("（已经连续失败 %d 次，不再自动重试；可以点“重新生成”再试一次）", agent.InsightMaxFails)
			}
		}
	}
	v.Running = s.Agent.Busy(key)

	if v.Insight != nil && v.Insight.Status == "running" && !v.Running {
		v.Error = "上次生成没有完成（可能是服务重启了），可以重新生成"
	}
	// 只有打开了“自动生成 AI 内容”的用户，默认时间范围（近 7 天）才会在没有洞察或洞察超过 20 小时时自动生成；
	// 其他情况等用户点“生成洞察”：生成要花积分，不能在用户不知情的时候花
	u := currentUser(r)
	auto := rg.Key == agent.DefaultInsightRange(site) && u != nil && u.AutoAI
	if !auto && v.Insight != nil && v.Insight.Status == "done" &&
		(period.D(v.Insight.PeriodStart) != period.D(rg.Start) || period.D(v.Insight.PeriodEnd) != period.D(rg.End)) {
		v.Outdated = true
	}
	stale := v.Insight == nil || (v.Insight.Status != "running" && time.Since(v.Insight.CreatedAt) > agent.InsightTTL)
	if !v.NoAI && !v.Running && stale && v.Error == "" {
		if auto {
			if err := s.startInsight(ctx, site, rg); err != nil {
				v.Error = err.Error()
			} else {
				v.Running = true
			}
		} else if v.Insight == nil {
			v.Manual = true
		}
	}
	return v
}

// startInsight 在后台生成洞察，并等到它真正开始（占上任务、建好新记录）或者马上失败（积分不够、全站用量到上限等），
// 返回失败的原因。这样页面拿到的一定是“正在生成”或者“为什么没开始”，不会点了之后看起来没反应。
// ctx 只用来取当前用户：请求结束后后台任务还要继续，所以不沿用它的取消。
func (s *Server) startInsight(ctx context.Context, site *store.Site, rg period.Range) error {
	uid := store.UserID(ctx)
	var sid *int64
	if site != nil {
		sid = &site.ID
	}
	before := int64(0)
	if ins, err := s.Store.LatestInsight(ctx, sid, rg.Key); err == nil {
		before = ins.ID
	}
	errc := make(chan error, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("后台任务出错", "task", "insight", "panic", v)
				errc <- errors.New("生成时出了内部错误，请稍后再试")
			}
		}()
		gctx, cancel := context.WithTimeout(store.WithUser(context.Background(), uid), 6*time.Minute)
		defer cancel()
		_, err := s.Agent.GenerateInsight(gctx, site, rg)
		errc <- err
		if err != nil && err != agent.ErrNoAI && err != agent.ErrBudget && err != agent.ErrNoCredits {
			s.Log.Warn("生成洞察失败", "err", err)
		}
	}()
	key := agent.InsightKey(uid, site, rg.Key)
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case err := <-errc: // 很快就结束了：多半是没能开始（原因在 err 里）；已经有人在生成时返回 nil，页面会显示“正在生成”
			return err
		case <-deadline:
			return nil
		case <-tick.C:
			if !s.Agent.Busy(key) {
				continue
			}
			if ins, err := s.Store.LatestInsight(ctx, sid, rg.Key); err == nil && ins.ID > before {
				return nil
			}
		}
	}
}

func (s *Server) overviewInsight(w http.ResponseWriter, r *http.Request) {
	key := rangeKey(r)
	if key == "" {
		key = "d7"
	}
	rg := period.Resolve(key, time.Now(), s.loc())
	s.part(w, "overview-insight", s.loadInsight(r, nil, rg))
}

func (s *Server) overviewInsightRegen(w http.ResponseWriter, r *http.Request) {
	key := rangeKey(r)
	if key == "" {
		key = "d7"
	}
	rg := period.Resolve(key, time.Now(), s.loc())
	s.regenInsight(w, r, nil, rg)
	s.part(w, "overview-insight", s.loadInsight(r, nil, rg))
}

// regenInsight 处理“生成 / 重新生成”：开始了就让页面进入“正在生成”；开始不了就弹出原因，不让按钮看起来没反应。
func (s *Server) regenInsight(w http.ResponseWriter, r *http.Request, site *store.Site, rg period.Range) {
	if st := s.aiStatus(r, "insight"); !st.Ready {
		toast(w, st.Msg)
		return
	}
	if err := s.startInsight(r.Context(), site, rg); err != nil {
		toast(w, "没能开始生成："+err.Error())
	}
}

// overviewLive 异步返回“此刻在线”：页面加载后 htmx 拉一次，填充页头的总数和每张卡片的在线人数（卡片用 OOB 更新）。
func (s *Server) overviewLive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sites, err := s.Store.Sites(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.part(w, "overview-live", map[string]any{"Live": s.M.LiveCounts(ctx, sites)})
}

func (s *Server) dismissSignal(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DismissSignal(r.Context(), pathID(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "已忽略，同一个信号以后不再提醒")
	w.WriteHeader(http.StatusOK)
}

// ========== 网站详情 ==========

type siteData struct {
	Site     store.Site
	Sites    []store.Site
	Statuses map[int64]string
	Ranges   []struct{ Key, Label string }
	Range    period.Range
	Live     int
	GASync   time.Time
	GSC      time.Time
	Status   string
	Metric   string
	Tiles    []metrics.Tile
	Today    string // 自选日期最晚能选到哪天
	// NoHistory：选了“去年同期”，但那段时间没有数据可比
	NoHistory bool
	// DetailFrom：明细数据（页面、来源等）从哪天开始有；选的范围比它早时要提示
	DetailFrom time.Time
	CanYoY     bool   // 套餐能不能和去年同期比
	HasAds     bool   // 有广告收益数据（GA4 关联了 AdSense）时才显示“广告收益”一节
	AdNote     string // 还没有广告数据时的提示：waiting（已关联、在等数据）/ unlinked（别的网站关联了，这个没有）/ ""
	Note       string // 所选范围超出套餐保留期时的说明
}

// adNote 决定还没有广告数据的网站要不要提示：已关联 AdSense 的说明在等数据；没关联的，只有当用户的其他网站
// 用着 AdSense 时才提醒（说明他在用广告，这个网站可能是漏了关联），没用广告的用户不打扰。
func (s *Server) adNote(ctx context.Context, site store.Site) string {
	switch s.Store.AdSenseStatus(ctx, site.ID) {
	case store.AdSenseLinked:
		return "waiting"
	case store.AdSenseNone:
		sites, _ := s.Store.Sites(ctx)
		for _, x := range sites {
			if x.ID != site.ID && (s.Store.HasAdData(ctx, x.ID) || s.Store.AdSenseStatus(ctx, x.ID) == store.AdSenseLinked) {
				return "unlinked"
			}
		}
	}
	return ""
}

// siteAndRange 返回网站和请求的日期范围，对比期是紧挨着的上一段。内容、来源、搜索这些明细都用它。
func (s *Server) siteAndRange(r *http.Request) (store.Site, period.Range, error) {
	site, err := s.Store.Site(r.Context(), pathID(r))
	if err != nil {
		return site, period.Range{}, err
	}
	key := rangeKey(r)
	if key == "" {
		key = "d7"
	}
	return site, s.M.SyncedRange(r.Context(), site, key), nil
}

// siteAndRangeCmp 和 siteAndRange 一样，但对比期按 ?cmp= 来（可以是去年同期）。只有核心指标和趋势图用它。
func (s *Server) siteAndRangeCmp(r *http.Request) (store.Site, period.Range, error) {
	site, rg, err := s.siteAndRange(r)
	rg, _ = s.withCompare(r, rg)
	return site, rg, err
}

func (s *Server) sitePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	site, rg, err := s.siteAndRangeCmp(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := siteData{Site: site, Ranges: period.Keys, Range: rg, Metric: "v",
		Today: period.D(time.Now().In(site.Location()).AddDate(0, 0, -1))}
	d.NoHistory = rg.Compare == period.CompareYoY && !s.M.HasDaily(ctx, site.ID, rg.PrevStart, rg.PrevEnd)
	d.CanYoY = s.planFor(r).CanYoY(len(rg.Dates()))
	d.HasAds = s.M.HasAds(ctx, site.ID)
	if !d.HasAds && !site.Demo {
		d.AdNote = s.adNote(ctx, site)
	}
	d.Note = s.retentionNote(r, rg, site.Location())
	// 明细从哪天开始有（清理过期数据时会跟着往后挪）
	if st, err := s.Store.SyncState(ctx, site.ID, "ga"); err == nil && st.BackfilledFrom != nil && !site.Demo {
		if period.D(rg.Sequential().PrevStart) < period.D(*st.BackfilledFrom) {
			d.DetailFrom = *st.BackfilledFrom
		}
	}
	d.Sites, _ = s.Store.Sites(ctx)
	d.Live = s.M.Realtime(ctx, site)
	d.GASync = s.M.GALatest(ctx, site.ID)
	d.GSC = s.M.GSCLatest(ctx, site.ID)
	d.Tiles = s.M.Tiles(ctx, site, rg)
	d.Statuses = map[int64]string{}
	sigs, _ := s.Store.OpenSignals(ctx, 0, 14)
	for _, x := range sigs {
		if _, ok := d.Statuses[x.SiteID]; !ok {
			d.Statuses[x.SiteID] = x.Severity
		}
	}
	d.Status = "flat"
	if len(d.Tiles) > 0 && d.Tiles[0].Prev >= 30 {
		switch delta := d.Tiles[0].Cur/d.Tiles[0].Prev - 1; {
		case d.Statuses[site.ID] == "critical":
			d.Status = "anomaly"
		case delta > 0.1:
			d.Status = "up"
		case delta < -0.1:
			d.Status = "down"
		}
	}
	s.page(w, r, "site", site.Name, "overview", d)
}

func (s *Server) siteInsight(w http.ResponseWriter, r *http.Request) {
	site, rg, err := s.siteAndRange(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.part(w, "site-insight", s.loadInsight(r, &site, rg))
}

func (s *Server) siteInsightRegen(w http.ResponseWriter, r *http.Request) {
	site, rg, err := s.siteAndRange(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.regenInsight(w, r, &site, rg)
	s.part(w, "site-insight", s.loadInsight(r, &site, rg))
}

type chartView struct {
	SiteID int64
	Range  period.Range
	Tile   metrics.Tile
	Chart  chartOpts
}

func (s *Server) siteChart(w http.ResponseWriter, r *http.Request) {
	site, rg, err := s.siteAndRangeCmp(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key := r.URL.Query().Get("metric")
	t, ok := metrics.TileDef(key)
	if !ok {
		t, _ = metrics.TileDef("v")
	}
	if rg.Hourly && t.Key != "v" {
		t, _ = metrics.TileDef("v")
	}
	ser := s.M.MetricSeries(r.Context(), site, rg, t.Key)
	c := chartJSON(ser, "", t.Label, t.Kind)
	c.Axes, c.Zero, c.Invert = true, t.Kind == "count", t.Kind == "pos"
	c.PrevName = rg.PrevName()
	if !rg.Hourly {
		c.Ann, c.Markers = s.M.ChartMarks(r.Context(), site.ID, ser.Dates)
	}
	s.part(w, "site-chart", chartView{SiteID: site.ID, Range: rg, Tile: t, Chart: c})
}

type tabView struct {
	SiteID int64
	Range  period.Range
	Tab    string
	Tabs   []struct{ Key, Label string }
	Rows   any
	Max    float64
	Extra  any
	Error  string
}

func (s *Server) siteContent(w http.ResponseWriter, r *http.Request) {
	site, rg, err := s.siteAndRange(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "rising"
	}
	rows, err := s.M.Content(r.Context(), site, rg, tab)
	v := tabView{SiteID: site.ID, Range: rg, Tab: tab, Tabs: metrics.ContentTabs, Rows: rows}
	if err != nil {
		v.Error = err.Error()
	}
	for _, x := range rows {
		v.Max = max(v.Max, float64(x.Views))
	}
	s.part(w, "site-content", v)
}

type pageDetailView struct {
	SiteID int64
	Range  period.Range
	D      metrics.PageDetail
	Chart  chartOpts
}

func (s *Server) sitePageDetail(w http.ResponseWriter, r *http.Request) {
	site, rg, err := s.siteAndRange(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := s.M.PageDetail(r.Context(), site, rg, r.URL.Query().Get("path"))
	s.part(w, "page-detail", pageDetailView{SiteID: site.ID, Range: rg, D: d, Chart: chartJSON(d.Series, "", "浏览", "count")})
}

func (s *Server) siteSources(w http.ResponseWriter, r *http.Request) {
	site, rg, err := s.siteAndRange(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "channels"
	}
	rows, err := s.M.Sources(r.Context(), site, rg, tab)
	v := tabView{SiteID: site.ID, Range: rg, Tab: tab, Tabs: metrics.SourceTabs, Rows: rows}
	if err != nil {
		v.Error = err.Error()
	}
	for _, x := range rows {
		v.Max = max(v.Max, float64(x.Sessions))
	}
	s.part(w, "site-sources", v)
}

type searchView struct {
	tabView
	Smalls  []metrics.SearchSmall
	Charts  []chartOpts
	Latest  time.Time
	HasData bool
	Pages   []metrics.PageQueries
	NoGSC   bool
}

func (s *Server) siteSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	site, rg, err := s.siteAndRange(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "top"
	}
	v := searchView{tabView: tabView{SiteID: site.ID, Range: rg, Tab: tab, Tabs: metrics.SearchTabs}}
	v.NoGSC = site.GSCSite == "" && !site.Demo
	v.Smalls, v.Latest, v.HasData = s.M.SearchSmalls(ctx, site, rg)
	for _, sm := range v.Smalls {
		c := chartJSON(sm.Series, "", sm.Label, sm.Kind)
		c.Invert = sm.Kind == "pos"
		v.Charts = append(v.Charts, c)
	}
	if tab == "pages" {
		v.Pages, err = s.M.PagesWithQueries(ctx, site, rg)
	} else {
		var rows []metrics.QueryRow
		rows, err = s.M.Search(ctx, site, rg, tab)
		v.Rows = rows
		for _, x := range rows {
			v.Max = max(v.Max, float64(x.Clicks))
		}
	}
	if err != nil {
		v.Error = err.Error()
	}
	if r.URL.Query().Get("only") == "tab" {
		s.part(w, "search-tab", v)
		return
	}
	s.part(w, "site-search", v)
}

func (s *Server) siteAudience(w http.ResponseWriter, r *http.Request) {
	site, rg, err := s.siteAndRange(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	a := s.M.Audience(r.Context(), site, rg)
	s.part(w, "site-audience", map[string]any{"A": a, "Range": rg, "SiteID": site.ID})
}

// siteAds 是“广告收益”一节：合计、每日收入图、收入最高的页面 / 流量大收入低的页面。
func (s *Server) siteAds(w http.ResponseWriter, r *http.Request) {
	site, rg, err := s.siteAndRange(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab != "low" {
		tab = "top"
	}
	a := s.M.Ads(r.Context(), site, rg, tab)
	s.part(w, "site-ads", map[string]any{"SiteID": site.ID, "Range": rg, "Tab": tab, "Tabs": metrics.AdTabs, "Ads": a,
		"Tiles": metrics.AdTiles(a.Cur, a.Prev)})
}

func (s *Server) siteHealth(w http.ResponseWriter, r *http.Request) {
	site, err := s.Store.Site(r.Context(), pathID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cs := s.M.Health(r.Context(), site)
	s.part(w, "site-health", map[string]any{"Checks": cs, "Summary": metrics.HealthSummary(cs)})
}

// ========== 诊断 ==========

type diagnosisView struct {
	D       store.Diagnosis
	Result  agent.DiagnosisResult
	Done    bool
	Error   string
	NoAI    bool
	Example bool // 演示账号看到的预先写好的示例
}

// demoReadable 是演示账号也能 POST 的地址：只读出预先写好的示例，不调用 AI、不改数据。
func demoReadable(p string) bool {
	return strings.HasPrefix(p, "/sites/") && strings.HasSuffix(p, "/diagnose")
}

func (s *Server) diagnoseStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	site, err := s.Store.Site(ctx, pathID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if u := currentUser(r); u != nil && u.Demo {
		// 演示账号不能调用 AI：给它看这个网站预先写好的示例诊断
		d, err := s.Store.LatestDiagnosis(ctx, site.ID)
		if err != nil {
			s.part(w, "diagnosis", diagnosisView{NoAI: true, Error: "演示账号不能调用 AI。这个网站没有示例诊断，可以去「JSON 工具箱」或「技术博客」点图上的异常点看看。注册后就能诊断自己的网站了。"})
			return
		}
		v := diagnosisView{D: d, Done: true, Example: true}
		_ = json.Unmarshal(d.Result, &v.Result)
		s.part(w, "diagnosis", v)
		return
	}
	if st := s.aiStatus(r, "diagnose"); !st.Ready {
		s.part(w, "diagnosis", diagnosisView{NoAI: true, Error: st.Msg})
		return
	}
	// 标题和问题都会写进给 AI 的提示里，限制长度（表单可以被随意改，不能只靠页面上的输入框）
	title := truncate(strings.TrimSpace(r.FormValue("title")), 120)
	question := title
	target := map[string]any{}
	if sid := formID(r, "signal"); sid > 0 {
		if sig, err := s.Store.SignalByID(ctx, sid); err == nil {
			title = "为什么：" + sig.Title
			question = fmt.Sprintf("规则引擎在 %s 检测到：%s。请找出原因。", period.D(sig.EventDate), sig.Title)
			target = map[string]any{"signal_id": sig.ID, "kind": sig.Kind, "date": period.D(sig.EventDate), "evidence": sig.Evidence}
		}
	}
	if q := strings.TrimSpace(r.FormValue("question")); q != "" {
		question = truncate(q, 1000)
	}
	if title == "" {
		title = "诊断：" + site.Name
	}
	id, err := s.Agent.StartDiagnosis(ctx, site, title, question, target)
	if err != nil {
		s.part(w, "diagnosis", diagnosisView{Error: err.Error()})
		return
	}
	d, _ := s.Store.Diagnosis(ctx, id)
	s.part(w, "diagnosis", diagnosisView{D: d})
}

func (s *Server) diagnosePoll(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.Diagnosis(r.Context(), pathID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v := diagnosisView{D: d, Done: d.Status != "running", Error: d.Error}
	if d.Status == "running" && time.Since(d.CreatedAt) > 8*time.Minute {
		v.Done, v.Error = true, "诊断超时了，请重试"
	}
	if len(d.Result) > 0 {
		_ = json.Unmarshal(d.Result, &v.Result)
	}
	s.part(w, "diagnosis", v)
}

// ========== 待办 ==========

func (s *Server) todoToggle(w http.ResponseWriter, r *http.Request) {
	done := r.FormValue("done") == "on" || r.FormValue("done") == "true"
	if err := s.Store.SetTodoDone(r.Context(), pathID(r), done); err != nil {
		s.fail(w, r, err)
		return
	}
	if done {
		toast(w, "记下了，下一期报告会对比改动前后的数据")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) todoAdd(w http.ResponseWriter, r *http.Request) {
	var sid *int64
	if id := formID(r, "site_id"); id > 0 {
		sid = &id
	}
	_, err := s.Store.AddTodo(r.Context(), store.Todo{SiteID: sid, Source: "report", SourceID: formID(r, "report_id"), Text: truncate(strings.TrimSpace(r.FormValue("text")), 300), Why: truncate(strings.TrimSpace(r.FormValue("why")), 500)})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "已加入待办，下一期报告会检查效果")
	w.Write([]byte(`<button class="btn sm added" disabled>已加入 ✓</button>`))
}

// diagnoseSave 把诊断结论保存起来，下一期报告会附上。
func (s *Server) diagnoseSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := s.Store.Diagnosis(ctx, pathID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var res agent.DiagnosisResult
	if len(d.Result) == 0 || json.Unmarshal(d.Result, &res) != nil || res.Conclusion == "" {
		http.Error(w, "诊断还没完成", 400)
		return
	}
	var sb strings.Builder
	sb.WriteString(res.Conclusion)
	if res.Caveat != "" {
		sb.WriteString("\n\n" + res.Caveat)
	}
	if len(res.Suggestions) > 0 {
		sb.WriteString("\n\n建议：\n")
		for i, x := range res.Suggestions {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, x)
		}
	}
	sid := d.SiteID
	if err := s.Store.AddNote(ctx, store.Note{SiteID: &sid, Source: "diagnosis", SourceID: d.ID, Title: d.Title, Content: sb.String()}); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "已保存，下一期报告会附上这次诊断")
	w.Write([]byte(`<button class="btn" type="button" disabled>已保存到报告 ✓</button>`))
}

type pageAnalysisView struct {
	Site     store.Site
	Ranges   []struct{ Key, Label string }
	Range    period.Range
	P        metrics.PageAnalysis
	Tiles    []metrics.Tile
	Chart    chartOpts
	Diagnose string
}

// sitePageAnalysis 是“单页分析”：一个页面的流量、来源和搜索词。
func (s *Server) sitePageAnalysis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	site, rg, err := s.siteAndRange(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Redirect(w, r, fmt.Sprintf("/sites/%d", site.ID), http.StatusSeeOther)
		return
	}
	p := s.M.PageAnalysis(ctx, site, rg, path)
	if rg.Hourly {
		rg = s.M.SyncedRange(ctx, site, "d7")
	}
	v := pageAnalysisView{Site: site, Ranges: period.Keys[2:], Range: rg, P: p}
	v.Tiles = []metrics.Tile{
		{Label: "浏览", Tip: "这个页面被浏览的次数", Kind: "count", Better: 1, Cur: p.Views, Prev: p.PrevViews, HasVal: true},
		{Label: "平均停留", Tip: "每次浏览平均真正在看页面的时间", Kind: "dur", Better: 1, Cur: p.Dwell, Prev: p.PrevDwell, HasVal: p.Views > 0},
		{Label: "作为入口", Tip: "有多少次访问是从这个页面进入网站的", Kind: "count", Better: 1, Cur: p.Entries, Prev: p.PrevEntries, HasVal: true},
		{Label: "认真阅读比例", Tip: "从这个页面进来的访问里，停留超过 10 秒或看了 2 页以上的占比", Kind: "pct", Better: 1, Cur: p.EngRate, Prev: p.PrevEngRate, HasVal: p.Entries > 0},
		{Label: "搜索点击", Tip: "在 Google 搜索结果里点到这个页面的次数", Kind: "count", Better: 1, Cur: p.Clicks, Prev: p.PrevClicks, HasVal: p.HasSearch, Note: "没有搜索数据"},
		{Label: "搜索曝光", Tip: "这个页面出现在 Google 搜索结果里的次数", Kind: "count", Better: 1, Cur: p.Impr, Prev: p.PrevImpr, HasVal: p.HasSearch, Note: "没有搜索数据"},
		{Label: "点击率", Tip: "搜索点击 ÷ 搜索曝光", Kind: "pct", Better: 1, Cur: ratio0(p.Clicks, p.Impr), Prev: ratio0(p.PrevClicks, p.PrevImpr), HasVal: p.Impr > 0, Note: "没有搜索数据"},
		{Label: "平均排名", Tip: "在 Google 搜索结果中的平均位置，数字越小越靠前", Kind: "pos", Better: -1, Cur: p.Pos, Prev: p.PrevPos, HasVal: p.Impr > 0, Note: "没有搜索数据"},
	}
	v.Chart = chartJSON(p.Series, "", "浏览", "count")
	v.Chart.Axes, v.Chart.Zero = true, true
	name := metrics.BookTitle(p.Title, p.Path)
	q := url.Values{}
	q.Set("title", "分析页面："+name)
	q.Set("question", fmt.Sprintf("请分析页面 %s（路径 %s）在 %s 到 %s 的表现：流量从哪来、和上一期相比为什么涨或跌、搜索表现怎么样，以及最值得做的 2~3 个改进。", name, p.Path, period.D(rg.Start), period.D(rg.End)))
	v.Diagnose = fmt.Sprintf("/sites/%d/diagnose?%s", site.ID, q.Encode())
	title := p.Path
	if p.Title != "" {
		title = p.Title
	}
	s.page(w, r, "page", title, "overview", v)
}

func ratio0(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
