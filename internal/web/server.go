// Package web 是服务端渲染的网页（html/template + htmx）。
package web

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"pingoo/internal/agent"
	"pingoo/internal/config"
	"pingoo/internal/mail"
	"pingoo/internal/metrics"
	"pingoo/internal/pow"
	"pingoo/internal/scheduler"
	"pingoo/internal/signals"
	"pingoo/internal/store"
	"pingoo/internal/syncer"
	webassets "pingoo/web"
)

const Version = "0.1"

type Server struct {
	Cfg      *config.Config
	Store    *store.Store
	M        *metrics.Svc
	Agent    *agent.Agent
	Sync     *syncer.Syncer
	Sched    *scheduler.Scheduler
	Detector *signals.Detector
	Log      *slog.Logger
	Mail     *mail.Mailer
	Pow      *pow.Issuer

	base     *template.Template
	pages    map[string]*template.Template
	mux      *http.ServeMux // 用来判断一个地址有没有对应的页面（没登录访问不存在的地址时直接给 404，不跳登录）
	assetVer string         // 静态文件内容的哈希，用于让浏览器在文件更新后重新下载

	loginMu     sync.Mutex
	failures    map[string]*loginFail
	recentFails []time.Time // 最近一分钟全站的失败尝试（所有来源合计）
	lastPrune   time.Time

	breakerLogged time.Time // 上次记“注册熔断”日志的时间

	lookupUser func(ctx context.Context, email string) (store.User, error) // 测试里替换；nil 表示查数据库
}

func (s *Server) loadTemplates() error {
	base := template.New("").Funcs(funcMap())
	var err error
	base, err = base.ParseFS(webassets.FS, "templates/layouts/*.html", "templates/partials/*.html")
	if err != nil {
		return err
	}
	pages, err := fs.Glob(webassets.FS, "templates/pages/*.html")
	if err != nil {
		return err
	}
	s.base = base
	s.assetVer = assetHash()
	s.pages = map[string]*template.Template{}
	for _, p := range pages {
		t, err := base.Clone()
		if err != nil {
			return err
		}
		if t, err = t.ParseFS(webassets.FS, p); err != nil {
			return err
		}
		s.pages[strings.TrimSuffix(path.Base(p), ".html")] = t
	}
	return nil
}

func assetHash() string {
	h := sha1.New()
	_ = fs.WalkDir(webassets.FS, "static", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := webassets.FS.ReadFile(p)
			h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// view 是传给模板的数据。
type view struct {
	Asset   string
	Page    string
	Title   string
	Nav     string
	Version string
	Path    string
	User    *store.User // 没登录时为 nil
	HasDemo bool        // 有演示数据：没登录的访客在顶栏能看到“看演示”
	D       any
}

// publicPages 用对外的页面布局（顶部是“首页 / 关于 / 登录 / 注册”），其余用应用里的布局。
var publicPages = map[string]bool{"landing": true, "help": true, "about": true, "privacy": true, "terms": true,
	"login": true, "register": true, "forgot": true, "reset": true, "notice": true}

func (s *Server) page(w http.ResponseWriter, r *http.Request, name, title, nav string, d any) {
	s.render(w, r, http.StatusOK, name, title, nav, d)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, name, title, nav string, d any) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "模板不存在: "+name, 500)
		return
	}
	var buf bytes.Buffer
	layout := "layout"
	if publicPages[name] || (name == "error" && currentUser(r) == nil) { // 出错页：登录了用应用里的布局，没登录用对外的
		layout = "public"
	}
	hasDemo := false
	if layout == "public" && currentUser(r) == nil && s.Store != nil {
		_, err := s.Store.DemoUserID(r.Context(), false)
		hasDemo = err == nil
	}
	if err := t.ExecuteTemplate(&buf, layout, view{Asset: s.assetVer, Page: name, Title: title, Nav: nav, Version: Version, Path: r.URL.Path,
		User: currentUser(r), HasDemo: hasDemo, D: d}); err != nil {
		s.Log.Error("渲染页面失败", "page", name, "err", err)
		http.Error(w, "页面渲染出错", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}

// wantsPage 判断出错时该不该给一个完整的出错页：浏览器直接打开的页面要，htmx 局部请求和表单提交只要一行文字（前端会弹提示）。
func wantsPage(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && !isHX(r)
}

// notFound 是所有“找不到”的出口：地址不存在、网站或报告已经删掉、不是自己的数据、非管理员访问后台。
// 这几种情况给出完全一样的页面，外人分辨不出某个地址是否存在。
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if !wantsPage(r) {
		http.Error(w, "找不到，可能已经被删除了", http.StatusNotFound)
		return
	}
	s.render(w, r, http.StatusNotFound, "error", "页面不存在", "", map[string]any{"Code": 404, "Head": "页面不存在",
		"Msg": "这个地址可能输错了，或者内容已经被删除（比如网站、报告、对话被删掉了）。"})
}

// serverError 给浏览器一个友好的出错页，不把内部错误（SQL、文件路径等）显示给用户，详情只记日志。
func (s *Server) serverError(w http.ResponseWriter, r *http.Request) {
	if !wantsPage(r) {
		http.Error(w, "服务器出错了，请稍后再试", http.StatusInternalServerError)
		return
	}
	s.render(w, r, http.StatusInternalServerError, "error", "出错了", "", map[string]any{"Code": 500, "Head": "出错了",
		"Msg": "服务器处理这个请求时出了问题，我们已经记录下来。请稍后刷新再试；一直这样的话，请告诉我们你做了什么。"})
}

// part 渲染一个局部模板（htmx 请求用）。
func (s *Server) part(w http.ResponseWriter, name string, d any) {
	var buf bytes.Buffer
	if err := s.base.ExecuteTemplate(&buf, name, d); err != nil {
		s.Log.Error("渲染片段失败", "part", name, "err", err)
		http.Error(w, "渲染出错："+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = buf.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	s.Log.Error("请求失败", "path", r.URL.Path, "err", err)
	if !wantsPage(r) { // htmx 请求：前端把这行文字弹成提示，保持原样方便用户描述问题
		http.Error(w, "出错了："+err.Error(), http.StatusInternalServerError)
		return
	}
	s.serverError(w, r)
}

func (s *Server) Handler() (http.Handler, error) {
	s.failures = map[string]*loginFail{}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	static, _ := fs.Sub(webassets.FS, "static")
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(static)))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/", s.notFound) // 其他路由都没匹配上
	s.mux = mux

	// 对外的页面
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /help", s.helpPage)
	mux.HandleFunc("POST /feedback", s.feedback)
	mux.HandleFunc("GET /about", s.staticPage("about", "关于 Pingoo"))
	mux.HandleFunc("GET /privacy", s.staticPage("privacy", "隐私政策"))
	mux.HandleFunc("GET /terms", s.staticPage("terms", "服务条款"))
	mux.HandleFunc("POST /demo", s.demoEnter)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("GET /register", s.registerPage)
	mux.HandleFunc("POST /register", s.registerPost)
	mux.HandleFunc("GET /forgot", s.forgotPage)
	mux.HandleFunc("POST /forgot", s.forgotPost)
	mux.HandleFunc("GET /reset", s.resetPage)
	mux.HandleFunc("POST /reset", s.resetPost)
	mux.HandleFunc("POST /logout", s.logout)

	mux.HandleFunc("GET /overview", s.overview)
	mux.HandleFunc("GET /part/overview-insight", s.overviewInsight)
	mux.HandleFunc("POST /part/overview-insight", s.overviewInsightRegen)
	mux.HandleFunc("GET /part/overview-live", s.overviewLive)
	mux.HandleFunc("POST /signals/{id}/dismiss", s.dismissSignal)

	mux.HandleFunc("GET /sites/{id}", s.sitePage)
	mux.HandleFunc("GET /sites/{id}/page", s.sitePageAnalysis)
	mux.HandleFunc("GET /sites/{id}/part/insight", s.siteInsight)
	mux.HandleFunc("POST /sites/{id}/part/insight", s.siteInsightRegen)
	mux.HandleFunc("GET /sites/{id}/part/chart", s.siteChart)
	mux.HandleFunc("GET /sites/{id}/part/content", s.siteContent)
	mux.HandleFunc("GET /sites/{id}/part/page", s.sitePageDetail)
	mux.HandleFunc("GET /sites/{id}/part/sources", s.siteSources)
	mux.HandleFunc("GET /sites/{id}/part/search", s.siteSearch)
	mux.HandleFunc("GET /sites/{id}/part/audience", s.siteAudience)
	mux.HandleFunc("GET /sites/{id}/part/health", s.siteHealth)
	mux.HandleFunc("GET /sites/{id}/part/ads", s.siteAds)
	mux.HandleFunc("POST /sites/{id}/diagnose", s.diagnoseStart)
	mux.HandleFunc("GET /diagnoses/{id}", s.diagnosePoll)
	mux.HandleFunc("POST /diagnoses/{id}/save", s.diagnoseSave)
	mux.HandleFunc("POST /diagnoses/{id}/stop", s.diagnoseStop)
	mux.HandleFunc("POST /todos/{id}", s.todoToggle)
	mux.HandleFunc("POST /todos", s.todoAdd)

	mux.HandleFunc("GET /reports", s.reportsPage)
	mux.HandleFunc("POST /reports", s.reportGenerate)
	mux.HandleFunc("GET /reports/{id}", s.reportPage)
	mux.HandleFunc("GET /reports/{id}/status", s.reportStatus)

	mux.HandleFunc("GET /chat", s.chatPage)
	mux.HandleFunc("GET /chat/{id}", s.chatPage)
	mux.HandleFunc("POST /chat", s.chatSend)
	mux.HandleFunc("POST /chat/{id}", s.chatSend)
	mux.HandleFunc("POST /chat/{id}/scope", s.chatScope)
	mux.HandleFunc("POST /chat/{id}/delete", s.chatDelete)
	mux.HandleFunc("GET /messages/{id}", s.messagePoll)
	mux.HandleFunc("POST /messages/{id}/save", s.messageSave)

	mux.HandleFunc("GET /credits", s.creditsPage)
	mux.HandleFunc("GET /settings", s.settingsPage)
	mux.HandleFunc("POST /settings/google", s.saveGoogle)
	mux.HandleFunc("POST /settings/google/test", s.testGoogle)
	mux.HandleFunc("POST /settings/ai", s.saveAI)
	mux.HandleFunc("POST /settings/redeem", s.redeem)
	mux.HandleFunc("POST /settings/password", s.changePassword)
	mux.HandleFunc("POST /settings/email/code", s.emailCodeRequest)
	mux.HandleFunc("POST /settings/email", s.emailChange)
	mux.HandleFunc("POST /settings/account/delete", s.deleteAccount)
	mux.HandleFunc("GET /settings/sites/{id}", s.siteEditForm)
	mux.HandleFunc("POST /settings/sites/{id}", s.siteSave)
	mux.HandleFunc("POST /settings/sites/{id}/delete", s.siteDelete)
	mux.HandleFunc("GET /settings/import", s.importForm)
	mux.HandleFunc("POST /settings/import", s.importSites)
	mux.HandleFunc("POST /settings/reports", s.saveReports)
	mux.HandleFunc("POST /settings/sync", s.syncNow)
	mux.HandleFunc("GET /settings/sync/table", s.syncTable)
	mux.HandleFunc("POST /settings/sessions/others", s.logoutOthers)
	mux.HandleFunc("POST /settings/signals", s.saveSignals)

	// 管理后台（只有管理员能进，其他人看到的是 404）
	mux.HandleFunc("GET /admin", s.adminPage)
	mux.HandleFunc("POST /admin/settings", s.adminSaveSettings)
	mux.HandleFunc("POST /admin/mail/test", s.adminMailTest)
	mux.HandleFunc("POST /admin/users/{id}/credits", s.adminCredits)
	mux.HandleFunc("POST /admin/users/{id}/disable", s.adminDisable)
	mux.HandleFunc("POST /admin/users/{id}/plan", s.adminUserPlan)
	mux.HandleFunc("POST /admin/plans", s.adminPlans)
	mux.HandleFunc("GET /admin/models/{id}", s.adminModelForm)
	mux.HandleFunc("POST /admin/models/{id}", s.adminModelSave)
	mux.HandleFunc("POST /admin/models/{id}/delete", s.adminModelDelete)
	mux.HandleFunc("POST /admin/models/{id}/test", s.adminModelTest)
	mux.HandleFunc("POST /admin/codes", s.adminCodes)
	mux.HandleFunc("POST /admin/codes/delete", s.adminCodesDelete)
	mux.HandleFunc("POST /admin/cache/clear", s.clearCache)
	mux.HandleFunc("POST /admin/demo", s.adminDemo)

	return s.recoverer(s.limitBody(s.csrf(s.auth(mux)))), nil
}

// ========== 中间件 ==========

const cookieName = "pingoo_session"

type ctxKey int

const (
	sessionKey ctxKey = 1
	userCtxKey ctxKey = 2
)

// publicPaths 不登录也能访问。
var publicPaths = map[string]bool{"/": true, "/help": true, "/about": true, "/privacy": true, "/terms": true, "/demo": true, "/healthz": true,
	"/login": true, "/register": true, "/forgot": true, "/reset": true, "/logout": true}

// currentUser 返回当前登录的用户；没登录返回 nil。
func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(userCtxKey).(*store.User)
	return u
}

// auth 认出当前用户，把用户 ID 放进 context（之后所有按用户区分的查询都从这里取）：
//   - 没登录只能访问 publicPaths；
//   - 演示账号只能浏览，不能改任何东西；
//   - /admin 只有管理员能进，其他人看到的是 404。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/static/") || p == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
			id := hashToken(c.Value)
			if uid, renewed, _ := s.Store.TouchSession(ctx, id); uid > 0 {
				if u, err := s.Store.User(ctx, uid); err == nil && !u.Disabled {
					if renewed > 0 {
						// 会话续期了：cookie 也要跟着延长，不然浏览器会按登录时的期限把它丢掉
						s.setSessionCookie(w, r, c.Value, renewed)
					}
					ctx = context.WithValue(store.WithUser(ctx, u.ID), sessionKey, id)
					ctx = context.WithValue(ctx, userCtxKey, &u)
					r = r.WithContext(ctx)
				}
			}
		}
		u := currentUser(r)
		switch {
		case u == nil && !publicPaths[p] && s.routeMissing(r):
			s.notFound(w, r) // 不存在的地址直接 404，不用先登录
			return
		case u == nil && !publicPaths[p]:
			if isHX(r) {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		case u != nil && u.Demo && r.Method != http.MethodGet && r.Method != http.MethodHead && p != "/logout" && p != "/demo" && !demoReadable(p):
			toast(w, "演示账号只能浏览。注册一个账号就能连接自己的网站了")
			w.WriteHeader(http.StatusForbidden)
			return
		case u != nil && u.Demo && (p == "/settings" || strings.HasPrefix(p, "/settings/")):
			// 演示账号是大家共用的：设置页里有“已登录的设备”（其他访客的 IP），导入网站会去调 Google 接口，都不能给看
			if isHX(r) {
				toast(w, "演示账号没有设置页。注册一个账号就能连接自己的网站了")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			http.Redirect(w, r, homeApp, http.StatusSeeOther)
			return
		case (p == "/admin" || strings.HasPrefix(p, "/admin/")) && (u == nil || !u.IsAdmin):
			s.notFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// routeMissing 表示这个地址没有对应的页面（只会落到兜底的 404）。
func (s *Server) routeMissing(r *http.Request) bool {
	if s.mux == nil {
		return false
	}
	_, pattern := s.mux.Handler(r)
	return pattern == "/"
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.siteURL(r.Context()), "https://"),
		SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
}

// siteURL 是对外访问的地址（后台设置），邮件里的链接用它：不能从请求的 Host 头取，Host 头可以被伪造。
// 没设置时退回 http://localhost:端口；以 https 开头时登录 cookie 会加上 Secure。
func (s *Server) siteURL(ctx context.Context) string {
	if s.Store != nil {
		if u := strings.TrimRight(s.Store.SettingOr(ctx, store.KeySiteURL, ""), "/"); u != "" {
			return u
		}
	}
	addr := s.Cfg.ListenAddr
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return "http://" + addr
}

// csrf：POST 请求必须来自本站（检查 Origin / Referer，或带有 htmx 的自定义请求头）。
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		same := func(raw string) bool {
			u, err := url.Parse(raw)
			return err == nil && u.Host == r.Host
		}
		origin, ref := r.Header.Get("Origin"), r.Header.Get("Referer")
		ok := (origin != "" && same(origin)) || (origin == "" && ref != "" && same(ref)) ||
			(origin == "" && ref == "" && r.Header.Get("HX-Request") == "true")
		if !ok {
			http.Error(w, "请求来源不对", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitBody 限制请求体大小：表单最多 1MB，上传服务账号密钥的接口 2MB。
// 登录接口不需要登录就能访问，不限制的话可以用超大请求耗尽内存。
func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			limit := int64(1 << 20)
			if r.URL.Path == "/settings/google" {
				limit = 2 << 20
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// csp 只允许加载本站的脚本、样式、图片和接口。AI 的回答会按 Markdown 渲染，而 GA 里的页面标题、来源、搜索词
// 外人都能伪造，可能诱导 AI 输出指向外部地址的图片把数据带出去；有了这条策略，浏览器不会去请求站外资源。
// 脚本只允许本站的文件：页面里没有内联脚本和 onclick 这类属性（交互都在 app.js 里），
// 这样即使有内容被当成 HTML 插进页面，里面的脚本也不会执行。样式因为大量使用 style 属性，仍然允许内联。
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'self'; " +
	"frame-ancestors 'none'; object-src 'none'"

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic", "path", r.URL.Path, "err", v)
				s.serverError(w, r)
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// ========== 小工具 ==========

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func formID(r *http.Request, key string) int64 {
	id, _ := strconv.ParseInt(r.FormValue(key), 10, 64)
	return id
}

// clientIP 返回发起请求的地址，用于登录限速和会话记录。
// 默认只看 TCP 连接的对端地址：X-Forwarded-For 是请求方自己可以随便填的，直接信任它，
// 攻击者每次换一个假 IP 就能绕过限速，还能冒充别人的 IP 把对方锁在外面。
// 只有对端是 TRUSTED_PROXIES 里的反向代理时才读 X-Forwarded-For，并且从右往左取第一个不是代理的地址
// （右边是离我们最近的代理追加的，左边的部分仍然是客户端可以伪造的）。
// 配了 REAL_IP_HEADER（例如 Cloudflare 的 CF-Connecting-IP）时，对端可信就优先用这个头：CDN 会用访客的真实 IP
// 覆盖它，访客自己填的值到不了我们这里。前提是所有流量都经过 CDN——绕过 CDN 直连源站的人可以随便填这个头。
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap().WithZone("")
	if !s.trustedProxy(peer) {
		return peer.String()
	}
	if h := s.Cfg.RealIPHeader; h != "" {
		if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(h))); err == nil {
			return a.Unmap().WithZone("").String()
		}
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		a = a.Unmap().WithZone("")
		if !s.trustedProxy(a) {
			return a.String()
		}
	}
	return peer.String()
}

func (s *Server) trustedProxy(a netip.Addr) bool {
	if s.Cfg == nil {
		return false
	}
	for _, p := range s.Cfg.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// rateKey 是登录限速用的键：IPv4 按单个地址，IPv6 按 /64（一个用户通常拿到一整个 /64，逐个地址限速等于没限）。
func rateKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || a.Is4() {
		return ip
	}
	return netip.PrefixFrom(a, 64).Masked().String()
}

func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// toast 让前端显示一条提示。
func toast(w http.ResponseWriter, msg string) {
	w.Header().Set("HX-Trigger", asciiJSON(toJSON(map[string]string{"toast": msg})))
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if isHX(r) {
		// 目标和当前页面只差一个 #锚点 时，浏览器只会滚动、不会重新加载，所以改成刷新当前页
		if cur, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil && cur.Path != "" {
			if tgt, err := url.Parse(to); err == nil && tgt.Path == cur.Path && tgt.RawQuery == cur.RawQuery {
				w.Header().Set("HX-Refresh", "true")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
