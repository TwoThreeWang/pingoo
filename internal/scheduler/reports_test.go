package scheduler

import (
	"testing"
	"time"

	"pingoo/internal/store"
)

func TestReportSites(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	end := time.Date(2026, 11, 8, 0, 0, 0, 0, loc) // 周日
	at := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, loc) }
	sites := []store.Site{
		{ID: 1, CreatedAt: at(1, 9)},                                      // 这一期之前就有：要
		{ID: 2, CreatedAt: at(8, 23)},                                     // 这一期最后一天添加的：要
		{ID: 3, CreatedAt: at(9, 1)},                                      // 这一期结束之后才添加：不要（刚装好不补“上周”的报告）
		{ID: 4, CreatedAt: at(1, 9), Demo: true},                          // 演示网站：不要
		{ID: 5, CreatedAt: time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC)}, // UTC 11/8 20:00 在上海已经是 11/9：不要
	}
	got := reportSites(sites, end)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Errorf("got %+v", got)
	}
}

func TestReportReady(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	end := time.Date(2026, 11, 8, 0, 0, 0, 0, loc) // 周日
	db := func(d int) *time.Time {                 // 数据库 date 列读出来的样子
		v := time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC)
		return &v
	}
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, loc) }
	ga := store.Site{ID: 1, GAProperty: "properties/1"}
	gscOnly := store.Site{ID: 2, GSCSite: "sc-domain:a.com"}
	none := store.Site{ID: 3}
	states := func(gaDay, gscDay int) map[int64]map[string]store.SyncState {
		return map[int64]map[string]store.SyncState{
			1: {"ga": {LastSynced: db(gaDay)}},
			2: {"gsc": {LastSynced: db(gscDay)}},
		}
	}
	all := []store.Site{ga, gscOnly, none}
	for _, c := range []struct {
		name  string
		sites []store.Site
		st    map[int64]map[string]store.SyncState
		now   time.Time
		want  bool
	}{
		{"周一 04:30，还没到点", all, states(8, 8), at(9, 4, 30), false},
		{"周一 05:10，都同步到周日了", all, states(8, 8), at(9, 5, 10), true},
		{"周一 05:10，GA 网站还停在周六", all, states(7, 8), at(9, 5, 10), false},
		{"周一 05:10，只连搜索的网站还停在周六", all, states(8, 7), at(9, 5, 10), false},
		{"单个网站的报告只看它自己", []store.Site{ga}, states(8, 7), at(9, 5, 10), true},
		{"从没同步过的网站要等", []store.Site{ga}, map[int64]map[string]store.SyncState{}, at(9, 12, 0), false},
		{"没连数据源的网站没有可等的", []store.Site{none}, nil, at(9, 5, 10), true},
		{"周二 05:59，还在等", all, states(7, 8), at(10, 5, 59), false},
		{"周二 06:00，同步一直没好也不再等", all, states(7, 8), at(10, 6, 0), true},
		{"周四补生成（周一那天服务没开）", all, states(3, 3), at(12, 10, 0), true},
	} {
		if got := reportReady(c.sites, c.st, end, c.now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// 月末最后一天在月底，“第二天”要正确跨月
	oct := time.Date(2026, 10, 31, 0, 0, 0, 0, loc)
	synced := map[int64]map[string]store.SyncState{1: {"ga": {LastSynced: func() *time.Time { v := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC); return &v }()}}}
	if reportReady([]store.Site{ga}, synced, oct, time.Date(2026, 11, 1, 4, 0, 0, 0, loc)) {
		t.Error("11 月 1 日 04:00 还没到点")
	}
	if !reportReady([]store.Site{ga}, synced, oct, time.Date(2026, 11, 1, 5, 0, 0, 0, loc)) {
		t.Error("11 月 1 日 05:00 应该可以生成 10 月的月报")
	}
}
