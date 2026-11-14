package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"pingoo/internal/config"
	"pingoo/internal/mail"
	"pingoo/internal/secret"
	"pingoo/internal/store"
)

const sessionTTL = 30 * 24 * time.Hour

func hashToken(t string) string { return secret.Hash(t) }

// homeApp 是登录后的默认页面（概览）。首页 / 是对外的介绍页，不和它共用路径。
const homeApp = "/overview"

// safeNext 只接受站内路径，登录后跳回去用。除了 //evil.com、/\evil.com 这类写法，
// 还要拒绝控制字符：浏览器解析地址时会先去掉 Tab 和换行，"/<Tab>/evil.com" 会变成 "//evil.com" 跳到站外。
func safeNext(n string) string {
	if n == "" || n == "/" || n[0] != '/' || strings.HasPrefix(n, "//") || strings.Contains(n, `\`) {
		return homeApp
	}
	for _, c := range n {
		if c < 0x20 || c == 0x7f {
			return homeApp
		}
	}
	u, err := url.Parse(n)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return homeApp
	}
	return n
}

// ========== 人机校验 ==========

// 工作量证明的难度（前导 0 的位数）。平时 20 位：手机浏览器约 1 秒；
// 熔断时（每小时的激活邮件超过 registerAlert 封）注册页改用 23 位，约慢 8 倍。
var (
	powBits     = 20 // 测试里会调低
	powBitsHigh = 23
)

const registerAlert = 50

// powScope 是每个页面的挑战用途：注册页的挑战不能拿去登录。
var powScope = map[string]string{"login": "login", "register": "register", "forgot": "forgot"}

// powBitsFor 返回某个用途当前要求的难度。
func (s *Server) powBitsFor(ctx context.Context, scope string) int {
	if scope == "register" && s.Store.MailCount(ctx, "register", "", "", 0, time.Now().Add(-time.Hour)) > registerAlert {
		s.breakerLog()
		return powBitsHigh
	}
	return powBits
}

// breakerLog 在熔断期间最多每 10 分钟记一次日志。
func (s *Server) breakerLog() {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if time.Since(s.breakerLogged) > 10*time.Minute {
		s.breakerLogged = time.Now()
		s.Log.Warn("注册激活邮件一小时内超过阈值，注册页改用高难度人机校验", "threshold", registerAlert)
	}
}

// human 在查库、比对密码之前先做人机校验：陷阱字段被填了、或者工作量证明不对，都直接拒绝。
func (s *Server) human(r *http.Request, scope string) bool {
	if r.FormValue(trapField) != "" {
		return false
	}
	err := s.Pow.Verify(r.FormValue("pow_token"), r.FormValue("pow_nonce"), scope, s.powBitsFor(r.Context(), scope))
	if err != nil {
		s.Log.Info("人机校验没通过", "scope", scope, "reason", err.Error(), "ip", s.clientIP(r))
	}
	return err == nil
}

// trapField 是陷阱输入框的名字：移到屏幕外，正常用户看不见、浏览器也不会自动填（不叫 website / company 这类名字）。
const trapField = "pg_hp_note"

const errHuman = "安全校验没通过，请再提交一次。"

// authPage 是登录、注册、找回密码页面唯一的渲染入口：每次渲染都签发一个新挑战，出错重新渲染时也不会漏掉。
func (s *Server) authPage(w http.ResponseWriter, r *http.Request, name, title string, code int, d map[string]any) {
	if d == nil {
		d = map[string]any{}
	}
	if scope, ok := powScope[name]; ok {
		d["Pow"] = s.Pow.Issue(scope, s.powBitsFor(r.Context(), scope))
		d["Trap"] = trapField
	}
	if code != 0 && code != http.StatusOK {
		w.WriteHeader(code)
	}
	s.page(w, r, name, title, "", d)
}

// ========== 登录 ==========

// 登录限速（按来源 IP；IPv6 按 /64）：
//   - 同一来源连续输错 loginMaxFails 次，锁定 loginLock；超过 loginWindow 没再输错，次数清零；
//   - 所有来源合计，每分钟最多 loginGlobalMax 次失败，超过后暂时拒绝新的尝试（防止换着 IP 爆破）。
//     每次尝试都要先算一遍工作量证明，大规模爆破本身就很贵，所以全站上限放得比较宽。
const (
	loginMaxFails   = 5
	loginLock       = 15 * time.Minute
	loginWindow     = 15 * time.Minute
	loginGlobalMax  = 300
	loginMaxTracked = 20000 // 最多同时记这么多个来源，防止内存被刷爆
)

// loginDelay 是输错密码后故意等的时间（测试里会改成 0）。
var loginDelay = 400 * time.Millisecond

type loginFail struct {
	n     int
	last  time.Time
	until time.Time
}

type loginVerdict int

const (
	loginOK        loginVerdict = iota // 可以校验密码
	loginLocked                        // 这个来源被锁定了
	loginThrottled                     // 全站失败次数太多
)

// loginBegin 在校验密码之前登记一次尝试（先按“失败”记，成功后由 loginSucceeded 撤销）。
// 先登记再校验，同时发来的多个请求就不能一起挤过次数检查。lastTry 为 true 表示这次再输错就会被锁定。
func (s *Server) loginBegin(key string, now time.Time) (v loginVerdict, wait time.Duration, lastTry bool) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if now.Sub(s.lastPrune) > time.Minute {
		s.lastPrune = now
		for k, f := range s.failures {
			if now.After(f.until) && now.Sub(f.last) > loginWindow {
				delete(s.failures, k)
			}
		}
	}
	f := s.failures[key]
	if f != nil && now.Before(f.until) {
		return loginLocked, f.until.Sub(now), false
	}
	keep := s.recentFails[:0]
	for _, t := range s.recentFails {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	s.recentFails = keep
	if len(s.recentFails) >= loginGlobalMax {
		return loginThrottled, time.Minute - now.Sub(s.recentFails[0]), false
	}
	if f == nil && len(s.failures) >= loginMaxTracked {
		return loginThrottled, time.Minute, false
	}
	if f == nil {
		f = &loginFail{}
		s.failures[key] = f
	} else if now.Sub(f.last) > loginWindow {
		f.n = 0
	}
	f.n++
	f.last = now
	if f.n >= loginMaxFails {
		f.until = now.Add(loginLock)
	}
	s.recentFails = append(s.recentFails, now)
	return loginOK, 0, f.n >= loginMaxFails
}

// loginSucceeded 撤销 loginBegin 记的那一次。
func (s *Server) loginSucceeded(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	delete(s.failures, key)
	if n := len(s.recentFails); n > 0 {
		s.recentFails = s.recentFails[:n-1]
	}
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if u := currentUser(r); u != nil && !u.Demo {
		http.Redirect(w, r, homeApp, http.StatusSeeOther)
		return
	}
	q := r.URL.Query()
	s.authPage(w, r, "login", "登录", 0, map[string]any{"Next": safeNext(q.Get("next")), "Email": q.Get("email"), "Notice": loginNotices[q.Get("m")]})
}

var loginNotices = map[string]string{
	"reset":   "密码已经改好了，请用新密码登录。",
	"deleted": "账号已注销，所有数据都已删除。",
}

// dummyHash 用于邮箱不存在时也做一次同样耗时的密码比对，别人没法从响应时间判断邮箱有没有注册。
var dummyHash = secret.HashPassword("pingoo-timing-equalizer")

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	key := rateKey(ip)
	next := safeNext(r.FormValue("next"))
	email := store.NormEmail(r.FormValue("email"))
	reject := func(code int, msg string) {
		s.authPage(w, r, "login", "登录", code, map[string]any{"Next": next, "Email": email, "Error": msg})
	}
	if !s.human(r, "login") {
		reject(http.StatusBadRequest, errHuman)
		return
	}
	verdict, wait, lastTry := s.loginBegin(key, time.Now())
	switch verdict {
	case loginLocked:
		reject(http.StatusTooManyRequests, fmt.Sprintf("输错次数太多，请 %d 分钟后再试。", int(wait.Minutes())+1))
		return
	case loginThrottled:
		reject(http.StatusTooManyRequests, "登录尝试太频繁，请 1 分钟后再试。")
		return
	}
	lookup := s.Store.UserByEmail
	if s.lookupUser != nil {
		lookup = s.lookupUser
	}
	u, err := lookup(r.Context(), email)
	hash := dummyHash
	if err == nil && !u.Demo {
		hash = u.PassHash
	}
	if ok := secret.CheckPassword(hash, r.FormValue("password")); !ok || err != nil || u.Demo {
		msg := fmt.Sprintf("邮箱或密码不对。连续输错 %d 次，会锁定 %d 分钟。", loginMaxFails, int(loginLock.Minutes()))
		if lastTry {
			msg = fmt.Sprintf("输错次数太多，已锁定 %d 分钟。", int(loginLock.Minutes()))
		}
		time.Sleep(loginDelay)
		reject(http.StatusUnauthorized, msg)
		return
	}
	if u.Disabled {
		reject(http.StatusForbidden, "这个账号已被停用，有疑问请联系管理员。")
		return
	}
	s.loginSucceeded(key)
	ttl := 12 * time.Hour
	if r.FormValue("remember") != "" {
		ttl = sessionTTL
	}
	if err := s.startSession(w, r, u.ID, ttl); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Store.MarkLogin(r.Context(), u.ID)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, uid int64, ttl time.Duration) error {
	token := secret.RandomToken(32)
	ua := r.UserAgent()
	if utf8.RuneCountInString(ua) > 300 {
		ua = string([]rune(ua)[:300])
	}
	if err := s.Store.CreateSession(r.Context(), uid, hashToken(token), ua, s.clientIP(r), ttl); err != nil {
		return err
	}
	s.setSessionCookie(w, r, token, ttl)
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.Store.DeleteSession(r.Context(), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	redirect(w, r, "/")
}

func (s *Server) logoutOthers(w http.ResponseWriter, r *http.Request) {
	id, _ := r.Context().Value(sessionKey).(string)
	if err := s.Store.DeleteOtherSessions(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "其他设备已退出登录")
	s.renderSessions(w, r)
}

// demoEntries 记录每个来源最近一小时进入演示的次数：每次进入都会建一条会话，不限的话脚本可以无限刷会话表。
var demoEntries = struct {
	sync.Mutex
	m map[string][]time.Time
}{m: map[string][]time.Time{}}

const demoPerIPHour = 20

// demoEnter 让访客进入共享的只读演示账号。
func (s *Server) demoEnter(w http.ResponseWriter, r *http.Request) {
	if u := currentUser(r); u != nil && u.Demo { // 已经在演示里了，不用再建会话
		http.Redirect(w, r, homeApp, http.StatusSeeOther)
		return
	}
	key := rateKey(s.clientIP(r))
	demoEntries.Lock()
	recent := demoEntries.m[key][:0]
	for _, t := range demoEntries.m[key] {
		if time.Since(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	tooMany := len(recent) >= demoPerIPHour
	if !tooMany {
		recent = append(recent, time.Now())
	}
	demoEntries.m[key] = recent
	if len(demoEntries.m) > 10000 { // 只是防刷，满了就清掉重来，不让这张表无限长大
		demoEntries.m = map[string][]time.Time{}
	}
	demoEntries.Unlock()
	if tooMany {
		http.Error(w, "进入演示太频繁了，请稍后再试", http.StatusTooManyRequests)
		return
	}
	uid, err := s.Store.DemoUserID(r.Context(), false)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := s.startSession(w, r, uid, 6*time.Hour); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, homeApp, http.StatusSeeOther)
}

// ========== 注册 ==========

// 发信限额。计数按“尝试”算（发信前先记一条），并且在查库之前判断，不会暴露邮箱有没有注册。
const (
	registerPerEmailDay = 3
	registerPerIPDay    = 10
	resetPerEmailMin    = 1
	resetPerEmailDay    = 5
	resetPerIPDay       = 10
	codePerEmailDay     = 3 // 同一个新邮箱每天最多收几封改邮箱验证码（防止多个账号轮流轰炸同一个邮箱）
	codePerUserMin      = 1
	codePerUserDay      = 5
)

func (s *Server) registerPage(w http.ResponseWriter, r *http.Request) {
	s.authPage(w, r, "register", "注册", 0, nil)
}

func (s *Server) registerPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := rateKey(s.clientIP(r)) // IPv6 按 /64 计：一个 /64 里的地址随便换，不能借此绕过按 IP 的限额
	email := store.NormEmail(r.FormValue("email"))
	fail := func(code int, msg string) {
		s.authPage(w, r, "register", "注册", code, map[string]any{"Email": email, "Error": msg})
	}
	if !s.human(r, "register") {
		fail(http.StatusBadRequest, errHuman)
		return
	}
	if !mail.ValidAddress(email) {
		fail(http.StatusBadRequest, "邮箱格式不对，请检查一下。")
		return
	}
	// 当场提示收不了信的域名（放在发信计数之前：拼错的邮箱不该占用限额）
	if !s.Mail.MX.CanReceive(ctx, email) {
		fail(http.StatusBadRequest, mail.ErrUndeliverable.Error()+"。")
		return
	}
	if s.Store.SettingOr(ctx, store.KeySignupOff, "") == "true" {
		fail(http.StatusForbidden, "暂时不开放注册，请稍后再来。")
		return
	}
	day := time.Now().Add(-24 * time.Hour)
	// 只有达到发信服务的硬上限才真正暂停注册（全站熔断只是提高人机校验难度，不拒绝任何人）
	if limit := s.Store.MailLimit(ctx); s.Store.MailCount(ctx, "", "", "", 0, day) >= limit {
		s.Log.Error("已达到发信服务的 24 小时上限，暂停注册", "limit", limit)
		fail(http.StatusServiceUnavailable, "今天注册的人太多，邮件额度用完了，请明天再来。")
		return
	}
	if s.Store.MailCount(ctx, "register", email, "", 0, day) >= registerPerEmailDay {
		fail(http.StatusTooManyRequests, "这个邮箱今天已经发过好几封了，请先查收（包括垃圾邮件），或者明天再试。")
		return
	}
	if s.Store.MailCount(ctx, "register", "", ip, 0, day) >= registerPerIPDay {
		fail(http.StatusTooManyRequests, "操作太频繁，请明天再试。")
		return
	}
	msg := mail.Msg{Kind: "register", To: email, IP: ip}
	u, err := s.Store.UserByEmail(ctx, email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		pw := secret.RandomPassword()
		bonus := s.Store.IntSetting(ctx, store.KeySignupBonus, 0)
		id, err := s.Store.CreateUser(ctx, email, secret.HashPassword(pw), bonus)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		msg.UserID = id
		s.passwordMail(ctx, &msg, email, pw)
	case err != nil:
		s.fail(w, r, err)
		return
	case u.Demo || u.Disabled:
		s.Store.LogMail(ctx, "register", email, ip, u.ID) // 不发信，但照样计数，响应和正常注册一样
		s.registerSent(w, r, email)
		return
	case u.LastLoginAt == nil:
		// 注册过但还没登录过：初始密码大概没收到，换一个新的再发一次
		pw := secret.RandomPassword()
		if err := s.Store.SetPassword(ctx, u.ID, secret.HashPassword(pw)); err != nil {
			s.fail(w, r, err)
			return
		}
		msg.UserID = u.ID
		s.passwordMail(ctx, &msg, email, pw)
	default:
		msg.UserID, msg.Subject, msg.Title = u.ID, "这个邮箱已经注册过 Pingoo", "这个邮箱已经注册过"
		msg.Body = fmt.Sprintf("刚才有人用这个邮箱（%s）在 Pingoo 注册。这个邮箱已经有账号了，不需要再注册，直接登录就行；忘记密码可以点下面的链接重新设置。"+
			"如非本人操作，请忽略此邮件，你的账号不受影响。", email)
		msg.Action, msg.ActionURL = "重新设置密码", s.siteURL(ctx)+"/forgot"
	}
	if err := s.Mail.Send(ctx, msg); err != nil {
		fail(http.StatusServiceUnavailable, "邮件没能发出去："+err.Error()+"。请稍后再试。")
		return
	}
	s.registerSent(w, r, email)
}

// passwordMail 填好“初始密码”这封邮件的内容。
func (s *Server) passwordMail(ctx context.Context, msg *mail.Msg, email, pw string) {
	msg.Subject, msg.Title = "欢迎使用 Pingoo：你的初始密码", "欢迎使用 Pingoo"
	msg.Body = fmt.Sprintf("你的账号已经开通，登录后可以在「设置 → 账号」里修改密码。如非本人注册，请忽略此邮件。\n\n    登录邮箱：%s\n    初始密码：%s", email, pw)
	msg.Action, msg.ActionURL = "登录 Pingoo", s.siteURL(ctx)+"/login"
}

// registerSent 不管邮箱是不是已经注册过，都显示同样的结果，不暴露邮箱是否存在。
func (s *Server) registerSent(w http.ResponseWriter, r *http.Request, email string) {
	s.page(w, r, "notice", "查收邮件", "", map[string]any{
		"Title": "初始密码已经发出",
		"Body":  "我们给 " + email + " 发了一封邮件，里面有你的初始密码。请查收（也看看垃圾邮件），然后用它登录。几分钟后还没收到，可以回到注册页再提交一次。",
		"Link":  "/login?email=" + url.QueryEscape(email), "LinkText": "去登录",
	})
}

// ========== 找回密码 ==========

func (s *Server) forgotPage(w http.ResponseWriter, r *http.Request) {
	s.authPage(w, r, "forgot", "找回密码", 0, nil)
}

func (s *Server) forgotPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := rateKey(s.clientIP(r))
	email := store.NormEmail(r.FormValue("email"))
	fail := func(code int, msg string) {
		s.authPage(w, r, "forgot", "找回密码", code, map[string]any{"Email": email, "Error": msg})
	}
	if !s.human(r, "forgot") {
		fail(http.StatusBadRequest, errHuman)
		return
	}
	if !mail.ValidAddress(email) {
		fail(http.StatusBadRequest, "邮箱格式不对，请检查一下。")
		return
	}
	// 限额在查库之前判断：不管邮箱存不存在，计数都一样，别人没法借此判断邮箱有没有注册
	if s.Store.MailCount(ctx, "reset", email, "", 0, time.Now().Add(-time.Minute)) >= resetPerEmailMin {
		fail(http.StatusTooManyRequests, "发得太快了，请 1 分钟后再试。")
		return
	}
	if s.Store.MailCount(ctx, "reset", email, "", 0, time.Now().Add(-24*time.Hour)) >= resetPerEmailDay {
		fail(http.StatusTooManyRequests, "这个邮箱今天已经发过好几次了，请明天再试。")
		return
	}
	// 同一来源每天最多 resetPerIPDay 次：不然换着邮箱填，就能给所有注册用户发信、耗光全站的发信额度
	if s.Store.MailCount(ctx, "reset", "", ip, 0, time.Now().Add(-24*time.Hour)) >= resetPerIPDay {
		fail(http.StatusTooManyRequests, "操作太频繁，请明天再试。")
		return
	}
	u, err := s.Store.UserByEmail(ctx, email)
	if err == nil && !u.Demo && !u.Disabled {
		link := s.siteURL(ctx) + "/reset?t=" + s.resetToken(u, time.Now().Add(30*time.Minute))
		_ = s.Mail.Send(ctx, mail.Msg{Kind: "reset", To: email, IP: ip, UserID: u.ID, Subject: "重新设置 Pingoo 密码", Title: "重新设置密码",
			Body:   "你申请了重新设置密码，请点下面的链接完成操作。链接 30 分钟内有效，只能用一次。如非本人操作，请忽略此邮件，原来的密码仍然有效。",
			Action: "重新设置密码", ActionURL: link})
	} else {
		s.Store.LogMail(ctx, "reset", email, ip, 0) // 没有账号也记一次，计数和有账号时一样
	}
	s.page(w, r, "notice", "查收邮件", "", map[string]any{
		"Title": "请查收邮件",
		"Body":  "如果 " + email + " 注册过 Pingoo，我们已经发了一封重新设置密码的邮件（30 分钟内有效）。没收到的话，看看垃圾邮件，或者 1 分钟后再试。",
		"Link":  "/login", "LinkText": "返回登录",
	})
}

// resetToken 生成重置密码的链接参数：用户 ID、过期时间、当前密码哈希的指纹，再加签名。
// 密码一改指纹就变了，所以链接天然只能用一次，不需要存在数据库里。
func (s *Server) resetToken(u store.User, exp time.Time) string {
	body := strconv.FormatInt(u.ID, 10) + "." + strconv.FormatInt(exp.Unix(), 10) + "." + secret.Hash(u.PassHash)[:16]
	return base64.RawURLEncoding.EncodeToString([]byte(body)) + "." + s.Store.Box.Sign("reset:"+body)
}

func (s *Server) checkResetToken(ctx context.Context, t string) (store.User, bool) {
	enc, sig, ok := strings.Cut(t, ".")
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if !ok || err != nil || !s.Store.Box.Verify("reset:"+string(raw), sig) {
		return store.User{}, false
	}
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 {
		return store.User{}, false
	}
	uid, _ := strconv.ParseInt(parts[0], 10, 64)
	exp, _ := strconv.ParseInt(parts[1], 10, 64)
	if time.Now().Unix() > exp {
		return store.User{}, false
	}
	u, err := s.Store.User(ctx, uid)
	if err != nil || u.Disabled || u.Demo || secret.Hash(u.PassHash)[:16] != parts[2] {
		return store.User{}, false
	}
	return u, true
}

func (s *Server) resetPage(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("t")
	_, ok := s.checkResetToken(r.Context(), t)
	s.page(w, r, "reset", "设置新密码", "", map[string]any{"Token": t, "Invalid": !ok})
}

func (s *Server) resetPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := r.FormValue("t")
	u, ok := s.checkResetToken(ctx, t)
	if !ok {
		s.page(w, r, "reset", "设置新密码", "", map[string]any{"Token": t, "Invalid": true})
		return
	}
	pw := r.FormValue("password")
	if msg := passwordProblem(pw, r.FormValue("password2")); msg != "" {
		w.WriteHeader(http.StatusBadRequest)
		s.page(w, r, "reset", "设置新密码", "", map[string]any{"Token": t, "Error": msg})
		return
	}
	if err := s.Store.SetPassword(ctx, u.ID, secret.HashPassword(pw)); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.Store.DeleteUserSessions(ctx, u.ID, "") // 其他设备上的登录全部失效
	s.Store.MarkLogin(ctx, u.ID)
	http.Redirect(w, r, "/login?m=reset&email="+url.QueryEscape(u.Email), http.StatusSeeOther)
}

func passwordProblem(pw, again string) string {
	switch {
	case utf8.RuneCountInString(pw) < config.MinPasswordLen:
		return fmt.Sprintf("密码至少要 %d 个字符。", config.MinPasswordLen)
	case utf8.RuneCountInString(pw) > 200:
		return "密码太长了。"
	case pw != again:
		return "两次输入的密码不一样。"
	}
	return ""
}

// ========== 账号设置 ==========

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	if !secret.CheckPassword(u.PassHash, r.FormValue("current")) {
		time.Sleep(loginDelay)
		toast(w, "当前密码不对")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if msg := passwordProblem(r.FormValue("password"), r.FormValue("password2")); msg != "" {
		toast(w, msg)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.Store.SetPassword(ctx, u.ID, secret.HashPassword(r.FormValue("password"))); err != nil {
		s.fail(w, r, err)
		return
	}
	cur, _ := ctx.Value(sessionKey).(string)
	_ = s.Store.DeleteUserSessions(ctx, u.ID, cur)
	toast(w, "密码已修改，其他设备上的登录已退出")
	redirect(w, r, "/settings#account")
}

// 改邮箱的验证码不存数据库：由用户 ID、新邮箱和 10 分钟的时间窗算出来（HMAC），校验时重新算一遍。
// 允许上一个时间窗的码，所以有效期是 10～20 分钟。猜码的次数在内存里限制。
const codeWindow = 10 * time.Minute

func (s *Server) emailCode(uid int64, email string, at time.Time) string {
	sig := s.Store.Box.Sign(fmt.Sprintf("email:%d:%s:%d", uid, store.NormEmail(email), at.Unix()/int64(codeWindow.Seconds())))
	n := 0
	for _, c := range sig {
		n = (n*31 + int(c)) % 1000000
	}
	return fmt.Sprintf("%06d", n)
}

func (s *Server) emailCodeRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	email := store.NormEmail(r.FormValue("email"))
	say := func(msg string) { toast(w, msg); w.WriteHeader(http.StatusNoContent) }
	if !secret.CheckPassword(u.PassHash, r.FormValue("password")) {
		time.Sleep(loginDelay)
		say("当前密码不对")
		return
	}
	if !mail.ValidAddress(email) {
		say("新邮箱格式不对")
		return
	}
	if email == u.Email {
		say("新邮箱和现在的一样")
		return
	}
	if !s.Mail.MX.CanReceive(ctx, email) {
		say(mail.ErrUndeliverable.Error())
		return
	}
	if s.Store.MailCount(ctx, "email_code", "", "", u.ID, time.Now().Add(-time.Minute)) >= codePerUserMin {
		say("发得太快了，请 1 分钟后再试")
		return
	}
	if s.Store.MailCount(ctx, "email_code", "", "", u.ID, time.Now().Add(-24*time.Hour)) >= codePerUserDay {
		say("今天已经发过好几次验证码了，请明天再试")
		return
	}
	if s.Store.MailCount(ctx, "email_code", email, "", 0, time.Now().Add(-24*time.Hour)) >= codePerEmailDay {
		say("这个邮箱今天已经收过好几次验证码了，请明天再试")
		return
	}
	if _, err := s.Store.UserByEmail(ctx, email); err == nil {
		say("这个邮箱已经被其他账号使用")
		return
	}
	err := s.Mail.Send(ctx, mail.Msg{Kind: "email_code", To: email, IP: rateKey(s.clientIP(r)), UserID: u.ID, Subject: "Pingoo 邮箱验证码",
		Title: "邮箱验证码",
		Body:  "你正在把 Pingoo 账号的登录邮箱改成这个邮箱，验证码 10 分钟内有效。如非本人操作，请忽略此邮件。\n\n    验证码：" + s.emailCode(u.ID, email, time.Now())})
	if err != nil {
		say("验证码没能发出去：" + err.Error())
		return
	}
	say("验证码已发到 " + email + "，10 分钟内有效")
}

// codeTries 记录每个用户最近 10 分钟输错验证码的次数，防止猜码。
var codeTries = struct {
	sync.Mutex
	m map[int64][]time.Time
}{m: map[int64][]time.Time{}}

func tooManyCodeTries(uid int64) bool {
	codeTries.Lock()
	defer codeTries.Unlock()
	keep := codeTries.m[uid][:0]
	for _, t := range codeTries.m[uid] {
		if time.Since(t) < codeWindow {
			keep = append(keep, t)
		}
	}
	codeTries.m[uid] = keep
	return len(keep) >= 5
}

func addCodeTry(uid int64) {
	codeTries.Lock()
	codeTries.m[uid] = append(codeTries.m[uid], time.Now())
	codeTries.Unlock()
}

func (s *Server) emailChange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	email := store.NormEmail(r.FormValue("email"))
	code := strings.TrimSpace(r.FormValue("code"))
	say := func(msg string) { toast(w, msg); w.WriteHeader(http.StatusNoContent) }
	if tooManyCodeTries(u.ID) {
		say("验证码输错太多次了，请 10 分钟后重新获取")
		return
	}
	now := time.Now()
	if code == "" || (code != s.emailCode(u.ID, email, now) && code != s.emailCode(u.ID, email, now.Add(-codeWindow))) {
		addCodeTry(u.ID)
		say("验证码不对或已过期")
		return
	}
	if err := s.Store.SetEmail(ctx, u.ID, email); err != nil {
		say("这个邮箱已经被其他账号使用")
		return
	}
	// 通知旧邮箱：万一不是本人改的，至少原来的主人知道
	go func(old string) {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.Mail.Send(cctx, mail.Msg{Kind: "notice", To: old, UserID: u.ID, Subject: "你的 Pingoo 登录邮箱已修改", Title: "登录邮箱已修改",
			Body: "你的 Pingoo 账号的登录邮箱刚刚从 " + old + " 改成了 " + email + "。如非本人操作，请尽快联系管理员。"})
	}(u.Email)
	toast(w, "登录邮箱已改为 "+email)
	redirect(w, r, "/settings#account")
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	if u.IsAdmin {
		toast(w, "管理员账号不能注销")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !secret.CheckPassword(u.PassHash, r.FormValue("password")) {
		time.Sleep(loginDelay)
		toast(w, "密码不对")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.Store.DeleteUser(ctx, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	redirect(w, r, "/login?m=deleted")
}

// ========== 小工具 ==========

// asciiJSON 把非 ASCII 字符转成 \uXXXX，才能放进 HTTP 响应头（HX-Trigger）。
func asciiJSON(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 {
			b.WriteRune(r)
			continue
		}
		if r > 0xFFFF {
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
			continue
		}
		fmt.Fprintf(&b, `\u%04x`, r)
	}
	return b.String()
}
