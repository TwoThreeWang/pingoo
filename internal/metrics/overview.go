package metrics

import (
	"context"
	"math"
	"sync"
	"time"

	"pingoo/internal/period"
	"pingoo/internal/store"
)

type SiteCard struct {
	Site         store.Site
	Visitors     int
	VisitorsPrev int
	Approx       bool
	PV, PVPrev   int
	Clicks       int
	ClicksPrev   int
	HasSearch    bool
	Live         int // -1 表示未知
	Series       Series
	Status       string // anomaly / down / up / flat / empty
	Highlight    *store.Signal
	Annotation   *Annotation
	Delta        float64
	// 网站上的广告收入（GA4 关联了 AdSense 才有；各网站货币可能不同，所以不做合计）
	HasAds               bool
	AdRevenue, AdRevPrev float64
}

type Annotation struct {
	I    int    `json:"i"`
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
}

type Overview struct {
	Range        period.Range
	Cards        []SiteCard
	Signals      []store.Signal
	Visitors     int
	VisitorsPrev int
	Clicks       int
	ClicksPrev   int
	HasSearch    bool
	Live         int
	LiveSites    int
	LiveKnown    bool
	Series       Series
	GSCLatest    time.Time
	GALatest     time.Time
	LastSync     time.Time
	Approx       bool // 有网站的访客数是近似值
}

// SyncedRange 解析日期范围；如果最近几天的数据还没同步进来，就让范围以最后一个有数据的日子结束。
func (m *Svc) SyncedRange(ctx context.Context, site store.Site, key string) period.Range {
	r := period.Resolve(key, time.Now(), site.Location())
	if r.Hourly {
		return r
	}
	return r.EndAt(m.GALatest(ctx, site.ID))
}

// SearchTotals 返回某段时间的搜索点击；结束日期会截到 GSC 最新日期，并让对比期等长。
func (m *Svc) SearchTotals(ctx context.Context, siteID int64, r period.Range, latest time.Time) (cur, prev int, ok bool) {
	if r.Hourly || latest.IsZero() {
		return 0, 0, false
	}
	cr := r.ClampEnd(latest)
	if cr.Days <= 0 {
		return 0, 0, false
	}
	_ = m.Store.Pool.QueryRow(ctx, `SELECT coalesce(sum(clicks) FILTER (WHERE date BETWEEN $2 AND $3),0),
		coalesce(sum(clicks) FILTER (WHERE date BETWEEN $4 AND $5),0) FROM gsc_daily WHERE site_id=$1 AND date BETWEEN $4 AND $3`,
		siteID, cr.Start, cr.End, cr.PrevStart, cr.PrevEnd).Scan(&cur, &prev)
	return cur, prev, true
}

func (m *Svc) Overview(ctx context.Context, sites []store.Site, r period.Range, loc *time.Location) Overview {
	ov := Overview{Range: r, Live: 0}
	ov.Signals, _ = m.Store.OpenSignals(ctx, 0, 14)
	// 卡片上的亮点优先挑突发的事件（统计中断、暴涨暴跌、飙升、排名下滑），其次才是长期问题
	topSignal := map[int64]*store.Signal{}
	for i := range ov.Signals {
		s := &ov.Signals[i]
		cur := topSignal[s.SiteID]
		if cur == nil || kindPriority(s.Kind) < kindPriority(cur.Kind) ||
			(kindPriority(s.Kind) == kindPriority(cur.Kind) && s.EventDate.After(cur.EventDate)) {
			topSignal[s.SiteID] = s
		}
	}
	ov.GSCLatest = m.GSCLatest(ctx, 0)
	ov.LastSync = m.LastSyncAt(ctx)
	ov.GALatest = m.GALatest(ctx, 0)

	cards := make([]SiteCard, len(sites))
	var wg sync.WaitGroup
	for i, site := range sites {
		wg.Add(1)
		go func(i int, site store.Site) {
			defer wg.Done()
			sr := m.SyncedRange(ctx, site, r.Key).WithCompare(r.Compare)
			c := SiteCard{Site: site}
			cur, prev := m.Totals(ctx, site, sr)
			c.Visitors, c.VisitorsPrev, c.Approx = cur.Users, prev.Users, cur.Approx
			c.PV, c.PVPrev = cur.PV, prev.PV
			c.Clicks, c.ClicksPrev, c.HasSearch = m.SearchTotals(ctx, site.ID, sr, m.GSCLatest(ctx, site.ID))
			c.Live = -1 // “此刻在线”改成页面加载后异步取（见 LiveCounts），不在首屏阻塞等 GA 实时接口
			if !sr.Hourly && m.HasAds(ctx, site.ID) {
				c.HasAds = true
				c.AdRevenue = m.AdTotals(ctx, site.ID, sr.Start, sr.End).Revenue
				c.AdRevPrev = m.AdTotals(ctx, site.ID, sr.PrevStart, sr.PrevEnd).Revenue
			}
			c.Series = m.VisitorSeries(ctx, site, sr)
			c.Highlight = topSignal[site.ID]
			c.Delta = pct(float64(c.Visitors), float64(c.VisitorsPrev))
			if math.IsInf(c.Delta, 0) || math.IsNaN(c.Delta) {
				c.Delta = 0
			}
			c.Status = cardStatus(c)
			if !sr.Hourly {
				// 图上标注：这段时间里最重要、且带短标签的信号
				var best *store.Signal
				for i := range ov.Signals {
					s := &ov.Signals[i]
					if s.SiteID != site.ID {
						continue
					}
					if short, _ := s.Evidence["short"].(string); short == "" {
						continue
					}
					if best == nil || kindPriority(s.Kind) < kindPriority(best.Kind) {
						best = s
					}
				}
				if best != nil {
					for j, d := range c.Series.Dates {
						if period.D(d) == period.D(best.EventDate) {
							c.Annotation = &Annotation{I: j, Text: best.Evidence["short"].(string)}
						}
					}
				}
			}
			cards[i] = c
		}(i, site)
	}
	wg.Wait()
	ov.Cards = cards
	for _, c := range cards {
		if c.Approx && c.Visitors > 0 {
			ov.Approx = true
		}
	}

	known := false
	for _, c := range cards {
		ov.Visitors += c.Visitors
		ov.VisitorsPrev += c.VisitorsPrev
		if c.HasSearch {
			ov.HasSearch = true
			ov.Clicks += c.Clicks
			ov.ClicksPrev += c.ClicksPrev
		}
		if c.Live >= 0 {
			known = true
			ov.Live += c.Live
			if c.Live > 0 {
				ov.LiveSites++
			}
		}
	}
	ov.LiveKnown = known
	// 合计趋势：各网站逐点相加（长度一致时）
	for i, c := range cards {
		if i == 0 {
			ov.Series = Series{Labels: c.Series.Labels, PrevLabels: c.Series.PrevLabels, Dates: c.Series.Dates,
				Cur: append([]float64(nil), c.Series.Cur...), Prev: append([]float64(nil), c.Series.Prev...)}
			continue
		}
		if len(c.Series.Cur) == len(ov.Series.Cur) {
			for j := range c.Series.Cur {
				ov.Series.Cur[j] += c.Series.Cur[j]
				ov.Series.Prev[j] += c.Series.Prev[j]
			}
		}
	}
	return ov
}

var kindRank = map[string]int{
	"tracking_broken": 0, "traffic_drop": 1, "traffic_spike": 1, "page_surge": 2, "rank_drop": 2, "new_referrer": 3,
	"ai_referrer": 4, "page_new": 4, "page_decline": 5, "low_ctr": 6, "near_top": 6, "mobile_gap": 7, "not_found": 8,
}

func kindPriority(k string) int {
	if v, ok := kindRank[k]; ok {
		return v
	}
	return 9
}

func cardStatus(c SiteCard) string {
	if c.Highlight != nil && c.Highlight.Severity == "critical" {
		return "anomaly"
	}
	if c.Visitors == 0 && c.VisitorsPrev == 0 {
		return "empty"
	}
	sd := 0.0
	if c.HasSearch && c.ClicksPrev > 30 {
		sd = pct(float64(c.Clicks), float64(c.ClicksPrev))
	}
	switch {
	case c.VisitorsPrev >= 30 && c.Delta < -0.1, sd < -0.2:
		return "down"
	case c.VisitorsPrev >= 30 && c.Delta > 0.1:
		return "up"
	}
	return "flat"
}

// LiveVisitors 是“此刻在线”的异步结果：总数、有在线访客的网站数，以及每个网站各自的在线人数。
type LiveVisitors struct {
	Total   int
	Sites   int           // 有人在线的网站数
	Count   int           // 参与统计的网站总数（分母）
	Known   bool          // 至少有一个网站拿到了实时数据（否则显示“需要连接 GA”）
	PerSite map[int64]int // 网站 ID → 在线人数；-1 表示这个网站没拿到
}

// LiveCounts 并发查所有网站的实时在线人数。放在单独的接口里，页面先渲染、再异步拉这个，
// 这样首屏不用等 GA 实时接口（每个站最多 8 秒、缓存只有 1 分钟且重启即清空，是首屏最常见的卡顿来源）。
func (m *Svc) LiveCounts(ctx context.Context, sites []store.Site) LiveVisitors {
	lv := LiveVisitors{Count: len(sites), PerSite: make(map[int64]int, len(sites))}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, site := range sites {
		wg.Add(1)
		go func(site store.Site) {
			defer wg.Done()
			n := m.Realtime(ctx, site)
			mu.Lock()
			lv.PerSite[site.ID] = n
			if n >= 0 {
				lv.Known = true
				lv.Total += n
				if n > 0 {
					lv.Sites++
				}
			}
			mu.Unlock()
		}(site)
	}
	wg.Wait()
	return lv
}
