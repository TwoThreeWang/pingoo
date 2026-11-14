package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"pingoo/internal/demo"
	"pingoo/internal/mail"
	"pingoo/internal/store"
)

// DigestHour 是每天发邮件简报的时刻（用户“报告时区”的当地时间）：这时候各网站昨天的数据一般都同步完了。
const DigestHour = 8

// mails 发每天的邮件简报和套餐到期提醒。每轮调度都会调用，靠发信记录保证每封只发一次。
func (s *Scheduler) mails(ctx context.Context) {
	if s.Mail == nil {
		return
	}
	s.guard("digest", func() { s.digests(ctx) })
	s.guard("plan-reminders", func() { s.planReminders(ctx) })
}

func (s *Scheduler) siteURL(ctx context.Context) string {
	return strings.TrimRight(s.Store.SettingOr(ctx, store.KeySiteURL, ""), "/")
}

// digests 给付费套餐、没关掉简报的用户发“昨天怎么样”：每个网站的访客、浏览、搜索点击，需要关注的变化，新出的报告。
func (s *Scheduler) digests(ctx context.Context) {
	users, err := s.Store.DigestUsers(ctx)
	if err != nil {
		return
	}
	for _, u := range users {
		uctx := store.WithUser(ctx, u.ID)
		loc, err := time.LoadLocation(s.Store.ReportSettings(uctx).TZ)
		if err != nil {
			loc = time.UTC
		}
		now := time.Now().In(loc)
		if now.Hour() < DigestHour {
			continue
		}
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		if s.Store.MailCount(ctx, "digest", "", "", u.ID, midnight) > 0 {
			continue
		}
		msg, ok := s.digest(uctx, u, now)
		if !ok {
			continue // 没有任何数据（比如还没连 Google）就不发
		}
		if err := s.Mail.Send(ctx, msg); err != nil {
			s.Log.Warn("邮件简报没能发出", "user", u.ID, "err", err)
		}
	}
}

func signedPct(cur, prev int) string {
	if prev <= 0 {
		return ""
	}
	p := float64(cur-prev) / float64(prev) * 100
	return fmt.Sprintf("（比前一天 %+.0f%%）", p)
}

// digest 拼出一封简报。ok 为 false 表示没什么可说的。
func (s *Scheduler) digest(ctx context.Context, u store.User, now time.Time) (msg mail.Msg, ok bool) {
	sites, err := s.Store.Sites(ctx)
	if err != nil {
		return msg, false
	}
	plan := s.Store.PlanOf(ctx, u.ID)
	var b strings.Builder
	yday := now.AddDate(0, 0, -1)
	fmt.Fprintf(&b, "这是你的网站昨天（%d 月 %d 日）的情况。\n\n", yday.Month(), yday.Day())
	for _, site := range sites {
		if site.Demo || plan.Paused(site) {
			continue
		}
		sy := time.Now().In(site.Location()).AddDate(0, 0, -1)
		t, prev := s.Store.Totals(ctx, site.ID, sy), s.Store.Totals(ctx, site.ID, sy.AddDate(0, 0, -1))
		if !t.HasGA && !t.HasGSC {
			continue
		}
		ok = true
		fmt.Fprintf(&b, "▸ %s\n", site.Name)
		if t.HasGA {
			fmt.Fprintf(&b, "  访客 %d%s · 会话 %d · 浏览 %d\n", t.Users, signedPct(t.Users, prev.Users), t.Sessions, t.Views)
		}
		if t.HasGSC {
			fmt.Fprintf(&b, "  搜索点击 %d · 曝光 %d（%d 月 %d 日，Search Console 有两三天延迟）\n", t.Clicks, t.Impressions, t.GSCDay.Month(), t.GSCDay.Day())
		}
		if sigs, err := s.Store.OpenSignals(ctx, site.ID, 2); err == nil {
			for i, sg := range sigs {
				if i == 3 {
					fmt.Fprintf(&b, "  ……还有 %d 条需要关注\n", len(sigs)-3)
					break
				}
				fmt.Fprintf(&b, "  需要关注：%s\n", sg.Title)
			}
		}
		b.WriteString("\n")
	}
	if !ok {
		return msg, false
	}
	base := s.siteURL(ctx)
	if reps, err := s.Store.Reports(ctx, -1, "", 10); err == nil {
		for _, r := range reps {
			if r.Status != "done" || r.FinishedAt == nil || time.Since(*r.FinishedAt) > 24*time.Hour {
				continue
			}
			var c struct {
				Title string `json:"title"`
			}
			_ = json.Unmarshal(r.Content, &c)
			name := map[string]string{"weekly": "周报", "monthly": "月报"}[r.Kind]
			if name == "" {
				name = "报告"
			}
			fmt.Fprintf(&b, "新%s：%s\n", name, strings.TrimSpace(c.Title))
			if base != "" {
				fmt.Fprintf(&b, "%s/reports/%d\n", base, r.ID)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("不想再收到这封邮件：在 Pingoo 的「设置 → 报告」里关掉“每日邮件简报”。")
	title := fmt.Sprintf("每日简报 · %d 月 %d 日", yday.Month(), yday.Day())
	msg = mail.Msg{Kind: "digest", To: u.Email, UserID: u.ID, Subject: "Pingoo " + title, Title: title, Body: b.String()}
	if base != "" {
		msg.Action, msg.ActionURL = "打开 Pingoo 看详情", base+"/overview"
	}
	return msg, true
}

// planReminders 在付费套餐到期前 3 天提醒一次、到期后提醒一次（说清楚 7 天宽限期之后会怎样）。
func (s *Scheduler) planReminders(ctx context.Context) {
	now := time.Now()
	cfg := s.Store.Plans(ctx)
	base := s.siteURL(ctx)
	// 邮件里的到期时间统一按后台默认时区显示。
	loc, err := time.LoadLocation(s.Store.DefaultTZ(ctx))
	if err != nil {
		loc = time.UTC
	}
	expAt := func(u store.User) string { return u.PlanExpiresAt.In(loc).Format("2006-01-02 15:04") }
	// 怎么续费：这一档有自己的购买地址就写在正文里；邮件下面的链接通到设置页，在那里兑换新的套餐卡密。
	buy := func(p store.Plan) string {
		s := "续费：在「设置 → 套餐与积分」里兑换新的套餐卡密。"
		if p.BuyURL != "" {
			s += "\n购买" + p.Name + "卡密：" + p.BuyURL
		}
		return "\n\n" + s
	}
	send := func(kind string, u store.User, subject, title, body string) {
		if s.Store.MailCount(ctx, kind, "", "", u.ID, now.AddDate(0, 0, -5)) > 0 {
			return
		}
		msg := mail.Msg{Kind: kind, To: u.Email, UserID: u.ID, Subject: subject, Title: title, Body: body}
		if base != "" {
			msg.Action, msg.ActionURL = "去续费", base+"/settings#ai"
		}
		if err := s.Mail.Send(ctx, msg); err != nil {
			s.Log.Warn("套餐提醒没能发出", "user", u.ID, "err", err)
		}
	}
	free := cfg.Get(store.PlanFree)
	if users, err := s.Store.ExpiringUsers(ctx, now, now.AddDate(0, 0, 3)); err == nil {
		for _, u := range users {
			p := cfg.Get(u.Plan)
			send("plan_expiring", u, "你的 Pingoo "+p.Name+"快到期了", p.Name+"快到期了", fmt.Sprintf(
				"你的 Pingoo %s将在 %s 到期。到期后会回到%s：只保留 %d 个网站在同步、%d 天的明细数据，不再有每日邮件简报和每月积分。"+
					"到期后 %d 天内续费，数据一点都不会少。%s",
				p.Name, expAt(u), free.Name, free.Sites, free.DetailDays, store.GraceDays, buy(p)))
		}
	}
	if users, err := s.Store.ExpiringUsers(ctx, now.AddDate(0, 0, -1), now); err == nil {
		for _, u := range users {
			p := cfg.Get(u.Plan)
			send("plan_expired", u, "你的 Pingoo "+p.Name+"已经到期", p.Name+"已经到期", fmt.Sprintf(
				"你的 Pingoo %s已经在 %s 到期，现在按%s使用：超出 %d 个的网站暂停同步（数据还在），不再有每日邮件简报和每月积分。\n\n"+
					"%d 天内续费，所有数据都会保留；超过 %d 天，超出%s保留期（明细 %d 天、每日总数 %d 天）的数据会被删除。%s",
				p.Name, expAt(u), free.Name, free.Sites, store.GraceDays, store.GraceDays, free.Name,
				free.DetailDays, free.TotalDays, buy(p)))
		}
	}
}

// ========== 每天半夜 ==========

// NightHour 是每天半夜维护的时刻（后台“默认时区”的当地时间）：这时候访问最少，也在各网站 04:00 同步之前。
const NightHour = 3

// nightly 每天半夜做一次：刷新演示数据、整理被撑大的同步数据表。做没做过记在设置里，重启也不会重复做。
func (s *Scheduler) nightly(ctx context.Context) {
	loc, err := time.LoadLocation(s.Store.DefaultTZ(ctx))
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	today := now.Format("2006-01-02")
	if now.Hour() != NightHour || s.Store.SettingOr(ctx, store.KeyNightlyDone, "") == today {
		return
	}
	_ = s.Store.SetSetting(ctx, store.KeyNightlyDone, today)
	go s.guard("nightly", func() {
		if s.Detector != nil {
			if done, err := demo.Refresh(ctx, s.Store, s.Detector); err != nil {
				s.Log.Warn("刷新演示数据失败", "err", err)
			} else if done {
				s.Log.Info("演示数据已刷新到昨天")
			}
		}
		for _, r := range s.Store.Compact(ctx, 16<<20) {
			s.Log.Info("已整理同步数据表的空间", "table", r.Table, "before_mb", r.Before>>20, "after_mb", r.After>>20)
		}
	})
}
