package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pingoo/internal/period"
	"pingoo/internal/store"
)

// PageRow 是内容表里的一行。
type PageRow struct {
	Path      string
	Title     string
	Views     int
	PrevViews int
	Dwell     float64 // 每次浏览的平均停留秒数
	Main      string  // “Reddit 80%”或者原因
	Extra     string  // 不同标签页的附加列
	FirstSeen time.Time
	Sessions  int
	EngRate   float64
	PPV       float64
}

var ContentTabs = []struct{ Key, Label string }{
	{"rising", "飙升中"}, {"declining", "下滑中"}, {"evergreen", "常青"}, {"fresh", "新冒头"}, {"landing", "入口页"}, {"optimize", "待优化"},
}

// pageAgg 汇总本期/对比期每个页面的浏览量，只返回两期里至少有一期达到 minViews 的页面。
// 不查标题：大网站有几十万个页面，标题只给最后展示的那几行查（见 pageTitles）。
func (m *Svc) pageAgg(ctx context.Context, site store.Site, r period.Range, minViews int) ([]PageRow, error) {
	rows, err := m.Store.Pool.Query(ctx, `
		SELECT path,
		  coalesce(sum(page_views) FILTER (WHERE date BETWEEN $2 AND $3),0) AS cur,
		  coalesce(sum(page_views) FILTER (WHERE date BETWEEN $4 AND $5),0) AS prev,
		  coalesce(sum(engagement_seconds) FILTER (WHERE date BETWEEN $2 AND $3),0)
		FROM ga_daily_pages
		WHERE site_id=$1 AND date BETWEEN $4 AND $3 AND NOT (path LIKE ANY($6::text[]))
		GROUP BY path
		HAVING greatest(coalesce(sum(page_views) FILTER (WHERE date BETWEEN $2 AND $3),0),
		                coalesce(sum(page_views) FILTER (WHERE date BETWEEN $4 AND $5),0)) >= $7`,
		site.ID, r.Start, r.End, r.PrevStart, r.PrevEnd, excludePatterns(site), minViews)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageRow
	for rows.Next() {
		var p PageRow
		var eng float64
		if err := rows.Scan(&p.Path, &p.Views, &p.PrevViews, &eng); err != nil {
			return nil, err
		}
		p.Dwell = ratio(eng, float64(p.Views))
		out = append(out, p)
	}
	return out, rows.Err()
}

// pageTitles 给几个页面查标题（取浏览最多的那天的标题）。
// 同步时把标题跟着搜索词变的页面（例如 /search）存成空标题；这类页面多数日子是空标题时只显示路径。
func (m *Svc) pageTitles(ctx context.Context, siteID int64, from, to time.Time, paths []string) map[string]string {
	out := map[string]string{}
	if len(paths) == 0 {
		return out
	}
	rows, err := m.Store.Pool.Query(ctx, `SELECT path, CASE WHEN count(*) FILTER (WHERE title = '') * 2 > count(*) THEN ''
		  ELSE coalesce((array_agg(title ORDER BY page_views DESC) FILTER (WHERE title <> ''))[1], '') END
		FROM ga_daily_pages WHERE site_id=$1 AND path = ANY($2) AND date BETWEEN $3 AND $4 GROUP BY path`, siteID, paths, from, to)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p, t string
		if rows.Scan(&p, &t) == nil {
			out[p] = t
		}
	}
	return out
}

// mainSources 返回每个落地页本期最主要的来源，例如“Reddit 80%”。
func (m *Svc) mainSources(ctx context.Context, siteID int64, from, to time.Time, paths []string) map[string]string {
	out := map[string]string{}
	if len(paths) == 0 {
		return out
	}
	// 先按显示名合并（yandex.tm、tv.yandex.com 都算“Yandex 搜索”），再取最大的一个
	rows, err := m.Store.Pool.Query(ctx, `SELECT path, channel, source, sum(sessions) FROM ga_daily_landing
		WHERE site_id=$1 AND date BETWEEN $2 AND $3 AND path = ANY($4) GROUP BY path, channel, source`, siteID, from, to, paths)
	if err != nil {
		return out
	}
	defer rows.Close()
	byLabel := map[string]map[string]float64{}
	totals := map[string]float64{}
	for rows.Next() {
		var path, channel, source string
		var n float64
		if rows.Scan(&path, &channel, &source, &n) != nil {
			continue
		}
		if byLabel[path] == nil {
			byLabel[path] = map[string]float64{}
		}
		byLabel[path][SourceLabel(channel, source)] += n
		totals[path] += n
	}
	for path, labels := range byLabel {
		best, bestN := "", -1.0
		for l, n := range labels {
			if n > bestN || (n == bestN && l < best) {
				best, bestN = l, n
			}
		}
		if totals[path] > 0 {
			out[path] = fmt.Sprintf("%s %.0f%%", best, bestN/totals[path]*100)
		}
	}
	return out
}

var searchEngines = [][2]string{
	{"baidu", "百度搜索"}, {"yandex", "Yandex 搜索"}, {"ya.ru", "Yandex 搜索"}, {"duckduckgo", "DuckDuckGo"},
	{"sogou", "搜狗搜索"}, {"so.com", "360 搜索"}, {"360", "360 搜索"}, {"sm.cn", "神马搜索"}, {"naver", "Naver 搜索"},
	{"yahoo", "Yahoo 搜索"}, {"ecosia", "Ecosia"}, {"brave", "Brave 搜索"}, {"seznam", "Seznam 搜索"}, {"daum", "Daum 搜索"},
}

// SourceLabel 把渠道和来源变成一个好懂的名字。
func SourceLabel(channel, source string) string {
	s := strings.ToLower(source)
	switch {
	case s == "(other)":
		return "其他（GA 合并的小来源）"
	case IsAISource(s):
		return prettyHost(s)
	case channel == "Organic Search":
		if strings.Contains(s, "google") {
			return "Google 搜索"
		}
		if strings.Contains(s, "bing") {
			return "Bing 搜索"
		}
		for _, e := range searchEngines {
			if strings.Contains(s, e[0]) {
				return e[1]
			}
		}
		return "搜索引擎"
	case channel == "Direct" || s == "(direct)":
		return "直接访问"
	case channel == "Email" || strings.Contains(s, "mail"):
		return "邮件"
	}
	return prettyHost(s)
}

var knownHosts = map[string]string{
	"reddit.com": "Reddit", "news.ycombinator.com": "Hacker News", "v2ex.com": "V2EX", "t.co": "X", "x.com": "X", "twitter.com": "X",
	"github.com": "GitHub", "producthunt.com": "Product Hunt", "lobste.rs": "Lobsters", "zhihu.com": "知乎", "weibo.com": "微博",
	"juejin.cn": "掘金", "chatgpt.com": "ChatGPT", "chat.openai.com": "ChatGPT", "perplexity.ai": "Perplexity", "claude.ai": "Claude",
	"gemini.google.com": "Gemini", "copilot.microsoft.com": "Copilot", "linkedin.com": "LinkedIn", "facebook.com": "Facebook",
	"youtube.com": "YouTube", "bilibili.com": "B 站", "xiaohongshu.com": "小红书", "jike.city": "即刻", "okjike.com": "即刻",
	"kimi.com": "Kimi", "kimi.moonshot.cn": "Kimi", "chat.deepseek.com": "DeepSeek", "doubao.com": "豆包",
}

func prettyHost(s string) string {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(s), "www."), "m.")
	for _, sfx := range []string{"old.", "new."} {
		s = strings.TrimPrefix(s, sfx)
	}
	if v, ok := knownHosts[s]; ok {
		return v
	}
	for k, v := range knownHosts {
		if strings.HasSuffix(s, "."+k) {
			return v
		}
	}
	if s == "" || s == "(not set)" {
		return "未知"
	}
	return s
}

// Content 返回内容表某个标签页的数据。
func (m *Svc) Content(ctx context.Context, site store.Site, r period.Range, tab string) ([]PageRow, error) {
	if r.Hourly {
		r = period.Resolve("d7", time.Now(), site.Location())
	}
	switch tab {
	case "landing":
		return m.landing(ctx, site, r, false)
	case "optimize":
		return m.landing(ctx, site, r, true)
	case "evergreen":
		return m.evergreen(ctx, site)
	case "fresh":
		return m.fresh(ctx, site, r)
	}
	minViews := max(20, r.Days*3)
	all, err := m.pageAgg(ctx, site, r, minViews)
	if err != nil {
		return nil, err
	}
	var out []PageRow
	for _, p := range all {
		switch tab {
		case "declining":
			if p.PrevViews >= minViews && float64(p.Views) < float64(p.PrevViews)*0.8 {
				out = append(out, p)
			}
		default: // rising
			if p.Views >= minViews && float64(p.Views) > float64(p.PrevViews)*1.3 {
				out = append(out, p)
			}
		}
	}
	if tab == "declining" {
		sortBy(out, func(a, b PageRow) bool { return a.PrevViews-a.Views > b.PrevViews-b.Views })
	} else {
		sortBy(out, func(a, b PageRow) bool { return a.Views-a.PrevViews > b.Views-b.PrevViews })
	}
	out = topN(out, 10)
	paths := make([]string, len(out))
	for i, p := range out {
		paths[i] = p.Path
	}
	titles := m.pageTitles(ctx, site.ID, r.PrevStart, r.End, paths)
	for i := range out {
		out[i].Title = titles[out[i].Path]
	}
	if tab == "declining" {
		reasons := m.declineReasons(ctx, site, r, paths)
		for i := range out {
			out[i].Main = reasons[out[i].Path]
		}
	} else {
		src := m.mainSources(ctx, site.ID, r.Start, r.End, paths)
		for i := range out {
			out[i].Main = src[out[i].Path]
		}
	}
	return out, nil
}

// declineReasons 用搜索数据给下滑的页面找一个最可能的原因。
func (m *Svc) declineReasons(ctx context.Context, site store.Site, r period.Range, paths []string) map[string]string {
	out := map[string]string{}
	if len(paths) == 0 {
		return out
	}
	latest := m.GSCLatest(ctx, site.ID)
	cr := r.ClampEnd(latest)
	rows, err := m.Store.Pool.Query(ctx, `SELECT path,
		coalesce(sum(clicks) FILTER (WHERE date BETWEEN $2 AND $3),0), coalesce(sum(clicks) FILTER (WHERE date BETWEEN $4 AND $5),0),
		coalesce(sum(pos_x_impr) FILTER (WHERE date BETWEEN $2 AND $3),0)/nullif(sum(impressions) FILTER (WHERE date BETWEEN $2 AND $3),0),
		coalesce(sum(pos_x_impr) FILTER (WHERE date BETWEEN $4 AND $5),0)/nullif(sum(impressions) FILTER (WHERE date BETWEEN $4 AND $5),0)
		FROM gsc_daily_pages WHERE site_id=$1 AND date BETWEEN $4 AND $3 AND path = ANY($6) GROUP BY path`,
		site.ID, cr.Start, cr.End, cr.PrevStart, cr.PrevEnd, paths)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var path string
			var c1, c2 float64
			var p1, p2 *float64
			if rows.Scan(&path, &c1, &c2, &p1, &p2) != nil {
				continue
			}
			switch {
			case p1 != nil && p2 != nil && *p1-*p2 >= 1.5:
				out[path] = fmt.Sprintf("搜索排名 %.1f → %.1f", *p2, *p1)
			case c2 >= 20 && c1 < c2*0.75:
				out[path] = fmt.Sprintf("搜索点击 −%.0f%%，排名基本不变，搜的人变少了", (1-c1/c2)*100)
			}
		}
	}
	for _, p := range paths {
		if out[p] == "" {
			out[p] = "各渠道同比例下降，可能是季节性或话题热度下降"
		}
	}
	return out
}

func (m *Svc) evergreen(ctx context.Context, site store.Site) ([]PageRow, error) {
	end := m.GALatest(ctx, site.ID)
	if end.IsZero() {
		return nil, nil
	}
	start := end.AddDate(0, 0, -83) // 12 周
	rows, err := m.Store.Pool.Query(ctx, `
		WITH w AS (
		  SELECT path, (date - $2::date) / 7 AS wk, sum(page_views) AS v, sum(engagement_seconds) AS e
		  FROM ga_daily_pages WHERE site_id=$1 AND date BETWEEN $2 AND $3 AND NOT (path LIKE ANY($4::text[]))
		  GROUP BY path, wk)
		SELECT path, '', count(*) FILTER (WHERE v >= 10), sum(v), sum(e)
		FROM w GROUP BY path HAVING count(*) FILTER (WHERE v >= 10) >= 11
		ORDER BY sum(v) DESC LIMIT 10`, site.ID, start, end, excludePatterns(site))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageRow
	var paths []string
	for rows.Next() {
		var p PageRow
		var weeks int
		var v, e float64
		if err := rows.Scan(&p.Path, &p.Title, &weeks, &v, &e); err != nil {
			return nil, err
		}
		p.Views = int(v / 12)
		p.Dwell = ratio(e, v)
		p.Extra = fmt.Sprintf("%d/12", weeks)
		out = append(out, p)
		paths = append(paths, p.Path)
	}
	src := m.mainSources(ctx, site.ID, start, end, paths)
	titles := m.pageTitles(ctx, site.ID, start, end, paths)
	for i := range out {
		out[i].Main = src[out[i].Path]
		out[i].Title = titles[out[i].Path]
	}
	return out, nil
}

func (m *Svc) fresh(ctx context.Context, site store.Site, r period.Range) ([]PageRow, error) {
	// 本期浏览 ≥ 20 的页面里，找出前 120 天没出现过的；只给最后 10 个查标题
	rows, err := m.Store.Pool.Query(ctx, `
		WITH c AS (
		  SELECT path, sum(page_views) AS v, sum(engagement_seconds) AS e, min(date) AS first FROM ga_daily_pages
		  WHERE site_id=$1 AND date BETWEEN $2 AND $3 AND NOT (path LIKE ANY($4::text[]))
		  GROUP BY path HAVING sum(page_views) >= 20),
		f AS (
		  SELECT * FROM c WHERE NOT EXISTS (
		    SELECT 1 FROM ga_daily_pages x WHERE x.site_id=$1 AND x.path=c.path AND x.date >= $2::date - 120 AND x.date < $2)
		  ORDER BY v DESC LIMIT 10)
		SELECT f.path, coalesce((SELECT title FROM ga_daily_pages t WHERE t.site_id=$1 AND t.path=f.path AND t.date BETWEEN $2 AND $3
		         ORDER BY t.page_views DESC LIMIT 1),''), f.first, f.v, f.e
		FROM f ORDER BY f.v DESC`, site.ID, r.Start, r.End, excludePatterns(site))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageRow
	var paths []string
	for rows.Next() {
		var p PageRow
		var e float64
		if err := rows.Scan(&p.Path, &p.Title, &p.FirstSeen, &p.Views, &e); err != nil {
			return nil, err
		}
		p.Dwell = ratio(e, float64(p.Views))
		out = append(out, p)
		paths = append(paths, p.Path)
	}
	src := m.mainSources(ctx, site.ID, r.Start, r.End, paths)
	for i := range out {
		out[i].Main = src[out[i].Path]
	}
	return out, nil
}

// landing 返回入口页；onlyWeak 为 true 时只返回“流量不小但认真阅读比例低”的待优化页面。
func (m *Svc) landing(ctx context.Context, site store.Site, r period.Range, onlyWeak bool) ([]PageRow, error) {
	// 先汇总出入口页前 60 名，再只给这 60 个页面查标题和停留时间（大网站每天有上万个页面）
	rows, err := m.Store.Pool.Query(ctx, `
		WITH l AS (
		  SELECT path, sum(sessions) AS s, sum(engaged_sessions) AS e, sum(page_views) AS pv FROM ga_daily_landing
		  WHERE site_id=$1 AND date BETWEEN $2 AND $3 AND NOT (path LIKE ANY($4::text[]))
		  GROUP BY path ORDER BY 2 DESC LIMIT 60)
		SELECT l.path, coalesce(p.title,''), l.s, l.e, l.pv, coalesce(p.dwell,0)
		FROM l LEFT JOIN LATERAL (
		  SELECT (array_agg(title ORDER BY page_views DESC))[1] AS title, sum(engagement_seconds)/nullif(sum(page_views),0) AS dwell
		  FROM ga_daily_pages WHERE site_id=$1 AND path=l.path AND date BETWEEN $2 AND $3) p ON true
		ORDER BY l.s DESC`, site.ID, r.Start, r.End, excludePatterns(site))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageRow
	for rows.Next() {
		var p PageRow
		var eng, pv int
		if err := rows.Scan(&p.Path, &p.Title, &p.Sessions, &eng, &pv, &p.Dwell); err != nil {
			return nil, err
		}
		p.Views = p.Sessions
		p.EngRate = ratio(float64(eng), float64(p.Sessions))
		p.PPV = ratio(float64(pv), float64(p.Sessions))
		out = append(out, p)
	}
	if !onlyWeak {
		out = topN(out, 10)
		for i := range out {
			out[i].Main = landingNote(out[i])
		}
		return out, nil
	}
	var weak []PageRow
	minSess := max(30, r.Days*2)
	for _, p := range out {
		if p.Sessions >= minSess && p.EngRate < 0.4 {
			p.Main = weakReason(p)
			weak = append(weak, p)
		}
	}
	return topN(weak, 10), nil
}

func landingNote(p PageRow) string {
	switch {
	case p.EngRate < 0.4:
		return "进来的人大多没认真看就走了"
	case p.PPV < 1.25:
		return "看完就走，可以加相关文章推荐"
	case p.EngRate >= 0.65:
		return "读者质量高"
	}
	return "表现正常"
}

func weakReason(p PageRow) string {
	lp := strings.ToLower(p.Path + " " + p.Title)
	switch {
	case strings.Contains(lp, "tag") || strings.Contains(lp, "标签") || strings.Contains(lp, "categor") || strings.Contains(lp, "分类"):
		return "列表页信息太少，读者找不到想看的；可以加摘要和推荐"
	case strings.Contains(lp, "archive") || strings.Contains(lp, "归档"):
		return "归档页平铺太长，可以加分类和搜索"
	case p.Dwell > 0 && p.Dwell < 15:
		return fmt.Sprintf("平均只停留 %.0f 秒，标题和内容可能不一致", p.Dwell)
	}
	return fmt.Sprintf("%.0f%% 的访问没有认真看就离开", (1-p.EngRate)*100)
}

// ========== 单个页面展开后的详情 ==========

type PageDetail struct {
	Path    string
	Series  Series
	Sources []Share
	Queries []QueryRow
}

type Share struct {
	Label string
	Value float64
	Pct   float64
}

func (m *Svc) PageDetail(ctx context.Context, site store.Site, r period.Range, path string) PageDetail {
	if r.Hourly {
		r = period.Resolve("d7", time.Now(), site.Location())
	}
	d := PageDetail{Path: path}
	dates := r.Dates()
	d.Series = Series{Dates: dates}
	d.Series.Cur = m.dailySeriesWhere(ctx, "ga_daily_pages", "sum(page_views)::float8", site.ID, dates, "path", path)
	d.Series.Prev = m.dailySeriesWhere(ctx, "ga_daily_pages", "sum(page_views)::float8", site.ID, r.PrevDates(), "path", path)
	for _, x := range dates {
		d.Series.Labels = append(d.Series.Labels, period.MD(x))
	}
	for _, x := range r.PrevDates() {
		d.Series.PrevLabels = append(d.Series.PrevLabels, period.MD(x))
	}
	rows, err := m.Store.Pool.Query(ctx, `SELECT channel, source, sum(sessions) FROM ga_daily_landing
		WHERE site_id=$1 AND path=$2 AND date BETWEEN $3 AND $4 GROUP BY channel, source ORDER BY 3 DESC`, site.ID, path, r.Start, r.End)
	if err == nil {
		agg := map[string]float64{}
		var order []string
		total := 0.0
		for rows.Next() {
			var ch, src string
			var n float64
			if rows.Scan(&ch, &src, &n) == nil {
				l := SourceLabel(ch, src)
				if _, ok := agg[l]; !ok {
					order = append(order, l)
				}
				agg[l] += n
				total += n
			}
		}
		rows.Close()
		other := 0.0
		for i, l := range order {
			if i < 3 {
				d.Sources = append(d.Sources, Share{Label: l, Value: agg[l], Pct: ratio(agg[l], total)})
			} else {
				other += agg[l]
			}
		}
		if other > 0 {
			d.Sources = append(d.Sources, Share{Label: "其他", Value: other, Pct: ratio(other, total)})
		}
	}
	latest := m.GSCLatest(ctx, site.ID)
	cr := r.ClampEnd(latest)
	d.Queries, _ = m.queryRows(ctx, site.ID, cr, "AND path=$6", []any{path}, "c1 DESC", 5)
	return d
}

func (m *Svc) dailySeriesWhere(ctx context.Context, table, expr string, siteID int64, dates []time.Time, col, val string) []float64 {
	out := make([]float64, len(dates))
	if len(dates) == 0 {
		return out
	}
	rows, err := m.Store.Pool.Query(ctx, `SELECT date, `+expr+` FROM `+table+` WHERE site_id=$1 AND date BETWEEN $2 AND $3 AND `+col+`=$4 GROUP BY date`,
		siteID, dates[0], dates[len(dates)-1], val)
	if err != nil {
		return out
	}
	defer rows.Close()
	idx := map[string]int{}
	for i, d := range dates {
		idx[period.D(d)] = i
	}
	for rows.Next() {
		var d time.Time
		var v *float64
		if rows.Scan(&d, &v) == nil && v != nil {
			if i, ok := idx[period.D(d)]; ok {
				out[i] = *v
			}
		}
	}
	return out
}
