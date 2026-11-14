package period

import (
	"testing"
	"time"
)

// 数据库 date 列读出来是 UTC 零点。ClampEnd 拿它和当地零点比较时，不能因为时区多裁或少算一天。
func TestClampEndAcrossTimezones(t *testing.T) {
	for _, name := range []string{"Asia/Shanghai", "UTC", "America/Los_Angeles", "Pacific/Auckland"} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		r := Resolve("d28", time.Date(2026, 11, 10, 12, 0, 0, 0, loc), loc)
		db := func(d time.Time) time.Time { return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC) }

		c := r.ClampEnd(db(r.End)) // 数据已经到范围结束日：不用裁
		if c.Days != 28 || D(c.End) != D(r.End) || D(c.PrevStart) != D(r.PrevStart) {
			t.Errorf("%s: 数据到结束日时不应该裁剪: days=%d end=%s prev=%s", name, c.Days, D(c.End), D(c.PrevStart))
		}
		c = r.ClampEnd(db(r.End.AddDate(0, 0, -3))) // 数据落后 3 天：本期和对比期都是 25 天
		if c.Days != 25 || D(c.End) != D(r.End.AddDate(0, 0, -3)) || len(c.Dates()) != 25 || len(c.PrevDates()) != 25 {
			t.Errorf("%s: 落后 3 天应为 25 天对 25 天: days=%d cur=%d prev=%d", name, c.Days, len(c.Dates()), len(c.PrevDates()))
		}
		if c = r.ClampEnd(db(r.Start.AddDate(0, 0, -1))); c.Days != 0 {
			t.Errorf("%s: 数据还没到范围开始日时应为 0 天，得到 %d", name, c.Days)
		}
	}
}

// 夏令时切换的那天只有 23 或 25 小时，天数不能用小时数除以 24 来算。
func TestCustomAcrossDST(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, la) }
	for _, c := range []struct {
		start, end time.Time
		days       int
		prevStart  string
	}{
		{day(3, 1), day(3, 31), 31, "2026-01-29"},   // 3 月 8 日拨快 1 小时
		{day(11, 1), day(11, 30), 30, "2026-10-02"}, // 11 月 1 日拨慢 1 小时
		{day(3, 2), day(3, 8), 7, "2026-02-23"},
	} {
		r := Custom(c.start, c.end)
		if r.Days != c.days || D(r.PrevStart) != c.prevStart || len(r.Dates()) != c.days || len(r.PrevDates()) != c.days {
			t.Errorf("%s~%s: days=%d prev=%s~%s（期望 %d 天，对比期从 %s 开始）", D(c.start), D(c.end), r.Days, D(r.PrevStart), D(r.PrevEnd), c.days, c.prevStart)
		}
	}
	// 一头是数据库的 UTC 零点、一头是当地零点
	if n := DaysBetween(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), day(11, 9)); n != 6 {
		t.Errorf("DaysBetween 应只看日历日，得到 %d", n)
	}
}

func TestEndAtShiftLimit(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	r := Resolve("d7", time.Date(2026, 11, 10, 12, 0, 0, 0, la), la) // 11/3 ~ 11/9
	if x := r.EndAt(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); D(x.End) != "2026-11-06" || D(x.Start) != "2026-10-31" {
		t.Errorf("落后 3 天应该整体前移: %s~%s", D(x.Start), D(x.End))
	}
	if x := r.EndAt(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)); D(x.End) != "2026-11-09" {
		t.Errorf("落后超过 3 天不应该前移: %s", D(x.End))
	}
}
