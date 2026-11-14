package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pingoo/internal/period"
	"pingoo/internal/store"
)

type DeviceRow struct {
	Name    string
	Share   float64
	EngRate float64
	Dwell   float64 // 每次访问平均停留秒数
}

type GeoRow struct {
	Country string
	Share   float64
	EngRate float64
}

type Audience struct {
	Devices    []DeviceRow
	DeviceNote string
	Geo        []GeoRow
	GeoNote    string
	Langs      []GeoRow // Country 字段放语言名
	LangNote   string
	Heat       [7][24]float64 // 周一..周日 × 0..23 点
	HeatMax    float64
	HeatNote   string
	HeatDays   int
	NewShare   float64
	NewPrev    float64
	HasNew     bool
}

var deviceNames = map[string]string{"mobile": "手机", "desktop": "电脑", "tablet": "平板", "smart tv": "电视"}

var countryNames = map[string]string{
	"China": "中国", "United States": "美国", "Germany": "德国", "Japan": "日本", "United Kingdom": "英国", "Canada": "加拿大",
	"India": "印度", "Singapore": "新加坡", "Hong Kong": "中国香港", "Taiwan": "中国台湾", "France": "法国", "Australia": "澳大利亚",
	"Netherlands": "荷兰", "South Korea": "韩国", "Russia": "俄罗斯", "Brazil": "巴西", "Colombia": "哥伦比亚", "Argentina": "阿根廷", "Türkiye": "土耳其", "Pakistan": "巴基斯坦", "Bangladesh": "孟加拉国", "Egypt": "埃及", "Nigeria": "尼日利亚", "Spain": "西班牙", "Italy": "意大利",
	"Vietnam": "越南", "Indonesia": "印度尼西亚", "Malaysia": "马来西亚", "Thailand": "泰国", "Sweden": "瑞典", "Poland": "波兰",
	"Switzerland": "瑞士", "Turkey": "土耳其", "Ukraine": "乌克兰", "Mexico": "墨西哥", "Philippines": "菲律宾", "(not set)": "未知",
}

func CountryName(c string) string {
	if c == "(other)" || c == "(not set)" {
		return "其他"
	}
	if v, ok := countryNames[c]; ok {
		return v
	}
	return c
}

func (m *Svc) Audience(ctx context.Context, site store.Site, r period.Range) Audience {
	var a Audience
	if r.Hourly {
		r = period.Resolve("d7", time.Now(), site.Location())
	}
	// 设备
	rows, err := m.Store.Pool.Query(ctx, `SELECT device, sum(sessions), sum(engaged_sessions), sum(engagement_seconds)
		FROM ga_daily_tech WHERE site_id=$1 AND date BETWEEN $2 AND $3 GROUP BY device ORDER BY 2 DESC`, site.ID, r.Start, r.End)
	if err == nil {
		total := 0.0
		type raw struct {
			name      string
			sess, eng float64
			sec       float64
		}
		var rs []raw
		for rows.Next() {
			var x raw
			if rows.Scan(&x.name, &x.sess, &x.eng, &x.sec) == nil {
				rs = append(rs, x)
				total += x.sess
			}
		}
		rows.Close()
		for _, x := range rs {
			n := deviceNames[strings.ToLower(x.name)]
			if n == "" {
				n = x.name
			}
			a.Devices = append(a.Devices, DeviceRow{Name: n, Share: ratio(x.sess, total), EngRate: ratio(x.eng, x.sess), Dwell: ratio(x.sec, x.sess)})
		}
		a.DeviceNote = deviceNote(a.Devices)
	}
	// 地区
	rows, err = m.Store.Pool.Query(ctx, `SELECT country, sum(sessions), sum(engaged_sessions) FROM ga_daily_geo
		WHERE site_id=$1 AND date BETWEEN $2 AND $3 GROUP BY country ORDER BY 2 DESC`, site.ID, r.Start, r.End)
	if err == nil {
		total := 0.0
		var gs []GeoRow
		var sess []float64
		for rows.Next() {
			var c string
			var s, e float64
			if rows.Scan(&c, &s, &e) == nil {
				gs = append(gs, GeoRow{Country: CountryName(c), EngRate: ratio(e, s)})
				sess = append(sess, s)
				total += s
			}
		}
		rows.Close()
		for i := range gs {
			gs[i].Share = ratio(sess[i], total)
		}
		a.Geo = topN(gs, 8)
		if len(a.Geo) >= 2 {
			a.GeoNote = fmt.Sprintf("%s和%s的读者占 %.0f%%。", a.Geo[0].Country, a.Geo[1].Country, (a.Geo[0].Share+a.Geo[1].Share)*100)
		} else if len(a.Geo) == 1 {
			a.GeoNote = fmt.Sprintf("几乎所有读者都来自%s。", a.Geo[0].Country)
		}
	}
	// 语言
	rows, err = m.Store.Pool.Query(ctx, `SELECT language, sum(sessions), sum(engaged_sessions) FROM ga_daily_lang
		WHERE site_id=$1 AND date BETWEEN $2 AND $3 GROUP BY language ORDER BY 2 DESC`, site.ID, r.Start, r.End)
	if err == nil {
		total := 0.0
		var ls []GeoRow
		var sess []float64
		for rows.Next() {
			var l string
			var s, e float64
			if rows.Scan(&l, &s, &e) == nil {
				ls = append(ls, GeoRow{Country: LanguageName(l), EngRate: ratio(e, s)})
				sess = append(sess, s)
				total += s
			}
		}
		rows.Close()
		for i := range ls {
			ls[i].Share = ratio(sess[i], total)
		}
		a.Langs = topN(ls, 6)
		a.LangNote = langNote(a.Langs)
	}
	// 时段热力图：至少看 28 天，避免偶然
	hs, he := r.Start, r.End
	if r.Days < 28 {
		hs = he.AddDate(0, 0, -27)
	}
	a.HeatDays = period.DaysBetween(hs, he) + 1
	rows, err = m.Store.Pool.Query(ctx, `SELECT (extract(isodow FROM date)::int - 1), hour, sum(sessions) FROM ga_hourly
		WHERE site_id=$1 AND date BETWEEN $2 AND $3 GROUP BY 1, 2`, site.ID, hs, he)
	if err == nil {
		for rows.Next() {
			var d, h int
			var v float64
			if rows.Scan(&d, &h, &v) == nil && d >= 0 && d < 7 && h >= 0 && h < 24 {
				a.Heat[d][h] = v
				if v > a.HeatMax {
					a.HeatMax = v
				}
			}
		}
		rows.Close()
		a.HeatNote = heatNote(a.Heat)
	}
	cur, prev := m.Totals(ctx, site, r)
	if cur.Users > 0 {
		a.HasNew = true
		a.NewShare, a.NewPrev = cur.NewShare(), prev.NewShare()
	}
	return a
}

func deviceNote(ds []DeviceRow) string {
	var mob, desk *DeviceRow
	for i := range ds {
		switch ds[i].Name {
		case "手机":
			mob = &ds[i]
		case "电脑":
			desk = &ds[i]
		}
	}
	if mob == nil || desk == nil {
		return ""
	}
	if desk.EngRate-mob.EngRate >= 0.15 && mob.Share >= 0.2 {
		return fmt.Sprintf("手机读者占 %.0f%%，但认真阅读的比例只有 %.0f%%，电脑是 %.0f%%，手机体验可能有问题。", mob.Share*100, mob.EngRate*100, desk.EngRate*100)
	}
	if mob.Share > desk.Share {
		return fmt.Sprintf("手机读者更多（%.0f%%），两边的阅读质量差不多。", mob.Share*100)
	}
	return fmt.Sprintf("电脑读者更多（%.0f%%），两边的阅读质量差不多。", desk.Share*100)
}

var languageNames = map[string]string{
	"English": "英语", "Chinese": "中文", "Japanese": "日语", "German": "德语", "French": "法语", "Spanish": "西班牙语",
	"Korean": "韩语", "Russian": "俄语", "Portuguese": "葡萄牙语", "Italian": "意大利语", "Dutch": "荷兰语", "Polish": "波兰语",
	"Turkish": "土耳其语", "Vietnamese": "越南语", "Indonesian": "印尼语", "Thai": "泰语", "Arabic": "阿拉伯语", "Hindi": "印地语",
	"(not set)": "未知",
}

func LanguageName(l string) string {
	if l == "(other)" || l == "(not set)" {
		return "其他"
	}
	if v, ok := languageNames[l]; ok {
		return v
	}
	return l
}

// langNote 在“读者语言和网站语言可能不一致”时给一句提示。
func langNote(ls []GeoRow) string {
	if len(ls) == 0 {
		return ""
	}
	top := ls[0]
	if len(ls) >= 2 && ls[1].Share >= 0.25 {
		return fmt.Sprintf("%s读者占 %.0f%%，%s读者也有 %.0f%%。如果网站只有一种语言，可以考虑为另一种语言的读者做些内容。", top.Country, top.Share*100, ls[1].Country, ls[1].Share*100)
	}
	return fmt.Sprintf("大部分读者使用%s（%.0f%%）。", top.Country, top.Share*100)
}

var weekdayNames = []string{"周一", "周二", "周三", "周四", "周五", "周六", "周日"}

func WeekdayName(i int) string { return weekdayNames[i%7] }

func heatNote(h [7][24]float64) string {
	// 找出连续 3 小时总和最大的时段
	bestD, bestH, best := 0, 0, 0.0
	for d := 0; d < 7; d++ {
		for x := 0; x < 22; x++ {
			s := h[d][x] + h[d][x+1] + h[d][x+2]
			if s > best {
				bestD, bestH, best = d, x, s
			}
		}
	}
	if best == 0 {
		return ""
	}
	// 看看相邻几天的同一时段是否也很高
	days := []int{bestD}
	for _, d := range []int{bestD - 1, bestD + 1, bestD - 2, bestD + 2} {
		if d < 0 || d > 6 {
			continue
		}
		s := h[d][bestH] + h[d][bestH+1] + h[d][bestH+2]
		if s >= best*0.85 {
			days = append(days, d)
		}
	}
	lo, hi := bestD, bestD
	for _, d := range days {
		lo, hi = min(lo, d), max(hi, d)
	}
	when := weekdayNames[lo]
	if hi > lo {
		when = weekdayNames[lo] + "到" + weekdayNames[hi]
	}
	return fmt.Sprintf("%s %02d:00–%02d:00 读者最多，适合在这个时段前发布和转发。", when, bestH, bestH+3)
}
