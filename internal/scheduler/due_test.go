package scheduler

import (
	"testing"
	"time"

	"pingoo/internal/store"
)

func TestSyncDue(t *testing.T) {
	dbDate := func(y int, m time.Month, d int) *time.Time { // 数据库 date 列读出来的样子：UTC 零点
		v := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return &v
	}
	for _, name := range []string{"Asia/Shanghai", "UTC", "America/Los_Angeles", "Pacific/Auckland"} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		noon := time.Date(2026, 11, 10, 12, 0, 0, 0, loc)
		early := time.Date(2026, 11, 10, 2, 0, 0, 0, loc)
		if syncDue(store.SyncState{LastSynced: dbDate(2026, 11, 9)}, noon, loc, 0) {
			t.Errorf("%s: 已经同步到昨天，不应该再同步", name)
		}
		if !syncDue(store.SyncState{LastSynced: dbDate(2026, 11, 8)}, noon, loc, 0) {
			t.Errorf("%s: 只同步到前天，过了同步时间应该同步", name)
		}
		if syncDue(store.SyncState{LastSynced: dbDate(2026, 11, 8)}, early, loc, 0) {
			t.Errorf("%s: 还没到当地的同步时间，不应该同步", name)
		}
		if !syncDue(store.SyncState{}, early, loc, 0) {
			t.Errorf("%s: 从没同步过的网站不用等到同步时间", name)
		}
		failed := store.SyncState{LastSynced: dbDate(2026, 11, 8), Status: "error", UpdatedAt: noon.Add(-10 * time.Minute)}
		if syncDue(failed, noon, loc, 0) {
			t.Errorf("%s: 出错后一小时内不应该重试", name)
		}
		failed.UpdatedAt = noon.Add(-2 * time.Hour)
		if !syncDue(failed, noon, loc, 0) {
			t.Errorf("%s: 出错一小时后应该重试", name)
		}
		// 专业版：已经同步到昨天，过了 6 小时再刷新一次
		done := store.SyncState{LastSynced: dbDate(2026, 11, 9), Status: "ok", UpdatedAt: noon.Add(-5 * time.Hour)}
		if syncDue(done, noon, loc, 6) {
			t.Errorf("%s: 刷新间隔没到不应该同步", name)
		}
		done.UpdatedAt = noon.Add(-7 * time.Hour)
		if !syncDue(done, noon, loc, 6) || syncDue(done, noon, loc, 0) {
			t.Errorf("%s: 专业版过了 6 小时应该刷新，免费版不应该", name)
		}
	}
}

func TestPrimarySource(t *testing.T) {
	for _, c := range []struct {
		ga, gsc, want string
	}{{"properties/1", "sc-domain:a.com", "ga"}, {"properties/1", "", "ga"}, {"", "sc-domain:a.com", "gsc"}, {"", "", ""}} {
		if got := primarySource(store.Site{GAProperty: c.ga, GSCSite: c.gsc}); got != c.want {
			t.Errorf("ga=%q gsc=%q: got %q, want %q", c.ga, c.gsc, got, c.want)
		}
	}
}

func TestReportDue(t *testing.T) {
	now := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name         string
		have, failed int
		lastFail     time.Time
		want         bool
	}{
		{"还没生成过", 0, 0, time.Time{}, true},
		{"已经有一份（生成中或已完成）", 1, 0, time.Time{}, false},
		{"有一份成功的，之前失败过", 1, 2, now.Add(-3 * time.Hour), false},
		{"刚失败，10 分钟后的下一轮不重试", 0, 1, now.Add(-10 * time.Minute), false},
		{"失败超过一小时，重试", 0, 1, now.Add(-61 * time.Minute), true},
		{"失败次数到上限，不再重试", 0, maxReportTries, now.Add(-5 * time.Hour), false},
	} {
		if got := reportDue(c.have, c.failed, c.lastFail, now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
