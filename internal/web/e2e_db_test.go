package web

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pingoo/internal/agent"
	"pingoo/internal/config"
	"pingoo/internal/demo"
	"pingoo/internal/mail"
	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/pow"
	"pingoo/internal/scheduler"
	"pingoo/internal/secret"
	"pingoo/internal/signals"
	"pingoo/internal/store"
	"pingoo/internal/syncer"
	"pingoo/internal/testdb"
)

// e2e 是一个接了真实数据库（临时 schema）、假 AI 接口和不真发信的邮件服务的完整服务。
type e2e struct {
	t       *testing.T
	srv     *Server
	h       http.Handler
	st      *store.Store
	cookie  *http.Cookie
	aiCalls atomic.Int32
	aiFail  atomic.Bool  // 为 true 时假 AI 返回 500
	ip      string       // 请求的来源地址（RemoteAddr），空表示默认的 198.51.100.7
	noUsage atomic.Bool  // 为 true 时假 AI 不返回用量（usage）
	bigUse  atomic.Bool  // 为 true 时假 AI 报告一次很大的用量（超过预扣）
	aiDelay atomic.Int64 // 假 AI 每次回答前等多少毫秒（模拟真实 AI 要跑一会儿）
	mu      sync.Mutex
	mails   []mail.Msg
}

const adminEmail, adminPass = "admin@pingoo.test", "correct-horse-battery"

func newE2E(t *testing.T) *e2e {
	pool := testdb.Open(t)
	e := &e2e{t: t}
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.aiCalls.Add(1)
		if d := e.aiDelay.Load(); d > 0 {
			time.Sleep(time.Duration(d) * time.Millisecond)
		}
		if e.aiFail.Load() {
			http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
			return
		}
		content := "访客 **1,200**，比上期多 10%。\n\n![图](https://evil.example/a.png?d=1)\n\n追问建议：问题一 | 问题二"
		out := map[string]any{"model": "fake", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 10000, "completion_tokens": 5000}}
		if e.noUsage.Load() {
			delete(out, "usage")
		}
		if e.bigUse.Load() {
			out["usage"] = map[string]any{"prompt_tokens": 1000000, "completion_tokens": 100000}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(ai.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	cfg := &config.Config{AdminEmail: adminEmail, AdminPassword: adminPass, DatabaseURL: "postgres://pg:s3cret@db/pingoo", ListenAddr: ":5004"}
	e.st = store.New(pool, secret.New("0123456789abcdef0123456789abcdef"))
	if err := e.st.EnsureAdmin(ctx, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	_ = e.st.SetSetting(ctx, store.KeySiteURL, "http://pingoo.test") // 邮件里的链接用它
	// 一个默认模型：输入 1 元/百万、输出 2 元/百万、倍率 1 → 一次假回答（1 万 + 5 千 tokens）= 0.02 元 = 2 积分
	if _, err := e.st.SaveModel(ctx, store.AIModel{Name: "假模型", Model: "fake", BaseURL: ai.URL, APIKey: "k", PriceIn: 1, PriceOut: 2, PriceCache: 0.5,
		Multiplier: 1, Enabled: true, IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	_ = e.st.AddCredits(ctx, 1, 100000, "admin", "测试")
	sy := syncer.New(e.st, log)
	m := metrics.New(e.st, sy, log)
	ag := agent.New(e.st, m, sy, log)
	det := &signals.Detector{M: m}
	mailer := &mail.Mailer{DryRun: true, Store: e.st, Log: log,
		MX: &mail.MX{Lookup: func(ctx context.Context, d string) ([]*net.MX, error) {
			if d == "nomx.test" {
				return nil, &net.DNSError{Err: "no such host", Name: d, IsNotFound: true}
			}
			return []*net.MX{{Host: "mx." + d + ".", Pref: 10}}, nil
		}},
		Sent: func(m mail.Msg) { e.mu.Lock(); e.mails = append(e.mails, m); e.mu.Unlock() }}
	e.srv = &Server{Cfg: cfg, Store: e.st, M: m, Agent: ag, Sync: sy, Detector: det, Log: log, Mail: mailer,
		Pow:   pow.NewWithKey([]byte("test-key-0123456789abcdef0123456")),
		Sched: &scheduler.Scheduler{Store: e.st, Sync: sy, Detector: det, Agent: ag, Log: log}}
	h, err := e.srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	old, oldBits := loginDelay, powBits
	loginDelay, powBits = 0, 2
	t.Cleanup(func() { loginDelay, powBits = old, oldBits })
	return e
}

func (e *e2e) do(method, path string, form url.Values) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, "http://pingoo.test"+path, body)
	req.RemoteAddr = "198.51.100.7:1000"
	if e.ip != "" {
		req.RemoteAddr = e.ip
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://pingoo.test")
	}
	if e.cookie != nil {
		req.AddCookie(e.cookie)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// withPow 给表单加上一个“5 秒前签发”的挑战和答案。
func (e *e2e) withPow(scope string, form url.Values) url.Values {
	tok := e.srv.Pow.IssueAt(scope, powBits, time.Now().Add(-5*time.Second))
	form.Set("pow_token", tok)
	form.Set("pow_nonce", pow.Solve(tok))
	return form
}

// loginAs 登录，把会话 cookie 记到 e.cookie 上（之后的请求都以这个用户的身份发）。
func (e *e2e) loginAs(email, pw string) {
	e.t.Helper()
	e.cookie = nil
	rec := e.do("POST", "/login", e.withPow("login", url.Values{"email": {email}, "password": {pw}, "next": {"/\t/evil.example"}, "remember": {"1"}}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != homeApp {
		e.t.Fatalf("登录应该成功并跳回站内: code=%d Location=%q body=%.300s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			e.cookie = c
		}
	}
	if e.cookie == nil {
		e.t.Fatal("没有拿到会话 cookie")
	}
}

func (e *e2e) login() { e.loginAs(adminEmail, adminPass) }

// lastMail 返回最近发给某个邮箱的一封信。
func (e *e2e) lastMail(to string) (mail.Msg, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var last mail.Msg
	n := 0
	for _, m := range e.mails {
		if m.To == to {
			last, n = m, n+1
		}
	}
	return last, n
}

// newUser 通过注册流程建一个用户、登录，返回 ID。
func (e *e2e) newUser(email string) int64 {
	e.t.Helper()
	e.cookie = nil
	if rec := e.do("POST", "/register", e.withPow("register", url.Values{"email": {email}})); rec.Code != 200 {
		e.t.Fatalf("注册失败: %d %.300s", rec.Code, rec.Body.String())
	}
	m, _ := e.lastMail(email)
	pw := regexp.MustCompile(`初始密码：(\S+)`).FindStringSubmatch(m.Body)
	if pw == nil {
		e.t.Fatalf("邮件里没有初始密码: %q", m.Body)
	}
	e.loginAs(email, pw[1])
	u, _ := e.st.UserByEmail(context.Background(), email)
	return u.ID
}

func (e *e2e) count(q string, args ...any) (n int) {
	e.t.Helper()
	if err := e.st.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return
}

func (e *e2e) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.st.Pool.Exec(context.Background(), q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

// waitMessage 等一条回答结束（消息按用户隔离，所以用对话主人的身份去读）。
func (e *e2e) waitMessage(id int64) store.Message {
	var owner int64
	_ = e.st.Pool.QueryRow(context.Background(), `SELECT c.user_id FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE m.id=$1`, id).Scan(&owner)
	ctx := store.WithUser(context.Background(), owner)
	for i := 0; i < 100; i++ {
		if msg, err := e.st.Message(ctx, id); err == nil && msg.Status != "running" {
			return msg
		}
		time.Sleep(100 * time.Millisecond)
	}
	msg, _ := e.st.Message(ctx, id)
	return msg
}

func TestE2EPagesRangesAndSession(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	if rec := e.do("GET", "/", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "免费注册") {
		t.Fatalf("没登录时首页应该是介绍页: %d", rec.Code)
	}
	for _, p := range []string{"/about", "/privacy", "/terms", "/help", "/login", "/register", "/forgot"} {
		if rec := e.do("GET", p, nil); rec.Code != 200 {
			t.Errorf("GET %s（没登录）= %d", p, rec.Code)
		}
	}
	// 帮助页：没登录也能看，价格表按后台的模型算（假模型输入 1 元/百万 × 倍率 1 = 100 积分/百万）
	if body := e.do("GET", "/help", nil).Body.String(); !strings.Contains(body, "假模型") || !strings.Contains(body, "<td>100</td>") || !strings.Contains(body, "/login?next=/help%23feedback") {
		t.Error("帮助页应该列出模型价格，没登录时提示登录后反馈")
	}
	if rec := e.do("GET", "/overview", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("没登录访问概览应该跳到登录页: %d", rec.Code)
	}
	if rec := e.do("GET", "/settings", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("没登录访问设置应该跳到登录页: %d", rec.Code)
	}
	e.login()
	for _, p := range []string{"/overview", "/settings", "/reports", "/chat", "/admin", "/help"} {
		if rec := e.do("GET", p, nil); rec.Code != 200 {
			t.Errorf("GET %s（空库）= %d", p, rec.Code)
		}
	}
	// 首页和概览不共用路径：登录后首页仍是介绍页，按钮进概览
	if body := e.do("GET", "/", nil).Body.String(); !strings.Contains(body, `href="/overview">进入 Pingoo`) || !strings.Contains(body, "三步开始") {
		t.Error("登录后的首页应该还是介绍页，带“进入 Pingoo”")
	}
	if rec := e.do("GET", "/login", nil); rec.Header().Get("Location") != homeApp {
		t.Errorf("已登录打开登录页应该跳到概览: %q", rec.Header().Get("Location"))
	}
	if body := e.do("GET", "/settings", nil).Body.String(); strings.Contains(body, "s3cret") {
		t.Error("设置页不应该显示数据库密码")
	}

	// 会话续期：有效期用掉一部分后再访问，过期时间延后，cookie 也重新下发
	if rec := e.do("GET", "/overview", nil); len(rec.Result().Cookies()) != 0 {
		t.Error("刚登录不需要重新下发 cookie")
	}
	e.exec(`UPDATE sessions SET expires_at = now() + interval '1 day'`)
	rec := e.do("GET", "/overview", nil)
	var renewed *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			renewed = c
		}
	}
	if renewed == nil || renewed.Value != e.cookie.Value || renewed.MaxAge != int(sessionTTL.Seconds()) || !renewed.HttpOnly {
		t.Errorf("续期后应该重新下发同一个 cookie、有效期 30 天: %+v", renewed)
	}
	if e.count(`SELECT count(*) FROM sessions WHERE expires_at > now() + interval '29 days'`) != 1 {
		t.Error("数据库里的过期时间没有延后")
	}

	// 演示账号：从首页进入，只能浏览
	if err := demo.Seed(ctx, e.st, e.srv.Detector); err != nil {
		t.Fatal(err)
	}
	e.cookie = nil
	if body := e.do("GET", "/", nil).Body.String(); !strings.Contains(body, `action="/demo"`) {
		t.Error("有演示数据时首页应该有“先看看演示”")
	}
	rec = e.do("POST", "/demo", url.Values{})
	if rec.Header().Get("Location") != homeApp {
		t.Errorf("进入演示后应该到概览: %q", rec.Header().Get("Location"))
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			e.cookie = c
		}
	}
	demoID, _ := e.st.DemoUserID(ctx, false)
	sites, _ := e.st.Sites(store.WithUser(ctx, demoID))
	if e.cookie == nil || len(sites) == 0 {
		t.Fatalf("进入演示失败: cookie=%v sites=%d", e.cookie, len(sites))
	}
	sid := sites[0].ID
	loc := sites[0].Location()
	yesterday := time.Now().In(loc).AddDate(0, 0, -1)
	from, to := yesterday.AddDate(0, 0, -9).Format("2006-01-02"), yesterday.Format("2006-01-02")

	// 概览和网站页：预设范围、自选日期、去年同期
	for _, p := range []string{"/overview", "/overview?range=d28", "/overview?from=" + from + "&to=" + to, "/overview?range=d28&cmp=yoy", "/overview?range=today&cmp=yoy",
		fmt.Sprintf("/sites/%d", sid), fmt.Sprintf("/sites/%d?range=yesterday", sid)} {
		if rec := e.do("GET", p, nil); rec.Code != 200 {
			t.Errorf("GET %s = %d: %s", p, rec.Code, rec.Body.String())
		}
	}
	body := e.do("GET", fmt.Sprintf("/sites/%d?from=%s&to=%s", sid, from, to), nil).Body.String()
	for _, want := range []string{`class="custom-range on"`, `value="` + from + `"`, `value="` + to + `"`, "range=" + from + "_" + to} {
		if !strings.Contains(body, want) {
			t.Errorf("自选日期的网站页里应该有 %q", want)
		}
	}
	body = e.do("GET", fmt.Sprintf("/sites/%d?range=d28&cmp=yoy", sid), nil).Body.String()
	for _, want := range []string{"对比去年同期", "去年同期 ", "cmp=yoy", "还没有数据"} { // 演示数据只有 120 天，一年前没有可比的
		if !strings.Contains(body, want) {
			t.Errorf("去年同期的网站页里应该有 %q", want)
		}
	}
	body = e.do("GET", fmt.Sprintf("/sites/%d/part/chart?range=d28&metric=v&cmp=yoy", sid), nil).Body.String()
	if !strings.Contains(body, "prevName") || !strings.Contains(body, "去年同期") {
		t.Errorf("趋势图应该按去年同期画对比线: %.300s", body)
	}
	// 明细表格始终和上一期比，带不带 cmp 结果一样
	a := e.do("GET", fmt.Sprintf("/sites/%d/part/content?range=d28&tab=landing", sid), nil).Body.String()
	b := e.do("GET", fmt.Sprintf("/sites/%d/part/content?range=d28&tab=landing&cmp=yoy", sid), nil).Body.String()
	if a != b || !strings.Contains(a, "data-detail") {
		t.Error("内容表格不应该受对比方式影响")
	}
	// 不合法的范围退回默认（7 天），不报错
	if rec := e.do("GET", fmt.Sprintf("/sites/%d?from=2026-13-40&to=x", sid), nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "range=d7") {
		t.Errorf("不合法的日期应该退回默认范围: %d", rec.Code)
	}
	// 演示账号不能改任何东西、不能用 AI、进不了后台
	if rec := e.do("POST", "/settings/sites/0", url.Values{"name": {"x"}}); rec.Code != http.StatusForbidden {
		t.Errorf("演示账号不能添加网站: %d", rec.Code)
	}
	if rec := e.do("POST", "/chat", url.Values{"text": {"hi"}}); rec.Code != http.StatusForbidden {
		t.Errorf("演示账号不能用问答: %d", rec.Code)
	}
	if rec := e.do("GET", "/admin", nil); rec.Code != http.StatusNotFound {
		t.Errorf("演示账号进不了后台: %d", rec.Code)
	}
	// 演示账号不能调用 AI，但能看到预先写好的示例：洞察、周报、诊断
	if body := e.do("GET", fmt.Sprintf("/sites/%d/part/insight?range=d7", sid), nil).Body.String(); !strings.Contains(body, "演示示例") {
		t.Errorf("演示账号应该看到示例洞察: %.300s", body)
	}
	if body := e.do("GET", "/part/overview-insight?range=d7", nil).Body.String(); !strings.Contains(body, "个网站近 7 天共") {
		t.Errorf("演示账号应该看到全部网站的示例洞察: %.300s", body)
	}
	var repID int64
	_ = e.st.Pool.QueryRow(ctx, `SELECT id FROM reports WHERE user_id=$1 AND status='done'`, demoID).Scan(&repID)
	if body := e.do("GET", fmt.Sprintf("/reports/%d", repID), nil).Body.String(); repID == 0 || !strings.Contains(body, "演示示例") {
		t.Errorf("演示账号应该有一份示例周报: id=%d", repID)
	}
	calls := e.aiCalls.Load()
	if rec := e.do("POST", fmt.Sprintf("/sites/%d/diagnose", sid), url.Values{}); rec.Code != 200 || !strings.Contains(rec.Body.String(), "演示示例") || e.aiCalls.Load() != calls {
		t.Errorf("演示账号点诊断应该看到示例诊断、不调用 AI: %d %.200s", rec.Code, rec.Body.String())
	}
	// 广告收益：JSON 工具箱放了广告，最近广告单价掉了，“JSON 转 CSV”收入很低
	var jsonID int64
	_ = e.st.Pool.QueryRow(ctx, `SELECT id FROM sites WHERE user_id=$1 AND name='JSON 工具箱'`, demoID).Scan(&jsonID)
	if body := e.do("GET", fmt.Sprintf("/sites/%d", jsonID), nil).Body.String(); !strings.Contains(body, `id="ads"`) {
		t.Error("有广告数据的网站应该有“广告收益”一节")
	}
	if body := e.do("GET", fmt.Sprintf("/sites/%d", sid), nil).Body.String(); sid != jsonID && strings.Contains(body, `id="ads"`) && e.count(`SELECT count(*) FROM ga_daily WHERE site_id=$1 AND ad_impressions>0`, sid) == 0 {
		t.Error("没有广告数据的网站不显示“广告收益”")
	}
	if body := e.do("GET", fmt.Sprintf("/sites/%d/part/ads?range=d28&tab=low", jsonID), nil).Body.String(); !strings.Contains(body, "/json-to-csv") || !strings.Contains(body, "USD") {
		t.Errorf("“流量大、收入低”应该列出 /json-to-csv: %.300s", body)
	}
	if e.count(`SELECT count(*) FROM signals WHERE site_id=$1 AND kind IN ('ad_rpm_drop','ad_low_page')`, jsonID) != 2 {
		t.Error("演示数据应该触发“广告收益下滑”和“高流量低收益页面”两条提醒")
	}
	if body := e.do("GET", "/overview", nil).Body.String(); !strings.Contains(body, "广告收入 <b>") {
		t.Error("概览的网站卡片上应该显示广告收入")
	}
}

func TestE2ESettingsAndAI(t *testing.T) {
	e := newE2E(t)
	ctx := store.WithUser(context.Background(), 1)
	e.login()

	// 导入：同一个媒体资源只导入一次（服务账号能看到的列表按用户缓存）
	type lists struct {
		P []map[string]string
		G []map[string]string
	}
	e.st.CacheSet(ctx, "google:lists:1", lists{P: []map[string]string{{"Property": "properties/111", "DisplayName": "站点甲", "Domain": "a.example", "TimeZone": "America/Los_Angeles"}}}, time.Hour)
	e.do("POST", "/settings/import", url.Values{"ga": {"properties/111", "properties/111"}, "gsc_111": {"sc-domain:a.example"}})
	e.do("POST", "/settings/import", url.Values{"ga": {"properties/111"}, "gsc_111": {"-"}})
	sites, _ := e.st.Sites(ctx)
	if len(sites) != 1 {
		t.Fatalf("同一个媒体资源应该只导入一次，实际 %d 个网站", len(sites))
	}
	site := sites[0]

	// 编辑网站：改灵敏度不动数据；换 GA 清掉 GA 的旧数据、保留 Search Console
	day := time.Now().AddDate(0, 0, -3)
	e.exec(`INSERT INTO ga_daily(site_id,date,active_users,new_users,returning_users,sessions,engaged_sessions,page_views,engagement_seconds) VALUES($1,$2,10,5,5,12,6,30,100)`, site.ID, day)
	e.exec(`INSERT INTO gsc_daily(site_id,date,clicks,impressions,pos_x_impr) VALUES($1,$2,3,30,90)`, site.ID, day)
	_ = e.st.SaveSyncState(ctx, store.SyncState{SiteID: site.ID, Source: "ga", LastSynced: &day, BackfilledFrom: &day, Status: "ok"})
	_ = e.st.SaveSyncState(ctx, store.SyncState{SiteID: site.ID, Source: "gsc", LastSynced: &day, BackfilledFrom: &day, Status: "ok"})
	form := url.Values{"name": {site.Name}, "domain": {site.Domain}, "ga_property": {site.GAProperty}, "gsc_site": {site.GSCSite}, "timezone": {site.Timezone}, "signal_level": {"high"}}
	e.do("POST", fmt.Sprintf("/settings/sites/%d", site.ID), form)
	if got, _ := e.st.Site(ctx, site.ID); got.SignalLevel != "high" || e.count(`SELECT count(*) FROM ga_daily`) != 1 {
		t.Errorf("只改灵敏度：应该存上并且不动数据 (level=%q)", got.SignalLevel)
	}
	form.Set("ga_property", "properties/222")
	e.do("POST", fmt.Sprintf("/settings/sites/%d", site.ID), form)
	if e.count(`SELECT count(*) FROM ga_daily`) != 0 || e.count(`SELECT count(*) FROM gsc_daily`) != 1 || e.count(`SELECT count(*) FROM sync_state`) != 1 {
		t.Error("换 GA 媒体资源后：GA 的旧数据应该清掉，Search Console 的保留")
	}
	if body := e.do("GET", fmt.Sprintf("/settings/sites/%d", site.ID), nil).Body.String(); !strings.Contains(body, `value="high" selected`) {
		t.Error("编辑表单里应该选中当前的灵敏度")
	}

	// 后台全站设置：每日用量上限、网站数；提醒类型是个人设置
	e.do("POST", "/admin/settings", url.Values{"ai_daily_token_cap": {"2,000,000"}, "signup_bonus": {"30"}})
	if e.st.DailyTokenCap(ctx) != 2000000 || e.st.IntSetting(ctx, store.KeySignupBonus, 0) != 30 {
		t.Errorf("全站设置没存上: cap=%d", e.st.DailyTokenCap(ctx))
	}
	// 站点、发信设置：格式不对的项不保存；SMTP 密码加密保存，留空不修改；“暂停注册”只在它自己的表单里处理
	rec := e.do("POST", "/admin/settings", url.Values{"site_url": {"https://pingoo.example.com/"}, "default_tz": {"Asia/Tokyo"},
		"smtp_host": {"smtp.example.com"}, "smtp_port": {"465"}, "smtp_user": {"u"}, "smtp_password": {"p@ss"}, "smtp_from": {"no-reply@example.com"},
		"mail_daily_limit": {"5000"}})
	if e.st.SettingOr(ctx, store.KeySiteURL, "") != "https://pingoo.example.com" || e.st.DefaultTZ(ctx) != "Asia/Tokyo" || e.st.MailLimit(ctx) != 5000 ||
		e.srv.Mail.Config(ctx).Password != "p@ss" || e.count(`SELECT count(*) FROM app_settings WHERE key='smtp_password' AND encrypted AND value<>'p@ss'`) != 1 {
		t.Errorf("站点和发信设置没存好: %q", rec.Header().Get("HX-Trigger"))
	}
	rec = e.do("POST", "/admin/settings", url.Values{"site_url": {"pingoo.example.com/app"}, "smtp_from": {"bad"}, "smtp_password": {""}})
	if !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("站点地址")) || e.st.SettingOr(ctx, store.KeySiteURL, "") != "https://pingoo.example.com" ||
		e.srv.Mail.Config(ctx).Password != "p@ss" || e.srv.Mail.Config(ctx).From != "no-reply@example.com" {
		t.Error("格式不对的站点地址、发件地址不应该保存；密码留空不应该被清掉")
	}
	_ = e.st.SetSetting(ctx, store.KeySignupOff, "true")
	e.do("POST", "/admin/settings", url.Values{"default_tz": {"Asia/Shanghai"}})
	if e.st.SettingOr(ctx, store.KeySignupOff, "") != "true" {
		t.Error("没带“暂停注册”的表单不应该改动它")
	}
	_ = e.st.SetSetting(ctx, store.KeySignupOff, "")
	_ = e.st.SetSetting(ctx, store.KeySiteURL, "http://pingoo.test")
	e.do("POST", "/admin/mail/test", url.Values{})
	if m, _ := e.lastMail(adminEmail); m.Subject != "Pingoo 测试邮件" {
		t.Errorf("测试邮件应该发给管理员自己: %+v", m)
	}

	e.do("POST", "/settings/signals", url.Values{"kind": {"tracking_broken", "traffic_drop"}})
	muted := e.st.MutedSignals(ctx)
	if len(muted) != len(signals.Kinds)-2 || strings.Contains(strings.Join(muted, ","), "traffic_drop") {
		t.Errorf("没勾选的类型应该都记为关闭: %v", muted)
	}
	if body := e.do("GET", "/settings", nil).Body.String(); !strings.Contains(body, `name="kind" value="near_top" >`) || !strings.Contains(body, `name="kind" value="traffic_drop" checked>`) {
		t.Error("设置页里的提醒类型没有按保存的显示")
	}
	if body := e.do("GET", "/admin", nil).Body.String(); !strings.Contains(body, `value="2000000"`) || strings.Contains(body, "retention_months") {
		i := strings.Index(body, `name="ai_daily_token_cap"`)
		t.Errorf("后台应该显示保存的全站设置，不再有保留期限的选项: %q", body[max(0, i-20):min(len(body), i+120)])
	}

	// 问答：正常回答、按实际用量扣积分；回答里的外链图片不渲染
	before := e.count(`SELECT credits FROM users WHERE id=1`)
	e.do("POST", "/chat", url.Values{"text": {"最近怎么样？"}})
	var convID, msgID int64
	_ = e.st.Pool.QueryRow(ctx, `SELECT conversation_id, id FROM messages WHERE role='assistant' ORDER BY id DESC LIMIT 1`).Scan(&convID, &msgID)
	if msg := e.waitMessage(msgID); msg.Status != "done" || len(msg.Followups) != 2 {
		t.Fatalf("问答应该正常完成: %+v", msg)
	}
	if got := e.count(`SELECT credits FROM users WHERE id=1`); got != before-2 {
		t.Errorf("一次问答（1 万输入 + 5 千输出，1 元/2 元每百万）应该扣 2 积分：%d → %d", before, got)
	}
	if body := e.do("GET", fmt.Sprintf("/chat/%d", convID), nil).Body.String(); strings.Contains(body, "<img") || !strings.Contains(body, "<strong>1,200</strong>") {
		t.Error("回答应该按 Markdown 渲染，但不渲染图片")
	}

	// 卡住的回答：1 分钟前的还算进行中，10 分钟前的标成失败后放行
	e.exec(`INSERT INTO messages(conversation_id,role,status,created_at) VALUES($1,'assistant','running',now()-interval '1 minute')`, convID)
	calls := e.aiCalls.Load()
	e.do("POST", fmt.Sprintf("/chat/%d", convID), url.Values{"text": {"第二问"}})
	if e.aiCalls.Load() != calls {
		t.Error("上一个回答还在进行时不应该接受新问题")
	}
	e.exec(`UPDATE messages SET created_at=now()-interval '10 minutes' WHERE status='running'`)
	e.do("POST", fmt.Sprintf("/chat/%d", convID), url.Values{"text": {"第二问"}})
	_ = e.st.Pool.QueryRow(ctx, `SELECT id FROM messages WHERE role='assistant' ORDER BY id DESC LIMIT 1`).Scan(&msgID)
	if msg := e.waitMessage(msgID); msg.Status != "done" || e.count(`SELECT count(*) FROM messages WHERE status='running'`) != 0 {
		t.Errorf("卡住的回答应该被标成失败，新问题能正常回答: %+v", msg)
	}

	// 全站每日用量上限：到了之后问答直接说明原因，洞察不再自动生成（也不会反复触发）
	_ = e.st.SetSetting(ctx, store.KeyAIDailyCap, "100")
	calls = e.aiCalls.Load()
	msgs := e.count(`SELECT count(*) FROM messages`)
	rec = e.do("POST", fmt.Sprintf("/chat/%d", convID), url.Values{"text": {"第三问"}})
	if !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("上限")) || e.count(`SELECT count(*) FROM messages`) != msgs {
		t.Errorf("到上限后问答应该直接提示原因、不建消息: %q", rec.Header().Get("HX-Trigger"))
	}
	e.exec(`UPDATE users SET auto_ai=true WHERE id=1`)
	for i := 0; i < 2; i++ {
		body := e.do("GET", fmt.Sprintf("/sites/%d/part/insight?range=d7", site.ID), nil).Body.String()
		if !strings.Contains(body, "上限") || strings.Contains(body, "hx-trigger=\"every") {
			t.Errorf("到上限后洞察应该说明原因，而且不再轮询: %.200s", body)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if e.aiCalls.Load() != calls || e.count(`SELECT count(*) FROM insights`) != 0 {
		t.Error("到上限后不应该再调用 AI，也不应该留下洞察记录")
	}
}

// TestE2EIsolation：一个用户无论怎么改 URL 或表单里的 ID，都看不到、改不了别人的数据，AI 也查不到。
func TestE2EIsolation(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	actx := store.WithUser(ctx, 1)
	// 管理员（用户甲）的网站和各种记录
	aSite, _ := e.st.CreateSite(actx, store.Site{Name: "甲的秘密网站", Domain: "a-secret.example", Timezone: "UTC"})
	e.exec(`INSERT INTO ga_daily(site_id,date,active_users,new_users,returning_users,sessions,engaged_sessions,page_views,engagement_seconds) VALUES($1,current_date-1,10,5,5,12,6,30,100)`, aSite)
	_ = e.st.UpsertSignal(actx, store.Signal{SiteID: aSite, Kind: "traffic_drop", Severity: "warn", Title: "甲的提醒", EventDate: time.Now()}, "iso-1")
	sig, _ := e.st.OpenSignals(actx, aSite, 30)
	diag, _ := e.st.CreateDiagnosis(actx, aSite, "甲的诊断", nil)
	rep, _ := e.st.CreateReport(actx, &aSite, "weekly", time.Now().AddDate(0, 0, -7), time.Now())
	conv, _ := e.st.CreateConversation(actx, &aSite, "甲的对话")
	msg, _ := e.st.AddMessage(actx, store.Message{ConversationID: conv, Role: "user", Content: "甲问的问题"})
	todo, _ := e.st.AddTodo(actx, store.Todo{SiteID: &aSite, Source: "insight", Text: "甲的待办"})
	if len(sig) != 1 || diag == 0 || rep == 0 || conv == 0 || msg == 0 || todo == 0 {
		t.Fatalf("准备数据失败: sig=%d diag=%d rep=%d conv=%d msg=%d todo=%d", len(sig), diag, rep, conv, msg, todo)
	}

	bID := e.newUser("b@example.com")
	secrets := []string{"甲的秘密网站", "a-secret.example", "甲的提醒", "甲的诊断", "甲的对话", "甲问的问题", "甲的待办"}
	gets := []string{"/", "/reports", "/chat", "/settings",
		fmt.Sprintf("/sites/%d", aSite), fmt.Sprintf("/sites/%d/page?path=/", aSite), fmt.Sprintf("/settings/sites/%d", aSite),
		fmt.Sprintf("/diagnoses/%d", diag), fmt.Sprintf("/reports/%d", rep), fmt.Sprintf("/reports/%d/status", rep),
		fmt.Sprintf("/chat/%d", conv), fmt.Sprintf("/messages/%d", msg)}
	for _, part := range []string{"insight", "chart?metric=v", "content?tab=rising", "page?path=/", "sources?tab=channels", "search?tab=top", "audience", "health"} {
		gets = append(gets, fmt.Sprintf("/sites/%d/part/%s", aSite, part))
	}
	for _, p := range gets {
		rec := e.do("GET", p, nil)
		for _, sec := range secrets {
			if strings.Contains(rec.Body.String(), sec) {
				t.Errorf("用户乙打开 %s 看到了甲的数据 %q（%d）", p, sec, rec.Code)
			}
		}
		if strings.Contains(p, fmt.Sprint(aSite)) || strings.Contains(p, fmt.Sprint(conv)) {
			if rec.Code == 200 {
				t.Errorf("用户乙打开 %s 应该找不到，实际 200", p)
			}
		}
	}
	// 改不了：删网站、忽略提醒、勾待办、删对话、把对话挂到甲的网站、诊断甲的网站、给甲的网站生成报告、同步甲的网站
	e.do("POST", fmt.Sprintf("/settings/sites/%d/delete", aSite), url.Values{})
	e.do("POST", fmt.Sprintf("/settings/sites/%d", aSite), url.Values{"name": {"被改了"}})
	e.do("POST", fmt.Sprintf("/signals/%d/dismiss", sig[0].ID), url.Values{})
	e.do("POST", fmt.Sprintf("/todos/%d", todo), url.Values{"done": {"on"}})
	e.do("POST", fmt.Sprintf("/chat/%d/delete", conv), url.Values{})
	e.do("POST", fmt.Sprintf("/sites/%d/diagnose", aSite), url.Values{"title": {"x"}})
	e.do("POST", "/reports", url.Values{"kind": {"weekly"}, "site": {fmt.Sprint(aSite)}})
	e.do("POST", "/settings/sync", url.Values{"site": {fmt.Sprint(aSite)}})
	_ = e.st.AddCredits(ctx, bID, 1000, "admin", "测试")
	e.do("POST", "/chat", url.Values{"text": {"甲的网站怎么样"}, "scope": {fmt.Sprint(aSite)}})
	if e.count(`SELECT count(*) FROM sites WHERE id=$1 AND name='甲的秘密网站'`, aSite) != 1 {
		t.Error("乙删掉或改掉了甲的网站")
	}
	if e.count(`SELECT count(*) FROM signals WHERE dismissed_at IS NULL AND site_id=$1`, aSite) != 1 || e.count(`SELECT count(*) FROM todos WHERE id=$1 AND status='open'`, todo) != 1 {
		t.Error("乙改动了甲的提醒或待办")
	}
	if e.count(`SELECT count(*) FROM conversations WHERE id=$1`, conv) != 1 {
		t.Error("乙删掉了甲的对话")
	}
	if n := e.count(`SELECT count(*) FROM conversations WHERE user_id=$1 AND site_id=$2`, bID, aSite) +
		e.count(`SELECT count(*) FROM diagnoses WHERE site_id=$1`, aSite) - 1 +
		e.count(`SELECT count(*) FROM reports WHERE user_id=$1`, bID); n != 0 {
		t.Errorf("乙不应该能在甲的网站下建对话、诊断或报告（%d 条）", n)
	}
	// AI 工具：乙的 AI 拿着甲的网站 ID 也查不到
	if err := e.srv.Agent.ToolCheck(store.WithUser(ctx, bID), aSite); err == nil {
		t.Error("AI 工具不应该能查别人的网站")
	}
	if err := e.srv.Agent.ToolCheck(actx, aSite); err != nil {
		t.Errorf("AI 工具应该能查自己的网站: %v", err)
	}
	// 普通用户进不了后台
	if rec := e.do("GET", "/admin", nil); rec.Code != http.StatusNotFound {
		t.Errorf("普通用户打开后台应该 404，得到 %d", rec.Code)
	}
	if rec := e.do("POST", fmt.Sprintf("/admin/users/%d/credits", bID), url.Values{"delta": {"999"}}); rec.Code != http.StatusNotFound {
		t.Errorf("普通用户不能给自己加积分: %d", rec.Code)
	}
}

// TestE2ERegisterResetEmail：注册、重复注册、限额、找回密码、改邮箱。
func TestE2ERegisterResetEmail(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	reg := func(email string, extra ...string) *httptest.ResponseRecorder {
		e.cookie = nil
		f := e.withPow("register", url.Values{"email": {email}})
		for i := 0; i+1 < len(extra); i += 2 {
			f.Set(extra[i], extra[i+1])
		}
		return e.do("POST", "/register", f)
	}
	if rec := reg("someone@nomx.test"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "无法接收邮件") {
		t.Errorf("收不了信的域名应该当场提示: %d", rec.Code)
	}
	if rec := reg("not-an-email"); rec.Code != http.StatusBadRequest {
		t.Errorf("格式不对的邮箱应该拒绝: %d", rec.Code)
	}
	if rec := reg("bot@example.com", trapField, "x"); rec.Code != http.StatusBadRequest {
		t.Errorf("填了陷阱字段应该拒绝: %d", rec.Code)
	}
	if e.count(`SELECT count(*) FROM mail_log`) != 0 || e.count(`SELECT count(*) FROM users WHERE email LIKE '%nomx%' OR email='bot@example.com'`) != 0 {
		t.Error("被拒绝的注册不应该建用户、不应该占发信额度")
	}

	// 新注册：送积分（后台设了 30）
	_ = e.st.SetSetting(ctx, store.KeySignupBonus, "30")
	uid := e.newUser("new@example.com")
	if u, _ := e.st.User(ctx, uid); u.Credits != 30 || u.LastLoginAt == nil {
		t.Errorf("新用户应该有 30 赠送积分、登录时间已记录: %+v", u)
	}
	// 已经登录过的邮箱再注册：响应和正常一样，邮件是“已经注册过”的提醒
	rec := reg("new@example.com")
	m, n := e.lastMail("new@example.com")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "初始密码已经发出") || n != 2 || !strings.Contains(m.Subject, "已经注册过") {
		t.Errorf("重复注册：响应应该一样、只发提醒: code=%d mails=%d subject=%q", rec.Code, n, m.Subject)
	}
	// 同一邮箱每天最多 3 封
	reg("new@example.com")
	if rec := reg("new@example.com"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("同一邮箱第 4 封应该被拦住: %d", rec.Code)
	}

	// 注册了但没登录过：再注册一次换一个新密码，旧的作废
	reg("fresh@example.com")
	m1, _ := e.lastMail("fresh@example.com")
	reg("fresh@example.com")
	m2, _ := e.lastMail("fresh@example.com")
	pw := regexp.MustCompile(`初始密码：(\S+)`)
	p1, p2 := pw.FindStringSubmatch(m1.Body)[1], pw.FindStringSubmatch(m2.Body)[1]
	if p1 == p2 {
		t.Fatal("没登录过的账号再次注册应该换新密码")
	}
	e.cookie = nil
	if rec := e.do("POST", "/login", e.withPow("login", url.Values{"email": {"fresh@example.com"}, "password": {p1}})); rec.Code != http.StatusUnauthorized {
		t.Errorf("旧的初始密码应该作废: %d", rec.Code)
	}
	e.loginAs("fresh@example.com", p2)

	// 找回密码：不存在的邮箱和存在的邮箱响应一样，都计数
	e.cookie = nil
	ghost := e.do("POST", "/forgot", e.withPow("forgot", url.Values{"email": {"ghost@example.com"}}))
	real := e.do("POST", "/forgot", e.withPow("forgot", url.Values{"email": {"new@example.com"}}))
	if ghost.Code != 200 || real.Code != 200 || !strings.Contains(ghost.Body.String(), "如果 ghost@example.com 注册过") {
		t.Errorf("找回密码不应该暴露邮箱是否存在: %d %d", ghost.Code, real.Code)
	}
	if _, n := e.lastMail("ghost@example.com"); n != 0 || e.count(`SELECT count(*) FROM mail_log WHERE kind='reset' AND email='ghost@example.com'`) != 1 {
		t.Error("不存在的邮箱不发信，但要计数")
	}
	if rec := e.do("POST", "/forgot", e.withPow("forgot", url.Values{"email": {"ghost@example.com"}})); rec.Code != http.StatusTooManyRequests {
		t.Errorf("同一邮箱 1 分钟内第二次应该被拦住（不管存不存在）: %d", rec.Code)
	}
	m, _ = e.lastMail("new@example.com")
	link := regexp.MustCompile(`http://pingoo\.test(/reset\?t=\S+)`).FindStringSubmatch(m.ActionURL)
	if link == nil {
		t.Fatalf("重置邮件里没有链接: %q", m.Text())
	}
	if body := e.do("GET", link[1], nil).Body.String(); strings.Contains(body, "已经失效") {
		t.Fatal("新的重置链接不应该失效")
	}
	tok, _ := url.QueryUnescape(strings.TrimPrefix(link[1], "/reset?t="))
	if rec := e.do("POST", "/reset", url.Values{"t": {tok}, "password": {"short"}, "password2": {"short"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("太短的密码应该拒绝: %d", rec.Code)
	}
	if rec := e.do("POST", "/reset", url.Values{"t": {tok}, "password": {"brand-new-pass"}, "password2": {"brand-new-pass"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("重置密码失败: %d", rec.Code)
	}
	if body := e.do("POST", "/reset", url.Values{"t": {tok}, "password": {"another-pass"}, "password2": {"another-pass"}}).Body.String(); !strings.Contains(body, "已经失效") {
		t.Error("重置链接只能用一次")
	}
	e.loginAs("new@example.com", "brand-new-pass")

	// 改登录邮箱：要原密码，验证码发到新邮箱，旧邮箱收到通知
	if rec := e.do("POST", "/settings/email/code", url.Values{"email": {"renamed@example.com"}, "password": {"wrong"}}); !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("密码不对")) {
		t.Error("没有原密码不能发验证码")
	}
	e.do("POST", "/settings/email/code", url.Values{"email": {"renamed@example.com"}, "password": {"brand-new-pass"}})
	cm, _ := e.lastMail("renamed@example.com")
	code := regexp.MustCompile(`\b(\d{6})\b`).FindStringSubmatch(cm.Body)
	if code == nil {
		t.Fatalf("验证码邮件里没有 6 位数字: %q", cm.Body)
	}
	if rec := e.do("POST", "/settings/email/code", url.Values{"email": {"renamed@example.com"}, "password": {"brand-new-pass"}}); !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("1 分钟")) {
		t.Error("1 分钟内第二次发验证码应该被拦住")
	}
	e.do("POST", "/settings/email", url.Values{"email": {"renamed@example.com"}, "code": {"000000"}})
	e.do("POST", "/settings/email", url.Values{"email": {"renamed@example.com"}, "code": {code[1]}})
	if u, _ := e.st.User(ctx, uid); u.Email != "renamed@example.com" {
		t.Errorf("邮箱没有改成功: %s", u.Email)
	}
}

// TestE2EBilling：没积分不能用 AI；卡密充值；按用量扣积分；失败全额退回；后台调整和停用。
func TestE2EBilling(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	uid := e.newUser("payer@example.com")
	credits := func() int { return e.count(`SELECT credits FROM users WHERE id=$1`, uid) }

	calls := e.aiCalls.Load()
	rec := e.do("POST", "/chat", url.Values{"text": {"你好"}})
	if !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("积分不足")) || e.aiCalls.Load() != calls || e.count(`SELECT count(*) FROM conversations WHERE user_id=$1`, uid) != 0 {
		t.Errorf("没有积分时问答应该直接提示、不调用 AI: %q", rec.Header().Get("HX-Trigger"))
	}

	// 管理员生成卡密（直接下载 CSV），用户兑换
	userCookie := e.cookie
	e.login()
	rec = e.do("POST", "/admin/codes", url.Values{"count": {"2"}, "credits": {"50"}, "batch": {"测试批次"}})
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rec.Body.String(), "\xEF\xBB\xBF"))).ReadAll()
	if err != nil || len(rows) != 3 || rows[1][1] != "50 积分" {
		t.Fatalf("卡密 CSV 不对: %v %v", rows, err)
	}
	if e.count(`SELECT count(*) FROM redeem_codes WHERE code_hash=$1`, rows[1][0]) != 0 {
		t.Error("数据库里不应该存卡密原文")
	}
	// 积分卡密购买链接：不是 http(s) 的不保存；填了之后设置页和积分明细页都有“购买积分卡密”
	e.do("POST", "/admin/settings", url.Values{"credits_buy_url": {"javascript:alert(1)"}})
	if e.st.SettingOr(ctx, store.KeyCreditsBuy, "") != "" {
		t.Error("不是 http(s) 的购买链接不应该保存")
	}
	e.do("POST", "/admin/settings", url.Values{"credits_buy_url": {"https://shop.example.com/credits"}})
	e.cookie = userCookie
	for _, path := range []string{"/settings", "/credits"} {
		if body := e.do("GET", path, nil).Body.String(); !strings.Contains(body, `href="https://shop.example.com/credits"`) {
			t.Errorf("%s 应该有积分卡密的购买链接", path)
		}
	}
	code := strings.ToLower(strings.ReplaceAll(rows[1][0], "-", " "))
	e.do("POST", "/settings/redeem", url.Values{"code": {code}})
	if credits() != 50 {
		t.Fatalf("兑换后应该有 50 积分，实际 %d", credits())
	}
	if rec := e.do("POST", "/settings/redeem", url.Values{"code": {rows[1][0]}}); !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("已经用过")) || credits() != 50 {
		t.Error("同一张卡密不能兑换两次")
	}

	// 问答按用量扣 2 积分，流水里有一条已结算的记录
	e.do("POST", "/chat", url.Values{"text": {"最近怎么样"}})
	var msgID int64
	_ = e.st.Pool.QueryRow(ctx, `SELECT m.id FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE c.user_id=$1 AND m.role='assistant' ORDER BY m.id DESC LIMIT 1`, uid).Scan(&msgID)
	if msg := e.waitMessage(msgID); msg.Status != "done" {
		t.Fatalf("问答应该成功: %+v", msg)
	}
	if credits() != 48 || e.count(`SELECT count(*) FROM ledger WHERE user_id=$1 AND kind='chat' AND credits=-2 AND NOT pending AND prompt_tokens=10000`, uid) != 1 {
		t.Errorf("应该扣 2 积分并记一条流水，余额 %d", credits())
	}

	// 接口没返回用量：按字数估算后照样扣（至少 1 积分），流水里注明是估算的
	e.noUsage.Store(true)
	e.do("POST", "/chat", url.Values{"text": {"换个问法"}})
	_ = e.st.Pool.QueryRow(ctx, `SELECT m.id FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE c.user_id=$1 AND m.role='assistant' ORDER BY m.id DESC LIMIT 1`, uid).Scan(&msgID)
	if msg := e.waitMessage(msgID); msg.Status != "done" {
		t.Fatalf("问答应该成功: %+v", msg)
	}
	spent := 48 - credits()
	if spent < 1 || e.count(`SELECT count(*) FROM ledger WHERE user_id=$1 AND kind='chat' AND credits=$2 AND prompt_tokens>0 AND completion_tokens>0 AND note='接口没有返回用量，按字数估算'`, uid, -spent) != 1 {
		t.Errorf("没有用量时应该按字数估算扣费并注明，扣了 %d", spent)
	}
	e.noUsage.Store(false)
	_ = e.st.AddCredits(ctx, uid, int64(spent), "admin", "测试") // 补回 48，下面的数字照旧

	// 实际用量超过预扣：按实际扣（100 万 + 10 万 tokens = 1.2 元 = 120 积分，预扣只有 14），余额变负，之后不能再用 AI
	e.bigUse.Store(true)
	e.do("POST", "/chat", url.Values{"text": {"写一份很长的分析"}})
	_ = e.st.Pool.QueryRow(ctx, `SELECT m.id FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE c.user_id=$1 AND m.role='assistant' ORDER BY m.id DESC LIMIT 1`, uid).Scan(&msgID)
	if msg := e.waitMessage(msgID); msg.Status != "done" {
		t.Fatalf("问答应该成功: %+v", msg)
	}
	e.bigUse.Store(false)
	if credits() != 48-120 || e.count(`SELECT count(*) FROM ledger WHERE user_id=$1 AND kind='chat' AND credits=-120 AND note LIKE '%超过预扣 106 积分%'`, uid) != 1 {
		t.Errorf("超过预扣时应该按实际扣 120 积分并注明，余额 %d", credits())
	}
	calls = e.aiCalls.Load()
	if rec := e.do("POST", "/chat", url.Values{"text": {"再来"}}); !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("积分不足")) || e.aiCalls.Load() != calls {
		t.Error("余额为负时不能再用 AI")
	}
	_ = e.st.AddCredits(ctx, uid, -10, "admin", "测试")
	if credits() != 48-120 {
		t.Errorf("余额已经是负数时，后台扣减不能再扣，也不能反而加上: %d", credits())
	}
	_ = e.st.AddCredits(ctx, uid, 120, "admin", "测试") // 充值先抵扣欠的，回到 48
	if credits() != 48 {
		t.Fatalf("充值后应该先抵扣: %d", credits())
	}

	// AI 出错：全额退回
	e.aiFail.Store(true)
	e.do("POST", "/chat", url.Values{"text": {"再问一次"}})
	_ = e.st.Pool.QueryRow(ctx, `SELECT m.id FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE c.user_id=$1 AND m.role='assistant' ORDER BY m.id DESC LIMIT 1`, uid).Scan(&msgID)
	if msg := e.waitMessage(msgID); msg.Status != "error" {
		t.Fatalf("AI 出错时问答应该失败: %+v", msg)
	}
	if credits() != 48 || e.count(`SELECT count(*) FROM ledger WHERE user_id=$1 AND pending`, uid) != 0 {
		t.Errorf("失败的任务应该全额退回，余额 %d", credits())
	}
	e.aiFail.Store(false)

	// 预扣中进程退出：启动时全额退回
	if _, err := e.st.Reserve(ctx, uid, "chat", "假模型", 10); err != nil || credits() != 38 {
		t.Fatalf("预扣失败: %v %d", err, credits())
	}
	if n := e.st.RefundPending(ctx); n != 1 || credits() != 48 {
		t.Errorf("重启时应该退回没结算的预扣: n=%d 余额 %d", n, credits())
	}

	// 后台：扣减不会扣成负数；停用后登录失效
	e.cookie = nil
	e.login()
	e.do("POST", fmt.Sprintf("/admin/users/%d/credits", uid), url.Values{"delta": {"-1000"}})
	if credits() != 0 || e.count(`SELECT count(*) FROM ledger WHERE user_id=$1 AND kind='admin' AND credits=-48`, uid) != 1 {
		t.Errorf("扣减不能扣成负数，流水记实际扣掉的数: %d", credits())
	}
	e.do("POST", fmt.Sprintf("/admin/users/%d/disable", uid), url.Values{})
	e.cookie = userCookie
	if rec := e.do("GET", "/settings", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("停用后原来的登录应该失效: %d", rec.Code)
	}

	// 后台添加模型：用户可以选，选的模型被删掉后自动改用默认模型
	e.cookie = nil
	e.login()
	e.do("POST", "/admin/models/0", url.Values{"name": {"贵模型"}, "model": {"pricey"}, "base_url": {"https://example.com/v1"}, "api_key": {"k2"},
		"price_in": {"10"}, "price_out": {"20"}, "price_cache": {"1"}, "multiplier": {"2"}, "enabled": {"on"},
		"thinking_style": {"effort"}, "effort_diagnose": {"high"}, "effort_chat": {"乱写"}})
	ms, _ := e.st.Models(ctx, true)
	if len(ms) != 2 {
		t.Fatalf("应该有两个模型: %+v", ms)
	}
	var pricey store.AIModel
	for _, m := range ms {
		if m.Name == "贵模型" {
			pricey = m
		}
	}
	// 思考强度按任务设：选了的存下来，没选的（和乱写的）用默认值；编辑表单里能看到
	if pricey.ThinkingStyle != "effort" || pricey.TaskEffort != `{"diagnose":"high"}` || pricey.Effort("chat") != "low" ||
		pricey.Thinking("diagnose")["reasoning_effort"] != "high" || pricey.Thinking("insight")["reasoning_effort"] != "low" {
		t.Errorf("思考强度没存对: style=%q effort=%q", pricey.ThinkingStyle, pricey.TaskEffort)
	}
	if body := e.do("GET", fmt.Sprintf("/admin/models/%d", pricey.ID), nil).Body.String(); !strings.Contains(body, `name="effort_diagnose"`) ||
		!strings.Contains(body, `<option value="high" selected>多想</option>`) || !strings.Contains(body, `<option value="effort" selected>`) ||
		!strings.Contains(body, "默认（不思考）") {
		t.Errorf("模型表单里应该有思考强度的设置: %s", body)
	}
	// 积分明细里标出命中缓存的 tokens
	if _, err := e.st.Pool.Exec(ctx, `INSERT INTO ledger(user_id,kind,model,prompt_tokens,completion_tokens,cached_tokens,credits) VALUES(1,'chat','假模型',52000,8000,41000,-3)`); err != nil {
		t.Fatal(err)
	}
	if body := e.do("GET", "/credits", nil).Body.String(); !strings.Contains(body, "60K") || !strings.Contains(body, "其中 41K 命中缓存") {
		t.Errorf("积分明细应该标出命中缓存的 tokens: %s", body)
	}
	if pricey.APIKey != "k2" || e.count(`SELECT count(*) FROM ai_models WHERE api_key='k2'`) != 0 {
		t.Error("模型的 API Key 应该加密保存")
	}
	if got := pricey.Cost(1000000, 1000000, 500000); got != (500000*10+500000*1+1000000*20)*2*100/1000000 {
		t.Errorf("计费公式不对: %d", got)
	}
	e.do("POST", "/settings/ai", url.Values{"model": {fmt.Sprint(pricey.ID)}})
	if m, _ := e.st.ModelFor(ctx, 1); m.ID != pricey.ID {
		t.Error("用户选的模型没有生效")
	}
	e.do("POST", fmt.Sprintf("/admin/models/%d/delete", pricey.ID), url.Values{})
	if m, _ := e.st.ModelFor(ctx, 1); m.Name != "假模型" {
		t.Errorf("选的模型被删掉后应该改用默认模型: %s", m.Name)
	}
}

// TestE2EFeedback：登录用户的反馈发到管理员邮箱（回复地址是用户），每天有上限；没登录不能提交。
func TestE2EFeedback(t *testing.T) {
	e := newE2E(t)
	if rec := e.do("POST", "/feedback", url.Values{"topic": {"使用问题"}, "text": {"没登录也想反馈"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("没登录不能提交反馈: %d", rec.Code)
	}
	uid := e.newUser("fb@example.com")
	if body := e.do("GET", "/help", nil).Body.String(); !strings.Contains(body, `hx-post="/feedback"`) {
		t.Fatal("登录后帮助页应该有反馈表单")
	}
	if rec := e.do("POST", "/feedback", url.Values{"topic": {"使用问题"}, "text": {"短"}}); !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("请写")) {
		t.Error("太短的反馈应该提示")
	}
	rec := e.do("POST", "/feedback", url.Values{"topic": {"<script>"}, "text": {"导入网站时 Search Console 没配上"}})
	m, n := e.lastMail(adminEmail)
	if rec.Code != 200 || n != 1 || m.Kind != "feedback" || m.ReplyTo != "fb@example.com" || !strings.Contains(m.Body, "导入网站时") ||
		!strings.Contains(m.Subject, "其他") || m.UserID != uid {
		t.Fatalf("反馈应该发到管理员邮箱、回复地址是用户、未知类型归到“其他”: code=%d n=%d %+v", rec.Code, n, m)
	}
	for i := 1; i < feedbackPerDay; i++ {
		e.do("POST", "/feedback", url.Values{"topic": {"功能建议"}, "text": {fmt.Sprintf("第 %d 条建议", i)}})
	}
	if rec := e.do("POST", "/feedback", url.Values{"topic": {"功能建议"}, "text": {"再来一条建议"}}); !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("明天")) {
		t.Error("超过每天上限应该提示")
	}
	if _, n := e.lastMail(adminEmail); n != feedbackPerDay {
		t.Errorf("应该一共发出 %d 封反馈: %d", feedbackPerDay, n)
	}
}

// TestE2EErrorPages：不存在的地址给友好的 404 页（没登录也不跳登录页）；htmx 请求只要一行文字；
// 普通用户打开后台、打开别人的网站，看到的和地址不存在一模一样。
func TestE2EErrorPages(t *testing.T) {
	e := newE2E(t)
	rec := e.do("GET", "/no-such-page", nil)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "页面不存在") || !strings.Contains(rec.Body.String(), `href="/">回到首页`) {
		t.Fatalf("没登录访问不存在的地址应该是 404 页: %d %.200s", rec.Code, rec.Body.String())
	}
	if rec := e.do("GET", "/settings", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("存在但要登录的页面仍然跳登录页: %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "http://pingoo.test/no-such-part", nil)
	req.Header.Set("HX-Request", "true")
	hx := httptest.NewRecorder()
	e.h.ServeHTTP(hx, req)
	if hx.Code != http.StatusNotFound || strings.Contains(hx.Body.String(), "<html") {
		t.Errorf("htmx 请求不存在的地址应该只返回一行文字: %d %.100s", hx.Code, hx.Body.String())
	}

	e.login()
	adminSite, _ := e.st.CreateSite(store.WithUser(context.Background(), 1), store.Site{Name: "管理员的网站", GAProperty: "properties/9", Timezone: "UTC"})
	missing := e.do("GET", "/no-such-page", nil).Body.String()
	if !strings.Contains(missing, `href="/overview">回到概览`) {
		t.Error("登录后的 404 页应该能回到概览")
	}

	e.newUser("other@example.com")
	pages := map[string]*httptest.ResponseRecorder{
		"后台":     e.do("GET", "/admin", nil),
		"别人的网站":  e.do("GET", fmt.Sprintf("/sites/%d", adminSite), nil),
		"不存在的地址": e.do("GET", "/no-such-page", nil),
	}
	for name, rec := range pages {
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "页面不存在") {
			t.Errorf("%s 应该是同样的 404 页: %d", name, rec.Code)
		}
	}
	if rec := e.do("POST", "/admin/settings", url.Values{"signup_bonus": {"999"}}); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("普通用户提交后台表单应该 404 且只有一行文字: %d", rec.Code)
	}

	// 500：浏览器打开的页面给出错页，不显示内部错误；htmx 请求保留原因，弹在提示里
	boom := errors.New("ERROR: relation secret_table does not exist")
	page := httptest.NewRecorder()
	e.srv.fail(page, httptest.NewRequest("GET", "/reports", nil), boom)
	if page.Code != 500 || !strings.Contains(page.Body.String(), "告诉我们发生了什么") || strings.Contains(page.Body.String(), "secret_table") {
		t.Errorf("出错页不应该显示内部错误: %d", page.Code)
	}
	hxReq := httptest.NewRequest("POST", "/reports", nil)
	hxReq.Header.Set("HX-Request", "true")
	part := httptest.NewRecorder()
	e.srv.fail(part, hxReq, boom)
	if part.Code != 500 || strings.Contains(part.Body.String(), "<html") {
		t.Errorf("htmx 请求出错应该只返回一行文字: %d", part.Code)
	}
}

// TestE2EAbuseLimits：会造成损失的刷量入口都有上限。
func TestE2EAbuseLimits(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	mails := func() int { e.mu.Lock(); defer e.mu.Unlock(); return len(e.mails) }

	// 找回密码：同一来源换着邮箱填，每天最多 10 次（不然能给所有用户发信、耗光发信额度）
	e.ip = "203.0.113.9:1000"
	for i := 0; i < resetPerIPDay; i++ {
		e.do("POST", "/forgot", e.withPow("forgot", url.Values{"email": {fmt.Sprintf("r%d@example.com", i)}}))
	}
	if rec := e.do("POST", "/forgot", e.withPow("forgot", url.Values{"email": {"r99@example.com"}})); rec.Code != http.StatusTooManyRequests {
		t.Errorf("同一来源第 11 次找回密码应该被拒绝: %d", rec.Code)
	}

	// IPv6：同一个 /64 里换地址不能绕过按 IP 的注册限额
	for i := 0; i < registerPerIPDay; i++ {
		e.ip = fmt.Sprintf("[2001:db8:1:2::%x]:1000", i+1)
		e.do("POST", "/register", e.withPow("register", url.Values{"email": {fmt.Sprintf("v6-%d@example.com", i)}}))
	}
	e.ip = "[2001:db8:1:2:ffff:ffff:ffff:ffff]:1000"
	if rec := e.do("POST", "/register", e.withPow("register", url.Values{"email": {"v6-last@example.com"}})); rec.Code != http.StatusTooManyRequests {
		t.Errorf("同一个 /64 的第 11 次注册应该被拒绝: %d", rec.Code)
	}
	e.ip = "[2001:db8:1:3::1]:1000"
	if rec := e.do("POST", "/register", e.withPow("register", url.Values{"email": {"v6-other@example.com"}})); rec.Code != 200 {
		t.Errorf("别的 /64 不受影响: %d", rec.Code)
	}
	e.ip = ""

	// 改邮箱验证码：同一个新邮箱每天最多收 3 封（多个账号轮流轰炸同一个邮箱也不行）
	e.login()
	e.exec(`INSERT INTO mail_log(kind,email,ip,user_id) SELECT 'email_code','victim@example.com','x',NULL FROM generate_series(1,$1)`, codePerEmailDay)
	rec := e.do("POST", "/settings/email/code", url.Values{"email": {"victim@example.com"}, "password": {adminPass}})
	if !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("收过好几次")) {
		t.Errorf("同一个邮箱收验证码有上限: %q", rec.Header().Get("HX-Trigger"))
	}

	// 卡密输错：每小时 10 次，记录在数据库里（服务重启不会清零）
	for i := 0; i < redeemFailsPerHour; i++ {
		e.do("POST", "/settings/redeem", url.Values{"code": {fmt.Sprintf("PGO-AAAA-BBBB-CCCC-%04d", i)}})
	}
	if v, _ := e.st.Setting(store.WithUser(ctx, 1), store.KeyRedeemFails); len(strings.Split(v, ",")) != redeemFailsPerHour {
		t.Errorf("输错的记录应该存在数据库里: %q", v)
	}
	if rec := e.do("POST", "/settings/redeem", url.Values{"code": {"PGO-AAAA-BBBB-CCCC-9999"}}); !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("输错太多次")) {
		t.Error("一小时内输错 10 次后应该暂停兑换")
	}

	// 全站 24 小时发信硬上限：所有邮件都受它管，到了之后一封都不发
	_ = e.st.SetSetting(ctx, store.KeyMailLimit, fmt.Sprint(e.count(`SELECT count(*) FROM mail_log WHERE created_at > now() - interval '24 hours'`)))
	before := mails()
	rec = e.do("POST", "/feedback", url.Values{"topic": {"其他"}, "text": {"额度用完以后还能发吗"}})
	if mails() != before || !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("没能提交")) {
		t.Errorf("到了发信上限不应该再发任何邮件: %d → %d", before, mails())
	}
	_ = e.st.SetSetting(ctx, store.KeyMailLimit, "")

	// 问题太长：不调用 AI
	calls := e.aiCalls.Load()
	rec = e.do("POST", "/chat", url.Values{"text": {strings.Repeat("问", maxQuestion+1)}})
	if !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("问题太长")) || e.aiCalls.Load() != calls {
		t.Error("超长的问题应该直接拒绝")
	}

	// 演示账号：没有设置页（里面有其他访客的 IP、会调 Google 接口）；已经在演示里不再建会话；同一来源每小时最多进 20 次
	if err := demo.Seed(ctx, e.st, e.srv.Detector); err != nil {
		t.Fatal(err)
	}
	e.cookie = nil
	e.ip = "192.0.2.50:1000"
	rec = e.do("POST", "/demo", url.Values{})
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			e.cookie = c
		}
	}
	for _, p := range []string{"/settings", "/settings/import"} {
		if rec := e.do("GET", p, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != homeApp {
			t.Errorf("演示账号打开 %s 应该回到概览: %d %q", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	sessions := e.count(`SELECT count(*) FROM sessions`)
	if rec := e.do("POST", "/demo", url.Values{}); rec.Header().Get("Location") != homeApp || e.count(`SELECT count(*) FROM sessions`) != sessions {
		t.Error("已经在演示里时再点“看演示”不应该再建会话")
	}
	e.cookie = nil
	for i := 1; i < demoPerIPHour; i++ {
		e.do("POST", "/demo", url.Values{})
	}
	if rec := e.do("POST", "/demo", url.Values{}); rec.Code != http.StatusTooManyRequests {
		t.Errorf("同一来源一小时内第 21 次进演示应该被拒绝: %d", rec.Code)
	}
}

// TestE2EPlans：免费版的限制（1 个网站、不能比去年同期）；后台生成套餐卡密，用户兑换后升级；后台改套餐数值、直接设置用户套餐。
func TestE2EPlans(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	if body := e.do("GET", "/", nil).Body.String(); strings.Count(body, `class="price-card`) != 3 || !strings.Contains(body, "注册后升级") || strings.Contains(body, "看演示") {
		t.Error("首页价格区应该有三张卡片；没设购买链接时按钮是“注册后升级”；没有演示数据时不显示“看演示”")
	}
	if body := e.do("GET", "/help", nil).Body.String(); strings.Contains(body, "price-card") {
		t.Error("帮助页不放价格卡片")
	}
	cfg0 := e.st.Plans(ctx)
	basic := cfg0.Plans["basic"]
	basic.BuyURL = "https://shop.example.com/basic"
	cfg0.Plans["basic"] = basic
	if err := e.st.SavePlans(ctx, cfg0); err != nil {
		t.Fatal(err)
	}
	if body := e.do("GET", "/", nil).Body.String(); !strings.Contains(body, `href="https://shop.example.com/basic"`) {
		t.Error("设了购买链接后，对应那一档的卡片按钮应该链到它")
	}
	if err := demo.Seed(ctx, e.st, e.srv.Detector); err != nil {
		t.Fatal(err)
	}
	if body := e.do("GET", "/help", nil).Body.String(); !strings.Contains(body, `action="/demo" class="nav-demo"`) {
		t.Error("有演示数据时，没登录的访客在顶栏能看到“看演示”")
	}
	uid := e.newUser("plan@example.com")
	userCookie := e.cookie
	uctx := store.WithUser(ctx, uid)
	if _, err := e.st.CreateSite(uctx, store.Site{Name: "一号", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do("POST", "/settings/sites/0", url.Values{"name": {"二号"}, "timezone": {"UTC"}}); !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("最多 1 个网站")) {
		t.Errorf("免费版只能有 1 个网站: %q", rec.Header().Get("HX-Trigger"))
	}
	if body := e.do("GET", "/overview?cmp=yoy", nil).Body.String(); !strings.Contains(body, "比去年同期 🔒") {
		t.Error("免费版的“比去年同期”应该是锁着的")
	}
	if body := e.do("GET", "/settings", nil).Body.String(); !strings.Contains(body, "当前套餐</span> <b>免费版</b>") || strings.Contains(body, `name="digest"`) {
		t.Error("设置页应该显示免费版，没有邮件简报开关")
	}

	// 管理员生成 30 天专业版卡密
	e.login()
	rec := e.do("POST", "/admin/codes", url.Values{"kind": {"pro"}, "count": {"1"}, "plan_days": {"30"}, "batch": {"专业月卡"}})
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rec.Body.String(), "\xEF\xBB\xBF"))).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][1] != "专业版 30 天" {
		t.Fatalf("套餐卡密 CSV 不对: %v %v", rows, err)
	}
	e.cookie = userCookie
	rec = e.do("POST", "/settings/redeem", url.Values{"code": {rows[1][0]}})
	if !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("已开通专业版")) {
		t.Fatalf("兑换套餐卡密应该提示开通: %q", rec.Header().Get("HX-Trigger"))
	}
	if p := e.st.PlanOf(ctx, uid); p.ID != store.PlanPro || e.count(`SELECT credits FROM users WHERE id=$1`, uid) != 2000 {
		t.Errorf("兑换后应该是专业版、送 2000 积分: %s", p.ID)
	}
	if rec := e.do("POST", "/settings/sites/0", url.Values{"name": {"二号"}, "timezone": {"UTC"}}); strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("最多")) {
		t.Error("专业版可以添加更多网站")
	}
	if body := e.do("GET", "/overview?cmp=yoy", nil).Body.String(); strings.Contains(body, "比去年同期 🔒") {
		t.Error("专业版近 7 天可以和去年同期比")
	}
	body := e.do("GET", "/settings", nil).Body.String()
	if !strings.Contains(body, "<b>专业版</b>") || !strings.Contains(body, `name="digest" checked`) {
		t.Error("设置页应该显示专业版和打开的邮件简报开关")
	}
	e.do("POST", "/settings/reports", url.Values{"digest_form": {"1"}, "weekly": {"on"}})
	if e.st.SettingOr(uctx, store.KeyDigestOff, "") != "true" {
		t.Error("取消勾选应该关掉邮件简报")
	}

	// 后台：改套餐数值；直接把用户设成免费版
	e.login()
	e.do("POST", "/admin/plans", url.Values{"currency": {"€"}, "basic_price": {"9"}, "basic_sites": {"3"}, "pro_price": {"29"}})
	cfg := e.st.Plans(ctx)
	if cfg.Currency != "€" || cfg.Get(store.PlanBasic).Sites != 3 || cfg.Get(store.PlanPro).Price != 29 || cfg.Get(store.PlanFree).DetailDays != 30 {
		t.Errorf("套餐设置没存好: %+v", cfg)
	}
	if rec := e.do("POST", "/admin/plans", url.Values{"free_total_days": {"10"}}); !strings.Contains(rec.Header().Get("HX-Trigger"), asciiJSON("不能少于明细")) {
		t.Error("每日总数比明细短应该拒绝")
	}
	e.do("POST", fmt.Sprintf("/admin/users/%d/plan", uid), url.Values{"plan": {"free"}})
	if e.st.PlanOf(ctx, uid).ID != store.PlanFree {
		t.Error("后台设成免费版应该立即生效")
	}
	e.cookie = userCookie
	if body := e.do("GET", "/settings", nil).Body.String(); !strings.Contains(body, "已暂停：超出套餐的网站数") {
		t.Error("降回免费版后，第二个网站应该显示暂停")
	}
}

// 还没有广告数据的网站：已关联 AdSense 时说明在等数据；没关联的，只在用户其他网站用着 AdSense 时才提醒。
func TestE2EAdSenseNotes(t *testing.T) {
	e := newE2E(t)
	e.login()
	uctx := store.WithUser(context.Background(), 1)
	a, _ := e.st.CreateSite(uctx, store.Site{Name: "甲", GAProperty: "properties/1", Timezone: "UTC"})
	b, _ := e.st.CreateSite(uctx, store.Site{Name: "乙", GAProperty: "properties/2", Timezone: "UTC"})
	page := func(id int64) string { return e.do("GET", fmt.Sprintf("/sites/%d", id), nil).Body.String() }
	if strings.Contains(page(a), `id="ads"`) {
		t.Error("还没查过关联状态时不显示广告一节")
	}
	e.st.SetAdSenseStatus(context.Background(), b, false, time.Hour)
	if strings.Contains(page(b), "单独关联") {
		t.Error("用户的其他网站都没用 AdSense 时，没关联的网站不用提醒")
	}
	e.st.SetAdSenseStatus(context.Background(), a, true, time.Hour)
	if !strings.Contains(page(a), "正在等数据") {
		t.Error("已关联、还没数据的网站应该说明在等数据")
	}
	if !strings.Contains(page(b), "单独关联一次") {
		t.Error("其他网站关联了 AdSense、这个没关联时应该提醒")
	}
	help := e.do("GET", "/help", nil).Body.String()
	for _, want := range []string{"不是 <b>Google Ads</b>", "都要单独关联一次", "正在等数据", "同一个 GA4 媒体资源"} {
		if !strings.Contains(help, want) {
			t.Errorf("帮助页的 AdSense 一节应该写到：%s", want)
		}
	}
}

// 点“重新生成”简报：开始了就马上进入“正在生成”（并且之后的轮询也认得出它在生成），开始不了要弹出原因。
// 以前概览判断“在不在生成”用的标记和生成时用的不一样，正在生成的简报会显示成“被中断了”，点重新生成也像没反应。
func TestE2EInsightRegenShowsProgress(t *testing.T) {
	e := newE2E(t)
	e.login()
	uctx := store.WithUser(context.Background(), 1)
	if _, err := e.st.CreateSite(uctx, store.Site{Name: "甲", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	rg := period.Resolve("d7", time.Now(), time.UTC)
	if _, err := e.st.CreateInsight(uctx, nil, "d7", rg.Start, rg.End); err != nil { // 一条停在“生成中”的旧记录（服务重启打断的）
		t.Fatal(err)
	}
	if body := e.do("GET", "/part/overview-insight?range=d7", nil).Body.String(); !strings.Contains(body, "没有完成") {
		t.Fatalf("被打断的简报应该提示可以重新生成：%.300s", body)
	}

	e.aiDelay.Store(1500)
	rec := e.do("POST", "/part/overview-insight?range=d7", url.Values{})
	if body := rec.Body.String(); !strings.Contains(body, "AI 正在查数据") || !strings.Contains(body, `hx-trigger="every 3s"`) {
		t.Fatalf("点重新生成后应该马上显示“正在生成”并开始轮询：%.300s", body)
	}
	if body := e.do("GET", "/part/overview-insight?range=d7", nil).Body.String(); !strings.Contains(body, "AI 正在查数据") {
		t.Fatalf("生成期间轮询也要显示“正在生成”，不能当成被中断：%.300s", body)
	}
	key := agent.InsightKey(1, nil, "d7")
	for i := 0; i < 200 && e.srv.Agent.Busy(key); i++ {
		time.Sleep(50 * time.Millisecond)
	}

	// 开始不了：积分不够时弹出原因
	e.exec(`UPDATE users SET credits=0 WHERE id=1`)
	rec = e.do("POST", "/part/overview-insight?range=d7", url.Values{})
	if trig := rec.Header().Get("HX-Trigger"); !strings.Contains(trig, "\\u79ef\\u5206") && !strings.Contains(trig, "积分") {
		t.Errorf("开始不了时应该弹出原因（积分不足），实际提示头：%q", trig)
	}
}

// 诊断还没出结果就关掉窗口：页面会调 /stop，后台要马上停下来，不再接着跑完、照常扣积分。
func TestE2EDiagnosisStop(t *testing.T) {
	e := newE2E(t)
	e.login()
	uctx := store.WithUser(context.Background(), 1)
	sid, err := e.st.CreateSite(uctx, store.Site{Name: "甲", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	e.aiDelay.Store(4000) // AI 要 4 秒才回答：停止必须不等它
	body := e.do("POST", fmt.Sprintf("/sites/%d/diagnose", sid), url.Values{"title": {"访客突然下降"}}).Body.String()
	m := regexp.MustCompile(`data-running="(\d+)"`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "停止并关闭") || !strings.Contains(body, "关闭窗口会停止这次诊断") {
		t.Fatalf("进行中的诊断应该带上 data-running，并提示关闭会停止：%.400s", body)
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	began := time.Now()
	if rec := e.do("POST", fmt.Sprintf("/diagnoses/%d/stop", id), url.Values{}); rec.Code != http.StatusNoContent {
		t.Fatalf("停止诊断应该返回 204，实际 %d", rec.Code)
	}
	var d store.Diagnosis
	for i := 0; i < 60; i++ {
		if d, err = e.st.Diagnosis(uctx, id); err != nil {
			t.Fatal(err)
		}
		if d.Status != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if d.Status != "error" || d.Error != agent.StoppedMsg {
		t.Fatalf("停止后诊断应该标成已停止，实际 status=%q error=%q", d.Status, d.Error)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Errorf("停止应该马上生效，不用等 AI 答完，实际用了 %v", took)
	}
	// 一轮都没跑完，用量是 0：预扣的积分全部退回，账上不留“处理中”
	if n := e.count(`SELECT count(*) FROM ledger WHERE kind='diagnose' AND (pending OR credits<>0)`); n != 0 {
		t.Errorf("刚开始就停的诊断不应该扣积分，也不应该还挂着预扣（%d 条）", n)
	}
	if n := e.count(`SELECT count(*) FROM ledger WHERE kind='diagnose' AND note LIKE '%中途停止%'`); n != 1 {
		t.Errorf("流水里应该注明是中途停止的（%d 条）", n)
	}
	if body := e.do("GET", fmt.Sprintf("/diagnoses/%d", id), nil).Body.String(); !strings.Contains(body, agent.StoppedMsg) || strings.Contains(body, "data-running") {
		t.Errorf("停止后不应该再轮询：%.300s", body)
	}

	// 已经结束的诊断，再调停止什么也不改
	e.aiDelay.Store(0)
	body = e.do("POST", fmt.Sprintf("/sites/%d/diagnose", sid), url.Values{"title": {"再来一次"}}).Body.String()
	if m = regexp.MustCompile(`data-running="(\d+)"`).FindStringSubmatch(body); m == nil {
		t.Fatalf("第二次诊断没有开始：%.300s", body)
	}
	id2, _ := strconv.ParseInt(m[1], 10, 64)
	for i := 0; i < 100; i++ {
		if d, _ = e.st.Diagnosis(uctx, id2); d.Status != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	done := d.Status
	if rec := e.do("POST", fmt.Sprintf("/diagnoses/%d/stop", id2), url.Values{}); rec.Code != http.StatusNoContent {
		t.Fatalf("对已结束的诊断调停止应该返回 204，实际 %d", rec.Code)
	}
	if d, _ = e.st.Diagnosis(uctx, id2); d.Status != done || d.Error == agent.StoppedMsg {
		t.Errorf("已经结束的诊断不应该被改成已停止：%q → %q", done, d.Status)
	}
}

// TestE2ESEO：对外页面有描述、canonical、分享卡片和统计代码，首页有结构化数据；
// 重置密码页不加载统计（地址里有令牌）；登录后的页面不让收录、不加载统计、CSP 不放行站外地址。
func TestE2ESEO(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	_ = e.st.SetSetting(ctx, store.KeySiteURL, "https://pingoo.example.com")
	_ = e.st.SavePlans(ctx, func() store.PlanConfig { c := e.st.Plans(ctx); c.Currency = "$"; return c }())

	rec := e.do("GET", "/", nil)
	body := rec.Body.String()
	for _, want := range []string{`<link rel="canonical" href="https://pingoo.example.com/">`, `<meta name="description" content="Pingoo 每天同步`,
		`<meta property="og:url" content="https://pingoo.example.com/">`, `<meta name="robots" content="index, follow">`,
		`<script type="application/ld+json">`, `"@type":"SoftwareApplication"`, `"priceCurrency":"USD"`,
		`googletagmanager.com/gtm.js?id=` + gtmID, `/static/js/gtm.js`} {
		if !strings.Contains(body, want) {
			t.Errorf("首页缺少 %s", want)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' https://*.googletagmanager.com") {
		t.Errorf("对外页面的 CSP 应该放行 GTM: %s", csp)
	}
	if body := e.do("GET", "/help", nil).Body.String(); !strings.Contains(body, `<link rel="canonical" href="https://pingoo.example.com/help">`) || strings.Contains(body, "ld+json") {
		t.Error("帮助页应该有自己的 canonical，没有首页的结构化数据")
	}
	if body := e.do("GET", "/login?next=/overview", nil).Body.String(); !strings.Contains(body, `href="https://pingoo.example.com/login">`) {
		t.Error("canonical 不应该带查询参数")
	}
	rec = e.do("GET", "/reset?t=secret-token", nil)
	if body := rec.Body.String(); strings.Contains(body, "googletagmanager") || !strings.Contains(body, `content="noindex, nofollow"`) ||
		strings.Contains(rec.Header().Get("Content-Security-Policy"), "google") {
		t.Error("重置密码页不应该加载统计代码，也不应该被收录")
	}

	robots := e.do("GET", "/robots.txt", nil).Body.String()
	if !strings.Contains(robots, "Disallow: /settings") || !strings.Contains(robots, "Sitemap: https://pingoo.example.com/sitemap.xml") {
		t.Errorf("robots.txt 不对:\n%s", robots)
	}
	sm := e.do("GET", "/sitemap.xml", nil)
	if b := sm.Body.String(); !strings.Contains(b, "<loc>https://pingoo.example.com/</loc>") || !strings.Contains(b, "<loc>https://pingoo.example.com/help</loc>") ||
		strings.Contains(b, "/overview") || !strings.HasPrefix(sm.Header().Get("Content-Type"), "application/xml") {
		t.Errorf("sitemap.xml 不对:\n%s", b)
	}
	for _, p := range sitemapPaths {
		if !seoPages[p.Page].Sitemap {
			t.Errorf("sitemap 里的 %s 在 seoPages 里没标 Sitemap", p.Path)
		}
	}

	e.newUser("seo@example.com")
	rec = e.do("GET", "/overview", nil)
	if body := rec.Body.String(); rec.Code != http.StatusOK || strings.Contains(body, "googletagmanager") || strings.Contains(body, "canonical") ||
		!strings.Contains(body, `content="noindex, nofollow"`) || rec.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Errorf("登录后的页面不应该被收录、不应该加载统计代码: %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != csp {
		t.Errorf("登录后的页面 CSP 不应该放行站外地址: %s", got)
	}
}

// TestE2EAdminActivity：后台概况的使用情况——不算管理员和演示账号，按 24 小时 / 7 天 / 30 天滚动；结果缓存几分钟。
func TestE2EAdminActivity(t *testing.T) {
	e := newE2E(t)
	a := e.newUser("act-a@example.com") // 刚注册并登录：24 小时内活跃
	b := e.newUser("act-b@example.com")
	// b 是 10 天前注册、10 天前最后一次访问的老用户，有一个有效的付费套餐
	e.exec(`UPDATE users SET created_at=now()-interval '40 days', last_login_at=now()-interval '10 days', plan='pro', plan_expires_at=now()+interval '5 days' WHERE id=$1`, b)
	e.exec(`UPDATE sessions SET last_seen_at=now()-interval '10 days' WHERE user_id=$1`, b)
	// AI 记录：a 今天问答 2 次、b 3 天前诊断 1 次、b 40 天前的问答不算；管理员自己的不算
	e.exec(`INSERT INTO ledger(user_id,kind,credits,created_at) VALUES ($1,'chat',-2,now()), ($1,'chat',-2,now()-interval '2 hours'),
		($2,'diagnose',-5,now()-interval '3 days'), ($2,'chat',-1,now()-interval '40 days'),
		((SELECT id FROM users WHERE is_admin),'chat',-1,now())`, a, b)

	e.login()
	if rec := e.do("GET", "/admin", nil); !strings.Contains(rec.Body.String(), `hx-get="/admin/part/activity"`) {
		t.Fatal("后台概况应该在页面打开后再加载使用情况")
	}
	body := e.do("GET", "/admin/part/activity", nil).Body.String()
	row := func(name string) string {
		m := regexp.MustCompile(name + `</td>((?:<td class="tnum">[^<]*</td>)+)`).FindStringSubmatch(body)
		if m == nil {
			return ""
		}
		return regexp.MustCompile(`<[^>]+>`).ReplaceAllString(strings.ReplaceAll(m[1], "</td><td", "</td>|<td"), "")
	}
	if got := row("AI 问答"); got != "2|2|2|1" {
		t.Errorf("问答应该是 24 小时 2 次、7 天 2 次、30 天 2 次、1 个人，实际 %q", got)
	}
	if got := row("AI 诊断"); got != "0|1|1|1" {
		t.Errorf("诊断应该是 0 / 1 / 1、1 个人，实际 %q", got)
	}
	if got := row("AI 报告"); got != "0|0|0|0" {
		t.Errorf("没用过的功能也要列出来，全是 0，实际 %q", got)
	}
	for _, want := range []string{`活跃用户 · 24 小时</div><div class="n">1</div>`, `7 天 1 · 30 天 2`,
		`新注册 · 24 小时</div><div class="n">1</div>`, `7 天 1 · 30 天 1`, `付费用户</div><div class="n">1</div>`} {
		if !strings.Contains(body, want) {
			t.Errorf("使用情况缺少 %s:\n%s", want, body)
		}
	}

	// 5 分钟内再打开用缓存，不重新查库
	e.exec(`INSERT INTO ledger(user_id,kind,credits) VALUES ($1,'report',-3)`, a)
	body = e.do("GET", "/admin/part/activity", nil).Body.String()
	if got := row("AI 报告"); got != "0|0|0|0" {
		t.Errorf("缓存期内不应该变: %q", got)
	}
	e.srv.activityMu.Lock()
	e.srv.activityAt = time.Time{}
	e.srv.activityMu.Unlock()
	body = e.do("GET", "/admin/part/activity", nil).Body.String()
	if got := row("AI 报告"); got != "1|1|1|1" {
		t.Errorf("缓存过期后应该重新统计: %q", got)
	}

	e.cookie = nil
	if rec := e.do("GET", "/admin/part/activity", nil); rec.Code == http.StatusOK {
		t.Error("没登录不能看使用情况")
	}
}
