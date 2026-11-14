// Package period 处理“今天 / 昨天 / 近 7 天 …”这些日期范围以及对比期。
package period

import (
	"fmt"
	"time"
)

type Range struct {
	Key       string
	Label     string
	Start     time.Time // 含，当地日期 00:00
	End       time.Time // 含
	PrevStart time.Time
	PrevEnd   time.Time
	Days      int
	Hourly    bool // 今天、昨天按小时画图
	IsCustom  bool // 站长自己选的起止日期（Key 形如 2026-10-01_2026-10-31）
	// Compare 是对比期的算法：空表示紧挨着的上一段等长时间；CompareYoY 表示去年同期。
	Compare string
	Live    bool // 今天：数据还没同步，需要实时查 GA
	NowHour int  // 今天已经过去的小时数
}

var Keys = []struct{ Key, Label string }{
	{"today", "今天"}, {"yesterday", "昨天"}, {"d7", "7 天"}, {"d28", "28 天"}, {"d90", "90 天"},
}

func Valid(key string) bool {
	for _, k := range Keys {
		if k.Key == key {
			return true
		}
	}
	_, _, ok := ParseCustomKey(key, time.UTC)
	return ok
}

// MaxCustomDays 是自选日期范围最长的天数。
const MaxCustomDays = 366

// CompareYoY 表示和去年同期对比。
const CompareYoY = "yoy"

// yoyShift 是“去年同期”往前挪的天数：52 周整。这样对比期和本期的星期几是对齐的
// （周一对周一），不会因为今年多一个周末就显得涨了或跌了。
const yoyShift = 364

// CustomKey 把起止日期拼成自选范围的 Key。
func CustomKey(start, end time.Time) string { return D(start) + "_" + D(end) }

// ParseCustomKey 解析自选范围的 Key（2026-10-01_2026-10-31）。日期不合法、结束早于开始、或者超过 MaxCustomDays 时 ok 为 false。
func ParseCustomKey(key string, loc *time.Location) (start, end time.Time, ok bool) {
	if len(key) != 21 || key[10] != '_' {
		return
	}
	start, err1 := time.ParseInLocation("2006-01-02", key[:10], loc)
	end, err2 := time.ParseInLocation("2006-01-02", key[11:], loc)
	if err1 != nil || err2 != nil || end.Before(start) || DaysBetween(start, end)+1 > MaxCustomDays {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// setPrev 按 Compare 重新算对比期。Start / End / Days 变了之后都要调用。
func (r *Range) setPrev() {
	if r.Compare == CompareYoY && !r.Hourly {
		r.PrevStart = r.Start.AddDate(0, 0, -yoyShift)
		r.PrevEnd = r.End.AddDate(0, 0, -yoyShift)
		return
	}
	r.Compare = ""
	r.PrevEnd = r.Start.AddDate(0, 0, -1)
	r.PrevStart = r.Start.AddDate(0, 0, -r.Days)
}

// WithCompare 换一种对比方式（"" 或 CompareYoY）。今天、昨天按小时对比，不支持去年同期。
func (r Range) WithCompare(mode string) Range {
	x := r
	x.Compare = mode
	x.setPrev()
	return x
}

// Sequential 返回对比期是“紧挨着的上一段”的同一个范围。内容、来源这些明细只同步了最近几个月，
// 没有一年前的数据可比，所以明细表格始终用这种对比。
func (r Range) Sequential() Range { return r.WithCompare("") }

// PrevName 是对比期在页面上的叫法。
func (r Range) PrevName() string {
	if r.Compare == CompareYoY {
		return "去年同期"
	}
	return "上期"
}

// Resolve 根据当地时间算出本期和对比期。日期范围（7/28/90 天）都截止到昨天，因为今天的数据还不完整。
func Resolve(key string, now time.Time, loc *time.Location) Range {
	if loc == nil {
		loc = time.UTC
	}
	now = now.In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	r := Range{Key: key}
	switch key {
	case "today":
		r.Label, r.Start, r.End, r.Days, r.Hourly, r.Live = "今天", today, today, 1, true, true
		r.NowHour = now.Hour()
	case "yesterday":
		y := today.AddDate(0, 0, -1)
		r.Label, r.Start, r.End, r.Days, r.Hourly = "昨天", y, y, 1, true
	case "d7", "d28", "d90":
		n := map[string]int{"d7": 7, "d28": 28, "d90": 90}[key]
		r.Label = fmt.Sprintf("%d 天", n)
		r.End = today.AddDate(0, 0, -1)
		r.Start = today.AddDate(0, 0, -n)
		r.Days = n
	default:
		// 自选范围：结束日期最晚到昨天（今天的数据还不完整）
		s, e, ok := ParseCustomKey(key, loc)
		yesterday := today.AddDate(0, 0, -1)
		if ok && e.After(yesterday) {
			e = yesterday
		}
		if !ok || e.Before(s) {
			return Resolve("d7", now, loc)
		}
		r = Custom(s, e)
		r.Key, r.IsCustom = CustomKey(s, e), true
		return r
	}
	r.setPrev()
	return r
}

// DaysBetween 返回从 a 到 b 隔了几个日历日（只看年月日，不看时区和时刻）。
// 不能用 b.Sub(a) 除以 24 小时：夏令时切换的那天只有 23 或 25 小时；
// 从数据库 date 列读出来的日期是 UTC 零点，和当地零点直接相减也会多出或少掉几个小时。
func DaysBetween(a, b time.Time) int {
	ua := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	ub := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(ub.Sub(ua).Hours() / 24)
}

// Custom 构造任意日期范围（报告用），对比期为紧挨着的同长度时间段。
func Custom(start, end time.Time) Range {
	days := DaysBetween(start, end) + 1
	r := Range{Key: "custom", Start: start, End: end, Days: days}
	r.setPrev()
	r.Label = fmt.Sprintf("%s – %s", MD(start), MD(end))
	return r
}

func (r Range) Dates() []time.Time     { return dates(r.Start, r.End) }
func (r Range) PrevDates() []time.Time { return dates(r.PrevStart, r.PrevEnd) }

func dates(a, b time.Time) []time.Time {
	var out []time.Time
	for d := a; !d.After(b); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

// Shift 把范围的结束日期限制在 maxEnd 以内（GSC 有 2~3 天延迟），并同步调整对比期长度。
func (r Range) ClampEnd(maxEnd time.Time) Range {
	if maxEnd.IsZero() {
		return r
	}
	// maxEnd 通常来自数据库（UTC 零点），先换成和 r.End 同一时区的那一天再比较
	maxEnd = time.Date(maxEnd.Year(), maxEnd.Month(), maxEnd.Day(), 0, 0, 0, 0, r.End.Location())
	if !r.End.After(maxEnd) {
		return r
	}
	if maxEnd.Before(r.Start) {
		x := r
		x.End = r.Start.AddDate(0, 0, -1)
		x.Days = 0
		return x
	}
	x := r
	x.End = maxEnd
	x.Days = DaysBetween(x.Start, x.End) + 1
	x.setPrev()
	return x
}

// EndAt 在数据还没同步到范围结束日时，把整个窗口往前挪，让它以 end 结束（长度不变）。
// 这样“近 7 天”始终是 7 天对比 7 天，趋势线也不会在最后一天掉到 0。今天、昨天不挪。
// MaxShiftDays 是 EndAt 最多往前挪的天数。
const MaxShiftDays = 3

func (r Range) EndAt(end time.Time) Range {
	if r.Hourly || r.IsCustom || end.IsZero() {
		return r // 自选范围是站长明确指定的日期，不挪
	}
	end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, r.End.Location())
	if !end.Before(r.End) {
		return r
	}
	// 只在落后一两天（GA 还没处理完、今天还没同步）时往前挪；落后更多说明还在回填或同步出了问题，
	// 这时保持原来的时间段，让各网站的日期对得上，缺的数据由健康检查提示
	if DaysBetween(end, r.End) > MaxShiftDays {
		return r
	}
	x := r
	x.End = end
	x.Start = end.AddDate(0, 0, -(r.Days - 1))
	x.setPrev()
	return x
}

func D(t time.Time) string { return t.Format("2006-01-02") }

func MD(t time.Time) string { return fmt.Sprintf("%d月%d日", int(t.Month()), t.Day()) }

// Describe 返回页面上那行“11月2日 – 11月8日 · 对比 10月26日 – 11月1日”。
func (r Range) Describe() string {
	switch r.Key {
	case "today":
		return fmt.Sprintf("今天 00:00 – %02d:00 · 对比昨天同一时段", r.NowHour+1)
	case "yesterday":
		return fmt.Sprintf("%s · 对比前一天", MD(r.Start))
	}
	if r.Compare == CompareYoY {
		return fmt.Sprintf("%s – %s · 对比去年同期 %s – %s", MD(r.Start), MD(r.End), YMD(r.PrevStart), MD(r.PrevEnd))
	}
	return fmt.Sprintf("%s – %s · 对比 %s – %s", MD(r.Start), MD(r.End), MD(r.PrevStart), MD(r.PrevEnd))
}

// YMD 带年份，用在跨年的对比期上。
func YMD(t time.Time) string {
	return fmt.Sprintf("%d年%d月%d日", t.Year(), int(t.Month()), t.Day())
}

// Kicker 是洞察标题上方的小字。
func (r Range) Kicker() string {
	switch r.Key {
	case "today":
		return "今日"
	case "yesterday":
		return "昨日"
	case "d7":
		return "本周"
	}
	if r.IsCustom {
		return r.Label + " "
	}
	return fmt.Sprintf("近 %d 天", r.Days)
}

// ISOWeek 返回“2026 年第 45 周”。
func ISOWeek(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d 年第 %d 周", y, w)
}
