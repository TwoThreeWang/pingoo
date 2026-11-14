package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"pingoo/internal/mail"
	"pingoo/internal/period"
	"pingoo/internal/scheduler"
	"pingoo/internal/store"
)

// home 是对外的首页（登录后也能看，右上角是“进入 Pingoo”）；登录后的概览在 /overview。
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	_, err := s.Store.DemoUserID(r.Context(), false)
	s.page(w, r, "landing", "每天早上，读一份关于你所有网站的简报", "", map[string]any{
		"HasDemo":    err == nil,
		"SignupOpen": s.Store.SettingOr(r.Context(), store.KeySignupOff, "") != "true",
		"Bonus":      s.Store.IntSetting(r.Context(), store.KeySignupBonus, 0),
		"Plans":      s.Store.Plans(r.Context()),
		"Contact":    s.Store.SettingOr(r.Context(), store.KeySMTPFrom, ""),
	})
}

// staticPage 渲染关于、隐私政策、服务条款这类固定内容的页面。
func (s *Server) staticPage(name, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.page(w, r, name, title, "", map[string]any{"Updated": "2026 年 11 月 13 日", "PerYuan": store.CreditsPerYuan, "Contact": s.Store.SettingOr(r.Context(), store.KeySMTPFrom, "")})
	}
}

// ========== 帮助 ==========

// helpTasks 是帮助页“一次大概多少积分”表格里的任务，顺序就是表格的行。
var helpTasks = []struct{ Kind, Name, Note string }{
	{"insight", "AI 洞察", "一个网站、一段时间"},
	{"chat", "问答", "每问一次"},
	{"diagnose", "诊断", "点一个异常深入分析"},
	{"report", "周报 / 月报", "全部网站汇总"},
}

type helpPrice struct {
	Name            string
	Default         bool
	In, Cached, Out float64 // 积分 / 百万 tokens（已经乘过倍率）
	Example         int64   // 示例用量（输入 2 万、其中 1 万命中缓存、输出 3 千）要扣的积分
	ExampleFormula  string
}

type helpCell struct {
	Hold, Avg int64
	HasAvg    bool
}

type helpRow struct {
	Name, Note string
	Cells      []helpCell
}

// helpPage 是给新手的帮助：怎么连接 Google、各功能怎么用、积分怎么算、有哪些限制、常见问题、反馈。
// 价格、限制都从当前设置里读，和实际计费一致。
func (s *Server) helpPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models, _ := s.Store.Models(ctx, true)
	avg, _ := s.Store.AvgTaskTokens(ctx, time.Now().AddDate(0, 0, -30), 5)
	perM := func(price, mult float64) float64 { return price * mult * store.CreditsPerYuan }
	var prices []helpPrice
	rows := make([]helpRow, len(helpTasks))
	for i, t := range helpTasks {
		rows[i] = helpRow{Name: t.Name, Note: t.Note}
	}
	for _, m := range models {
		p := helpPrice{Name: m.Name, Default: m.IsDefault, In: perM(m.PriceIn, m.Multiplier), Cached: perM(m.PriceCache, m.Multiplier), Out: perM(m.PriceOut, m.Multiplier)}
		p.Example = m.Cost(20000, 3000, 10000)
		p.ExampleFormula = fmt.Sprintf("(10,000 × %s + 10,000 × %s + 3,000 × %s) ÷ 1,000,000 = %s", trimFloat(p.In), trimFloat(p.Cached), trimFloat(p.Out),
			trimFloat((10000*p.In+10000*p.Cached+3000*p.Out)/1e6))
		prices = append(prices, p)
		for i, t := range helpTasks {
			c := helpCell{Hold: m.Hold(t.Kind)}
			if a, ok := avg[t.Kind]; ok {
				c.Avg, c.HasAvg = m.Cost(a[0], a[1], a[2]), true
			}
			rows[i].Cells = append(rows[i].Cells, c)
		}
	}
	d := map[string]any{
		"Prices": prices, "Rows": rows, "PerYuan": store.CreditsPerYuan,
		"Bonus":          s.Store.IntSetting(ctx, store.KeySignupBonus, 0),
		"Plans":          s.Store.Plans(ctx),
		"Grace":          store.GraceDays,
		"DailyCap":       s.Store.IntSetting(ctx, store.KeyAIDailyCap, 0) > 0,
		"MaxDays":        period.MaxCustomDays,
		"SyncHour":       scheduler.SyncHour,
		"Contact":        s.Store.SettingOr(ctx, store.KeySMTPFrom, ""),
		"Topics":         feedbackTopics,
		"FeedbackPerDay": feedbackPerDay,
	}
	if u := currentUser(r); u != nil && !u.Demo {
		g := s.googleView(ctx)
		d["Google"] = g
		d["Credits"] = u.Credits
		if m, err := s.Store.ModelFor(ctx, u.ID); err == nil {
			d["MyModel"] = m.Name
		}
		d["CanFeedback"] = true
	}
	s.page(w, r, "help", "帮助", "help", d)
}

// ========== 反馈 ==========

var feedbackTopics = []string{"使用问题", "连接 Google", "积分与充值", "功能建议", "数据不对", "其他"}

const (
	feedbackPerDay = 5
	feedbackMinLen = 5
	feedbackMaxLen = 3000
)

// feedback 把用户的反馈发到管理员的邮箱（Reply-To 是用户的邮箱，直接回复就能联系到用户）。
// 不单独建表：发信记录里有一条 kind=feedback 的记录，限额也按它算。
func (s *Server) feedback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	say := func(msg string) { toast(w, msg); w.WriteHeader(http.StatusNoContent) }
	topic := r.FormValue("topic")
	ok := false
	for _, t := range feedbackTopics {
		ok = ok || t == topic
	}
	if !ok {
		topic = "其他"
	}
	text := strings.TrimSpace(r.FormValue("text"))
	if n := utf8.RuneCountInString(text); n < feedbackMinLen || n > feedbackMaxLen {
		say(fmt.Sprintf("请写 %d～%d 个字", feedbackMinLen, feedbackMaxLen))
		return
	}
	if s.Store.MailCount(ctx, "feedback", "", "", u.ID, time.Now().Add(-24*time.Hour)) >= feedbackPerDay {
		say("今天已经提交过好几条了，我们会尽快看。还有问题的话明天再提交")
		return
	}
	to, err := s.Store.AdminEmail(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body := fmt.Sprintf("类型：%s\n用户：%s（ID %d）\n积分余额：%d\n网站数：%d\n时间：%s\n浏览器：%s\n\n%s\n\n— 直接回复这封邮件就能回复用户",
		topic, u.Email, u.ID, u.Credits, s.Store.SiteCount(ctx), time.Now().Format("2006-01-02 15:04"), truncate(r.UserAgent(), 160), text)
	err = s.Mail.Send(ctx, mail.Msg{Kind: "feedback", To: to, ReplyTo: u.Email, IP: rateKey(s.clientIP(r)), UserID: u.ID,
		Subject: "[Pingoo 反馈] " + topic + " · " + u.Email, Body: body})
	if err != nil {
		s.Log.Error("反馈没能发出", "err", err)
		say("没能提交，请稍后再试")
		return
	}
	toast(w, "收到了，谢谢！我们会通过邮件回复你")
	s.part(w, "feedback-done", nil)
}
