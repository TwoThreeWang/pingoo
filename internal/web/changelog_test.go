package web

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// 更新日志：最新的在最前面，版本号和日期都不能倒退；每个版本都写了内容。
func TestChangelogOrder(t *testing.T) {
	parse := func(v string) (n [3]int) {
		parts := strings.Split(v, ".")
		if len(parts) != 3 {
			t.Fatalf("版本号要写成 x.y.z: %q", v)
		}
		for i, p := range parts {
			n[i], _ = strconv.Atoi(p)
		}
		return n
	}
	less := func(a, b [3]int) bool {
		for i := range a {
			if a[i] != b[i] {
				return a[i] < b[i]
			}
		}
		return false
	}
	for i, r := range changelog {
		if r.Summary == "" || len(r.Groups) == 0 {
			t.Errorf("v%s 没写内容", r.Version)
		}
		for _, g := range r.Groups {
			if (g.Name != "新增" && g.Name != "改进" && g.Name != "修复") || len(g.Items) == 0 {
				t.Errorf("v%s 的分组 %q 不对或是空的", r.Version, g.Name)
			}
		}
		if i > 0 {
			prev := changelog[i-1]
			if !less(parse(r.Version), parse(prev.Version)) || r.Date > prev.Date {
				t.Errorf("v%s（%s）应该排在 v%s（%s）后面", r.Version, r.Date, prev.Version, prev.Date)
			}
		}
	}
	if Version != changelog[0].Version {
		t.Errorf("Version 应该是最新的版本 %s，实际 %s", changelog[0].Version, Version)
	}
}

func TestChangelogPage(t *testing.T) {
	_, h := testServer(t, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://pingoo.test/changelog", nil))
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("没登录也应该能看更新日志: %d", rec.Code)
	}
	for _, want := range []string{`id="v` + Version + `"`, `id="v2.0.0"`, `<a href="/changelog">更新日志</a>`, `Pingoo v` + Version + `</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("更新日志页缺少 %s", want)
		}
	}
}
