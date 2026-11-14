package scheduler

import "testing"

func TestUpdateLabel(t *testing.T) {
	cases := map[[2]string]string{
		{"Ranking", "May 2026 core update"}:                "Google 核心更新",
		{"Ranking", "September 2026 spam update"}:          "Google 垃圾内容更新",
		{"Ranking", "February 2026 Discover update"}:       "Google Discover 更新",
		{"Indexing", "Indexing was experiencing an issue"}: "Google 搜索故障",
		{"Other", "Something unrelated"}:                   "",
	}
	for in, want := range cases {
		if got := UpdateLabel(in[0], in[1]); got != want {
			t.Errorf("UpdateLabel(%q, %q) = %q，期望 %q", in[0], in[1], got, want)
		}
	}
}
