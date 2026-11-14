package metrics

import (
	"context"
	"fmt"
	"math"
	"time"

	"pingoo/internal/period"
	"pingoo/internal/store"
)

type QueryRow struct {
	Query      string
	Path       string
	Clicks     int
	PrevClicks int
	Impr       int
	PrevImpr   int
	Pos        float64
	PrevPos    float64 // 0 表示上期没有
	CTR        float64
	Expected   float64 // 同排名的平均点击率
	Gain       int     // 进入前 3 预计每期多出的点击
	Note       string
}

var SearchTabs = []struct{ Key, Label string }{
	{"top", "热门搜索词"}, {"rising", "上升中"}, {"near", "差一点上首页"}, {"ctr", "高曝光低点击"}, {"drops", "排名下滑"}, {"pages", "页面与搜索词"},
}

// queryRows 按搜索词汇总本期和上期（extra 是额外的 WHERE 条件，参数从 $6 开始）。
func (m *Svc) queryRows(ctx context.Context, siteID int64, r period.Range, extra string, args []any, order string, limit int) ([]QueryRow, error) {
	q := `
		WITH a AS (
		  SELECT query,
		    sum(clicks) FILTER (WHERE date BETWEEN $2 AND $3) AS c1, sum(clicks) FILTER (WHERE date BETWEEN $4 AND $5) AS c2,
		    sum(impressions) FILTER (WHERE date BETWEEN $2 AND $3) AS i1, sum(impressions) FILTER (WHERE date BETWEEN $4 AND $5) AS i2,
		    sum(pos_x_impr) FILTER (WHERE date BETWEEN $2 AND $3) AS p1, sum(pos_x_impr) FILTER (WHERE date BETWEEN $4 AND $5) AS p2
		  FROM gsc_daily_queries WHERE site_id=$1 AND date BETWEEN $4 AND $3 ` + extra + `
		  GROUP BY query)
		SELECT a.query, coalesce(c1,0), coalesce(c2,0), coalesce(i1,0), coalesce(i2,0),
		  coalesce(p1/nullif(i1,0),0), coalesce(p2/nullif(i2,0),0),
		  coalesce((SELECT path FROM gsc_daily_queries g WHERE g.site_id=$1 AND g.query=a.query AND g.date BETWEEN $2 AND $3
		            GROUP BY path ORDER BY sum(clicks) DESC, sum(impressions) DESC LIMIT 1),'')
		FROM a WHERE coalesce(i1,0) > 0 OR coalesce(i2,0) > 0 ORDER BY ` + order + ` LIMIT ` + fmt.Sprint(limit)
	all := append([]any{siteID, r.Start, r.End, r.PrevStart, r.PrevEnd}, args...)
	rows, err := m.Store.Pool.Query(ctx, q, all...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueryRow
	for rows.Next() {
		var x QueryRow
		if err := rows.Scan(&x.Query, &x.Clicks, &x.PrevClicks, &x.Impr, &x.PrevImpr, &x.Pos, &x.PrevPos, &x.Path); err != nil {
			return nil, err
		}
		x.CTR = ratio(float64(x.Clicks), float64(x.Impr))
		x.Expected = ExpectedCTR(x.Pos)
		out = append(out, x)
	}
	return out, rows.Err()
}

// SearchRange 返回截到 GSC 最新日期之后的范围；ok=false 表示没有搜索数据。
func (m *Svc) SearchRange(ctx context.Context, site store.Site, r period.Range) (period.Range, time.Time, bool) {
	latest := m.GSCLatest(ctx, site.ID)
	if latest.IsZero() {
		return r, latest, false
	}
	if r.Hourly {
		r = period.Resolve("d7", time.Now(), site.Location())
	}
	cr := r.ClampEnd(latest)
	return cr, latest, cr.Days > 0
}

func (m *Svc) Search(ctx context.Context, site store.Site, r period.Range, tab string) ([]QueryRow, error) {
	cr, _, ok := m.SearchRange(ctx, site, r)
	if !ok {
		return nil, nil
	}
	switch tab {
	case "rising":
		rows, err := m.queryRows(ctx, site.ID, cr, "", nil, "coalesce(c1,0)-coalesce(c2,0) DESC", 40)
		if err != nil {
			return nil, err
		}
		var out []QueryRow
		for _, x := range rows {
			if x.Clicks >= 5 && float64(x.Clicks) >= float64(x.PrevClicks)*1.5 {
				out = append(out, x)
			}
		}
		return topN(out, 10), nil
	case "near":
		rows, err := m.queryRows(ctx, site.ID, cr, "", nil, "coalesce(i1,0) DESC", 400)
		if err != nil {
			return nil, err
		}
		var out []QueryRow
		for _, x := range rows {
			if x.Pos >= 8 && x.Pos <= 20 && x.Impr >= max(50, cr.Days*5) {
				x.Gain = int(math.Max(0, float64(x.Impr)*(ExpectedCTR(3)-x.CTR)))
				out = append(out, x)
			}
		}
		sortBy(out, func(a, b QueryRow) bool { return a.Gain > b.Gain })
		return topN(out, 12), nil
	case "ctr":
		rows, err := m.queryRows(ctx, site.ID, cr, "", nil, "coalesce(i1,0) DESC", 400)
		if err != nil {
			return nil, err
		}
		var out []QueryRow
		for _, x := range rows {
			if x.Pos <= 10 && x.Impr >= max(100, cr.Days*10) && x.CTR < x.Expected*0.5 {
				x.Note = fmt.Sprintf("在第 %.1f 位，点击率只有同排名平均的 %.0f%%，标题或描述可能没打中搜索意图", x.Pos, x.CTR/x.Expected*100)
				out = append(out, x)
			}
		}
		return topN(out, 10), nil
	case "drops":
		rows, err := m.queryRows(ctx, site.ID, cr, "", nil, "coalesce(c2,0)-coalesce(c1,0) DESC", 200)
		if err != nil {
			return nil, err
		}
		var out []QueryRow
		for _, x := range rows {
			if x.PrevPos > 0 && x.PrevPos <= 15 && x.Pos-x.PrevPos >= 1.5 && x.PrevClicks >= 3 {
				out = append(out, x)
			}
		}
		return topN(out, 10), nil
	default:
		return m.queryRows(ctx, site.ID, cr, "", nil, "coalesce(c1,0) DESC, coalesce(i1,0) DESC", 15)
	}
}

type PageQueries struct {
	Path    string
	Clicks  int
	Queries []QueryRow
}

// PagesWithQueries 列出带来搜索点击最多的页面，以及每个页面的前 3 个搜索词。
func (m *Svc) PagesWithQueries(ctx context.Context, site store.Site, r period.Range) ([]PageQueries, error) {
	cr, _, ok := m.SearchRange(ctx, site, r)
	if !ok {
		return nil, nil
	}
	rows, err := m.Store.Pool.Query(ctx, `SELECT path, sum(clicks) FROM gsc_daily_pages WHERE site_id=$1 AND date BETWEEN $2 AND $3
		GROUP BY path ORDER BY 2 DESC LIMIT 10`, site.ID, cr.Start, cr.End)
	if err != nil {
		return nil, err
	}
	var out []PageQueries
	for rows.Next() {
		var p PageQueries
		if rows.Scan(&p.Path, &p.Clicks) == nil {
			out = append(out, p)
		}
	}
	rows.Close()
	for i := range out {
		out[i].Queries, _ = m.queryRows(ctx, site.ID, cr, "AND path=$6", []any{out[i].Path}, "coalesce(c1,0) DESC", 3)
	}
	return out, nil
}

// SearchSmall 是搜索区的四张小图。
type SearchSmall struct {
	Key, Label, Kind string
	Better           int
	Cur, Prev        float64
	Series           Series
}

func (m *Svc) SearchSmalls(ctx context.Context, site store.Site, r period.Range) ([]SearchSmall, time.Time, bool) {
	cr, latest, ok := m.SearchRange(ctx, site, r)
	if !ok {
		return nil, latest, false
	}
	defs := []SearchSmall{
		{Key: "sc", Label: "点击", Kind: "count", Better: 1},
		{Key: "imp", Label: "曝光", Kind: "count", Better: 1},
		{Key: "ctr", Label: "点击率", Kind: "pct", Better: 1},
		{Key: "pos", Label: "平均排名", Kind: "pos", Better: -1},
	}
	var c1, i1, p1, c2, i2, p2 float64
	_ = m.Store.Pool.QueryRow(ctx, `SELECT
		coalesce(sum(clicks) FILTER (WHERE date BETWEEN $2 AND $3),0), coalesce(sum(impressions) FILTER (WHERE date BETWEEN $2 AND $3),0),
		coalesce(sum(pos_x_impr) FILTER (WHERE date BETWEEN $2 AND $3),0),
		coalesce(sum(clicks) FILTER (WHERE date BETWEEN $4 AND $5),0), coalesce(sum(impressions) FILTER (WHERE date BETWEEN $4 AND $5),0),
		coalesce(sum(pos_x_impr) FILTER (WHERE date BETWEEN $4 AND $5),0)
		FROM gsc_daily WHERE site_id=$1 AND date BETWEEN $4 AND $3`, site.ID, cr.Start, cr.End, cr.PrevStart, cr.PrevEnd).
		Scan(&c1, &i1, &p1, &c2, &i2, &p2)
	vals := map[string][2]float64{
		"sc": {c1, c2}, "imp": {i1, i2}, "ctr": {ratio(c1, i1), ratio(c2, i2)}, "pos": {ratio(p1, i1), ratio(p2, i2)},
	}
	for i := range defs {
		e := metricExpr[defs[i].Key]
		defs[i].Cur, defs[i].Prev = vals[defs[i].Key][0], vals[defs[i].Key][1]
		defs[i].Series = m.DailySeries(ctx, site.ID, cr, e[0], e[1])
	}
	return defs, latest, true
}

// NearTopSummary 统计“排在 8~20 位、曝光不少”的搜索词个数和总曝光（洞察和信号用）。
func (m *Svc) NearTopSummary(ctx context.Context, site store.Site, r period.Range) (n int, impr int, top *QueryRow) {
	rows, _ := m.Search(ctx, site, r, "near")
	for i, x := range rows {
		n++
		impr += x.Impr
		if i == 0 {
			t := x
			top = &t
		}
	}
	return
}
