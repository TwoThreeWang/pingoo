package signals_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"pingoo/internal/demo"
	"pingoo/internal/metrics"
	"pingoo/internal/secret"
	"pingoo/internal/signals"
	"pingoo/internal/store"
	"pingoo/internal/syncer"
	"pingoo/internal/testdb"
)

// 用演示数据跑一遍规则引擎：能检测出信号；灵敏度越高信号越多；关掉的类型不再产生。
func TestDetectWithLevelsAndMute(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.New(pool, secret.New("0123456789abcdef0123456789abcdef"))
	m := metrics.New(st, syncer.New(st, log), log)
	det := &signals.Detector{M: m}
	if err := demo.Seed(ctx, st, det); err != nil {
		t.Fatal(err)
	}
	uid, _ := st.DemoUserID(ctx, false) // 演示数据在共享的演示账号下
	ctx = store.WithUser(ctx, uid)
	sites, err := st.Sites(ctx)
	if err != nil || len(sites) == 0 {
		t.Fatalf("sites=%d err=%v", len(sites), err)
	}
	known := map[string]bool{}
	for _, k := range signals.Kinds {
		known[k.Key] = true
	}
	rows, _ := pool.Query(ctx, `SELECT DISTINCT kind FROM signals`)
	var kinds []string
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		kinds = append(kinds, k)
		if !known[k] {
			t.Errorf("规则引擎产生了设置页里没有列出的类型 %q，站长没法关掉它", k)
		}
	}
	rows.Close()
	if len(kinds) < 3 {
		t.Fatalf("演示数据应该能触发好几种信号，实际只有 %v", kinds)
	}

	// 灵敏度：造一个数据完全可控的网站，两个页面最近 7 天的浏览都涨了 3 倍以上——
	// “小页面”涨到 105 次，“大页面”涨到 315 次。页面飙升的门槛：高 60、标准 150、低 375。
	id, err := st.CreateSite(ctx, store.Site{Name: "可控", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	for d := 1; d <= 40; d++ {
		if _, err := pool.Exec(ctx, `INSERT INTO ga_daily(site_id,date,active_users,new_users,returning_users,sessions,engaged_sessions,page_views,engagement_seconds)
			VALUES($1,current_date-$2::int,20,10,10,25,15,60,500)`, id, d); err != nil {
			t.Fatal(err)
		}
	}
	for d := 1; d <= 14; d++ {
		small, big := 3, 10 // 上一个 7 天：每天 3 次 / 10 次
		if d <= 7 {
			small, big = 15, 45 // 最近 7 天：每天 15 次 / 45 次
		}
		if _, err := pool.Exec(ctx, `INSERT INTO ga_daily_pages(site_id,date,path,title,page_views,users,engagement_seconds) VALUES
			($1,current_date-$2::int,'/small','小页面',$3,1,10),($1,current_date-$2::int,'/big','大页面',$4,1,10)`, id, d, small, big); err != nil {
			t.Fatal(err)
		}
	}
	site, _ := st.Site(ctx, id)
	surges := func(level string) (n int) {
		if _, err := pool.Exec(ctx, `DELETE FROM signals WHERE site_id=$1`, id); err != nil {
			t.Fatal(err)
		}
		site.SignalLevel = level
		det.Detect(ctx, site)
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM signals WHERE site_id=$1 AND kind='page_surge'`, id).Scan(&n)
		return
	}
	if low, normal, high := surges(signals.LevelLow), surges(signals.LevelNormal), surges(signals.LevelHigh); low != 0 || normal != 1 || high != 2 {
		t.Errorf("页面飙升的提醒数：低 %d（应为 0），标准 %d（应为 1），高 %d（应为 2）", low, normal, high)
	}

	// 关掉“页面飙升”：再跑一遍不会产生这一类，查询时也不返回已有的
	if n := surges(signals.LevelHigh); n != 2 {
		t.Fatalf("准备数据失败: %d", n)
	}
	_ = st.SetSetting(ctx, store.KeySignalsMuted, "page_surge")
	if open, _ := st.OpenSignals(ctx, id, 30); len(open) != 0 {
		t.Errorf("关掉的类型不应该再显示: %+v", open)
	}
	if n := surges(signals.LevelHigh); n != 0 {
		t.Errorf("关掉的类型不应该再产生，实际 %d 条", n)
	}
}
