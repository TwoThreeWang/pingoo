// Package demo 生成 6 个演示网站和 120 天的模拟数据，方便在没有连接 GA 时体验和开发。
// 演示网站不会去同步 Google 的数据，可以随时在设置里删除。
package demo

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"pingoo/internal/agent"
	"pingoo/internal/signals"
	"pingoo/internal/store"
)

type page struct {
	path, title string
	w           float64 // 浏览量权重
	search      float64 // 这个页面的流量里搜索占多少
}

type query struct {
	q    string
	path string
	impr float64 // 每天曝光
	pos  float64
}

type src struct {
	channel, source, medium string
	w                       float64
}

type siteSpec struct {
	name, domain, desc string
	base               float64 // 日访客
	trend              float64 // 120 天的整体增长
	ppv, eng           float64
	pages              []page
	queries            []query
	sources            []src
	mobileEng          float64
}

var commonSources = []src{
	{"Organic Search", "google", "organic", 0.52}, {"Direct", "(direct)", "(none)", 0.2}, {"Referral", "github.com", "referral", 0.06},
	{"Organic Social", "x.com", "social", 0.05}, {"Organic Search", "bing", "organic", 0.05}, {"Referral", "chatgpt.com", "referral", 0.025},
	{"Email", "newsletter", "email", 0.02}, {"Organic Social", "v2ex.com", "referral", 0.03}, {"Referral", "perplexity.ai", "referral", 0.01},
}

var specs = []siteSpec{
	{name: "技术博客", domain: "blog.example.com", desc: "Go 语言与独立开发的技术博客，目标是增加 Newsletter 订阅。读者主要是后端开发者。",
		base: 560, trend: 0.08, ppv: 1.85, eng: 0.56, mobileEng: 0.31,
		pages: []page{{"/", "首页", 0.14, 0.2}, {"/posts/go-errors", "Go 错误处理最佳实践", 0.13, 0.85}, {"/posts/go-concurrency-patterns", "Go 并发模式：从 channel 到 errgroup", 0.11, 0.75},
			{"/posts/go-modules", "Go module 完全指南", 0.09, 0.85}, {"/posts/go-1-22", "Go 1.22 新特性", 0.08, 0.85}, {"/posts/go-generics", "Go 泛型实战", 0.07, 0.8},
			{"/posts/go-docker", "用 Docker 部署 Go 服务", 0.05, 0.7}, {"/posts/sql-pool", "SQL 连接池调优", 0.05, 0.75}, {"/posts/indie-year-one", "独立开发第一年收入复盘", 0.04, 0.2},
			{"/posts/mac-setup", "我的 Mac 开发环境", 0.04, 0.4}, {"/tags/go", "标签：Go", 0.04, 0.3}, {"/archive", "归档", 0.03, 0.1}, {"/posts/go-static-blog", "用 Go 写一个静态博客", 0.02, 0.6}},
		queries: []query{{"golang error handling", "/posts/go-errors", 260, 3.1}, {"go concurrency patterns", "/posts/go-concurrency-patterns", 170, 3.2},
			{"go module", "/posts/go-modules", 140, 4.1}, {"go 1.22 新特性", "/posts/go-1-22", 150, 2.1}, {"golang generics", "/posts/go-generics", 330, 7.4},
			{"golang generics tutorial", "/posts/go-generics", 140, 11.3}, {"go embed files", "/posts/go-modules", 100, 9.6}, {"go static site generator", "/posts/go-static-blog", 120, 9.8},
			{"go errgroup example", "/posts/go-concurrency-patterns", 60, 3.6}, {"golang sqlc", "/posts/sql-pool", 80, 14.8}, {"go error wrapping", "/posts/go-errors", 120, 4.1}}},
	{name: "JSON 工具箱", domain: "json.example.com", desc: "在线 JSON 格式化和校验工具，靠搜索流量和广告收入。",
		base: 1350, trend: -0.05, ppv: 1.3, eng: 0.48, mobileEng: 0.45,
		pages: []page{{"/", "JSON 格式化工具", 0.62, 0.8}, {"/validator", "JSON 校验", 0.14, 0.8}, {"/json-to-csv", "JSON 转 CSV", 0.1, 0.85},
			{"/json-formatter-online", "在线 JSON 格式化", 0.05, 0.9}, {"/about", "关于", 0.02, 0.1}, {"/diff", "JSON 对比", 0.07, 0.8}},
		queries: []query{{"json formatter", "/", 1300, 3.0}, {"json beautifier", "/", 400, 4.2}, {"json validator", "/validator", 380, 2.8},
			{"json to csv", "/json-to-csv", 300, 5.1}, {"json 格式化", "/", 260, 2.2}, {"json diff", "/diff", 160, 6.3}}},
	{name: "壁纸站", domain: "wallpaper.example.com", desc: "",
		base: 800, trend: 0.02, ppv: 3.4, eng: 0.62, mobileEng: 0.6,
		pages: []page{{"/", "首页", 0.2, 0.3}, {"/4k", "4K 壁纸", 0.2, 0.7}, {"/minimal", "极简壁纸", 0.18, 0.7}, {"/anime", "动漫壁纸", 0.16, 0.6},
			{"/nature", "风景壁纸", 0.14, 0.6}, {"/mac", "Mac 壁纸", 0.12, 0.7}},
		queries: []query{{"4k wallpaper minimal", "/minimal", 400, 5.4}, {"mac wallpaper 4k", "/mac", 300, 6.1}, {"anime wallpaper", "/anime", 500, 9.2}, {"4k 壁纸", "/4k", 220, 3.8}}},
	{name: "图标库", domain: "icons.example.com", desc: "开源 SVG 图标库，目标是增加 GitHub star 和赞助。",
		base: 420, trend: 0.06, ppv: 2.6, eng: 0.6, mobileEng: 0.55,
		pages: []page{{"/", "首页", 0.35, 0.4}, {"/icons/arrows", "箭头图标", 0.15, 0.7}, {"/icons/weather", "天气图标", 0.12, 0.7}, {"/docs", "使用文档", 0.18, 0.3},
			{"/docs/react", "在 React 中使用", 0.12, 0.5}, {"/old-icons", "404 Not Found", 0.01, 0.2}, {"/sponsor", "赞助", 0.03, 0.05}},
		queries: []query{{"free icon pack svg", "/", 260, 8.6}, {"svg arrow icons", "/icons/arrows", 120, 5.2}, {"weather icons svg", "/icons/weather", 140, 4.4}, {"react svg icons", "/docs/react", 90, 12.5}}},
	{name: "Markdown 简历", domain: "resume.example.com", desc: "",
		base: 310, trend: 0.04, ppv: 2.1, eng: 0.55, mobileEng: 0.5,
		pages:   []page{{"/", "Markdown 简历生成器", 0.45, 0.5}, {"/templates", "简历模板", 0.25, 0.6}, {"/editor", "编辑器", 0.2, 0.1}, {"/blog/resume-tips", "程序员简历怎么写", 0.1, 0.8}},
		queries: []query{{"markdown resume template", "/templates", 200, 3.9}, {"markdown 简历", "/", 160, 2.4}, {"程序员简历模板", "/blog/resume-tips", 140, 6.6}}},
	{name: "产品官网", domain: "app.example.com", desc: "",
		base: 190, trend: -0.02, ppv: 2.4, eng: 0.5, mobileEng: 0.45,
		pages:   []page{{"/", "首页", 0.45, 0.4}, {"/pricing", "价格", 0.2, 0.6}, {"/docs", "文档", 0.2, 0.5}, {"/changelog", "更新日志", 0.1, 0.2}, {"/blog", "博客", 0.05, 0.6}},
		queries: []query{{"app pricing", "/pricing", 400, 4.0}, {"xx app alternative", "/", 120, 7.5}, {"xx app docs", "/docs", 80, 3.2}}},
}

var hourCurve = []float64{.35, .25, .18, .15, .15, .2, .35, .6, .9, 1.15, 1.3, 1.35, 1.3, 1.35, 1.4, 1.4, 1.35, 1.3, 1.25, 1.3, 1.4, 1.35, 1.0, .6}
var weekCurve = []float64{0.76, 1.0, 1.08, 1.1, 1.06, 0.98, 0.78} // 周日..周六

var countries = []struct {
	name string
	w    float64
}{{"China", 0.38}, {"United States", 0.21}, {"Germany", 0.06}, {"Japan", 0.05}, {"United Kingdom", 0.04}, {"Canada", 0.035}, {"Singapore", 0.028}, {"India", 0.03}}

// 演示数据放在一个共享的只读演示账号下（访客在首页点“看看演示”进入），不占用任何真实用户的空间。

// Remove 删除演示账号（连同它的网站、数据、报告），返回删除了几个演示网站。
func Remove(ctx context.Context, st *store.Store) (int64, error) {
	id, err := st.DemoUserID(ctx, false)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var n int64
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM sites WHERE user_id=$1`, id).Scan(&n)
	_, err = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1 AND demo`, id)
	return n, err
}

// Reset 删除演示账号，然后重新生成。
func Reset(ctx context.Context, st *store.Store, det *signals.Detector) error {
	if _, err := Remove(ctx, st); err != nil {
		return err
	}
	return Seed(ctx, st, det)
}

// Refresh 让演示数据保持“截至昨天”：演示数据是一次性生成的，过几天“昨天”就没有数据了，看起来像坏了。
// 过时了就删掉演示网站重新生成（演示账号本身不删，正在看演示的访客不会被登出）。返回是否刷新了。
func Refresh(ctx context.Context, st *store.Store, det *signals.Detector) (bool, error) {
	uid, err := st.DemoUserID(ctx, false)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	var latest *time.Time
	_ = st.Pool.QueryRow(ctx, `SELECT max(d.date) FROM ga_daily d JOIN sites s ON s.id = d.site_id WHERE s.user_id = $1`, uid).Scan(&latest)
	if latest != nil && latest.Format("2006-01-02") >= yesterday.Format("2006-01-02") {
		return false, nil
	}
	// 全部网站的洞察、全站周报不挂在某个网站下，要单独删；其余的跟着网站一起删
	for _, q := range []string{`DELETE FROM insights WHERE user_id = $1`, `DELETE FROM reports WHERE user_id = $1`, `DELETE FROM sites WHERE user_id = $1`} {
		if _, err := st.Pool.Exec(ctx, q, uid); err != nil {
			return false, err
		}
	}
	return true, Seed(ctx, st, det)
}

// Seed 写入演示数据。演示账号下已经有网站时只重新跑一遍规则。
func Seed(ctx context.Context, st *store.Store, det *signals.Detector) error {
	uid, err := st.DemoUserID(ctx, true)
	if err != nil {
		return err
	}
	ctx = store.WithUser(ctx, uid)
	if sites, _ := st.Sites(ctx); len(sites) > 0 {
		// 已经有演示网站：只重新跑一遍规则，刷新“需要关注”
		for _, s := range sites {
			det.Detect(ctx, s)
		}
		return nil
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	start := yesterday.AddDate(0, 0, -119)
	for i, sp := range specs {
		id, err := st.CreateSite(ctx, store.Site{Name: sp.name, Domain: sp.domain, Description: sp.desc, Timezone: "Asia/Shanghai", Demo: true})
		if err != nil {
			return err
		}
		if err := seedSite(ctx, st, id, i, sp, start, yesterday); err != nil {
			return err
		}
		for _, src := range []string{"ga", "gsc"} {
			bf, last := start, yesterday
			if src == "gsc" {
				last = yesterday.AddDate(0, 0, -2)
			}
			_ = st.SaveSyncState(ctx, store.SyncState{SiteID: id, Source: src, BackfilledFrom: &bf, LastSynced: &last, Status: "ok", Progress: 100})
		}
	}
	sites, _ := st.Sites(ctx)
	for _, s := range sites {
		det.Detect(ctx, s)
	}
	// 演示账号不能调用 AI：写几份示例洞察、周报和诊断，让访客看到 AI 功能是什么样子
	return agent.DemoExamples(ctx, st, det.M, sites)
}

// adSites 是演示里放了广告（AdSense）的网站：每千次浏览收入（美元），以及个别页面的收入系数。
// JSON 工具箱的“JSON 转 CSV”广告位没放好（收入只有平均的两成），最近 5 天整站的广告单价也掉了一截——
// 演示里能看到“广告收益下滑”和“高流量低收益页面”两种提醒。
var adSites = map[string]struct {
	rpm    float64
	factor map[string]float64
}{
	"JSON 工具箱": {4.6, map[string]float64{"/": 1.1, "/json-to-csv": 0.2, "/json-formatter-online": 1.2, "/about": 0.5, "/diff": 0.9}},
	"壁纸站":      {2.1, map[string]float64{"/": 0.6}},
}

func seedSite(ctx context.Context, st *store.Store, id int64, idx int, sp siteSpec, start, end time.Time) error {
	rnd := rand.New(rand.NewSource(int64(idx*7919 + 17)))
	ad, hasAds := adSites[sp.name]
	adFactor := func(path string) float64 {
		if f, ok := ad.factor[path]; ok {
			return f
		}
		return 1
	}
	adNorm := 0.0 // 让各页面的广告收入加起来等于全站
	if hasAds {
		var ws float64
		for _, p := range sp.pages {
			adNorm += p.w * adFactor(p.path)
			ws += p.w
		}
		adNorm /= ws
		st.SetSiteCurrency(ctx, id, "USD")
	}
	noise := func(a float64) float64 { return 1 + (rnd.Float64()*2-1)*a }
	days := int(end.Sub(start).Hours()/24) + 1
	tabs := map[string][][]any{}
	add := func(t string, row ...any) { tabs[t] = append(tabs[t], row) }

	for di := 0; di < days; di++ {
		d := start.AddDate(0, 0, di)
		back := days - 1 - di // 0 = 昨天
		t := float64(di) / float64(days)
		users := sp.base * weekCurve[d.Weekday()] * (1 + sp.trend*(t-0.5)) * noise(0.08)
		sources := append([]src(nil), commonSources...)
		pageBoost := map[string]float64{}
		engRate, mob := sp.eng, sp.mobileEng
		extra := map[string]float64{} // 额外来源的访客
		switch sp.name {
		case "技术博客":
			if back <= 2 {
				reddit := []float64{1120, 1380, 1650}[back]
				extra["Referral|reddit.com"] = reddit
				if back <= 1 {
					extra["Referral|lobste.rs"] = 60
				}
				pageBoost["/posts/go-static-blog"] = reddit * 1.05
				engRate -= 0.08
			}
			if back <= 20 && back%7 == 3 {
				pageBoost["/posts/indie-year-one"] = 260
				extra["Organic Social|v2ex.com"] = 240
			}
		case "壁纸站":
			if back == 0 {
				users = 12
			}
		case "图标库":
			if back <= 1 {
				hn := []float64{520, 720}[back]
				extra["Referral|news.ycombinator.com"] = hn
				pageBoost["/"] = hn * 1.4
			}
		case "Markdown 简历":
			sources[5].w *= 1 + 2.2*math.Max(0, t-0.7)/0.3
		}
		if users < 1 {
			users = 1
		}
		var extraTotal float64
		for _, v := range extra {
			extraTotal += v
		}
		totalUsers := users + extraTotal
		if sp.name == "壁纸站" && back == 0 {
			totalUsers = 12
		}
		newU := totalUsers * (0.68 + 0.05*noise(0.5) - 0.05)
		if extraTotal > 0 {
			newU = users*0.68 + extraTotal*0.92
		}
		sessions := totalUsers * 1.22
		engaged := sessions * engRate * noise(0.03)
		pv := sessions * sp.ppv * noise(0.04)
		engSec := totalUsers * 105 * noise(0.06)
		dayRPM, adRev, adImp, adClk := 0.0, 0.0, 0, 0
		if hasAds {
			dayRPM = ad.rpm * weekCurve[d.Weekday()] * noise(0.06)
			if sp.name == "JSON 工具箱" && back <= 4 {
				dayRPM *= 0.55 // 最近几天广告单价掉了一截
			}
			adRev = pv * dayRPM / 1000
			adImp = round(pv * 1.7 * noise(0.05))
			adClk = round(float64(adImp) * 0.007 * noise(0.2))
		}
		add("ga_daily", id, d, round(totalUsers), round(newU), round(totalUsers-newU), round(sessions), round(engaged), round(pv), engSec, adRev, adImp, adClk)

		// 来源
		wsum := 0.0
		for _, s := range sources {
			wsum += s.w
		}
		scale := 1.0
		if sp.name == "壁纸站" && back == 0 {
			scale = 12 / users
		}
		for _, s := range sources {
			ss := users * 1.22 * s.w / wsum * noise(0.1) * scale
			if ss < 0.5 {
				continue
			}
			er := engRate
			switch s.channel {
			case "Organic Search":
				er += 0.08
			case "Email":
				er += 0.2
			}
			if strings.Contains(s.source, "chatgpt") || strings.Contains(s.source, "perplexity") {
				er += 0.15
			}
			add("ga_daily_sources", id, d, s.channel, s.source, s.medium, "(not set)", round(ss), round(ss*math.Min(er, 0.95)), round(ss*0.7), round(ss*sp.ppv), ss*130)
		}
		for k, v := range extra {
			p := strings.SplitN(k, "|", 2)
			add("ga_daily_sources", id, d, p[0], p[1], "referral", "(not set)", round(v*1.1), round(v*1.1*0.38), round(v*0.92), round(v*1.25), v*95)
		}

		// 页面与入口页
		psum := 0.0
		for _, p := range sp.pages {
			psum += p.w
		}
		for _, p := range sp.pages {
			base := pv * p.w / psum * noise(0.15)
			v := base + pageBoost[p.path]
			if sp.name == "技术博客" && p.path == "/posts/go-1-22" {
				v *= 1.6 - 0.9*t // 慢慢过时
			}
			if v < 0.5 {
				continue
			}
			dwell := 120.0
			if strings.HasPrefix(p.path, "/tags") || p.path == "/archive" {
				dwell = 20
			}
			add("ga_daily_pages", id, d, p.path, p.title, round(v), round(v*0.8), v*dwell*noise(0.1))
			if hasAds {
				add("ga_daily_ads", id, d, p.path, v*dayRPM/1000*adFactor(p.path)/adNorm, round(v*1.7), round(v*1.7*0.007))
			}
			landSess := base / sp.ppv * 1.1
			for _, s := range []src{{"Organic Search", "google", "organic", p.search}, {"Direct", "(direct)", "(none)", 0.6 * (1 - p.search)}, {"Organic Social", "x.com", "social", 0.4 * (1 - p.search)}} {
				ls := landSess * s.w * noise(0.1)
				if ls < 0.5 {
					continue
				}
				er := engRate
				if strings.HasPrefix(p.path, "/tags") || p.path == "/archive" {
					er = 0.18
				}
				add("ga_daily_landing", id, d, p.path, s.channel, s.source, round(ls), round(ls*er), round(ls*0.7), round(ls*sp.ppv))
			}
		}
		for k, v := range extra {
			p := strings.SplitN(k, "|", 2)
			path := "/"
			if sp.name == "技术博客" {
				path = "/posts/go-static-blog"
				if p[1] == "v2ex.com" {
					path = "/posts/indie-year-one"
				}
			}
			add("ga_daily_landing", id, d, path, p[0], p[1], round(v), round(v*0.38), round(v*0.9), round(v*1.2))
		}

		// 语言
		for _, l := range []struct {
			name string
			w    float64
		}{{"Chinese", 0.46}, {"English", 0.38}, {"Japanese", 0.06}, {"German", 0.05}, {"French", 0.03}, {"Korean", 0.02}} {
			s := sessions * l.w * noise(0.1)
			add("ga_daily_lang", id, d, l.name, round(s), round(s*engRate))
		}

		// 图标库：最近 20 天有一个刷量来源，几乎没人认真看
		if sp.name == "图标库" && back < 20 {
			bots := 150 * noise(0.2)
			add("ga_daily_sources", id, d, "Referral", "traffic-bot.example", "referral", "(not set)", round(bots), round(bots*0.02), round(bots*0.98), round(bots), bots*0.5)
		}

		// 地区、设备、小时
		for _, c := range countries {
			s := sessions * c.w * noise(0.1)
			add("ga_daily_geo", id, d, c.name, round(s), round(s*engRate), round(s*0.82))
		}
		for _, dv := range []struct {
			name string
			w    float64
			er   float64
			sec  float64
		}{{"mobile", 0.46, mob, 38}, {"desktop", 0.5, math.Max(engRate+0.08, mob+0.05), 171}, {"tablet", 0.04, (mob + engRate) / 2, 100}} {
			for _, b := range []struct {
				name string
				w    float64
			}{{"Chrome", 0.62}, {"Safari", 0.38}} {
				s := sessions * dv.w * b.w * noise(0.08)
				if s < 0.5 {
					continue
				}
				add("ga_daily_tech", id, d, dv.name, b.name, round(s), round(s*dv.er), round(s*sp.ppv), s*dv.sec, round(s*0.82))
			}
		}
		hsum := 0.0
		for _, h := range hourCurve {
			hsum += h
		}
		for h := 0; h < 24; h++ {
			f := hourCurve[h] / hsum
			wd := d.Weekday()
			if wd >= 2 && wd <= 4 && h >= 21 && h <= 23 {
				f *= 1.5
			}
			add("ga_hourly", id, d, int16(h), round(sessions*f*noise(0.1)), round(totalUsers*f*noise(0.1)))
		}

		// Search Console（延迟 2 天）
		if back >= 2 {
			var dc, di2, dp float64
			pageAgg := map[string][3]float64{}
			for _, q := range sp.queries {
				impr := q.impr * weekCurve[d.Weekday()] * noise(0.12) * (1 + sp.trend*(t-0.5))
				pos := q.pos + (rnd.Float64()-0.5)*0.6
				switch {
				case sp.name == "JSON 工具箱" && q.q == "json formatter" && back <= 8:
					pos = 3.0 + float64(8-back)*0.85
				case sp.name == "技术博客" && q.q == "go static site generator" && back <= 4:
					pos = 4.2 + float64(back)*0.4
					impr *= 1.6
				case sp.name == "技术博客" && q.q == "go 1.22 新特性":
					pos = 2.1 + 3.7*t
				}
				ctr := ctrFor(pos)
				if sp.name == "产品官网" && q.q == "app pricing" {
					ctr = 0.008
				}
				clicks := impr * ctr * noise(0.15)
				page := "https://" + sp.domain + q.path
				add("gsc_daily_queries", id, d, q.q, page, q.path, round(clicks), round(impr), pos*math.Round(impr))
				a := pageAgg[page]
				pageAgg[page] = [3]float64{a[0] + clicks, a[1] + impr, a[2] + pos*impr}
				dc += clicks
				di2 += impr
				dp += pos * impr
			}
			for pg, a := range pageAgg {
				add("gsc_daily_pages", id, d, pg, strings.TrimPrefix(pg, "https://"+sp.domain), round(a[0]*1.15), round(a[1]*1.2), a[2]*1.2)
			}
			add("gsc_daily", id, d, round(dc*1.2), round(di2*1.3), dp*1.3)
		}
	}

	cols := map[string][]string{
		"ga_daily":          {"site_id", "date", "active_users", "new_users", "returning_users", "sessions", "engaged_sessions", "page_views", "engagement_seconds", "ad_revenue", "ad_impressions", "ad_clicks"},
		"ga_daily_ads":      {"site_id", "date", "path", "revenue", "impressions", "clicks"},
		"ga_daily_sources":  {"site_id", "date", "channel", "source", "medium", "campaign", "sessions", "engaged_sessions", "new_users", "page_views", "engagement_seconds"},
		"ga_daily_pages":    {"site_id", "date", "path", "title", "page_views", "users", "engagement_seconds"},
		"ga_daily_landing":  {"site_id", "date", "path", "channel", "source", "sessions", "engaged_sessions", "new_users", "page_views"},
		"ga_daily_geo":      {"site_id", "date", "country", "sessions", "engaged_sessions", "users"},
		"ga_daily_tech":     {"site_id", "date", "device", "browser", "sessions", "engaged_sessions", "page_views", "engagement_seconds", "users"},
		"ga_hourly":         {"site_id", "date", "hour", "sessions", "users"},
		"ga_daily_lang":     {"site_id", "date", "language", "sessions", "engaged_sessions"},
		"gsc_daily":         {"site_id", "date", "clicks", "impressions", "pos_x_impr"},
		"gsc_daily_pages":   {"site_id", "date", "page", "path", "clicks", "impressions", "pos_x_impr"},
		"gsc_daily_queries": {"site_id", "date", "query", "page", "path", "clicks", "impressions", "pos_x_impr"},
	}
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for name, rows := range tabs {
		rows = dedupe(name, rows)
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{name}, cols[name], pgx.CopyFromRows(rows)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// dedupe 合并主键相同的行（演示数据里来源可能重复）。
func dedupe(table string, rows [][]any) [][]any {
	keyN := map[string]int{"ga_daily_sources": 6, "ga_daily_landing": 5, "ga_daily_pages": 3, "ga_daily_geo": 3, "ga_daily_tech": 4, "ga_hourly": 3,
		"gsc_daily_pages": 3, "gsc_daily_queries": 4, "ga_daily": 2, "gsc_daily": 2, "ga_daily_lang": 3, "ga_daily_ads": 3}[table]
	idx := map[string]int{}
	var out [][]any
	for _, r := range rows {
		var sb strings.Builder
		for i := 0; i < keyN; i++ {
			sb.WriteString(toS(r[i]))
			sb.WriteByte(0)
		}
		k := sb.String()
		if j, ok := idx[k]; ok {
			for i := keyN; i < len(r); i++ {
				switch v := r[i].(type) {
				case int:
					out[j][i] = out[j][i].(int) + v
				case float64:
					out[j][i] = out[j][i].(float64) + v
				}
			}
			continue
		}
		idx[k] = len(out)
		out = append(out, append([]any(nil), r...))
	}
	return out
}

func toS(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case time.Time:
		return x.Format("2006-01-02")
	case int64:
		return string(rune(x))
	case int16:
		return string(rune(x + 1000))
	}
	return ""
}

func round(v float64) int { return int(math.Round(math.Max(0, v))) }

func ctrFor(pos float64) float64 {
	t := []float64{0.28, 0.15, 0.11, 0.08, 0.065, 0.05, 0.04, 0.032, 0.028, 0.025}
	i := int(math.Round(pos)) - 1
	if i < 0 {
		i = 0
	}
	if i < len(t) {
		return t[i]
	}
	if pos <= 20 {
		return 0.012
	}
	return 0.004
}
