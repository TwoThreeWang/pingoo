package scheduler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"pingoo/internal/mail"
	"pingoo/internal/secret"
	"pingoo/internal/store"
	"pingoo/internal/testdb"
)

// 付费用户每天收一封简报（过了 8 点才发，一天只发一次）；免费用户和关掉简报的不发；到期前提醒一次。
func TestDigestAndReminders(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.New(pool, secret.New("0123456789abcdef0123456789abcdef"))
	var mu sync.Mutex
	var sent []mail.Msg
	mailer := &mail.Mailer{DryRun: true, Store: st, Log: log, Sent: func(m mail.Msg) { mu.Lock(); sent = append(sent, m); mu.Unlock() }}
	s := &Scheduler{Store: st, Log: log, Mail: mailer}
	byKind := func(kind string, to string) (n int, last mail.Msg) {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range sent {
			if m.Kind == kind && m.To == to {
				n, last = n+1, m
			}
		}
		return
	}
	// 找一个现在已经过了早上 8 点（还没到半夜）的时区，用它当用户的报告时区
	tz := ""
	for off := -12; off <= 14 && tz == ""; off++ {
		name := fmt.Sprintf("Etc/GMT%+d", -off)
		if loc, err := time.LoadLocation(name); err == nil {
			if h := time.Now().In(loc).Hour(); h >= DigestHour+1 && h <= 22 {
				tz = name
			}
		}
	}
	if tz == "" {
		t.Skip("找不到合适的时区")
	}
	paid := testdb.User(t, pool, "paid@example.com")
	free := testdb.User(t, pool, "free@example.com")
	for _, uid := range []int64{paid, free} {
		uctx := store.WithUser(ctx, uid)
		_ = st.SetSetting(uctx, store.KeyReportTZ, tz)
		id, _ := st.CreateSite(uctx, store.Site{Name: fmt.Sprint("网站", uid), Timezone: "UTC"})
		for d, users := range map[int]int{1: 120, 2: 100} {
			if _, err := pool.Exec(ctx, `INSERT INTO ga_daily(site_id,date,active_users,new_users,returning_users,sessions,engaged_sessions,page_views,engagement_seconds)
				VALUES($1,current_date - $2::int,$3,0,0,150,100,300,0)`, id, d, users); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := pool.Exec(ctx, `INSERT INTO signals(site_id,kind,severity,title,event_date,dedupe_key) VALUES($1,'traffic_drop','warn','流量下滑了',current_date - 1,$2)`, id, fmt.Sprint("s", id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetPlan(ctx, paid, store.PlanBasic, 2); err != nil { // 两天后到期：会收到到期提醒
		t.Fatal(err)
	}
	s.mails(ctx)
	s.mails(ctx)
	n, m := byKind("digest", "paid@example.com")
	if n != 1 || !strings.Contains(m.Body, "访客 120（比前一天 +20%）") || !strings.Contains(m.Body, "需要关注：流量下滑了") {
		t.Fatalf("付费用户应该收到一封简报: n=%d\n%s", n, m.Body)
	}
	if n, _ := byKind("digest", "free@example.com"); n != 0 {
		t.Error("免费用户不发简报")
	}
	if n, m := byKind("plan_expiring", "paid@example.com"); n != 1 || !strings.Contains(m.Body, "快到期") && !strings.Contains(m.Subject, "快到期") {
		t.Errorf("到期前应该提醒一次: n=%d %q", n, m.Subject)
	}
	// 关掉简报：明天也不会再发（这里把今天的发信记录挪到昨天来模拟）
	_ = st.SetSetting(store.WithUser(ctx, paid), store.KeyDigestOff, "true")
	if _, err := pool.Exec(ctx, `UPDATE mail_log SET created_at = created_at - interval '1 day' WHERE kind='digest'`); err != nil {
		t.Fatal(err)
	}
	s.mails(ctx)
	if n, _ := byKind("digest", "paid@example.com"); n != 1 {
		t.Error("关掉简报后不应该再发")
	}
}
