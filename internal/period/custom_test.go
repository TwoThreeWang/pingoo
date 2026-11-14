package period

import (
	"testing"
	"time"
)

func TestCustomRangeKey(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, loc) // 昨天是 11/11
	for key, want := range map[string]bool{
		"d7": true, "today": true, "2026-10-01_2026-10-31": true, "2026-10-31_2026-10-31": true,
		"2026-10-31_2026-10-01": false, // 结束早于开始
		"2025-10-01_2026-10-31": false, // 超过 366 天
		"2026-13-01_2026-10-31": false, "2026-10-01": false, "": false, "d14": false, "2026-10-01_2026-10-3x": false,
	} {
		if Valid(key) != want {
			t.Errorf("Valid(%q) = %v", key, !want)
		}
	}
	r := Resolve("2026-10-01_2026-10-31", now, loc)
	if !r.IsCustom || r.Key != "2026-10-01_2026-10-31" || r.Days != 31 || D(r.Start) != "2026-10-01" || D(r.End) != "2026-10-31" ||
		D(r.PrevStart) != "2026-08-31" || D(r.PrevEnd) != "2026-09-30" || r.Hourly || r.Label != "10月1日 – 10月31日" {
		t.Errorf("自选范围解析不对: %+v", r)
	}
	// 结束日期选到了今天或以后：截到昨天，Key 也跟着变（缓存键才不会错）
	r = Resolve("2026-11-05_2026-11-20", now, loc)
	if D(r.End) != "2026-11-11" || r.Key != "2026-11-05_2026-11-11" || r.Days != 7 {
		t.Errorf("结束日期应该截到昨天: %+v", r)
	}
	// 整段都在未来、或者写错了：退回默认的 7 天
	for _, key := range []string{"2026-11-20_2026-11-25", "乱写"} {
		if r := Resolve(key, now, loc); r.Key != "d7" {
			t.Errorf("Resolve(%q) 应该退回 d7，得到 %q", key, r.Key)
		}
	}
	// 自选范围是明确指定的日期：数据落后时不往前挪
	r = Resolve("2026-11-05_2026-11-11", now, loc)
	if x := r.EndAt(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)); D(x.End) != "2026-11-11" {
		t.Errorf("自选范围不应该被挪动: %s", D(x.End))
	}
}

func TestCompareYoY(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, loc)
	r := Resolve("d28", now, loc) // 10/15 ~ 11/11
	y := r.WithCompare(CompareYoY)
	if y.Compare != CompareYoY || D(y.PrevStart) != "2025-10-16" || D(y.PrevEnd) != "2025-11-12" || len(y.PrevDates()) != 28 {
		t.Errorf("去年同期应该是 52 周前的同一段: %s ~ %s", D(y.PrevStart), D(y.PrevEnd))
	}
	if y.Start.Weekday() != y.PrevStart.Weekday() {
		t.Error("去年同期要和本期对齐星期几")
	}
	if y.PrevName() != "去年同期" || r.PrevName() != "上期" {
		t.Error("PrevName")
	}
	// 截断、前移之后仍然是去年同期
	c := y.ClampEnd(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	if c.Compare != CompareYoY || c.Days != 25 || D(c.PrevStart) != "2025-10-16" || D(c.PrevEnd) != "2025-11-09" {
		t.Errorf("ClampEnd 之后: %+v", c)
	}
	e := y.EndAt(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC))
	if e.Compare != CompareYoY || D(e.End) != "2026-11-10" || D(e.PrevEnd) != "2025-11-11" || e.Days != 28 {
		t.Errorf("EndAt 之后: %s ~ %s vs %s ~ %s", D(e.Start), D(e.End), D(e.PrevStart), D(e.PrevEnd))
	}
	// 换回上一期
	if s := y.Sequential(); s.Compare != "" || D(s.PrevEnd) != "2026-10-14" || D(s.PrevStart) != "2026-09-17" {
		t.Errorf("Sequential: %+v", s)
	}
	// 今天、昨天按小时比，不支持去年同期
	if h := Resolve("yesterday", now, loc).WithCompare(CompareYoY); h.Compare != "" || D(h.PrevStart) != "2026-11-10" {
		t.Errorf("按小时的范围不应该切到去年同期: %+v", h)
	}
	// 不认识的对比方式按上一期
	if x := r.WithCompare("whatever"); x.Compare != "" || D(x.PrevStart) != D(r.PrevStart) {
		t.Errorf("未知的对比方式: %+v", x)
	}
}
