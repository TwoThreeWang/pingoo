package web

import (
	"encoding/json"
	"strings"
	"testing"

	"pingoo/internal/metrics"
)

func TestTemplatesParse(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"overview", "site", "reports", "report", "chat", "settings", "login"} {
		if s.pages[p] == nil {
			t.Fatalf("缺少页面模板 %s", p)
		}
	}
}

func TestNum(t *testing.T) {
	cases := map[any]string{0: "0", 999: "999", 12400: "12,400", 1234567.4: "1,234,567", -2583: "−2,583"}
	for in, want := range cases {
		if got := num(in); got != want {
			t.Errorf("num(%v) = %q，期望 %q", in, got, want)
		}
	}
}

func TestDelta(t *testing.T) {
	if got := string(delta(110, 100, 1)); !strings.Contains(got, "good") || !strings.Contains(got, "+10.0%") {
		t.Errorf("增长应为 good: %s", got)
	}
	if got := string(delta(90, 100, -1)); !strings.Contains(got, "good") {
		t.Errorf("越小越好时下降应为 good: %s", got)
	}
	if got := string(delta(5, 3, 1)); !strings.Contains(got, "样本少") {
		t.Errorf("小样本应提示: %s", got)
	}
}

func TestTileDeltaPos(t *testing.T) {
	got := string(tileDelta(metrics.Tile{Kind: "pos", Better: -1, Cur: 14.2, Prev: 15.0, HasVal: true}))
	if !strings.Contains(got, "前进 0.8 位") || !strings.Contains(got, "good") {
		t.Errorf("排名提升显示不对: %s", got)
	}
}

func TestBoldEscapes(t *testing.T) {
	got := string(bold(`访客 **12** <script>x</script>`))
	if strings.Contains(got, "<script>") || !strings.Contains(got, "<b>12</b>") {
		t.Errorf("bold 处理不对: %s", got)
	}
}

func TestAsciiJSON(t *testing.T) {
	in := `{"toast":"已保存 🐧"}`
	got := asciiJSON(in)
	for _, r := range got {
		if r > 127 {
			t.Fatalf("还有非 ASCII 字符: %s", got)
		}
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(got), &v); err != nil || v["toast"] != "已保存 🐧" {
		t.Fatalf("转换后无法还原: %s %v", got, err)
	}
}

func TestSafeNext(t *testing.T) {
	// 不合格的都退回登录后的默认页（概览），首页 / 也一样：登录后不该回到介绍页
	h := homeApp
	for in, want := range map[string]string{"": h, "/": h, "/sites/1": "/sites/1", "//evil.com": h, "https://evil.com": h, "/\\evil": h,
		"/sites/1?range=d7&tab=top#search": "/sites/1?range=d7&tab=top#search", "/help#feedback": "/help#feedback",
		// 浏览器会先去掉 Tab 和换行："/\t/evil.com" 实际会跳到 //evil.com
		"/\t/evil.com": h, "/\n/evil.com": h, "/\t\\evil.com": h, "/a\\b": h, "sites/1": h} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q", in, got)
		}
	}
}

func TestMoney(t *testing.T) {
	for in, want := range map[float64]string{0: "0.00", 3.456: "3.46", 1234.5: "1,234.50", 1234567.891: "1,234,567.89", -1234.5: "-1,234.50"} {
		if got := money(in); got != want {
			t.Errorf("money(%v) = %q, want %q", in, got, want)
		}
	}
}
