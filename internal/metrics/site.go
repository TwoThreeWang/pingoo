package metrics

import (
	"context"
	"time"

	"pingoo/internal/period"
	"pingoo/internal/store"
)

// Tile 是详情页的一个核心指标。
type Tile struct {
	Key    string
	Label  string
	Tip    string
	Kind   string // count / pct / dur / dec / pos
	Better int    // 1 越大越好，-1 越小越好，0 无好坏
	Cur    float64
	Prev   float64
	HasVal bool
	Note   string // 无数据时的说明
}

var tileDefs = []Tile{
	{Key: "v", Label: "访客", Tip: "来了多少人（同一台设备只算一次）", Kind: "count", Better: 1},
	{Key: "pv", Label: "浏览量", Tip: "所有页面被打开的总次数（同一个人多次打开都算）", Kind: "count", Better: 1},
	{Key: "newShare", Label: "新访客占比", Tip: "第一次来的人占多少。高说明拉新好，太高说明老读者没回来", Kind: "pct", Better: 0},
	{Key: "ret", Label: "回头客", Tip: "之前来过、这次又来的人，代表粘性", Kind: "count", Better: 1},
	{Key: "ppv", Label: "人均看几页", Tip: "每次访问平均浏览几页，代表有没有兴趣继续逛", Kind: "dec", Better: 1},
	{Key: "dur", Label: "平均停留", Tip: "每位访客真正在看页面的平均时间", Kind: "dur", Better: 1},
	{Key: "eng", Label: "认真阅读比例", Tip: "停留超过 10 秒、或看了 2 页以上的访问占比（GA 里叫“互动率”）", Kind: "pct", Better: 1},
	{Key: "sc", Label: "搜索点击", Tip: "在 Google 搜索结果里点进来的次数", Kind: "count", Better: 1},
	{Key: "pos", Label: "平均排名", Tip: "在 Google 搜索结果中的平均位置，数字越小越靠前", Kind: "pos", Better: -1},
}

// adTileDefs 是“广告收益”一节的指标（不放在核心指标那一排，只在那一节和它的图里用）。
var adTileDefs = []Tile{
	{Key: "rev", Label: "广告收入", Tip: "网站上展示的广告带来的收入（AdSense 等，按 GA 媒体资源的货币）", Kind: "dec", Better: 1},
	{Key: "rpm", Label: "每千次浏览收入", Tip: "每 1000 次页面浏览带来多少广告收入（RPM），衡量变现效率", Kind: "dec", Better: 1},
	{Key: "aimp", Label: "广告展示", Tip: "广告被展示的次数", Kind: "count", Better: 1},
	{Key: "aclk", Label: "广告点击", Tip: "广告被点击的次数", Kind: "count", Better: 1},
}

// AdTiles 把广告收益的本期、上一期整理成和核心指标一样的格子。
func AdTiles(cur, prev AdTotals) []Tile {
	out := make([]Tile, len(adTileDefs))
	copy(out, adTileDefs)
	vals := [][2]float64{{cur.Revenue, prev.Revenue}, {cur.RPM(), prev.RPM()}, {float64(cur.Impressions), float64(prev.Impressions)}, {float64(cur.Clicks), float64(prev.Clicks)}}
	for i := range out {
		out[i].Cur, out[i].Prev, out[i].HasVal = vals[i][0], vals[i][1], true
	}
	return out
}

func TileDef(key string) (Tile, bool) {
	for _, t := range append(tileDefs, adTileDefs...) {
		if t.Key == key {
			return t, true
		}
	}
	return Tile{}, false
}

// Tiles 计算核心指标的本期和对比期。
func (m *Svc) Tiles(ctx context.Context, site store.Site, r period.Range) []Tile {
	cur, prev := m.Totals(ctx, site, r)
	out := make([]Tile, 0, len(tileDefs))
	for _, d := range tileDefs {
		t := d
		t.HasVal = true
		switch d.Key {
		case "v":
			t.Cur, t.Prev = float64(cur.Users), float64(prev.Users)
		case "pv":
			t.Cur, t.Prev = float64(cur.PV), float64(prev.PV)
		case "newShare":
			t.Cur, t.Prev = cur.NewShare(), prev.NewShare()
		case "ret":
			t.Cur, t.Prev = float64(cur.Returning()), float64(prev.Returning())
		case "ppv":
			t.Cur, t.Prev = cur.PPV(), prev.PPV()
		case "dur":
			t.Cur, t.Prev = cur.AvgDur(), prev.AvgDur()
		case "eng":
			t.Cur, t.Prev = cur.EngRate(), prev.EngRate()
		case "sc", "pos":
			latest := m.GSCLatest(ctx, site.ID)
			if r.Hourly || latest.IsZero() || site.GSCSite == "" && !site.Demo {
				t.HasVal = false
				t.Note = "GSC 延迟 2～3 天，暂无"
				if site.GSCSite == "" && !site.Demo {
					t.Note = "未连接 Search Console"
				}
				break
			}
			cr := r.ClampEnd(latest)
			var c1, i1, p1, c2, i2, p2 float64
			_ = m.Store.Pool.QueryRow(ctx, `SELECT
				coalesce(sum(clicks) FILTER (WHERE date BETWEEN $2 AND $3),0), coalesce(sum(impressions) FILTER (WHERE date BETWEEN $2 AND $3),0),
				coalesce(sum(pos_x_impr) FILTER (WHERE date BETWEEN $2 AND $3),0),
				coalesce(sum(clicks) FILTER (WHERE date BETWEEN $4 AND $5),0), coalesce(sum(impressions) FILTER (WHERE date BETWEEN $4 AND $5),0),
				coalesce(sum(pos_x_impr) FILTER (WHERE date BETWEEN $4 AND $5),0)
				FROM gsc_daily WHERE site_id=$1 AND date BETWEEN $4 AND $3`, site.ID, cr.Start, cr.End, cr.PrevStart, cr.PrevEnd).
				Scan(&c1, &i1, &p1, &c2, &i2, &p2)
			if d.Key == "sc" {
				t.Cur, t.Prev = c1, c2
			} else {
				t.Cur, t.Prev = ratio(p1, i1), ratio(p2, i2)
				if i1 == 0 {
					t.HasVal = false
					t.Note = "暂无搜索曝光"
				}
			}
		}
		out = append(out, t)
	}
	return out
}

// MetricExpr 是每个指标在每日表里的计算方式。
var metricExpr = map[string][2]string{
	"v":        {"ga_daily", "sum(active_users)::float8"},
	"pv":       {"ga_daily", "sum(page_views)::float8"},
	"newShare": {"ga_daily", "sum(new_users)::float8/nullif(sum(active_users),0)"},
	"ret":      {"ga_daily", "sum(returning_users)::float8"},
	"ppv":      {"ga_daily", "sum(page_views)::float8/nullif(sum(sessions),0)"},
	"dur":      {"ga_daily", "sum(engagement_seconds)/nullif(sum(active_users),0)"},
	"eng":      {"ga_daily", "sum(engaged_sessions)::float8/nullif(sum(sessions),0)"},
	"sc":       {"gsc_daily", "sum(clicks)::float8"},
	"pos":      {"gsc_daily", "sum(pos_x_impr)/nullif(sum(impressions),0)"},
	"imp":      {"gsc_daily", "sum(impressions)::float8"},
	"ctr":      {"gsc_daily", "sum(clicks)::float8/nullif(sum(impressions),0)"},
	"rev":      {"ga_daily", "sum(ad_revenue)"},
	"rpm":      {"ga_daily", "sum(ad_revenue)*1000/nullif(sum(page_views),0)"},
	"aimp":     {"ga_daily", "sum(ad_impressions)::float8"},
	"aclk":     {"ga_daily", "sum(ad_clicks)::float8"},
}

// MetricSeries 返回某个指标的每日（或每小时）趋势。今天/昨天只有访客有小时数据。
func (m *Svc) MetricSeries(ctx context.Context, site store.Site, r period.Range, key string) Series {
	if key == "v" || r.Hourly {
		return m.VisitorSeries(ctx, site, r)
	}
	e, ok := metricExpr[key]
	if !ok {
		e = metricExpr["v"]
	}
	if e[0] == "gsc_daily" {
		if latest := m.GSCLatest(ctx, site.ID); !latest.IsZero() {
			r = r.ClampEnd(latest)
		}
	}
	return m.DailySeries(ctx, site.ID, r, e[0], e[1])
}

// ChartMarks 把这段时间里的信号变成图上的标注和异常点。
func (m *Svc) ChartMarks(ctx context.Context, siteID int64, dates []time.Time) (ann []Annotation, markers []Annotation) {
	if len(dates) == 0 {
		return nil, nil
	}
	rows, err := m.Store.Pool.Query(ctx, `SELECT id, kind, event_date, evidence->>'short' FROM signals
		WHERE site_id=$1 AND event_date BETWEEN $2 AND $3 AND NOT (kind = ANY($4::text[]))
		ORDER BY event_date, CASE WHEN kind IN ('traffic_spike','traffic_drop','tracking_broken') THEN 0 WHEN kind='page_surge' THEN 1 ELSE 2 END`,
		siteID, dates[0], dates[len(dates)-1], m.Store.MutedSignals(ctx))
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	idx := map[string]int{}
	for i, d := range dates {
		idx[period.D(d)] = i
	}
	seen := map[string]bool{}
	annAt := map[int]bool{}
	lastMarker := -10
	for rows.Next() {
		var id int64
		var kind string
		var d time.Time
		var short *string
		if rows.Scan(&id, &kind, &d, &short) != nil {
			continue
		}
		i, ok := idx[period.D(d)]
		if !ok {
			continue
		}
		switch kind {
		case "traffic_spike", "traffic_drop", "tracking_broken":
			// 连续几天的同一次异常只标一个点（标在开始那天）
			if i-lastMarker > 1 {
				label := map[string]string{"traffic_spike": "访客异常上涨", "traffic_drop": "访客异常下降", "tracking_broken": "访客几乎归零"}[kind]
				markers = append(markers, Annotation{I: i, Text: period.MD(d) + " " + label, URL: diagnoseURL(siteID, id)})
			}
			lastMarker = i
		}
		// 同一天只放一个标注，相同的文字只出现一次
		if short != nil && *short != "" && !seen[*short] && !annAt[i] && !annAt[i-1] && len(ann) < 4 {
			seen[*short] = true
			annAt[i] = true
			ann = append(ann, Annotation{I: i, Text: *short})
		}
	}
	// Google 搜索更新：放在信号之后，同一天已经有标注时跳过
	if gs, err := m.Store.Annotations(ctx, siteID, dates[0], dates[len(dates)-1]); err == nil {
		for _, g := range gs {
			i, ok := idx[period.D(g.Date)]
			if !ok || annAt[i] || annAt[i-1] || annAt[i+1] || len(ann) >= 5 {
				continue
			}
			annAt[i] = true
			ann = append(ann, Annotation{I: i, Text: g.Label})
		}
	}
	return ann, markers
}

func diagnoseURL(siteID, signalID int64) string {
	return "/sites/" + itoa(siteID) + "/diagnose?signal=" + itoa(signalID)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
