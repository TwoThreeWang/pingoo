package period

import (
	"testing"
	"time"
)

func TestResolveD7(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 11, 9, 15, 20, 0, 0, loc)
	r := Resolve("d7", now, loc)
	if D(r.Start) != "2026-11-02" || D(r.End) != "2026-11-08" || D(r.PrevStart) != "2026-10-26" || D(r.PrevEnd) != "2026-11-01" {
		t.Fatalf("d7 = %s~%s / %s~%s", D(r.Start), D(r.End), D(r.PrevStart), D(r.PrevEnd))
	}
}

func TestEndAtShiftsWindow(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 11, 10, 2, 0, 0, 0, loc)
	r := Resolve("d7", now, loc).EndAt(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	if D(r.Start) != "2026-11-02" || D(r.End) != "2026-11-08" || r.Days != 7 || D(r.PrevStart) != "2026-10-26" {
		t.Fatalf("EndAt = %s~%s（%d 天），上期从 %s 开始", D(r.Start), D(r.End), r.Days, D(r.PrevStart))
	}
	// 数据已经同步到结束日之后，不应改变
	r2 := Resolve("d7", now, loc)
	if got := r2.EndAt(r2.End.AddDate(0, 0, 3)); !got.End.Equal(r2.End) {
		t.Fatal("数据更新时不应挪动窗口")
	}
	// 落后太多（还在回填或同步出错）时不挪，保持和其他网站一样的日期
	r3 := Resolve("d7", now, loc)
	if got := r3.EndAt(r3.End.AddDate(0, 0, -20)); !got.End.Equal(r3.End) {
		t.Fatalf("落后 20 天时不应挪动窗口，得到 %s", D(got.End))
	}
	// 今天、昨天不挪
	if y := Resolve("yesterday", now, loc); !y.EndAt(y.End.AddDate(0, 0, -5)).End.Equal(y.End) {
		t.Fatal("按小时的范围不应挪动")
	}
}

func TestClampEnd(t *testing.T) {
	loc := time.UTC
	r := Resolve("d7", time.Date(2026, 11, 9, 12, 0, 0, 0, loc), loc)
	c := r.ClampEnd(time.Date(2026, 11, 6, 0, 0, 0, 0, loc))
	if c.Days != 5 || D(c.PrevStart) != "2026-10-28" || D(c.PrevEnd) != "2026-11-01" {
		t.Fatalf("ClampEnd = %d 天，上期 %s~%s", c.Days, D(c.PrevStart), D(c.PrevEnd))
	}
}
