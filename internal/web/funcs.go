package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"

	"pingoo/internal/llm"
	"pingoo/internal/metrics"
	"pingoo/internal/period"
)

// md 渲染 AI 写的 Markdown。goldmark 默认会丢掉原始 HTML、过滤 javascript: 链接；
// 这里再把图片换成它的说明文字：AI 的回答不需要图片，而外链图片可以被用来把数据带到站外（见 server.go 的 csp）。
var md = goldmark.New(goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(noImages{}, 100))))

type noImages struct{}

func (noImages) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, func(w util.BufWriter, _ []byte, _ ast.Node, _ bool) (ast.WalkStatus, error) {
		return ast.WalkContinue, nil // 不输出 <img>，子节点（说明文字）照常渲染
	})
}

func toF(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case float64:
		return x
	case float32:
		return float64(x)
	}
	return 0
}

// num 把数字格式化成 12,400。
// ledgerKinds 是积分明细里各类记录的中文名。
var ledgerKinds = map[string]string{"insight": "AI 洞察", "diagnose": "AI 诊断", "chat": "AI 问答", "report": "AI 报告", "test": "测试",
	"redeem": "卡密充值", "bonus": "赠送", "admin": "管理员调整"}

func num(v any) string {
	f := toF(v)
	n := int64(math.Round(f))
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprint(n)
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

// compact 用在 token 用量这类大数字：1.82M、214K。
func compact(v any) string {
	f := toF(v)
	switch {
	case f >= 1e6:
		return fmt.Sprintf("%.2fM", f/1e6)
	case f >= 1e4:
		return fmt.Sprintf("%.0fK", f/1e3)
	}
	return num(f)
}

func pctStr(v any) string { return fmt.Sprintf("%.1f%%", toF(v)*100) }
func pct0(v any) string   { return fmt.Sprintf("%.0f%%", toF(v)*100) }

func durZh(v any) string {
	s := int(math.Round(toF(v)))
	if s < 60 {
		return fmt.Sprintf("%d 秒", s)
	}
	return fmt.Sprintf("%d 分 %d 秒", s/60, s%60)
}

func durShort(v any) string {
	s := int(math.Round(toF(v)))
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// delta 输出 <span class="d good">+9.6%</span>；better: 1 越大越好，-1 越小越好，0 只显示不上色。
func delta(cur, prev any, better int) template.HTML {
	c, p := toF(cur), toF(prev)
	if p < 30 && c < 30 {
		return `<span class="d flat" title="数量太少，参考意义不大">样本少</span>`
	}
	if p == 0 {
		return `<span class="d flat">新</span>`
	}
	r := (c - p) / p
	a := math.Abs(r * 100)
	txt := fmt.Sprintf("%.1f%%", a)
	if a >= 100 {
		txt = fmt.Sprintf("%.0f%%", a)
	}
	sign := "+"
	if r < 0 {
		sign = "−"
	}
	return template.HTML(fmt.Sprintf(`<span class="d %s" title="对比期 %s">%s%s</span>`, cls(r, a < 2, better), num(p), sign, txt))
}

func cls(r float64, flat bool, better int) string {
	if flat || better == 0 {
		return "flat"
	}
	if (r > 0) == (better > 0) {
		return "good"
	}
	return "bad"
}

// tileDelta 处理比例（百分点）和排名（前进/后退）。
func tileDelta(t metrics.Tile) template.HTML {
	if !t.HasVal {
		return ""
	}
	switch t.Kind {
	case "pct":
		pp := (t.Cur - t.Prev) * 100
		sign := "+"
		if pp < 0 {
			sign = "−"
		}
		return template.HTML(fmt.Sprintf(`<span class="d %s" title="百分点">%s%.1fpp</span>`, cls(pp, math.Abs(pp) < 0.5, t.Better), sign, math.Abs(pp)))
	case "pos":
		if t.Prev == 0 {
			return ""
		}
		d := t.Prev - t.Cur
		if math.Abs(d) < 0.05 {
			return `<span class="d flat">持平</span>`
		}
		word := "前进"
		if d < 0 {
			word = "后退"
		}
		return template.HTML(fmt.Sprintf(`<span class="d %s">%s %.1f 位</span>`, cls(d, math.Abs(d) < 0.2, 1), word, math.Abs(d)))
	}
	if t.Kind == "count" {
		return delta(t.Cur, t.Prev, t.Better)
	}
	if t.Prev == 0 {
		return ""
	}
	r := (t.Cur - t.Prev) / t.Prev
	a := math.Abs(r * 100)
	sign := "+"
	if r < 0 {
		sign = "−"
	}
	return template.HTML(fmt.Sprintf(`<span class="d %s">%s%.1f%%</span>`, cls(r, a < 2, t.Better), sign, a))
}

func fmtTile(kind string, v float64) string {
	switch kind {
	case "pct":
		return pctStr(v)
	case "dur":
		return durZh(v)
	case "dec":
		return fmt.Sprintf("%.2f", v)
	case "pos":
		return fmt.Sprintf("%.1f", v)
	}
	return num(v)
}

func mdDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return period.MD(t)
}

func shortDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("01-02")
}

func clock(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return "今天 " + t.Format("15:04")
	}
	return t.Format("01-02 15:04")
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 48*time.Hour:
		return "昨天"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
	return t.Format("01-02")
}

// chartData 把序列变成前端图表需要的 JSON。
type chartOpts struct {
	Cur      []float64            `json:"cur"`
	Prev     []float64            `json:"prev"`
	Labels   map[string][]string  `json:"labels"`
	Ann      []metrics.Annotation `json:"ann,omitempty"`
	Markers  []metrics.Annotation `json:"markers,omitempty"`
	Anomaly  bool                 `json:"anomaly,omitempty"`
	Title    string               `json:"title,omitempty"`
	Name     string               `json:"name,omitempty"`
	PrevName string               `json:"prevName,omitempty"` // 对比期的叫法，默认“上期”
	Kind     string               `json:"kind,omitempty"`
	Axes     bool                 `json:"axes,omitempty"`
	Invert   bool                 `json:"invert,omitempty"`
	Zero     bool                 `json:"zero,omitempty"`
}

func chartJSON(s metrics.Series, title, name, kind string) chartOpts {
	cur, prev := s.Cur, s.Prev
	if cur == nil {
		cur = []float64{}
	}
	if prev == nil {
		prev = []float64{}
	}
	return chartOpts{Cur: cur, Prev: prev, Labels: map[string][]string{"cur": nz(s.Labels), "prev": nz(s.PrevLabels)}, Title: title, Name: name, Kind: kind}
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func markdown(s string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(s), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(s))
	}
	return template.HTML(buf.String())
}

// bold 把文本里成对的 **xx** 变成加粗（用于 AI 写的短句），其余内容全部转义。
func bold(s string) template.HTML {
	esc := template.HTMLEscapeString(s)
	parts := strings.Split(esc, "**")
	if len(parts) < 3 {
		return template.HTML(esc)
	}
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 1 && i < len(parts)-1 {
			b.WriteString("<b>" + p + "</b>")
		} else {
			if i%2 == 1 {
				b.WriteString("**")
			}
			b.WriteString(p)
		}
	}
	return template.HTML(b.String())
}

// money 把金额写成两位小数、带千分位（例如 1,234.50）。
func money(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	i := strings.Index(s, ".")
	intPart, frac := s[:i], s[i:]
	neg := strings.HasPrefix(intPart, "-")
	intPart = strings.TrimPrefix(intPart, "-")
	var b strings.Builder
	for j, c := range intPart {
		if j > 0 && (len(intPart)-j)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String() + frac
	}
	return b.String() + frac
}

// keepText 把保留天数写成好读的说法：不到两个月写天数，整月附近写“N 个月”（90 → 3 个月，395 → 13 个月）。
func keepText(days int) string {
	if days < 60 {
		return strconv.Itoa(days) + " 天"
	}
	m := float64(days) / 30.4
	if math.Abs(m-math.Round(m)) < 0.1 {
		return strconv.Itoa(int(math.Round(m))) + " 个月"
	}
	return strconv.Itoa(days) + " 天"
}

// trimFloat 把小数写成最短形式（1.5、0.25），先舍到 6 位，避免 1.5 × 100 显示成 150.00000000000003。
func trimFloat(v float64) string {
	return strconv.FormatFloat(math.Round(v*1e6)/1e6, 'f', -1, 64)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

var icons = map[string]string{
	"critical": `<svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><circle cx="6" cy="6" r="5.5" fill="var(--bad)"/><path d="M6 3.2v3.4M6 8.4v.2" stroke="#fff" stroke-width="1.5" stroke-linecap="round"/></svg>`,
	"warn":     `<svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M6 1 11.3 10.5H.7Z" fill="var(--warn)"/><path d="M6 4.6v2.6M6 8.7v.1" stroke="#151514" stroke-width="1.4" stroke-linecap="round"/></svg>`,
	"chance":   `<svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><rect x="2.5" y="2.5" width="7" height="7" rx="1" transform="rotate(45 6 6)" fill="var(--accent)"/></svg>`,
	"good":     `<svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M6 1.5 10.5 7H7.5v3.5h-3V7H1.5Z" fill="var(--good)"/></svg>`,
	"info":     `<svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><circle cx="6" cy="6" r="4.6" fill="none" stroke="var(--ink-3)" stroke-width="1.6"/></svg>`,
	"down":     `<svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M6 10.5 1.5 5h3V1.5h3V5h3Z" fill="var(--warn)"/></svg>`,
	"flat":     `<svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><rect x="1.5" y="5" width="9" height="2" rx="1" fill="var(--ink-3)"/></svg>`,
	"ok":       `<svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="6.5" fill="var(--good)"/><path d="m4 7.2 2 2 4-4.2" fill="none" stroke="#fff" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
	"error":    `<svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="6.5" fill="var(--bad)"/><path d="M7 3.8v4M7 10v.2" stroke="#fff" stroke-width="1.7" stroke-linecap="round"/></svg>`,
	"warn14":   `<svg width="14" height="14" viewBox="0 0 12 12" aria-hidden="true"><path d="M6 1 11.3 10.5H.7Z" fill="var(--warn)"/><path d="M6 4.6v2.6M6 8.7v.1" stroke="#151514" stroke-width="1.4" stroke-linecap="round"/></svg>`,
	"info14":   `<svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="5.6" fill="none" stroke="var(--ink-3)" stroke-width="1.6"/><path d="M7 6.2v3.4M7 4.3v.1" stroke="var(--ink-3)" stroke-width="1.6" stroke-linecap="round"/></svg>`,
	"pending":  `<svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="5.6" fill="none" stroke="var(--ink-3)" stroke-width="1.6" stroke-dasharray="2.4 2.2"/></svg>`,
	"logo":     `<svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true"><rect width="18" height="18" rx="4" fill="currentColor"/><path d="M4 12.5 7.5 9l2.5 2.5L14 6" fill="none" stroke="var(--bg)" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
}

func icon(name string) template.HTML { return template.HTML(icons[name]) }

var sevLabels = map[string]string{"critical": "紧急", "warn": "注意", "chance": "机会", "good": "利好", "info": "提示"}

var statusInfo = map[string][2]string{
	"anomaly": {"critical", "异常"}, "down": {"down", "下滑"}, "up": {"good", "增长"}, "flat": {"flat", "平稳"}, "empty": {"flat", "暂无数据"},
}

func statusHTML(s string) template.HTML {
	x, ok := statusInfo[s]
	if !ok {
		x = statusInfo["flat"]
	}
	return template.HTML(`<span class="st">` + icons[x[0]] + x[1] + `</span>`)
}

func sevHTML(s string) template.HTML {
	return template.HTML(`<span class="st">` + icons[s] + sevLabels[s] + `</span>`)
}

func checkIcon(s string) template.HTML {
	return icon(map[string]string{"ok": "ok", "warn": "warn14", "error": "error", "info": "info14"}[s])
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"trimFloat":  trimFloat,
		"effortKeys": func() []string { return []string{llm.EffortOff, llm.EffortLow, llm.EffortMedium, llm.EffortHigh} },
		"effortName": func(k string) string {
			return map[string]string{llm.EffortOff: "不思考", llm.EffortLow: "少想", llm.EffortMedium: "正常", llm.EffortHigh: "多想"}[k]
		},
		"money":     money,
		"hasPrefix": strings.HasPrefix,
		"decDelta": func(cur, prev float64) template.HTML {
			return tileDelta(metrics.Tile{Kind: "dec", Better: 1, HasVal: true, Cur: cur, Prev: prev})
		},
		"keepText": keepText,
		"host": func(raw string) string {
			if u, err := url.Parse(raw); err == nil && u.Host != "" {
				return u.Host
			}
			return raw
		},
		"mailKind": func(k string) string {
			return map[string]string{"register": "注册（初始密码）", "reset": "找回密码", "email_code": "改邮箱验证码", "notice": "通知", "feedback": "用户反馈"}[k]
		},
		"ledgerKind": func(k string) string {
			if v, ok := ledgerKinds[k]; ok {
				return v
			}
			return k
		},
		"num": num, "compact": compact, "pct": pctStr, "pct0": pct0, "dur": durZh, "durShort": durShort,
		"delta": delta, "tileDelta": tileDelta, "fmtTile": fmtTile, "md": mdDate, "shortDate": shortDate, "clock": clock, "ago": ago,
		"json": toJSON, "markdown": markdown, "bold": bold, "truncate": truncate, "icon": icon,
		"status": statusHTML, "sev": sevHTML, "sevLabel": func(s string) string { return sevLabels[s] }, "checkIcon": checkIcon,
		"chart": chartJSON, "channel": metrics.ChannelName, "weekday": metrics.WeekdayName,
		"add": func(a, b int) int { return a + b }, "sub": func(a, b int) int { return a - b },
		"mul":  func(a, b any) float64 { return toF(a) * toF(b) },
		"sub0": func(a, b any) float64 { return toF(a) - toF(b) },
		"add0": func(a, b any) float64 { return toF(a) + toF(b) },
		"div0": func(a, b any) float64 {
			if toF(b) == 0 {
				return 0
			}
			return toF(a) / toF(b)
		},
		"mod": func(a, b int) int { return a % b },
		"deref2": func(t *time.Time) time.Time {
			if t == nil {
				return time.Time{}
			}
			return *t
		},
		"trimProp": func(p string) string { return strings.TrimPrefix(p, "properties/") },
		"pad2":     func(i int) string { return fmt.Sprintf("%02d", i) },
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i
			}
			return s
		},
		"widthPct": func(v, max any) string {
			m := toF(max)
			if m <= 0 {
				return "0%"
			}
			return fmt.Sprintf("%.1f%%", math.Max(1, toF(v)/m*100))
		},
		"alpha": func(v, max float64) string {
			if max <= 0 {
				return "0.06"
			}
			return fmt.Sprintf("%.3f", 0.06+0.89*v/max)
		},
		"title": metrics.BookTitle,
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m
		},
		"deref": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"ymd":   period.D,
		"ymdZh": period.YMD,
		"chartCmp": func(s metrics.Series, title, name, kind, prevName string) chartOpts {
			o := chartJSON(s, title, name, kind)
			o.PrevName = prevName
			return o
		},
		"cardChart": func(c metrics.SiteCard, prevName string) chartOpts {
			o := chartJSON(c.Series, c.Site.Name, "访客", "count")
			o.PrevName = prevName
			o.Anomaly = c.Status == "anomaly"
			if c.Annotation != nil {
				o.Ann = []metrics.Annotation{*c.Annotation}
			}
			return o
		},
		"smallDelta": func(sm metrics.SearchSmall) template.HTML {
			return tileDelta(metrics.Tile{Kind: sm.Kind, Better: sm.Better, Cur: sm.Cur, Prev: sm.Prev, HasVal: true})
		},
		"spark": func(cur []float64, name string) chartOpts {
			if cur == nil {
				cur = []float64{}
			}
			return chartOpts{Cur: cur, Prev: []float64{}, Labels: map[string][]string{"cur": {}, "prev": {}}, Title: name, Name: "访客"}
		},
		"initial": func(s string) string {
			for _, r := range s {
				return strings.ToUpper(string(r))
			}
			return "?"
		},
		"first": func(s []string) string {
			if len(s) == 0 {
				return ""
			}
			return s[0]
		},
		"last": func(s []string) string {
			if len(s) == 0 {
				return ""
			}
			return s[len(s)-1]
		},
		"signalSection": func(kind string) string {
			switch kind {
			case "page_surge", "page_decline", "page_new":
				return "content"
			case "new_referrer", "ai_referrer":
				return "sources"
			case "rank_drop", "near_top", "low_ctr":
				return "search"
			case "mobile_gap":
				return "audience"
			case "not_found", "tracking_broken":
				return "health"
			}
			return "metrics"
		},
		"diagnosable": func(sev string) bool { return sev == "critical" || sev == "warn" },
		"confBars":    func(c string) int { return map[string]int{"high": 3, "medium": 2, "low": 1}[c] },
		"confLabel":   func(c string) string { return map[string]string{"high": "高", "medium": "中", "low": "低"}[c] },
		"verdict": func(v string) template.HTML {
			switch v {
			case "effective":
				return `<span class="d good">有效</span>`
			case "ineffective":
				return `<span class="d bad">没效果</span>`
			case "unclear":
				return `<span class="d flat">还看不出</span>`
			}
			return `<span class="d flat">待观察</span>`
		},
		"kindLabel": func(k string) string {
			return map[string]string{"weekly": "周报", "monthly": "月报", "custom": "报告"}[k]
		},
		"contains": strings.Contains,
		// q 给放进 data-* / hx-* 属性里的网址参数做编码。html/template 只会自动编码 href、src 这类它认识的网址属性，
		// 其他属性里的值如果带 ? & # % +，前端拼出来的请求参数会被截断。
		"q":     url.QueryEscape,
		"lines": func(s string) []string { return strings.Split(strings.TrimSpace(s), "\n") },
		"absf":  math.Abs,
		"f64":   toF,
	}
}
