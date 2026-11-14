package agent

import (
	"testing"

	"pingoo/internal/metrics"
)

func TestTileText(t *testing.T) {
	cases := []struct {
		t                metrics.Tile
		cur, prev, delta string
	}{
		{metrics.Tile{Kind: "count", Cur: 15846, Prev: 9945}, "15,846", "9,945", "增长 59.3%"},
		{metrics.Tile{Kind: "pct", Cur: 0.417, Prev: 0.543}, "41.7%", "54.3%", "下降 12.6 个百分点"},
		{metrics.Tile{Kind: "pos", Cur: 8.2, Prev: 9.5}, "8.2", "9.5", "前进 1.3 位"},
		{metrics.Tile{Kind: "dur", Cur: 39, Prev: 52}, "39 秒", "52 秒", "下降 25.0%"},
		{metrics.Tile{Kind: "count", Cur: 5, Prev: 0}, "5", "0", "上期为 0"},
	}
	if got := countLine(3168, 36504); got != "本期 3,168，上期 36,504（下降 91.3%）" {
		t.Errorf("countLine = %q", got)
	}
	for _, c := range cases {
		cur, prev, d := tileText(c.t)
		if cur != c.cur || prev != c.prev || d != c.delta {
			t.Errorf("%s: got %q %q %q, want %q %q %q", c.t.Kind, cur, prev, d, c.cur, c.prev, c.delta)
		}
	}
}
