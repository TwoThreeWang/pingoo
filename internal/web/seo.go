package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// gtmID 是 Google Tag Manager 的容器 ID。统计代码只放在对外的页面上（首页、帮助、关于、登录注册等），
// 登录后的页面一律不加载：那里的地址和标题里有用户自己的网站名称、报告和问答，不应该发给第三方；
// 而且那些页面会渲染 AI 的回答，要保持只允许本站资源的 CSP（见 server.go 的 csp）。
const gtmID = "GTM-M5CXLHZX"

// publicCSP 是对外页面用的 CSP：在 csp 的基础上放行 Google Tag Manager 和 Google Analytics。
// 对外页面上没有 AI 内容和用户数据，放开这几个域名不会带来数据外泄的风险。
const publicCSP = "default-src 'self'; script-src 'self' https://*.googletagmanager.com; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https://*.google-analytics.com https://*.googletagmanager.com; font-src 'self' data:; " +
	"connect-src 'self' https://*.google-analytics.com https://*.analytics.google.com https://*.googletagmanager.com; " +
	"form-action 'self'; base-uri 'self'; frame-ancestors 'none'; object-src 'none'"

// seoPage 是一个对外页面的搜索引擎信息。没在 seoPages 里的页面（登录后的页面、忘记密码、出错页等）都不让搜索引擎收录。
type seoPage struct {
	Title    string // 完整的 <title>；空表示用“页面标题 · Pingoo”
	Desc     string
	Keywords string
	Sitemap  bool // 写进 sitemap.xml
}

const seoKeywords = "Pingoo,网站数据分析,Google Analytics,GA4,Search Console,SEO,网站流量,AI 分析,网站周报,独立开发者"

var seoPages = map[string]seoPage{
	"landing": {Title: "Pingoo · 用 AI 读懂 Google Analytics 和 Search Console 的网站数据简报",
		Desc:     "Pingoo 每天同步 Google Analytics 和 Search Console 的数据，自动找出流量上涨、下滑、新搜索词和统计代码失效等变化，再让 AI 用大白话讲清原因和下一步。适合同时运营多个网站的独立开发者，免费注册。",
		Keywords: seoKeywords + ",网站简报,流量异常提醒,多网站管理", Sitemap: true},
	"help": {Desc: "Pingoo 使用帮助：怎么连接 Google Analytics 和 Search Console 服务账号、各项指标和提醒是什么意思、积分和套餐怎么算、常见问题。",
		Keywords: seoKeywords + ",GA 服务账号,Search Console API,使用帮助", Sitemap: true},
	"about": {Desc: "关于 Pingoo：一个给独立开发者的网站数据简报工具，把 GA 和 Search Console 的数据变成每天能看懂的结论和建议。",
		Keywords: seoKeywords, Sitemap: true},
	"privacy": {Desc: "Pingoo 隐私政策：我们收集哪些信息、怎么使用和保护、数据保留多久，以及你可以怎么管理和删除它们。", Sitemap: true},
	"terms":   {Desc: "Pingoo 服务条款：账号、积分、套餐、AI 功能的使用规则和责任说明。", Sitemap: true},
	"register": {Desc: "免费注册 Pingoo，只需要一个邮箱。连接 Google Analytics 和 Search Console 后，每天自动收到所有网站的数据简报和 AI 分析。",
		Keywords: seoKeywords, Sitemap: true},
	"changelog": {Desc: "Pingoo 更新日志：每个版本新增的功能、改进和修复。", Sitemap: true},
	"login":     {Desc: "登录 Pingoo，查看你所有网站的 Google Analytics 和 Search Console 数据简报。"},
}

// seoView 是 <head> 里和搜索引擎、分享卡片、统计有关的内容。
type seoView struct {
	Index     bool   // 允许搜索引擎收录
	Title     string // 完整的 <title>
	Desc      string
	Keywords  string
	Canonical string // 规范地址（只在允许收录的页面上有）
	GTM       string // 统计代码的容器 ID；空表示这一页不加载统计
	LD        any    // 结构化数据（JSON-LD），只有首页有
}

// seoFor 算出一个页面的 seoView，并给对外页面换上放行统计代码的 CSP。layout 是 "public" 或 "layout"（登录后）。
func (s *Server) seoFor(w http.ResponseWriter, r *http.Request, name, title, layout string) seoView {
	v := seoView{Title: title + " · Pingoo"}
	if layout != "public" {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		return v
	}
	ctx := r.Context()
	base := s.siteURL(ctx)
	// 重置密码的地址里带着一次性的链接令牌，不能让统计代码把它发出去；本机调试、还没在后台填站点地址时也不统计
	if name != "reset" && !isLocalURL(base) {
		v.GTM = gtmID
		w.Header().Set("Content-Security-Policy", publicCSP)
	}
	p, ok := seoPages[name]
	if !ok {
		w.Header().Set("X-Robots-Tag", "noindex")
		return v
	}
	v.Index, v.Desc, v.Keywords, v.Canonical = true, p.Desc, p.Keywords, base+r.URL.Path
	if p.Title != "" {
		v.Title = p.Title
	}
	if name == "landing" {
		v.LD = s.landingLD(ctx, base, p.Desc)
	}
	return v
}

func isLocalURL(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return true
	}
	h := pu.Hostname()
	return h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".test")
}

// currencyCodes 把后台填的货币符号换成结构化数据要的 ISO 4217 代码；认不出的符号就不写价格。
var currencyCodes = map[string]string{"$": "USD", "US$": "USD", "¥": "CNY", "￥": "CNY", "CN¥": "CNY", "€": "EUR", "£": "GBP", "HK$": "HKD", "NT$": "TWD", "JP¥": "JPY"}

// landingLD 是首页的结构化数据：网站、组织，以及带各档价格的软件应用。
func (s *Server) landingLD(ctx context.Context, base, desc string) map[string]any {
	app := map[string]any{"@type": "SoftwareApplication", "name": "Pingoo", "url": base + "/", "description": desc,
		"applicationCategory": "BusinessApplication", "operatingSystem": "Web", "inLanguage": "zh-CN"}
	cfg := s.Store.Plans(ctx)
	if code := currencyCodes[strings.TrimSpace(cfg.Currency)]; code != "" {
		var offers []map[string]any
		for _, p := range cfg.List() {
			if p.Paid() && p.Price <= 0 {
				continue // 没填价格的付费档不展示价格
			}
			offers = append(offers, map[string]any{"@type": "Offer", "name": p.Name, "price": strconv.FormatFloat(p.Price, 'f', -1, 64),
				"priceCurrency": code, "url": base + "/#pricing"})
		}
		if len(offers) > 0 {
			app["offers"] = offers
		}
	}
	return map[string]any{"@context": "https://schema.org", "@graph": []any{
		map[string]any{"@type": "WebSite", "@id": base + "/#website", "name": "Pingoo", "url": base + "/", "inLanguage": "zh-CN", "description": desc},
		map[string]any{"@type": "Organization", "@id": base + "/#org", "name": "Pingoo", "url": base + "/"},
		app,
	}}
}

// robots 告诉搜索引擎只看对外的页面；登录后的页面没登录也打不开，这里再挡一次。
func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString("User-agent: *\nAllow: /\n")
	for _, p := range []string{"/overview", "/sites/", "/reports", "/chat", "/messages/", "/diagnoses/", "/todos", "/part/",
		"/credits", "/settings", "/admin", "/forgot", "/reset"} {
		b.WriteString("Disallow: " + p + "\n")
	}
	b.WriteString("\nSitemap: " + s.siteURL(r.Context()) + "/sitemap.xml\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(b.String()))
}

// sitemapPaths 是 sitemap.xml 里的页面，顺序就是文件里的顺序（和 seoPages 里 Sitemap 为 true 的一致）。
var sitemapPaths = []struct{ Path, Page, Freq, Priority string }{
	{"/", "landing", "weekly", "1.0"},
	{"/help", "help", "weekly", "0.8"},
	{"/register", "register", "monthly", "0.6"},
	{"/about", "about", "monthly", "0.5"},
	{"/changelog", "changelog", "weekly", "0.4"},
	{"/privacy", "privacy", "yearly", "0.3"},
	{"/terms", "terms", "yearly", "0.3"},
}

func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	base := s.siteURL(r.Context())
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range sitemapPaths {
		b.WriteString("  <url><loc>" + xmlEscape(base+p.Path) + "</loc><changefreq>" + p.Freq + "</changefreq><priority>" + p.Priority + "</priority></url>\n")
	}
	b.WriteString("</urlset>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(b.String()))
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}
