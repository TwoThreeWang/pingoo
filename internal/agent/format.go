package agent

import (
	"context"
	"fmt"
	"math"
	"strings"

	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/store"
)

// 给 AI 的数字一律先格式化好、算好变化：较弱的模型常把 0.417 当成 41.7%，或者把小幅下降说成“腰斩”。

// tileText 返回一个指标的本期、上期和变化，都是可以直接引用的文字。
func tileText(t metrics.Tile) (cur, prev, change string) {
	f := func(v float64) string {
		switch t.Kind {
		case "pct":
			return fmt.Sprintf("%.1f%%", v*100)
		case "dur":
			return fmt.Sprintf("%.0f 秒", v)
		case "dec":
			return fmt.Sprintf("%.2f", v)
		case "pos":
			return fmt.Sprintf("%.1f", v)
		}
		return metrics.Thousands(v)
	}
	cur, prev = f(t.Cur), f(t.Prev)
	switch t.Kind {
	case "pct":
		pp := (t.Cur - t.Prev) * 100
		switch {
		case math.Abs(pp) < 0.05:
			change = "持平"
		case pp > 0:
			change = fmt.Sprintf("上升 %.1f 个百分点", pp)
		default:
			change = fmt.Sprintf("下降 %.1f 个百分点", -pp)
		}
	case "pos":
		d := t.Prev - t.Cur
		switch {
		case t.Prev == 0:
			change = "上期没有排名"
		case math.Abs(d) < 0.05:
			change = "持平"
		case d > 0:
			change = fmt.Sprintf("前进 %.1f 位", d)
		default:
			change = fmt.Sprintf("后退 %.1f 位", -d)
		}
	default:
		change = pctChange(t.Cur, t.Prev)
	}
	return
}

// pctChange 返回“增长 59.3%”“下降 10.2%”。用文字写出方向，较弱的模型看到“−91%”也可能读反。
func pctChange(cur, prev float64) string {
	if prev == 0 {
		if cur == 0 {
			return "持平"
		}
		return "上期为 0"
	}
	d := (cur/prev - 1) * 100
	switch {
	case math.Abs(d) < 0.05:
		return "持平"
	case d > 0:
		return fmt.Sprintf("增长 %.1f%%", d)
	default:
		return fmt.Sprintf("下降 %.1f%%", -d)
	}
}

// countLine 返回“本期 15,846，上期 9,945（增长 59.3%）”。
func countLine(cur, prev int) string {
	return fmt.Sprintf("本期 %s，上期 %s（%s）", metrics.Thousands(float64(cur)), metrics.Thousands(float64(prev)), pctChange(float64(cur), float64(prev)))
}

// siteDetails 是生成洞察时 AI 几乎每次都会去查的数据：渠道、页面涨跌、搜索词。
// 直接写进提示，省掉好几轮工具调用（每多一轮，前面所有内容都要重发一遍）。
func (a *Agent) siteDetails(ctx context.Context, site store.Site, r period.Range) string {
	var sb strings.Builder
	if rows, err := a.M.Sources(ctx, site, r, "channels"); err == nil && len(rows) > 0 {
		sb.WriteString("\n渠道（访问次数）：\n")
		for _, x := range topN(rows, 8) {
			fmt.Fprintf(&sb, "- %s：%s，认真阅读 %.0f%%\n", x.Name, countLine(x.Sessions, x.Prev), x.EngRate*100)
		}
	}
	for _, tab := range []struct{ key, label string }{{"rising", "涨得最多的页面"}, {"declining", "跌得最多的页面"}} {
		rows, err := a.M.Content(ctx, site, r, tab.key)
		if err != nil || len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "\n%s（浏览量）：\n", tab.label)
		for _, p := range topN(rows, 5) {
			name := p.Path
			if p.Title != "" {
				name = metrics.BookTitle(p.Title, p.Path) + " " + p.Path
			}
			fmt.Fprintf(&sb, "- %s：%s", name, countLine(p.Views, p.PrevViews))
			if p.Main != "" {
				fmt.Fprintf(&sb, "；%s", p.Main)
			}
			sb.WriteString("\n")
		}
	}
	if site.GSCSite != "" || site.Demo {
		if rows, err := a.M.Search(ctx, site, r, "top"); err == nil && len(rows) > 0 {
			sb.WriteString("\n带来点击最多的搜索词：\n")
			for _, q := range topN(rows, 8) {
				fmt.Fprintf(&sb, "- %s：点击 %s，曝光 %s，点击率 %.1f%%，平均排名 %.1f", q.Query, countLine(q.Clicks, q.PrevClicks), metrics.Thousands(float64(q.Impr)), q.CTR*100, q.Pos)
				if q.PrevPos > 0 {
					fmt.Fprintf(&sb, "（上期 %.1f）", q.PrevPos)
				}
				if q.Path != "" {
					fmt.Fprintf(&sb, "，落地页 %s", q.Path)
				}
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}
