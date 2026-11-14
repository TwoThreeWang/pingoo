package syncer

import (
	"testing"
	"time"
)

func TestTrimLongTail(t *testing.T) {
	d1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	var rows [][]any
	// 第一天：2,100 行有 10 次浏览、400 行只有 1 次，再加 50 行有 5 次（排在 2,000 名之后，但 ≥3 也要保留）
	for i := 0; i < 2500; i++ {
		v := 1
		if i < 2100 {
			v = 10
		}
		rows = append(rows, []any{int64(1), d1, "/p", "t", v})
	}
	for i := 0; i < 50; i++ {
		rows = append(rows, []any{int64(1), d1, "/q", "t", 5})
	}
	// 第二天：只有 10 行，全部保留
	for i := 0; i < 10; i++ {
		rows = append(rows, []any{int64(1), d2, "/r", "t", 1})
	}
	out := trimLongTail(rows, 4)
	n1, n2, fives := 0, 0, 0
	for _, r := range out {
		if r[1].(time.Time).Equal(d1) {
			n1++
			if r[4].(int) == 5 {
				fives++
			}
		} else {
			n2++
		}
	}
	if n1 != 2150 || fives != 50 || n2 != 10 {
		t.Fatalf("day1=%d (fives=%d) day2=%d", n1, fives, n2)
	}
}

// split：明细保留期以前的日子只要每日总数，每日总数保留期以前的干脆不要。
func TestWindowSplit(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC) }
	w := window{detail: d(10), total: d(5), yesterday: d(20)}
	tc, tok, fc, fok := w.split(d(1), d(20))
	if !tok || tc != (chunk{d(5), d(9)}) || !fok || fc != (chunk{d(10), d(20)}) {
		t.Fatalf("从很早开始：totals=%v %v full=%v %v", tc, tok, fc, fok)
	}
	if _, tok, fc, fok := w.split(d(18), d(20)); tok || !fok || fc != (chunk{d(18), d(20)}) {
		t.Fatalf("只同步最近几天不需要单独补总数: %v %v", tok, fc)
	}
	if tc, tok, _, fok := w.split(d(6), d(8)); !tok || fok || tc != (chunk{d(6), d(8)}) {
		t.Fatalf("整段都在明细保留期以前: %v %v %v", tc, tok, fok)
	}
}
