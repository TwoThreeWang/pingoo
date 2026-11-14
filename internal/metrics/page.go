package metrics

import (
	"context"

	"pingoo/internal/period"
	"pingoo/internal/store"
)

type PageSource struct {
	Label    string
	Sessions int
	Share    float64
	EngRate  float64
}

// PageAnalysis 是“单页分析”页面需要的全部数据。
type PageAnalysis struct {
	Path, Title          string
	Views, PrevViews     float64
	Dwell, PrevDwell     float64 // 每次浏览的平均停留秒数
	Entries, PrevEntries float64 // 作为入口页的访问次数
	EngRate, PrevEngRate float64 // 作为入口时的认真阅读比例
	Clicks, PrevClicks   float64
	Impr, PrevImpr       float64
	Pos, PrevPos         float64
	HasSearch            bool
	SearchRange          period.Range
	Series               Series
	Sources              []PageSource
	Queries              []QueryRow
}

func (m *Svc) PageAnalysis(ctx context.Context, site store.Site, r period.Range, path string) PageAnalysis {
	if r.Hourly {
		r = m.SyncedRange(ctx, site, "d7")
	}
	p := PageAnalysis{Path: path}
	var e1, e2 float64
	_ = m.Store.Pool.QueryRow(ctx, `SELECT coalesce((array_agg(title ORDER BY page_views DESC) FILTER (WHERE title<>''))[1],''),
		coalesce(sum(page_views) FILTER (WHERE date BETWEEN $3 AND $4),0), coalesce(sum(page_views) FILTER (WHERE date BETWEEN $5 AND $6),0),
		coalesce(sum(engagement_seconds) FILTER (WHERE date BETWEEN $3 AND $4),0), coalesce(sum(engagement_seconds) FILTER (WHERE date BETWEEN $5 AND $6),0)
		FROM ga_daily_pages WHERE site_id=$1 AND path=$2 AND date BETWEEN $5 AND $4`,
		site.ID, path, r.Start, r.End, r.PrevStart, r.PrevEnd).Scan(&p.Title, &p.Views, &p.PrevViews, &e1, &e2)
	p.Dwell, p.PrevDwell = ratio(e1, p.Views), ratio(e2, p.PrevViews)

	var g1, g2 float64
	_ = m.Store.Pool.QueryRow(ctx, `SELECT
		coalesce(sum(sessions) FILTER (WHERE date BETWEEN $3 AND $4),0), coalesce(sum(sessions) FILTER (WHERE date BETWEEN $5 AND $6),0),
		coalesce(sum(engaged_sessions) FILTER (WHERE date BETWEEN $3 AND $4),0), coalesce(sum(engaged_sessions) FILTER (WHERE date BETWEEN $5 AND $6),0)
		FROM ga_daily_landing WHERE site_id=$1 AND path=$2 AND date BETWEEN $5 AND $4`,
		site.ID, path, r.Start, r.End, r.PrevStart, r.PrevEnd).Scan(&p.Entries, &p.PrevEntries, &g1, &g2)
	p.EngRate, p.PrevEngRate = ratio(g1, p.Entries), ratio(g2, p.PrevEntries)

	dates := r.Dates()
	p.Series = Series{Dates: dates}
	p.Series.Cur = m.dailySeriesWhere(ctx, "ga_daily_pages", "sum(page_views)::float8", site.ID, dates, "path", path)
	p.Series.Prev = m.dailySeriesWhere(ctx, "ga_daily_pages", "sum(page_views)::float8", site.ID, r.PrevDates(), "path", path)
	for _, d := range dates {
		p.Series.Labels = append(p.Series.Labels, period.MD(d))
	}
	for _, d := range r.PrevDates() {
		p.Series.PrevLabels = append(p.Series.PrevLabels, period.MD(d))
	}

	rows, err := m.Store.Pool.Query(ctx, `SELECT channel, source, sum(sessions), sum(engaged_sessions) FROM ga_daily_landing
		WHERE site_id=$1 AND path=$2 AND date BETWEEN $3 AND $4 GROUP BY channel, source ORDER BY 3 DESC`, site.ID, path, r.Start, r.End)
	if err == nil {
		agg := map[string]*PageSource{}
		eng := map[string]float64{}
		var order []string
		total := 0.0
		for rows.Next() {
			var ch, src string
			var n, e float64
			if rows.Scan(&ch, &src, &n, &e) != nil {
				continue
			}
			l := SourceLabel(ch, src)
			if agg[l] == nil {
				agg[l] = &PageSource{Label: l}
				order = append(order, l)
			}
			agg[l].Sessions += int(n)
			eng[l] += e
			total += n
		}
		rows.Close()
		for _, l := range topN(order, 8) {
			x := agg[l]
			x.Share = ratio(float64(x.Sessions), total)
			x.EngRate = ratio(eng[l], float64(x.Sessions))
			p.Sources = append(p.Sources, *x)
		}
	}

	if cr, _, ok := m.SearchRange(ctx, site, r); ok {
		p.SearchRange = cr
		var x1, x2 float64
		_ = m.Store.Pool.QueryRow(ctx, `SELECT
			coalesce(sum(clicks) FILTER (WHERE date BETWEEN $3 AND $4),0), coalesce(sum(clicks) FILTER (WHERE date BETWEEN $5 AND $6),0),
			coalesce(sum(impressions) FILTER (WHERE date BETWEEN $3 AND $4),0), coalesce(sum(impressions) FILTER (WHERE date BETWEEN $5 AND $6),0),
			coalesce(sum(pos_x_impr) FILTER (WHERE date BETWEEN $3 AND $4),0), coalesce(sum(pos_x_impr) FILTER (WHERE date BETWEEN $5 AND $6),0)
			FROM gsc_daily_pages WHERE site_id=$1 AND path=$2 AND date BETWEEN $5 AND $4`,
			site.ID, path, cr.Start, cr.End, cr.PrevStart, cr.PrevEnd).Scan(&p.Clicks, &p.PrevClicks, &p.Impr, &p.PrevImpr, &x1, &x2)
		p.Pos, p.PrevPos = ratio(x1, p.Impr), ratio(x2, p.PrevImpr)
		p.HasSearch = p.Impr > 0 || p.PrevImpr > 0
		p.Queries, _ = m.queryRows(ctx, site.ID, cr, "AND path=$6", []any{path}, "coalesce(c1,0) DESC, coalesce(i1,0) DESC", 20)
	}
	return p
}
