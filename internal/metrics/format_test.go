package metrics

import "testing"

func TestBookTitle(t *testing.T) {
	cases := [][3]string{
		{"随机小姐姐", "/a", "《随机小姐姐》"},
		{"《谷雨》(S01E01) - 在线播放", "/w", "《谷雨》(S01E01) - 在线播放"},
		{"", "/search", "/search"},
		{"(not set)", "/x", "/x"},
	}
	for _, c := range cases {
		if got := BookTitle(c[0], c[1]); got != c[2] {
			t.Errorf("BookTitle(%q) = %q, want %q", c[0], got, c[2])
		}
	}
	if got := Thousands(1234567); got != "1,234,567" {
		t.Errorf("Thousands = %q", got)
	}
}

func TestSourceLabel(t *testing.T) {
	cases := [][3]string{
		{"Organic Search", "yandex.tm", "Yandex 搜索"},
		{"Organic Search", "tv.yandex.com", "Yandex 搜索"},
		{"Organic Search", "google", "Google 搜索"},
		{"Direct", "(direct)", "直接访问"},
		{"Referral", "(other)", "其他（GA 合并的小来源）"},
	}
	for _, c := range cases {
		if got := SourceLabel(c[0], c[1]); got != c[2] {
			t.Errorf("SourceLabel(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
