package web

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"pingoo/internal/config"
	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/pow"
	"pingoo/internal/secret"
	"pingoo/internal/store"
	webassets "pingoo/web"
)

func testServer(t *testing.T, proxies string) (*Server, http.Handler) {
	t.Helper()
	tp, err := config.ParseProxies(proxies)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{TrustedProxies: tp}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Pow: pow.NewWithKey([]byte("test-key-0123456789abcdef0123456"))}
	// 没有数据库：只有一个用户 a@pingoo.test，密码 correct-horse
	hash := secret.HashPassword("correct-horse")
	s.lookupUser = func(ctx context.Context, email string) (store.User, error) {
		if email == "a@pingoo.test" {
			return store.User{ID: 1, Email: email, PassHash: hash}, nil
		}
		return store.User{}, store.ErrNotFound
	}
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	old, oldBits := loginDelay, powBits
	loginDelay, powBits = 0, 2
	t.Cleanup(func() { loginDelay, powBits = old, oldBits })
	testPow = s.Pow
	return s, h
}

// testPow 是当前测试服务器的签发器：powForm 用它造一个“2 秒前签发”的挑战并算好答案。
var testPow *pow.Issuer

func powForm(scope string, form url.Values) url.Values {
	tok := testPow.IssueAt(scope, powBits, time.Now().Add(-5*time.Second))
	form.Set("pow_token", tok)
	form.Set("pow_nonce", pow.Solve(tok))
	return form
}

// login 发一次登录请求。密码正确时后面要写会话，测试里没有数据库会走到 500，所以只用它测“被拦住”和“密码不对”的情况。
func login(h http.Handler, remote, xff, pw string) (int, string) {
	form := powForm("login", url.Values{"email": {"a@pingoo.test"}, "password": {pw}})
	req := httptest.NewRequest("POST", "http://pingoo.test/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://pingoo.test")
	req.RemoteAddr = remote
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		proxies, remote, xff, want string
	}{
		{"", "198.51.100.7:4000", "", "198.51.100.7"},
		{"", "198.51.100.7:4000", "1.2.3.4", "198.51.100.7"}, // 没有配置代理：不信任请求头
		{"10.0.0.0/8", "198.51.100.7:4000", "1.2.3.4", "198.51.100.7"},
		{"10.0.0.0/8", "10.0.0.2:4000", "203.0.113.9", "203.0.113.9"},
		{"10.0.0.0/8", "10.0.0.2:4000", "6.6.6.6, 203.0.113.9", "203.0.113.9"},           // 左边是客户端自己填的
		{"10.0.0.0/8", "10.0.0.2:4000", "6.6.6.6, 203.0.113.9, 10.0.0.3", "203.0.113.9"}, // 多层代理
		{"10.0.0.0/8", "10.0.0.2:4000", "", "10.0.0.2"},
		{"10.0.0.0/8", "10.0.0.2:4000", "not-an-ip", "10.0.0.2"},
		{"127.0.0.1", "127.0.0.1:4000", "2001:db8::1", "2001:db8::1"},
		{"127.0.0.1", "[::ffff:127.0.0.1]:4000", "203.0.113.9", "203.0.113.9"},
	}
	for _, c := range cases {
		tp, err := config.ParseProxies(c.proxies)
		if err != nil {
			t.Fatal(err)
		}
		s := &Server{Cfg: &config.Config{TrustedProxies: tp}}
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := s.clientIP(r); got != c.want {
			t.Errorf("proxies=%q remote=%q xff=%q: got %q, want %q", c.proxies, c.remote, c.xff, got, c.want)
		}
	}
	// REAL_IP_HEADER：只有对端可信时才读 CDN 的真实 IP 头；头缺失或不是 IP 时退回 X-Forwarded-For
	hdrCases := []struct {
		remote, hdr, xff, want string
	}{
		{"172.18.0.1:4000", "203.0.113.9", "203.0.113.9, 104.16.0.1, 198.51.100.20", "203.0.113.9"}, // 代理链不用逐个列出
		{"198.51.100.7:4000", "6.6.6.6", "", "198.51.100.7"},                                        // 绕过代理直连：不信任这个头
		{"172.18.0.1:4000", "", "203.0.113.9", "203.0.113.9"},
		{"172.18.0.1:4000", "not-an-ip", "203.0.113.9", "203.0.113.9"},
		{"172.18.0.1:4000", "2001:db8::1", "", "2001:db8::1"},
	}
	tp, err := config.ParseProxies("172.16.0.0/12")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range hdrCases {
		s := &Server{Cfg: &config.Config{TrustedProxies: tp, RealIPHeader: "CF-Connecting-IP"}}
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.hdr != "" {
			r.Header.Set("CF-Connecting-IP", c.hdr)
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := s.clientIP(r); got != c.want {
			t.Errorf("real-ip remote=%q hdr=%q xff=%q: got %q, want %q", c.remote, c.hdr, c.xff, got, c.want)
		}
	}
	if a, b := rateKey("2001:db8:1:2:aaaa::1"), rateKey("2001:db8:1:2:bbbb::2"); a != b {
		t.Errorf("同一个 /64 里的 IPv6 地址应该共用一个限速键: %q %q", a, b)
	}
	if rateKey("203.0.113.9") != "203.0.113.9" {
		t.Error("IPv4 应按单个地址限速")
	}
}

func TestLoginLockout(t *testing.T) {
	// 伪造 X-Forwarded-For 绕不过限速，也锁不到别人
	_, h := testServer(t, "")
	for i := 0; i < loginMaxFails; i++ {
		if code, _ := login(h, "198.51.100.7:1000", fmt.Sprintf("10.9.%d.1", i), "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次输错应该是 401，得到 %d", i+1, code)
		}
	}
	if code, body := login(h, "198.51.100.7:1000", "10.9.99.1", "correct-horse"); code != http.StatusTooManyRequests || !strings.Contains(body, "输错次数太多") {
		t.Fatalf("换了 X-Forwarded-For 之后应该仍然被锁定，得到 %d", code)
	}
	// 另一个来源不受影响：能走到密码校验（401），而不是被锁（429）
	if code, _ := login(h, "198.51.100.8:1000", "198.51.100.7", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("其他来源不应该被连带锁定，得到 %d", code)
	}
}

func TestLoginLockoutConcurrent(t *testing.T) {
	// 同时发来的请求不能一起挤过次数检查
	_, h := testServer(t, "")
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked := 0
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code, _ := login(h, "198.51.100.7:1000", "", "wrong"); code == http.StatusUnauthorized {
				mu.Lock()
				checked++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if checked != loginMaxFails {
		t.Fatalf("40 个并发请求里应该只有 %d 个被校验密码，实际 %d 个", loginMaxFails, checked)
	}
}

func TestLoginGlobalThrottle(t *testing.T) {
	// 换着来源爆破：全站每分钟的失败次数有上限
	s, h := testServer(t, "")
	checked := 0
	for i := 0; i < loginGlobalMax+10; i++ {
		if code, _ := login(h, fmt.Sprintf("198.51.%d.%d:1000", i/200, i%200+1), "", "wrong"); code == http.StatusUnauthorized {
			checked++
		}
	}
	if checked != loginGlobalMax {
		t.Fatalf("全站每分钟最多校验 %d 次失败的密码，实际 %d 次", loginGlobalMax, checked)
	}
	// 一分钟之后恢复；很久没再输错的记录会被清掉
	later := time.Now().Add(loginWindow + 2*time.Minute)
	if v, _, _ := s.loginBegin("203.0.113.1", later); v != loginOK {
		t.Fatalf("过了时间窗口应该恢复，得到 %v", v)
	}
	if n := len(s.failures); n != 1 {
		t.Fatalf("过期的记录应该被清理，还剩 %d 条", n)
	}
}

func TestLoginCountResets(t *testing.T) {
	s, _ := testServer(t, "")
	now := time.Now()
	for i := 0; i < loginMaxFails-1; i++ {
		s.loginBegin("203.0.113.1", now)
	}
	// 隔了很久再输错一次，不应该因为很久以前的 4 次被锁
	if _, _, last := s.loginBegin("203.0.113.1", now.Add(loginWindow+time.Minute)); last {
		t.Fatal("超过时间窗口后，之前的失败次数应该清零")
	}
	s.loginSucceeded("203.0.113.1")
	if len(s.failures) != 0 {
		t.Fatal("登录成功后应该清掉失败记录")
	}
}

func TestSecurityHeadersAndBodyLimit(t *testing.T) {
	_, h := testServer(t, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://pingoo.test/login", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self';", "img-src 'self' data:", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP 缺少 %q: %s", want, csp)
		}
	}
	// 超过 1MB 的登录请求：表单读不出来，人机校验那一步就拒绝，不会把整个请求体读进内存
	body := powForm("login", url.Values{"email": {"a@pingoo.test"}, "password": {"correct-horse"}}).Encode() + "&pad=" + strings.Repeat("a", 2<<20)
	req := httptest.NewRequest("POST", "http://pingoo.test/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://pingoo.test")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("超大请求应该读不到表单、被人机校验拦下（400），得到 %d", rec.Code)
	}
}

func TestMarkdownNoExternalImage(t *testing.T) {
	got := string(markdown("结论 ![统计图](https://evil.example/x.png?d=secret) 见上。\n\n[链接](https://example.com) <img src=x onerror=alert(1)>"))
	if strings.Contains(got, "<img") || strings.Contains(got, "evil.example") {
		t.Errorf("图片不应该被渲染: %s", got)
	}
	if !strings.Contains(got, "统计图") || !strings.Contains(got, `<a href="https://example.com">链接</a>`) {
		t.Errorf("图片的说明文字和普通链接应该保留: %s", got)
	}
}

func TestMaskDSN(t *testing.T) {
	for _, dsn := range []string{
		"postgres://pg:s3cret@db:5432/pingoo?sslmode=disable",
		"postgres://pg@db:5432/pingoo?password=s3cret&sslmode=disable",
		"host=db user=pg password=s3cret dbname=pingoo",
		"host=db user=pg password='s3cret with space' dbname=pingoo sslpassword=s3cret",
	} {
		got := maskDSN(dsn)
		if strings.Contains(got, "s3cret") || strings.Contains(got, "with space") {
			t.Errorf("maskDSN(%q) 漏出了密码: %s", dsn, got)
		}
		if !strings.Contains(got, "pingoo") || !strings.Contains(got, "pg") {
			t.Errorf("maskDSN(%q) 不应该去掉其他信息: %s", dsn, got)
		}
	}
}

// 放在 data-* 属性里的网址参数要自己编码（html/template 只管 href 这类属性）。
func TestDataAttrURLEncoding(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	path := "/post?id=1&lang=zh#top 100%+"
	var buf bytes.Buffer
	v := tabView{SiteID: 1, Range: period.Range{Key: "d28"}, Tab: "rising", Tabs: metrics.ContentTabs,
		Rows: []metrics.PageRow{{Path: path, Title: "t", Views: 10}}, Max: 10}
	if err := s.base.ExecuteTemplate(&buf, "site-content", v); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	i := strings.Index(out, `data-detail="`)
	if i < 0 {
		t.Fatal("没有找到 data-detail")
	}
	attr := out[i+len(`data-detail="`):]
	attr = html.UnescapeString(attr[:strings.Index(attr, `"`)]) // 浏览器读属性值时会做同样的还原
	u, err := url.Parse(attr)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("path"); got != path || u.Query().Get("range") != "d28" || u.Fragment != "" {
		t.Errorf("路径参数被截断了: path=%q（期望 %q），完整地址 %s", got, path, attr)
	}
}

// CSP 禁止内联脚本，所以模板里不能再出现 <script>…</script> 或 onclick 这类属性，否则对应的功能会悄悄失效。
func TestNoInlineScriptsInTemplates(t *testing.T) {
	inline := regexp.MustCompile(`(?i)<script(?:\s[^>]*)?>\s*[^<\s]|\son[a-z]+\s*=\s*["']|javascript:`)
	err := fs.WalkDir(webassets.FS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := webassets.FS.ReadFile(p)
		for i, line := range strings.Split(string(b), "\n") {
			if m := inline.FindString(line); m != "" {
				t.Errorf("%s:%d 有内联脚本 %q，请把逻辑放进 static/js/app.js", p, i+1, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRangeKeyFromRequest(t *testing.T) {
	for query, want := range map[string]string{
		"range=d7":                               "d7",
		"from=2026-10-01&to=2026-10-31":          "2026-10-01_2026-10-31",
		"range=d7&from=2026-10-01&to=2026-10-31": "2026-10-01_2026-10-31", // 自选日期优先
		"range=2026-10-01_2026-10-31":            "2026-10-01_2026-10-31",
		"from=2026-10-31&to=2026-10-01":          "", // 不合法：由调用方用默认范围
		"from=2026-10-01":                        "",
		"range=%27%3Bdrop":                       "",
	} {
		r := httptest.NewRequest("GET", "/?"+query, nil)
		if got := rangeKey(r); got != want {
			t.Errorf("rangeKey(%q) = %q, want %q", query, got, want)
		}
	}
	if compareMode(httptest.NewRequest("GET", "/?cmp=yoy", nil)) != period.CompareYoY || compareMode(httptest.NewRequest("GET", "/?cmp=x", nil)) != "" {
		t.Error("compareMode")
	}
}

// TestLoginPowAndTrap：没有人机校验、校验过期或用错地方、填了陷阱字段，都在比对密码之前被拒绝。
func TestLoginPowAndTrap(t *testing.T) {
	_, h := testServer(t, "")
	post := func(form url.Values) int {
		req := httptest.NewRequest("POST", "http://pingoo.test/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://pingoo.test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	base := func() url.Values { return url.Values{"email": {"a@pingoo.test"}, "password": {"wrong"}} }
	if code := post(base()); code != http.StatusBadRequest {
		t.Errorf("没有工作量证明应该 400，得到 %d", code)
	}
	if code := post(powForm("register", base())); code != http.StatusBadRequest {
		t.Errorf("注册页的挑战不能拿来登录，得到 %d", code)
	}
	trap := powForm("login", base())
	trap.Set(trapField, "bot")
	if code := post(trap); code != http.StatusBadRequest {
		t.Errorf("填了陷阱字段应该 400，得到 %d", code)
	}
	fresh := base()
	tok := testPow.Issue("login", powBits) // 刚签发：不到 2 秒
	fresh.Set("pow_token", tok)
	fresh.Set("pow_nonce", pow.Solve(tok))
	if code := post(fresh); code != http.StatusBadRequest {
		t.Errorf("签发后不到 2 秒就提交应该 400，得到 %d", code)
	}
	ok := powForm("login", base())
	if code := post(ok); code != http.StatusUnauthorized {
		t.Errorf("校验通过、密码不对应该 401，得到 %d", code)
	}
	if code := post(ok); code != http.StatusBadRequest {
		t.Errorf("同一个挑战第二次用应该 400，得到 %d", code)
	}
}

// TestLoginPageHasChallenge：登录页每次都带一个新挑战和陷阱字段，出错重新渲染时也带。
func TestLoginPageHasChallenge(t *testing.T) {
	_, h := testServer(t, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://pingoo.test/login", nil))
	body := rec.Body.String()
	for _, want := range []string{`name="pow_token" value="login.`, `name="` + trapField + `"`, `tabindex="-1"`, `autocomplete="off"`, `aria-hidden="true"`, `/static/js/pow.js`, "data-pow"} {
		if !strings.Contains(body, want) {
			t.Errorf("登录页里应该有 %q", want)
		}
	}
	_, body = login(h, "198.51.100.9:1000", "", "wrong")
	if !strings.Contains(body, `name="pow_token" value="login.`) || !strings.Contains(body, `value="a@pingoo.test"`) {
		t.Error("出错重新渲染时要签发新挑战，并保留已填的邮箱")
	}
}
