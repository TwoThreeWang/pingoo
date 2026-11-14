package demo

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"pingoo/internal/metrics"
	"pingoo/internal/secret"
	"pingoo/internal/signals"
	"pingoo/internal/store"
	"pingoo/internal/syncer"
	"pingoo/internal/testdb"
)

// 演示数据过时了（最新一天早于昨天）就重新生成，演示账号不变（正在看演示的访客不会被登出）；没过时不动。
func TestRefresh(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	st := store.New(pool, secret.New("0123456789abcdef0123456789abcdef"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	det := &signals.Detector{M: metrics.New(st, syncer.New(st, log), log)}
	if done, err := Refresh(ctx, st, det); err != nil || done {
		t.Fatalf("没有演示账号时什么都不做: %v %v", done, err)
	}
	if err := Seed(ctx, st, det); err != nil {
		t.Fatal(err)
	}
	uid, _ := st.DemoUserID(ctx, false)
	if done, _ := Refresh(ctx, st, det); done {
		t.Error("刚生成的演示数据不需要刷新")
	}
	if _, err := pool.Exec(ctx, `UPDATE ga_daily SET date = date - 3`); err != nil {
		t.Fatal(err)
	}
	if done, err := Refresh(ctx, st, det); err != nil || !done {
		t.Fatalf("过时的演示数据应该刷新: %v %v", done, err)
	}
	if again, _ := st.DemoUserID(ctx, false); again != uid {
		t.Error("刷新不能换掉演示账号")
	}
	var fresh bool
	_ = pool.QueryRow(ctx, `SELECT max(date) >= current_date - 2 FROM ga_daily`).Scan(&fresh)
	if !fresh {
		t.Error("刷新后应该有最近的数据")
	}
	// 示例跟着重新生成：每个网站一份洞察 + 全部网站一份、一份周报、有提醒的网站各一份诊断；不会越刷越多
	var insights, overview, reports, diagnoses int
	_ = pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE site_id IS NULL) FROM insights WHERE user_id=$1 AND status='done'`, uid).Scan(&insights, &overview)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM reports WHERE user_id=$1 AND status='done'`, uid).Scan(&reports)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM diagnoses`).Scan(&diagnoses)
	if insights != len(specs)+1 || overview != 1 || reports != 1 || diagnoses == 0 {
		t.Errorf("示例数量不对: 洞察 %d（全部网站 %d），周报 %d，诊断 %d", insights, overview, reports, diagnoses)
	}
}
