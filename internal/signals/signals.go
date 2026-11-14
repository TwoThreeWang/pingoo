// Package signals 是规则引擎：每次同步之后，从本地数据里找出值得注意的变化。
// 数字由规则算出（不会编造），AI 只负责解释原因和给建议。
package signals

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/store"
)

type Detector struct {
	M *metrics.Svc
}

// Kind 是一种提醒。Kinds 的顺序就是设置页里显示的顺序。
type Kind struct{ Key, Label, Desc string }

var Kinds = []Kind{
	{"tracking_broken", "统计中断", "访客突然接近 0，统计代码可能失效"},
	{"traffic_spike", "访客暴涨", "某一天的访客远高于平时"},
	{"traffic_drop", "访客暴跌", "某一天的访客远低于平时"},
	{"page_surge", "页面飙升", "某个页面近 7 天浏览翻了几倍"},
	{"page_decline", "页面下滑", "某个页面近 28 天浏览明显减少"},
	{"page_new", "新页面起量", "新出现的页面有了一定浏览"},
	{"new_referrer", "新外链", "第一次有网站给你带来访客"},
	{"ai_referrer", "AI 来源增长", "来自 ChatGPT、Perplexity 等的访客明显增加"},
	{"rank_drop", "排名下滑", "原来排在前面的搜索词掉了几位"},
	{"near_top", "差一点上首页", "有搜索词排在第 8～20 位、曝光不少"},
	{"low_ctr", "高曝光低点击", "搜索曝光很多，但点击率明显偏低"},
	{"mobile_gap", "手机端体验", "手机访客认真阅读的比例远低于电脑"},
	{"not_found", "404 页面", "有 404 页面仍在被访问"},
	{"ad_rpm_drop", "广告收益下滑", "流量变化不大，但每千次浏览的广告收入明显下降"},
	{"ad_low_page", "高流量低收益页面", "浏览很多的页面，广告收入远低于全站平均"},
}

// 灵敏度：同样的规则，按网站大小放宽或收紧“至少要有多少量才提醒”。
// 小网站用 high，量很小的变化也提醒；大网站用 low，只提醒量大的。
const (
	LevelLow    = "low"
	LevelNormal = "normal"
	LevelHigh   = "high"
)

func ValidLevel(l string) bool { return l == LevelLow || l == LevelNormal || l == LevelHigh }

// floorFactor 是各条规则里“最低数量”要乘的系数。
func floorFactor(level string) float64 {
	switch level {
	case LevelHigh:
		return 0.4
	case LevelLow:
		return 2.5
	}
	return 1
}

// detection 是对一个网站跑一遍规则时的上下文。
type detection struct {
	muted  map[string]bool
	factor float64
}

// atLeast 判断 v 有没有达到某条规则的最低数量（base 是标准灵敏度下的数）。
func (c detection) atLeast(v, base float64) bool { return v >= base*c.factor }

type ctxKey struct{}

func (d *Detector) emit(ctx context.Context, site store.Site, kind, sev, title, short string, date time.Time, dedupe string, ev map[string]any) {
	if c, ok := ctx.Value(ctxKey{}).(detection); ok && c.muted[kind] {
		return // 站长在设置里关掉了这类提醒
	}
	if ev == nil {
		ev = map[string]any{}
	}
	if short != "" {
		ev["short"] = short
	}
	err := d.M.Store.UpsertSignal(ctx, store.Signal{SiteID: site.ID, Kind: kind, Severity: sev, Title: title, Evidence: ev, EventDate: date},
		fmt.Sprintf("%d:%s:%s", site.ID, kind, dedupe))
	if err != nil {
		d.M.Log.Error("保存信号失败", "err", err)
	}
}

func fmtN(v float64) string {
	n := int64(math.Round(v))
	s := fmt.Sprint(n)
	if n < 1000 && n > -1000 {
		return s
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "−" + b.String()
	}
	return b.String()
}

func month(t time.Time) string { return t.Format("2006-01") }
func week(t time.Time) string  { y, w := t.ISOWeek(); return fmt.Sprintf("%d-W%02d", y, w) }

func title(t, path string) string { return metrics.BookTitle(t, path) }

// Detect 对一个网站跑一遍所有规则。
func (d *Detector) Detect(ctx context.Context, site store.Site) {
	ctx = store.WithUser(ctx, site.UserID) // 按网站主人关掉的提醒类型来检测
	latest := d.M.GALatest(ctx, site.ID)
	if latest.IsZero() {
		return
	}
	c := detection{muted: map[string]bool{}, factor: floorFactor(site.SignalLevel)}
	for _, k := range d.M.Store.MutedSignals(ctx) {
		c.muted[k] = true
	}
	ctx = context.WithValue(ctx, ctxKey{}, c)
	d.dailyAnomalies(ctx, site, latest, c)

	loc := site.Location()
	r7 := period.Custom(latest.AddDate(0, 0, -6), latest)
	r28 := period.Custom(latest.AddDate(0, 0, -27), latest)
	_ = loc

	// 页面飙升
	if rows, err := d.M.Content(ctx, site, r7, "rising"); err == nil {
		for _, p := range rows {
			if c.atLeast(float64(p.Views), 150) && float64(p.Views) >= float64(p.PrevViews)*3 {
				src := strings.TrimSpace(p.Main)
				short := "浏览飙升"
				if src != "" {
					if i := strings.LastIndex(src, " "); i > 0 {
						short = src[:i] + " 带来流量"
					}
				}
				growth := "新上线"
				if p.PrevViews > 0 {
					growth = fmt.Sprintf("+%.0f%%", (float64(p.Views)/float64(p.PrevViews)-1)*100)
				}
				t := fmt.Sprintf("%s近 7 天浏览 %s（上期 %s，%s）", title(p.Title, p.Path), fmtN(float64(p.Views)), fmtN(float64(p.PrevViews)), growth)
				if src != "" {
					t += "，主要来自 " + src
				}
				d.emit(ctx, site, "page_surge", "good", t, short, d.peakDay(ctx, site.ID, p.Path, r7), "page:"+p.Path+":"+week(latest),
					map[string]any{"path": p.Path, "views": p.Views, "prev": p.PrevViews})
			}
		}
	}
	// 页面下滑
	if rows, err := d.M.Content(ctx, site, r28, "declining"); err == nil {
		for _, p := range topN(rows, 2) {
			if c.atLeast(float64(p.PrevViews), 200) && float64(p.Views) <= float64(p.PrevViews)*0.65 {
				t := fmt.Sprintf("%s近 28 天浏览 −%.0f%%（%s → %s），%s", title(p.Title, p.Path), (1-float64(p.Views)/float64(p.PrevViews))*100,
					fmtN(float64(p.PrevViews)), fmtN(float64(p.Views)), p.Main)
				d.emit(ctx, site, "page_decline", "warn", t, "", latest, "page:"+p.Path+":"+month(latest), map[string]any{"path": p.Path})
			}
		}
	}
	// 新冒头的页面
	if rows, err := d.M.Content(ctx, site, r7, "fresh"); err == nil {
		for _, p := range topN(rows, 2) {
			if c.atLeast(float64(p.Views), 100) {
				t := fmt.Sprintf("新页面%s上线后 7 天内浏览 %s", title(p.Title, p.Path), fmtN(float64(p.Views)))
				if p.Main != "" {
					t += "，主要来自 " + p.Main
				}
				d.emit(ctx, site, "page_new", "good", t, shortTitle(p.Title, p.Path)+" 上线", p.FirstSeen, "page:"+p.Path,
					map[string]any{"path": p.Path})
			}
		}
	}
	// 新外链
	if rows, err := d.M.Sources(ctx, site, r7, "referrers"); err == nil {
		for _, s := range topN(rows, 3) {
			t := fmt.Sprintf("新外链 %s 带来 %s 位访客，平均停留 %s", s.Name, fmtN(float64(s.Sessions)), durZh(s.Dwell))
			if s.Landing != "" {
				t += "，落在" + s.Landing
			}
			d.emit(ctx, site, "new_referrer", "good", t, metrics.SourceLabel("Referral", s.Name), s.FirstSeen, "ref:"+s.Name,
				map[string]any{"source": s.Name, "sessions": s.Sessions})
		}
	}
	// AI 来源
	if rows, err := d.M.Sources(ctx, site, r28, "ai"); err == nil {
		cur, prev := 0, 0
		var top string
		for i, s := range rows {
			cur += s.Sessions
			prev += s.Prev
			if i == 0 {
				top = metrics.SourceLabel("Referral", s.Name)
			}
		}
		if c.atLeast(float64(cur), 30) && float64(cur) >= float64(prev)*1.5 {
			t := fmt.Sprintf("来自 AI 助手的访客近 28 天 %s 人", fmtN(float64(cur)))
			if prev > 0 {
				t += fmt.Sprintf("，比上期 +%.0f%%", (float64(cur)/float64(prev)-1)*100)
			}
			t += "，最多的是 " + top
			d.emit(ctx, site, "ai_referrer", "good", t, "", latest, "ai:"+month(latest), map[string]any{"sessions": cur, "prev": prev})
		}
	}
	// 搜索：排名下滑、差一点上首页、高曝光低点击
	if rows, err := d.M.Search(ctx, site, r7, "drops"); err == nil {
		for _, q := range topN(rows, 3) {
			if q.PrevPos <= 6 && q.Pos-q.PrevPos >= 2.5 && c.atLeast(float64(q.PrevClicks), 10) {
				t := fmt.Sprintf("“%s”排名 %.1f → %.1f，这个词的点击 %s", q.Query, q.PrevPos, q.Pos, signedPct(float64(q.Clicks), float64(q.PrevClicks)))
				d.emit(ctx, site, "rank_drop", "warn", t, "“"+q.Query+"”排名下滑", latest, "q:"+q.Query+":"+week(latest),
					map[string]any{"query": q.Query, "from": q.PrevPos, "to": q.Pos})
			}
		}
	}
	if n, impr, top := d.M.NearTopSummary(ctx, site, r28); n >= 3 && top != nil {
		t := fmt.Sprintf("%d 个搜索词排在第 8～20 位，合计 %s 次曝光，离首页只差一点；最有希望的是“%s”（第 %.1f 位）", n, fmtN(float64(impr)), top.Query, top.Pos)
		d.emit(ctx, site, "near_top", "chance", t, "", latest, "near:"+month(latest), map[string]any{"count": n, "impressions": impr})
	}
	if rows, err := d.M.Search(ctx, site, r28, "ctr"); err == nil && len(rows) > 0 {
		q := rows[0]
		t := fmt.Sprintf("“%s”曝光 %s 次，点击率仅 %.1f%%（同排名平均约 %.0f%%），标题和描述值得重写", q.Query, fmtN(float64(q.Impr)), q.CTR*100, q.Expected*100)
		d.emit(ctx, site, "low_ctr", "chance", t, "", latest, "ctr:"+q.Query+":"+month(latest), map[string]any{"query": q.Query, "path": q.Path})
	}
	// 手机端体验
	a := d.M.Audience(ctx, site, r28)
	var mob, desk *metrics.DeviceRow
	for i := range a.Devices {
		switch a.Devices[i].Name {
		case "手机":
			mob = &a.Devices[i]
		case "电脑":
			desk = &a.Devices[i]
		}
	}
	if mob != nil && desk != nil && mob.Share >= 0.2 && desk.EngRate-mob.EngRate >= 0.2 {
		t := fmt.Sprintf("手机端认真阅读的比例 %.0f%%，电脑端 %.0f%%，手机体验可能有问题", mob.EngRate*100, desk.EngRate*100)
		d.emit(ctx, site, "mobile_gap", "warn", t, "", latest, "mobile:"+month(latest), map[string]any{"mobile": mob.EngRate, "desktop": desk.EngRate})
	}
	// 404
	for _, c := range d.M.Health(ctx, site) {
		if strings.Contains(c.Title, "404") && c.Status == "warn" {
			d.emit(ctx, site, "not_found", "info", c.Title+"，"+strings.TrimSuffix(c.Detail, "。"), "", latest, "404:"+month(latest), nil)
		}
	}
	d.adSignals(ctx, site, latest, c)
}

// adSignals 是网站上广告收益（AdSense 等）的两条规则：
//   - 广告收益下滑：近 7 天的每千次浏览收入（RPM）比前 4 周低了 30% 以上，而浏览量没怎么变——
//     不是流量的问题，多半是广告单价下降、广告位被改动、或者广告没加载出来；
//   - 高流量低收益页面：近 28 天浏览很多、但 RPM 不到全站 30% 的页面，加广告位或检查广告是值得做的事。
func (d *Detector) adSignals(ctx context.Context, site store.Site, latest time.Time, c detection) {
	if !d.M.HasAds(ctx, site.ID) {
		return
	}
	cur := d.M.AdTotals(ctx, site.ID, latest.AddDate(0, 0, -6), latest)
	base := d.M.AdTotals(ctx, site.ID, latest.AddDate(0, 0, -34), latest.AddDate(0, 0, -7))
	if base.Revenue > 0 && base.Views > 0 && c.atLeast(float64(cur.Views), 1000) {
		rpm, baseRPM := cur.RPM(), base.RPM()
		viewsChange := (float64(cur.Views)/7)/(float64(base.Views)/28) - 1
		if baseRPM > 0 && rpm <= baseRPM*0.7 && viewsChange > -0.15 {
			t := fmt.Sprintf("近 7 天每千次浏览的广告收入 %.2f，前 4 周平均 %.2f（%s），浏览量变化 %s：可能是广告单价下降、广告位被改动或广告没加载出来",
				rpm, baseRPM, signedPct(rpm, baseRPM), signedPct(1+viewsChange, 1))
			d.emit(ctx, site, "ad_rpm_drop", "warn", t, "广告收入降", latest, "rpm:"+week(latest),
				map[string]any{"rpm": rpm, "base_rpm": baseRPM, "revenue": cur.Revenue, "currency": site.Currency})
		}
	}
	r28 := period.Custom(latest.AddDate(0, 0, -27), latest)
	for _, p := range topN(d.M.Ads(ctx, site, r28, "low").Pages, 2) {
		if !c.atLeast(float64(p.Views), 1000) || p.RPMVsSite >= 0.3 {
			continue
		}
		t := fmt.Sprintf("「%s」近 28 天浏览 %s 次，每千次浏览的广告收入只有全站的 %.0f%%，可以检查这个页面的广告位和广告加载",
			shortTitle(p.Title, p.Path), fmtN(float64(p.Views)), p.RPMVsSite*100)
		d.emit(ctx, site, "ad_low_page", "chance", t, "", latest, "lowad:"+p.Path+":"+month(latest),
			map[string]any{"path": p.Path, "views": p.Views, "rpm": p.RPM})
	}
}

// dailyAnomalies 检查最近 3 天的访客是否异常（统计中断、暴涨、暴跌）。
func (d *Detector) dailyAnomalies(ctx context.Context, site store.Site, latest time.Time, c detection) {
	from := latest.AddDate(0, 0, -30)
	rows, err := d.M.Store.Pool.Query(ctx, `SELECT date, active_users FROM ga_daily WHERE site_id=$1 AND date BETWEEN $2 AND $3 ORDER BY date`, site.ID, from, latest)
	if err != nil {
		return
	}
	vals := map[string]float64{}
	for rows.Next() {
		var t time.Time
		var v float64
		if rows.Scan(&t, &v) == nil {
			vals[period.D(t)] = v
		}
	}
	rows.Close()
	for back := 2; back >= 0; back-- {
		day := latest.AddDate(0, 0, -back)
		y, ok := vals[period.D(day)]
		if !ok {
			continue
		}
		var base []float64
		for i := 3; i <= 30; i++ {
			if v, ok := vals[period.D(day.AddDate(0, 0, -i))]; ok {
				base = append(base, v)
			}
		}
		if len(base) < 10 {
			continue
		}
		mean, std := meanStd(base)
		med := median(base)
		switch {
		case c.atLeast(med, 50) && y < med*0.1:
			t := fmt.Sprintf("%s访客只有 %s 人（平时约 %s），所有渠道同时下跌，统计代码可能失效", period.MD(day), fmtN(y), fmtN(med))
			var clicks float64
			if d.M.Store.Pool.QueryRow(ctx, `SELECT clicks FROM gsc_daily WHERE site_id=$1 AND date=$2`, site.ID, day).Scan(&clicks) == nil && clicks > 0 {
				t += fmt.Sprintf("；同一天 Search Console 仍有 %s 次搜索点击", fmtN(clicks))
			}
			d.emit(ctx, site, "tracking_broken", "critical", t, "统计中断？", day, "track:"+period.D(day), map[string]any{"value": y, "median": med})
		case c.atLeast(mean, 30) && y > mean+3*std && y > mean*1.5:
			src := d.driver(ctx, site.ID, day, true)
			t := fmt.Sprintf("%s访客 %s，是平时的 %.1f 倍", period.MD(day), fmtN(y), y/mean)
			short := "访客暴涨"
			if src != "" {
				t += "，主要来自 " + src
				short = src + " 带来流量"
			}
			d.emit(ctx, site, "traffic_spike", "good", t, short, day, "spike:"+period.D(day), map[string]any{"value": y, "mean": mean})
		case c.atLeast(mean, 30) && y < mean-3*std && y < mean*0.6:
			src := d.driver(ctx, site.ID, day, false)
			t := fmt.Sprintf("%s访客 %s，比平时少 %.0f%%", period.MD(day), fmtN(y), (1-y/mean)*100)
			if src != "" {
				t += "，主要是 " + src + " 少了"
			}
			d.emit(ctx, site, "traffic_drop", "warn", t, "访客下降", day, "drop:"+period.D(day), map[string]any{"value": y, "mean": mean})
		}
	}
}

// driver 找出某一天变化最大的来源（和之前 14 天的日均相比）。
func (d *Detector) driver(ctx context.Context, siteID int64, day time.Time, up bool) string {
	order := "DESC"
	if !up {
		order = "ASC"
	}
	var ch, src string
	err := d.M.Store.Pool.QueryRow(ctx, `SELECT channel, source FROM ga_daily_sources WHERE site_id=$1 AND date BETWEEN $2::date-14 AND $2
		GROUP BY channel, source
		ORDER BY coalesce(sum(sessions) FILTER (WHERE date=$2),0) - coalesce(sum(sessions) FILTER (WHERE date<$2),0)/14.0 `+order+` LIMIT 1`,
		siteID, day).Scan(&ch, &src)
	if err != nil {
		return ""
	}
	return metrics.SourceLabel(ch, src)
}

func (d *Detector) peakDay(ctx context.Context, siteID int64, path string, r period.Range) time.Time {
	var t time.Time
	if d.M.Store.Pool.QueryRow(ctx, `SELECT date FROM ga_daily_pages WHERE site_id=$1 AND path=$2 AND date BETWEEN $3 AND $4 ORDER BY page_views DESC LIMIT 1`,
		siteID, path, r.Start, r.End).Scan(&t) != nil {
		return r.End
	}
	return t
}

func meanStd(xs []float64) (float64, float64) {
	m := 0.0
	for _, x := range xs {
		m += x
	}
	m /= float64(len(xs))
	v := 0.0
	for _, x := range xs {
		v += (x - m) * (x - m)
	}
	return m, math.Sqrt(v / float64(len(xs)))
}

func median(xs []float64) float64 {
	c := append([]float64(nil), xs...)
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j] < c[j-1]; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

func signedPct(cur, prev float64) string {
	if prev == 0 {
		return "新出现"
	}
	p := (cur/prev - 1) * 100
	if p >= 0 {
		return fmt.Sprintf("+%.0f%%", p)
	}
	return fmt.Sprintf("−%.0f%%", -p)
}

func durZh(sec float64) string {
	s := int(math.Round(sec))
	if s < 60 {
		return fmt.Sprintf("%d 秒", s)
	}
	return fmt.Sprintf("%d 分 %d 秒", s/60, s%60)
}

func shortTitle(t, path string) string {
	if t == "" || t == "(not set)" {
		return path
	}
	t = strings.TrimSuffix(strings.TrimPrefix(t, "《"), "》")
	r := []rune(t)
	if len(r) > 12 {
		t = string(r[:12]) + "…"
	}
	return "《" + t + "》"
}

func topN[T any](xs []T, n int) []T {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}
