package metrics

import (
	"context"

	"pingoo/internal/period"
	"pingoo/internal/store"
)

// ========== 网站上的广告收益（AdSense 等，GA4 关联后才有） ==========

// AdTotals 是一段时间的广告收益合计。
type AdTotals struct {
	Revenue             float64
	Impressions, Clicks int
	Views               int // 同期的页面浏览量，用来算每千次浏览收入
}

// RPM 是每千次页面浏览带来的广告收入：比较不同时期、不同页面“变现效率”最直接的数。
func (t AdTotals) RPM() float64 {
	if t.Views == 0 {
		return 0
	}
	return t.Revenue / float64(t.Views) * 1000
}

// CTR 是广告点击率。
func (t AdTotals) CTR() float64 { return ratio(float64(t.Clicks), float64(t.Impressions)) }

// AdPage 是一个页面的广告收益。
type AdPage struct {
	Path, Title  string
	Revenue      float64
	PrevRevenue  float64
	Views        int
	Impressions  int
	RPM          float64
	RPMVsSite    float64 // 这个页面的 RPM 是全站的几倍（“高流量低收益”用）
	HasPrevValue bool
}

// Ads 是详情页“广告收益”一节的数据。
type Ads struct {
	Has       bool   // 这个网站有没有广告数据（从来没有时显示怎么关联 AdSense）
	Currency  string // 金额的单位（GA 媒体资源的货币）
	Cur, Prev AdTotals
	Pages     []AdPage
}

// AdTabs 是“广告收益”一节的两个标签。
var AdTabs = []struct{ Key, Label string }{
	{"top", "收入最高的页面"},
	{"low", "流量大、收入低"},
}

// HasAds 表示网站在保留期内有过广告展示。
func (m *Svc) HasAds(ctx context.Context, siteID int64) bool { return m.Store.HasAdData(ctx, siteID) }

// AdTotals 返回一段日期的广告收益合计。
func (m *Svc) AdTotals(ctx context.Context, siteID int64, from, to any) AdTotals {
	var t AdTotals
	_ = m.Store.Pool.QueryRow(ctx, `SELECT coalesce(sum(ad_revenue),0), coalesce(sum(ad_impressions),0), coalesce(sum(ad_clicks),0), coalesce(sum(page_views),0)
		FROM ga_daily WHERE site_id=$1 AND date BETWEEN $2 AND $3`, siteID, from, to).Scan(&t.Revenue, &t.Impressions, &t.Clicks, &t.Views)
	return t
}

// Ads 计算“广告收益”一节：本期和上一期的合计，以及按标签列出的页面。
//   - top：收入最高的页面（附带浏览量、RPM、和上一期比）；
//   - low：浏览量排前 50、但 RPM 不到全站一半的页面——流量已经有了，钱没赚到：可能广告位少、广告没加载出来，或者页面类型不适合放广告。
func (m *Svc) Ads(ctx context.Context, site store.Site, r period.Range, tab string) Ads {
	r = r.Sequential()
	a := Ads{Has: m.HasAds(ctx, site.ID), Currency: site.Currency}
	if !a.Has {
		return a
	}
	a.Cur = m.AdTotals(ctx, site.ID, r.Start, r.End)
	a.Prev = m.AdTotals(ctx, site.ID, r.PrevStart, r.PrevEnd)
	siteRPM := a.Cur.RPM()
	var q string
	if tab == "low" {
		q = `WITH v AS (SELECT path, sum(page_views) AS views FROM ga_daily_pages WHERE site_id=$1 AND date BETWEEN $2 AND $3 GROUP BY path ORDER BY 2 DESC LIMIT 50),
			a AS (SELECT path, sum(revenue) AS rev, sum(impressions) AS imp FROM ga_daily_ads WHERE site_id=$1 AND date BETWEEN $2 AND $3 GROUP BY path),
			p AS (SELECT path, sum(revenue) AS rev FROM ga_daily_ads WHERE site_id=$1 AND date BETWEEN $4 AND $5 GROUP BY path)
			SELECT v.path, coalesce(a.rev,0), coalesce(p.rev,0), p.path IS NOT NULL, v.views, coalesce(a.imp,0)
			FROM v LEFT JOIN a USING (path) LEFT JOIN p USING (path)
			WHERE coalesce(a.rev,0) * 1000 < v.views * $6 / 2 ORDER BY v.views DESC LIMIT 20`
	} else {
		q = `WITH a AS (SELECT path, sum(revenue) AS rev, sum(impressions) AS imp FROM ga_daily_ads WHERE site_id=$1 AND date BETWEEN $2 AND $3 GROUP BY path ORDER BY 2 DESC LIMIT 20),
			v AS (SELECT path, sum(page_views) AS views FROM ga_daily_pages WHERE site_id=$1 AND date BETWEEN $2 AND $3 AND path IN (SELECT path FROM a) GROUP BY path),
			p AS (SELECT path, sum(revenue) AS rev FROM ga_daily_ads WHERE site_id=$1 AND date BETWEEN $4 AND $5 AND path IN (SELECT path FROM a) GROUP BY path)
			SELECT a.path, a.rev, coalesce(p.rev,0), p.path IS NOT NULL, coalesce(v.views,0), a.imp
			FROM a LEFT JOIN v USING (path) LEFT JOIN p USING (path) WHERE $6::float8 >= 0 ORDER BY a.rev DESC`
	}
	rows, err := m.Store.Pool.Query(ctx, q, site.ID, r.Start, r.End, r.PrevStart, r.PrevEnd, siteRPM)
	if err != nil {
		return a
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p AdPage
		if rows.Scan(&p.Path, &p.Revenue, &p.PrevRevenue, &p.HasPrevValue, &p.Views, &p.Impressions) != nil {
			continue
		}
		if p.Views > 0 {
			p.RPM = p.Revenue / float64(p.Views) * 1000
		}
		if siteRPM > 0 {
			p.RPMVsSite = p.RPM / siteRPM
		}
		a.Pages = append(a.Pages, p)
		paths = append(paths, p.Path)
	}
	titles := m.pageTitles(ctx, site.ID, r.Start, r.End, paths)
	for i := range a.Pages {
		a.Pages[i].Title = titles[a.Pages[i].Path]
	}
	return a
}
