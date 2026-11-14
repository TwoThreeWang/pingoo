package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"pingoo/internal/google"
	"pingoo/internal/llm"
	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/store"
)

// 工具：AI 自己决定查什么。优先用本地数据（快、不耗配额），必要时再实时查 GA / Search Console。

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

var (
	pSite  = map[string]any{"type": "integer", "description": "网站 id（见系统提示里的网站列表）"}
	pStart = map[string]any{"type": "string", "description": "开始日期 YYYY-MM-DD"}
	pEnd   = map[string]any{"type": "string", "description": "结束日期 YYYY-MM-DD（含）"}
)

func toolDefs() []llm.Tool {
	def := func(name, desc string, params map[string]any) llm.Tool {
		return llm.Tool{Type: "function", Function: llm.ToolFunction{Name: name, Description: desc, Parameters: params}}
	}
	return []llm.Tool{
		def("site_overview", "某个网站一段时间的核心数字，并和紧挨着的上一段等长时间对比：访客（去重）、新访客、会话、浏览量、认真阅读比例、平均停留、搜索点击/曝光/排名，有广告收益的网站还有广告收入。数字已经格式化好、变化已经算好，引用时直接用，不要自己换算；平均排名越小越好；搜索数据有 2~3 天延迟，已自动截到最新日期。", obj(map[string]any{
			"site_id": pSite, "start_date": pStart, "end_date": pEnd}, "site_id", "start_date", "end_date")),
		def("daily_metric", "某个指标每天的数值，用来找出变化从哪天开始。", obj(map[string]any{
			"site_id": pSite, "start_date": pStart, "end_date": pEnd,
			"metric": map[string]any{"type": "string", "enum": []string{"visitors", "new_visitors", "sessions", "engaged_sessions", "page_views", "search_clicks", "search_impressions", "search_position", "ad_revenue", "ad_rpm"}},
		}, "site_id", "metric", "start_date", "end_date")),
		def("breakdown", "按某个维度拆分，同时给出本期和上一段等长时间的对比（来自本地同步数据）。dimension：pages 是页面浏览，landing 是入口页，channels 是渠道，sources 是来源网站，queries 是搜索词（Search Console），search_pages 是搜索带来的页面。",
			obj(map[string]any{
				"site_id": pSite, "start_date": pStart, "end_date": pEnd,
				"dimension": map[string]any{"type": "string", "enum": []string{"pages", "landing", "channels", "sources", "countries", "devices", "browsers", "queries", "search_pages"}},
				"filter":    map[string]any{"type": "string", "description": "可选。pages/landing/search_pages/queries 时按路径过滤（包含匹配）；sources 时按来源过滤；queries 时也可按搜索词过滤，写成 query:xxx"},
				"limit":     map[string]any{"type": "integer", "description": "最多返回几行，默认 15，最多 50"},
			}, "site_id", "dimension", "start_date", "end_date")),
		def("daily_breakdown", "按某个维度看每天的数值：一行一天，一列是这个维度里的一个成员（按整段时间的合计取前几名）。用来一次看清变化从哪天开始、出在哪个渠道 / 来源 / 页面 / 搜索词。channels、sources、landing（入口页）、countries、devices 的数值是访问次数，pages 是浏览量，queries、search_pages 是搜索点击。最多 92 天。",
			obj(map[string]any{
				"site_id": pSite, "start_date": pStart, "end_date": pEnd,
				"dimension": map[string]any{"type": "string", "enum": []string{"channels", "sources", "landing", "countries", "devices", "pages", "queries", "search_pages"}},
				"filter":    map[string]any{"type": "string", "description": "可选。只看名字里包含这段文字的成员，例如某个页面路径、某个来源、某个搜索词"},
				"top":       map[string]any{"type": "integer", "description": "最多几列，默认 6，最多 10"},
			}, "site_id", "dimension", "start_date", "end_date")),
		def("list_signals", "规则引擎检测到的“需要关注”的信号（异常、飙升、排名下滑、机会等），一行一条，带日期和证据。", obj(map[string]any{
			"site_id": map[string]any{"type": "integer", "description": "可选，不填表示全部网站"}})),
		def("ga_report", "实时调用 GA4 Data API 的 runReport，可以用任意 GA4 维度和指标（例如 dimensions=[\"sessionSource\",\"landingPage\"]，metrics=[\"sessions\",\"engagementRate\"]）。只有本地数据不够时才用，会消耗配额。",
			obj(map[string]any{
				"site_id": pSite, "start_date": pStart, "end_date": pEnd,
				"dimensions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"metrics":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"filter": map[string]any{"type": "object", "description": "可选的维度过滤", "properties": map[string]any{
					"field": map[string]any{"type": "string"}, "match": map[string]any{"type": "string", "enum": []string{"EXACT", "CONTAINS", "BEGINS_WITH", "FULL_REGEXP"}},
					"value": map[string]any{"type": "string"}, "not": map[string]any{"type": "boolean"}}},
				"limit": map[string]any{"type": "integer", "description": "默认 20，最多 100"},
			}, "site_id", "start_date", "end_date", "metrics")),
		def("gsc_query", "实时查询 Search Console 的搜索数据。dimensions 可选 date、query、page、device、country。", obj(map[string]any{
			"site_id": pSite, "start_date": pStart, "end_date": pEnd,
			"dimensions": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"date", "query", "page", "device", "country"}}},
			"filter": map[string]any{"type": "object", "properties": map[string]any{
				"dimension":  map[string]any{"type": "string", "enum": []string{"query", "page", "device", "country"}},
				"operator":   map[string]any{"type": "string", "enum": []string{"equals", "contains", "notContains", "includingRegex"}},
				"expression": map[string]any{"type": "string"}}},
			"limit": map[string]any{"type": "integer", "description": "默认 20，最多 100"},
		}, "site_id", "start_date", "end_date", "dimensions")),
	}
}

type toolEnv struct {
	a *Agent
}

func (a *Agent) handlers() map[string]llm.ToolFunc {
	e := toolEnv{a: a}
	return map[string]llm.ToolFunc{
		"site_overview":   e.siteOverview,
		"daily_metric":    e.dailyMetric,
		"breakdown":       e.breakdown,
		"daily_breakdown": e.dailyBreakdown,
		"list_signals":    e.listSignals,
		"ga_report":       e.gaReport,
		"gsc_query":       e.gscQuery,
	}
}

type baseArgs struct {
	SiteID    int64  `json:"site_id"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

func (e toolEnv) siteAndRange(ctx context.Context, b baseArgs) (store.Site, period.Range, error) {
	site, err := e.a.Store.Site(ctx, b.SiteID)
	if err != nil {
		return site, period.Range{}, fmt.Errorf("没有 id 为 %d 的网站", b.SiteID)
	}
	loc := site.Location()
	s, err1 := time.ParseInLocation("2006-01-02", b.StartDate, loc)
	t, err2 := time.ParseInLocation("2006-01-02", b.EndDate, loc)
	if err1 != nil || err2 != nil || t.Before(s) {
		return site, period.Range{}, errors.New("日期格式应为 YYYY-MM-DD，且结束日期不早于开始日期")
	}
	if t.Sub(s) > 500*24*time.Hour {
		return site, period.Range{}, errors.New("时间跨度太长，最多约 16 个月")
	}
	return site, period.Custom(s, t), nil
}

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func short(d string) string { return strings.TrimPrefix(d[5:], "") }

func (e toolEnv) siteOverview(ctx context.Context, raw json.RawMessage) (string, string, error) {
	var b baseArgs
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", "", err
	}
	site, r, err := e.siteAndRange(ctx, b)
	if err != nil {
		return "", "", err
	}
	// 和写进提示里的核心数字同一种写法（一行一个指标）：比 JSON 省一小半 token，AI 也不用在两种格式之间来回对
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s 的核心数字（本期 %s ~ %s，上期 %s ~ %s）：\n", site.Name, period.D(r.Start), period.D(r.End), period.D(r.PrevStart), period.D(r.PrevEnd))
	line := func(t metrics.Tile, always bool) {
		if !t.HasVal && !always {
			fmt.Fprintf(&sb, "- %s：%s\n", t.Label, t.Note)
			return
		}
		c, p, ch := tileText(t)
		fmt.Fprintf(&sb, "- %s：本期 %s，上期 %s（%s）\n", t.Label, c, p, ch)
	}
	for _, t := range e.a.M.Tiles(ctx, site, r) {
		line(t, false)
	}
	if e.a.M.HasAds(ctx, site.ID) {
		sq := r.Sequential()
		for _, t := range metrics.AdTiles(e.a.M.AdTotals(ctx, site.ID, sq.Start, sq.End), e.a.M.AdTotals(ctx, site.ID, sq.PrevStart, sq.PrevEnd)) {
			line(t, true) // 和原来一样：广告的几个数总是给出
		}
		fmt.Fprintf(&sb, "广告收入的单位是 %s。\n", site.Currency)
	}
	return sb.String(), fmt.Sprintf("概览 · %s · %s..%s", site.Name, short(b.StartDate), short(b.EndDate)), nil
}

func round(v float64) float64 {
	if v > 100 {
		return float64(int64(v + 0.5))
	}
	return float64(int64(v*1000+0.5)) / 1000
}

var dailyExpr = map[string][2]string{
	"visitors":           {"ga_daily", "sum(active_users)::float8"},
	"new_visitors":       {"ga_daily", "sum(new_users)::float8"},
	"sessions":           {"ga_daily", "sum(sessions)::float8"},
	"engaged_sessions":   {"ga_daily", "sum(engaged_sessions)::float8"},
	"page_views":         {"ga_daily", "sum(page_views)::float8"},
	"search_clicks":      {"gsc_daily", "sum(clicks)::float8"},
	"search_impressions": {"gsc_daily", "sum(impressions)::float8"},
	"search_position":    {"gsc_daily", "sum(pos_x_impr)/nullif(sum(impressions),0)"},
	"ad_revenue":         {"ga_daily", "sum(ad_revenue)"},
	"ad_rpm":             {"ga_daily", "sum(ad_revenue)*1000/nullif(sum(page_views),0)"},
}

func (e toolEnv) dailyMetric(ctx context.Context, raw json.RawMessage) (string, string, error) {
	var b struct {
		baseArgs
		Metric string `json:"metric"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", "", err
	}
	site, r, err := e.siteAndRange(ctx, b.baseArgs)
	if err != nil {
		return "", "", err
	}
	ex, ok := dailyExpr[b.Metric]
	if !ok {
		return "", "", errors.New("不支持的指标")
	}
	s := e.a.M.DailySeries(ctx, site.ID, r, ex[0], ex[1])
	var sb strings.Builder
	sb.WriteString("date,value\n")
	for i, d := range s.Dates {
		fmt.Fprintf(&sb, "%s,%v\n", period.D(d), round(s.Cur[i]))
	}
	return sb.String(), fmt.Sprintf("每日 %s · %s · %s..%s", b.Metric, site.Name, short(b.StartDate), short(b.EndDate)), nil
}

func (e toolEnv) breakdown(ctx context.Context, raw json.RawMessage) (string, string, error) {
	var b struct {
		baseArgs
		Dimension string `json:"dimension"`
		Filter    string `json:"filter"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", "", err
	}
	site, r, err := e.siteAndRange(ctx, b.baseArgs)
	if err != nil {
		return "", "", err
	}
	if b.Limit <= 0 || b.Limit > 50 {
		b.Limit = 15
	}
	type spec struct {
		table, dim, val, extra string
	}
	specs := map[string]spec{
		"pages":        {"ga_daily_pages", "path || ' | ' || max(title)", "sum(page_views)", ""},
		"landing":      {"ga_daily_landing", "path", "sum(sessions)", "sum(engaged_sessions)"},
		"channels":     {"ga_daily_sources", "channel", "sum(sessions)", "sum(engaged_sessions)"},
		"sources":      {"ga_daily_sources", "lower(source) || ' / ' || medium", "sum(sessions)", "sum(engaged_sessions)"},
		"countries":    {"ga_daily_geo", "country", "sum(sessions)", "sum(engaged_sessions)"},
		"devices":      {"ga_daily_tech", "device", "sum(sessions)", "sum(engaged_sessions)"},
		"browsers":     {"ga_daily_tech", "browser", "sum(sessions)", "sum(engaged_sessions)"},
		"queries":      {"gsc_daily_queries", "query", "sum(clicks)", "sum(impressions)"},
		"search_pages": {"gsc_daily_pages", "path", "sum(clicks)", "sum(impressions)"},
	}
	sp, ok := specs[b.Dimension]
	if !ok {
		return "", "", errors.New("不支持的维度")
	}
	if strings.HasPrefix(sp.table, "gsc") {
		if latest := e.a.M.GSCLatest(ctx, site.ID); !latest.IsZero() {
			r = r.ClampEnd(latest)
		}
	}
	groupCol := strings.Split(sp.dim, " ||")[0]
	if b.Dimension == "sources" {
		groupCol = "lower(source), medium"
	}
	where := ""
	args := []any{site.ID, r.Start, r.End, r.PrevStart, r.PrevEnd}
	if f := strings.TrimSpace(b.Filter); f != "" {
		switch {
		case strings.HasPrefix(f, "query:") && b.Dimension == "queries":
			where = " AND query ILIKE '%'||$6||'%'"
			args = append(args, strings.TrimPrefix(f, "query:"))
		case b.Dimension == "sources" || b.Dimension == "channels":
			where = " AND (source ILIKE '%'||$6||'%' OR channel ILIKE '%'||$6||'%')"
			args = append(args, f)
		case sp.table == "ga_daily_pages" || sp.table == "ga_daily_landing" || strings.HasPrefix(sp.table, "gsc"):
			where = " AND path ILIKE '%'||$6||'%'"
			args = append(args, f)
		}
	}
	extraSel := "0"
	if sp.extra != "" {
		extraSel = fmt.Sprintf("coalesce(%s FILTER (WHERE date BETWEEN $2 AND $3),0)", sp.extra)
	}
	posSel := "0"
	if strings.HasPrefix(sp.table, "gsc") {
		posSel = "coalesce(sum(pos_x_impr) FILTER (WHERE date BETWEEN $2 AND $3)/nullif(sum(impressions) FILTER (WHERE date BETWEEN $2 AND $3),0),0)"
	}
	q := fmt.Sprintf(`SELECT %s, coalesce(%s FILTER (WHERE date BETWEEN $2 AND $3),0), coalesce(%s FILTER (WHERE date BETWEEN $4 AND $5),0), %s, %s
		FROM %s WHERE site_id=$1 AND date BETWEEN $4 AND $3 %s GROUP BY %s ORDER BY 2 DESC LIMIT %d`,
		sp.dim, sp.val, sp.val, extraSel, posSel, sp.table, where, groupCol, b.Limit)
	rows, err := e.a.Store.Pool.Query(ctx, q, args...)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	head := map[string]string{
		"pages": "path | title,page_views,prev_page_views", "queries": "query,clicks,prev_clicks,impressions,position",
		"search_pages": "path,clicks,prev_clicks,impressions,position",
	}[b.Dimension]
	if head == "" {
		head = b.Dimension + ",sessions,prev_sessions,engaged_sessions"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "本期 %s~%s，上期 %s~%s\n%s\n", period.D(r.Start), period.D(r.End), period.D(r.PrevStart), period.D(r.PrevEnd), head)
	for rows.Next() {
		var k string
		var v1, v2, x, pos float64
		if err := rows.Scan(&k, &v1, &v2, &x, &pos); err != nil {
			return "", "", err
		}
		if b.Dimension == "channels" {
			k = metrics.ChannelName(k) // 用中文渠道名，免得 AI 照抄英文
		}
		switch b.Dimension {
		case "pages":
			fmt.Fprintf(&sb, "%s,%v,%v\n", k, v1, v2)
		case "queries", "search_pages":
			fmt.Fprintf(&sb, "%s,%v,%v,%v,%.1f\n", k, v1, v2, x, pos)
		default:
			fmt.Fprintf(&sb, "%s,%v,%v,%v\n", k, v1, v2, x)
		}
	}
	trace := fmt.Sprintf("拆分 %s · %s · %s..%s", b.Dimension, site.Name, short(period.D(r.Start)), short(period.D(r.End)))
	if b.Filter != "" {
		trace += " · 过滤 " + b.Filter
	}
	return sb.String(), trace, nil
}

// dailySpecs 是 daily_breakdown 每个维度查哪张表、按什么分组、加哪一列。
var dailySpecs = map[string]struct{ table, key, val, unit, label string }{
	"channels":     {"ga_daily_sources", "channel", "sum(sessions)", "访问次数", "渠道"},
	"sources":      {"ga_daily_sources", "lower(source)", "sum(sessions)", "访问次数", "来源网站"},
	"landing":      {"ga_daily_landing", "path", "sum(sessions)", "访问次数", "入口页"},
	"countries":    {"ga_daily_geo", "country", "sum(sessions)", "访问次数", "国家"},
	"devices":      {"ga_daily_tech", "device", "sum(sessions)", "访问次数", "设备"},
	"pages":        {"ga_daily_pages", "path", "sum(page_views)", "浏览量", "页面"},
	"queries":      {"gsc_daily_queries", "query", "sum(clicks)", "搜索点击", "搜索词"},
	"search_pages": {"gsc_daily_pages", "path", "sum(clicks)", "搜索点击", "搜索带来的页面"},
}

// dailyBreakdown 一次给出“每天 × 维度成员”的表。诊断里最常见的问题是“从哪天开始、出在哪个渠道”，
// 没有它的时候 AI 只能把日期切成几段、反复查 breakdown（实际出现过一次诊断查 6 遍渠道）。
func (e toolEnv) dailyBreakdown(ctx context.Context, raw json.RawMessage) (string, string, error) {
	var b struct {
		baseArgs
		Dimension string `json:"dimension"`
		Filter    string `json:"filter"`
		Top       int    `json:"top"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", "", err
	}
	site, r, err := e.siteAndRange(ctx, b.baseArgs)
	if err != nil {
		return "", "", err
	}
	sp, ok := dailySpecs[b.Dimension]
	if !ok {
		return "", "", errors.New("不支持的维度")
	}
	if strings.HasPrefix(sp.table, "gsc") {
		if latest := e.a.M.GSCLatest(ctx, site.ID); !latest.IsZero() {
			r = r.ClampEnd(latest)
		}
	}
	if r.Days > 92 {
		return "", "", errors.New("时间跨度太长，daily_breakdown 最多 92 天；更长的范围请缩短，或者用 daily_metric 看整体")
	}
	if b.Top <= 0 || b.Top > 10 {
		b.Top = 6
	}
	where, args := "", []any{site.ID, r.Start, r.End}
	if f := strings.TrimSpace(b.Filter); f != "" {
		where = " AND " + sp.key + " ILIKE '%'||$4||'%'"
		args = append(args, f)
	}
	// 先按整段时间的合计取前几名，再查这几名每天的数
	rows, err := e.a.Store.Pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE site_id=$1 AND date BETWEEN $2 AND $3%s
		GROUP BY 1 ORDER BY %s DESC, 1 LIMIT %d`, sp.key, sp.table, where, sp.val, b.Top), args...)
	if err != nil {
		return "", "", err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", "", err
	}
	trace := fmt.Sprintf("每日 %s · %s · %s..%s", b.Dimension, site.Name, short(period.D(r.Start)), short(period.D(r.End)))
	if b.Filter != "" {
		trace += " · 过滤 " + b.Filter
	}
	if len(keys) == 0 {
		return "（没有数据）\n", trace, nil
	}
	rows, err = e.a.Store.Pool.Query(ctx, fmt.Sprintf(`SELECT date, %s, %s::float8 FROM %s
		WHERE site_id=$1 AND date BETWEEN $2 AND $3 AND %s = ANY($4) GROUP BY 1, 2`, sp.key, sp.val, sp.table, sp.key),
		site.ID, r.Start, r.End, keys)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	vals := map[string]float64{} // 日期 + 成员 → 数值
	for rows.Next() {
		var d time.Time
		var k string
		var v float64
		if err := rows.Scan(&d, &k, &v); err != nil {
			return "", "", err
		}
		vals[period.D(d)+"\x00"+k] = v
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "每天的%s，按%s分列（按这段时间的合计从多到少，没有数据的日子记 0）\ndate", sp.unit, sp.label)
	for _, k := range keys {
		name := k
		if b.Dimension == "channels" {
			name = metrics.ChannelName(k) // 用中文渠道名，免得 AI 照抄英文
		}
		sb.WriteString("," + strings.ReplaceAll(name, ",", " "))
	}
	sb.WriteString("\n")
	for _, d := range r.Dates() {
		sb.WriteString(period.D(d))
		for _, k := range keys {
			fmt.Fprintf(&sb, ",%v", vals[period.D(d)+"\x00"+k])
		}
		sb.WriteString("\n")
	}
	return sb.String(), trace, nil
}

func (e toolEnv) listSignals(ctx context.Context, raw json.RawMessage) (string, string, error) {
	var b struct {
		SiteID int64 `json:"site_id"`
	}
	_ = json.Unmarshal(raw, &b)
	sigs, err := e.a.Store.OpenSignals(ctx, b.SiteID, 30)
	if err != nil {
		return "", "", err
	}
	if len(sigs) == 0 {
		return "（这段时间没有检测到需要关注的信号）\n", "读取需要关注的信号", nil
	}
	var sb strings.Builder
	sb.WriteString("[id][严重程度][网站][日期][类型] 标题 · 证据\n")
	for _, s := range sigs {
		fmt.Fprintf(&sb, "[%d][%s][%s][%s][%s] %s", s.ID, s.Severity, s.SiteName, period.D(s.EventDate), s.Kind, s.Title)
		if len(s.Evidence) > 0 {
			sb.WriteString(" · " + js(s.Evidence))
		}
		sb.WriteString("\n")
	}
	return sb.String(), "读取需要关注的信号", nil
}

func (e toolEnv) gaReport(ctx context.Context, raw json.RawMessage) (string, string, error) {
	var b struct {
		baseArgs
		Dimensions []string `json:"dimensions"`
		Metrics    []string `json:"metrics"`
		Filter     *struct {
			Field string `json:"field"`
			Match string `json:"match"`
			Value string `json:"value"`
			Not   bool   `json:"not"`
		} `json:"filter"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", "", err
	}
	site, r, err := e.siteAndRange(ctx, b.baseArgs)
	if err != nil {
		return "", "", err
	}
	trace := fmt.Sprintf("ga.runReport  dims=[%s]  metrics=[%s]  %s..%s", strings.Join(b.Dimensions, ","), strings.Join(b.Metrics, ","), short(b.StartDate), short(b.EndDate))
	if site.Demo || site.GAProperty == "" {
		return "", trace, errors.New("这个网站没有连接 GA（演示网站），请改用 breakdown / daily_metric 查本地数据")
	}
	gc, err := e.a.Sync.Client(ctx)
	if err != nil {
		return "", trace, err
	}
	if b.Limit <= 0 || b.Limit > 100 {
		b.Limit = 20
	}
	req := google.ReportRequest{
		Ranges:     []google.DateRange{{StartDate: period.D(r.Start), EndDate: period.D(r.End)}},
		Dimensions: b.Dimensions, Metrics: b.Metrics, Limit: b.Limit,
	}
	if len(b.Metrics) > 0 {
		req.OrderBy = b.Metrics[0]
	}
	if b.Filter != nil && b.Filter.Field != "" {
		req.Filters = []google.Filter{{Field: b.Filter.Field, Match: b.Filter.Match, Value: b.Filter.Value, Not: b.Filter.Not}}
		trace += fmt.Sprintf("  filter=%s~%s", b.Filter.Field, b.Filter.Value)
	}
	rep, err := gc.RunReport(ctx, site.GAProperty, req)
	if err != nil {
		return "", trace, err
	}
	var sb strings.Builder
	sb.WriteString(strings.Join(append(append([]string{}, rep.Dimensions...), b.Metrics...), ",") + "\n")
	for _, row := range rep.Rows {
		parts := append([]string{}, row.Dims...)
		for _, m := range row.Metrics {
			parts = append(parts, fmt.Sprint(round(m)))
		}
		sb.WriteString(strings.Join(parts, ",") + "\n")
	}
	if len(rep.Rows) == 0 {
		sb.WriteString("（没有数据）\n")
	}
	return sb.String(), trace, nil
}

func (e toolEnv) gscQuery(ctx context.Context, raw json.RawMessage) (string, string, error) {
	var b struct {
		baseArgs
		Dimensions []string `json:"dimensions"`
		Filter     *struct {
			Dimension  string `json:"dimension"`
			Operator   string `json:"operator"`
			Expression string `json:"expression"`
		} `json:"filter"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", "", err
	}
	site, r, err := e.siteAndRange(ctx, b.baseArgs)
	if err != nil {
		return "", "", err
	}
	if b.Limit <= 0 || b.Limit > 100 {
		b.Limit = 20
	}
	trace := fmt.Sprintf("gsc.query  dims=[%s]  %s..%s", strings.Join(b.Dimensions, ","), short(b.StartDate), short(b.EndDate))
	if b.Filter != nil && b.Filter.Expression != "" {
		trace += fmt.Sprintf("  %s %s \"%s\"", b.Filter.Dimension, b.Filter.Operator, b.Filter.Expression)
	}
	if site.Demo || site.GSCSite == "" {
		return "", trace, errors.New("这个网站没有连接 Search Console（或是演示网站），请改用 breakdown 的 queries / search_pages 查本地数据")
	}
	gc, err := e.a.Sync.Client(ctx)
	if err != nil {
		return "", trace, err
	}
	var fs []google.GSCFilter
	if b.Filter != nil && b.Filter.Expression != "" {
		fs = append(fs, google.GSCFilter{Dimension: b.Filter.Dimension, Operator: b.Filter.Operator, Expression: b.Filter.Expression})
	}
	rows, err := gc.SearchAnalytics(ctx, site.GSCSite, period.D(r.Start), period.D(r.End), b.Dimensions, fs, b.Limit)
	if err != nil {
		return "", trace, err
	}
	var sb strings.Builder
	sb.WriteString(strings.Join(b.Dimensions, ",") + ",clicks,impressions,ctr,position\n")
	for _, x := range rows {
		fmt.Fprintf(&sb, "%s,%v,%v,%.3f,%.1f\n", strings.Join(x.Keys, ","), x.Clicks, x.Impressions, x.CTR, x.Position)
	}
	if len(rows) == 0 {
		sb.WriteString("（没有数据）\n")
	}
	return sb.String(), trace, nil
}

// 让 metrics 包的导出函数在这里可用（避免未使用导入）。
var _ = metrics.ExpectedCTR

// ToolCheck 用 AI 的几个查数据工具查一遍某个网站，有一个查不到就返回错误。
// 测试里用它确认：工具和网页一样按用户隔离，AI 拿着别人的网站 ID 也查不到。
func (a *Agent) ToolCheck(ctx context.Context, siteID int64) error {
	end := time.Now().AddDate(0, 0, -1)
	args := fmt.Sprintf(`{"site_id":%d,"start_date":"%s","end_date":"%s","metric":"visitors","dimension":"pages"}`,
		siteID, period.D(end.AddDate(0, 0, -6)), period.D(end))
	for _, name := range []string{"site_overview", "daily_metric", "breakdown", "daily_breakdown"} {
		if _, _, err := a.handlers()[name](ctx, json.RawMessage(args)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}
