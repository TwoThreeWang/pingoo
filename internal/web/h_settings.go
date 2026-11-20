package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pingoo/internal/agent"
	"pingoo/internal/google"
	"pingoo/internal/signals"
	"pingoo/internal/store"
)

type googleView struct {
	Connected bool
	Email     string
	Project   string
	Uploaded  string
	Error     string
}

// aiView 是设置页的“AI 与积分”：余额、选用的模型、自动生成开关、积分流水。
type aiView struct {
	Credits int64
	Models  []store.AIModel
	ModelID int64 // 当前生效的模型
	AutoAI  bool
	Ledger  []store.LedgerRow
	Holds   map[string]int64 // 用当前模型时各类任务的预扣积分（上限）
	Usage   usageView
	PerYuan int
	BuyURL  string // 购买积分卡密的地址，空表示后台没填
	// 套餐
	Plan        store.Plan
	Admin       bool       // 管理员按专业版算、不限网站数，没有到期时间
	Expires     *time.Time // 付费套餐的到期时间
	DaysLeft    int
	Expired     *time.Time // 付费套餐已经到期（还在 7 天宽限期里）时的到期时间
	GraceEnd    time.Time
	SitesUsed   int
	NextCredits *time.Time // 下次发每月积分的时间
}

type siteRow struct {
	Site    store.Site
	GA, GSC store.SyncState
	Running bool
	Paused  bool // 超出套餐网站数，暂停同步
}

type usageView struct {
	Rows            []store.UsageRow
	Calls           int
	In, Out, Cached int64
	Credits         int64
	Max             int64
}

type settingsData struct {
	Google      googleView
	AI          aiView
	Sites       []siteRow
	MaxSites    int
	Reports     store.ReportSettings
	Runs        []store.SyncRun
	Usage       usageView
	Quotas      []store.Quota
	Sessions    []store.Session
	SignalKinds []signals.Kind
	Muted       map[string]bool
	Current     string
	Email       string
	IsAdmin     bool
	Version     string
	TZs         []string
	DigestOn    bool // 每日邮件简报开着（付费套餐才有）
	NextSync    string
}

// tzChoices 是时区下拉框里的选项。
var tzChoices = []string{"Asia/Shanghai", "Asia/Hong_Kong", "Asia/Taipei", "Asia/Tokyo", "Asia/Singapore", "Europe/London", "Europe/Berlin", "America/New_York", "America/Los_Angeles", "UTC"}

// aiState 说明当前用户现在能不能用 AI，不能的话为什么。
type aiState struct {
	Ready bool
	Msg   string
	Model store.AIModel
}

// aiStatus 检查当前用户能不能开始一类 AI 任务：平台要有可用的模型，余额要够这类任务的预扣额。
func (s *Server) aiStatus(r *http.Request, kind string) aiState {
	ctx := r.Context()
	u := currentUser(r)
	m, err := s.Store.ModelFor(ctx, store.UserID(ctx))
	switch {
	case u == nil:
		return aiState{Msg: "登录后才能使用 AI 功能"}
	case u.Demo:
		return aiState{Msg: "演示账号不能使用 AI 功能。注册后就可以用了"}
	case err != nil:
		return aiState{Msg: agent.ErrNoAI.Error()}
	case u.Credits < 0:
		return aiState{Model: m, Msg: fmt.Sprintf("积分不足：上次任务的实际用量超过了预扣，余额是 %d。到「设置 → AI 与积分」充值后会先抵扣，再继续使用", u.Credits)}
	case u.Credits < m.Hold(kind):
		return aiState{Model: m, Msg: fmt.Sprintf("积分不足：这一步开始前要先预扣 %d 积分（结束后按实际用量多退少补），你还有 %d。到「设置 → AI 与积分」里充值后就能用了", m.Hold(kind), u.Credits)}
	}
	if _, _, over := s.Agent.Budget(ctx); over {
		return aiState{Model: m, Msg: agent.ErrBudget.Error()}
	}
	return aiState{Ready: true, Model: m}
}

func (s *Server) googleView(ctx context.Context) googleView {
	v := googleView{}
	sa, err := s.Store.Setting(ctx, store.KeyGoogleSA)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	if sa == "" {
		return v
	}
	var meta struct {
		ClientEmail string `json:"client_email"`
	}
	_ = json.Unmarshal([]byte(sa), &meta)
	v.Connected, v.Email, v.Project = true, meta.ClientEmail, google.ProjectID(sa)
	if t, _ := s.Store.Setting(ctx, store.KeyGoogleSAUpload); t != "" {
		v.Uploaded = t
	}
	return v
}

func (s *Server) aiView(r *http.Request) aiView {
	ctx := r.Context()
	u := currentUser(r)
	v := aiView{Credits: u.Credits, AutoAI: u.AutoAI, Holds: map[string]int64{}, Usage: s.usageView(ctx), PerYuan: store.CreditsPerYuan,
		BuyURL: s.Store.SettingOr(ctx, store.KeyCreditsBuy, "")}
	v.Models, _ = s.Store.Models(ctx, true)
	if m, err := s.Store.ModelFor(ctx, u.ID); err == nil {
		v.ModelID = m.ID
		for _, k := range []string{"insight", "chat", "diagnose", "report"} {
			v.Holds[k] = m.Hold(k)
		}
	}
	v.Ledger, _ = s.Store.Ledger(ctx, 30)
	now := time.Now()
	v.Plan = s.Store.PlanOf(ctx, u.ID)
	v.SitesUsed = s.Store.SiteCount(ctx)
	v.Admin = u.IsAdmin
	if !u.IsAdmin && u.PlanExpiresAt != nil && u.Plan != store.PlanFree {
		switch {
		case u.PlanExpiresAt.After(now):
			v.Expires, v.DaysLeft = u.PlanExpiresAt, int(u.PlanExpiresAt.Sub(now).Hours()/24)
			if u.PlanCreditsAt != nil && v.Plan.MonthlyCredits > 0 {
				next := u.PlanCreditsAt.AddDate(0, 0, store.PlanCycleDays)
				if next.Before(*u.PlanExpiresAt) {
					v.NextCredits = &next
				}
			}
		case u.PlanExpiresAt.After(now.AddDate(0, 0, -store.GraceDays)):
			v.Expired, v.GraceEnd = u.PlanExpiresAt, u.PlanExpiresAt.AddDate(0, 0, store.GraceDays)
		}
	}
	return v
}

func (s *Server) siteRows(ctx context.Context) []siteRow {
	sites, _ := s.Store.Sites(ctx)
	states, _ := s.Store.SyncStates(ctx)
	plan := s.Store.PlanOf(ctx, store.UserID(ctx))
	var out []siteRow
	for _, x := range sites {
		row := siteRow{Site: x, GA: store.SyncState{Status: "pending"}, GSC: store.SyncState{Status: "pending"}, Running: s.Sync.Running(x.ID),
			Paused: plan.Paused(x)}
		if st, ok := states[x.ID]; ok {
			if g, ok := st["ga"]; ok {
				row.GA = g
			}
			if g, ok := st["gsc"]; ok {
				row.GSC = g
			}
		}
		out = append(out, row)
	}
	return out
}

// usageView 统计当前用户本月的 AI 用量。
func (s *Server) usageView(ctx context.Context) usageView {
	now := time.Now()
	v := usageView{}
	v.Rows, _ = s.Store.UsageSince(ctx, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()))
	for _, r := range v.Rows {
		v.Calls += r.Calls
		v.In += r.In
		v.Out += r.Out
		v.Cached += r.Cached
		v.Credits += r.Credits
		v.Max = max(v.Max, r.In+r.Out)
	}
	return v
}

// maxSites 是当前用户的套餐最多能添加几个网站（管理员不限）。
func (s *Server) maxSites(r *http.Request) int {
	if u := currentUser(r); u != nil {
		return s.Store.PlanOf(r.Context(), u.ID).Sites
	}
	return s.Store.Plans(r.Context()).Get(store.PlanFree).Sites
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	d := settingsData{Google: s.googleView(ctx), AI: s.aiView(r), Sites: s.siteRows(ctx), MaxSites: s.maxSites(r), Version: Version,
		Reports: s.Store.ReportSettings(ctx), Email: u.Email, IsAdmin: u.IsAdmin,
		TZs: tzChoices}
	d.DigestOn = s.Store.SettingOr(ctx, store.KeyDigestOff, "") != "true"
	d.Runs, _ = s.Store.RecentRuns(ctx, 20)
	d.Usage = s.usageView(ctx)
	d.SignalKinds, d.Muted = signals.Kinds, map[string]bool{}
	for _, k := range s.Store.MutedSignals(ctx) {
		d.Muted[k] = true
	}
	d.Quotas, _ = s.Store.Quotas(ctx)
	d.Sessions, _ = s.Store.Sessions(ctx)
	d.Current, _ = ctx.Value(sessionKey).(string)
	d.NextSync = fmt.Sprintf("每天 %02d:00（各网站当地时间）", 4)
	s.page(w, r, "settings", "设置", "settings", d)
}

type creditsData struct {
	Credits    int64
	MonthUsed  int64
	Rows       []store.LedgerRow
	More       bool
	NextBefore int64  // 下一页（更早）的游标
	Paged      bool   // 是否已经翻到第一页以后（用来显示“回到最新”）
	BuyURL     string // 购买积分卡密的地址，空表示后台没填
}

const creditsPageSize = 50

// creditsPage 是用户自己的积分明细：余额、本月消耗，以及分页展示的全部流水。
func (s *Server) creditsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	rows, more, err := s.Store.LedgerPage(ctx, before, creditsPageSize)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := creditsData{Credits: u.Credits, MonthUsed: s.usageView(ctx).Credits, Rows: rows, More: more, Paged: before > 0,
		BuyURL: s.Store.SettingOr(ctx, store.KeyCreditsBuy, "")}
	if more && len(rows) > 0 {
		d.NextBefore = rows[len(rows)-1].ID
	}
	s.page(w, r, "credits", "积分明细", "credits", d)
}

// kvPassword 匹配 “host=… password=…” 这种写法里的密码（值可以带单引号）。
var kvPassword = regexp.MustCompile(`(?i)(\w*password)\s*=\s*('(?:[^'\\]|\\.)*'|\S*)`)

// maskDSN 把数据库连接串里的密码去掉，用于在设置页展示。连接串有两种写法：
// postgres://user:pass@host/db?…（密码也可能写在 ?password= 参数里）和 host=… user=… password=…。
func maskDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if !strings.Contains(dsn, "://") {
		return kvPassword.ReplaceAllString(dsn, "$1=***")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "postgres://…"
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	q := u.Query()
	for k := range q {
		if strings.Contains(strings.ToLower(k), "password") {
			q.Set(k, "***")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ========== Google ==========

func (s *Server) saveGoogle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseMultipartForm(1 << 20)
	raw := strings.TrimSpace(r.FormValue("sa_json"))
	if f, _, err := r.FormFile("sa_file"); err == nil {
		b, _ := io.ReadAll(io.LimitReader(f, 1<<20))
		f.Close()
		if len(b) > 0 {
			raw = strings.TrimSpace(string(b))
		}
	}
	v := s.googleView(ctx)
	if raw == "" {
		v.Error = "请粘贴服务账号的 JSON 密钥，或上传密钥文件"
		s.part(w, "settings-google", v)
		return
	}
	if _, err := google.NewFromJSON(ctx, raw); err != nil {
		v.Error = err.Error()
		s.part(w, "settings-google", v)
		return
	}
	if err := s.Store.SetSetting(ctx, store.KeyGoogleSA, raw); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.Store.SetSetting(ctx, store.KeyGoogleSAUpload, inAppLoc(time.Now()).Format("2006-01-02 15:04"))
	s.Store.CacheDeletePrefix(ctx, listsKey(ctx)) // 换了服务账号，能看到的媒体资源也变了
	toast(w, "已保存，密钥已加密。可以点“测试连接”确认权限")
	s.part(w, "settings-google", s.googleView(ctx))
}

type testResult struct {
	Status string // ok / warn / error
	Title  string
	Detail string
}

func (s *Server) testGoogle(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	var res []testResult
	gc, err := s.Sync.Client(ctx)
	if err != nil {
		res = append(res, testResult{"error", "无法连接 Google", err.Error()})
		s.part(w, "test-results", res)
		return
	}
	props, err := gc.Properties(ctx)
	if err != nil {
		res = append(res, testResult{"error", "Google Analytics Admin API", err.Error()})
	} else {
		res = append(res, testResult{"ok", "Google Analytics Admin API", fmt.Sprintf("可以看到 %d 个 GA4 媒体资源", len(props))})
	}
	if len(props) > 0 {
		_, err := gc.RunReport(ctx, props[0].Property, google.ReportRequest{Ranges: []google.DateRange{{StartDate: "7daysAgo", EndDate: "yesterday"}}, Metrics: []string{"activeUsers"}})
		if err != nil {
			res = append(res, testResult{"error", "Google Analytics Data API", err.Error()})
		} else {
			res = append(res, testResult{"ok", "Google Analytics Data API", "可以读取报表数据"})
		}
	} else if err == nil {
		res = append(res, testResult{"warn", "Google Analytics Data API", "服务账号还看不到任何媒体资源：在 GA「管理 → 媒体资源访问权限管理」里把服务账号邮箱加为“查看者”"})
	}
	gsc, err := gc.GSCSites(ctx)
	if err != nil {
		res = append(res, testResult{"error", "Search Console API", err.Error()})
	} else {
		have := map[string]bool{}
		for _, x := range gsc {
			have[x.SiteURL] = true
		}
		sites, _ := s.Store.Sites(ctx)
		var missing []string
		for _, x := range sites {
			if x.GSCSite != "" && !have[x.GSCSite] {
				missing = append(missing, x.Name+"（"+x.GSCSite+"）")
			}
		}
		if len(missing) > 0 {
			res = append(res, testResult{"warn", fmt.Sprintf("Search Console API · 可以访问 %d 个网站", len(gsc)),
				strings.Join(missing, "、") + " 没有授权：在 Search Console 里把服务账号加为用户后，再测试一次"})
		} else {
			res = append(res, testResult{"ok", "Search Console API", fmt.Sprintf("可以访问 %d 个网站", len(gsc))})
		}
	}
	s.part(w, "test-results", res)
}

// ========== AI 与积分 ==========

// saveAI 保存用户选的模型和“自动生成 AI 内容”开关。
func (s *Server) saveAI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	var mid *int64
	if id := formID(r, "model"); id > 0 {
		if m, err := s.Store.Model(ctx, id); err == nil && m.Enabled {
			mid = &m.ID
		}
	}
	auto := r.FormValue("auto_ai") != ""
	if err := s.Store.SetUserPrefs(ctx, u.ID, mid, auto); err != nil {
		s.fail(w, r, err)
		return
	}
	if auto {
		toast(w, "已保存。每天同步后会自动生成洞察，并按报告设置生成周报月报（会消耗积分）")
	} else {
		toast(w, "已保存")
	}
	w.WriteHeader(http.StatusNoContent)
}

// redeemFailsPerHour 限制每个用户每小时兑换失败的次数（卡密猜不中，但也不让人一直试）。
// 失败记录存在用户自己的设置里（不是内存），服务重启不会清零。
const redeemFailsPerHour = 10

// recentRedeemFails 返回当前用户最近一小时兑换失败的时间（unix 秒）。
func (s *Server) recentRedeemFails(ctx context.Context) []string {
	v, _ := s.Store.Setting(ctx, store.KeyRedeemFails)
	cutoff := time.Now().Add(-time.Hour).Unix()
	var out []string
	for _, f := range strings.Split(v, ",") {
		if ts, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64); err == nil && ts > cutoff {
			out = append(out, f)
		}
	}
	return out
}

func (s *Server) redeem(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	recent := s.recentRedeemFails(r.Context())
	if len(recent) >= redeemFailsPerHour {
		toast(w, "输错太多次了，请 1 小时后再试")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	res, err := s.Store.Redeem(r.Context(), u.ID, r.FormValue("code"))
	if err != nil {
		_ = s.Store.SetSetting(r.Context(), store.KeyRedeemFails, strings.Join(append(recent, strconv.FormatInt(time.Now().Unix(), 10)), ","))
		if errors.Is(err, store.ErrBadCode) {
			toast(w, err.Error())
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.fail(w, r, err)
		return
	}
	if res.Plan != "" {
		msg := fmt.Sprintf("已开通%s，到期时间 %s", s.Store.Plans(r.Context()).Get(res.Plan).Name, inAppLoc(res.Expires).Format("2006-01-02 15:04"))
		if res.Granted > 0 {
			msg += fmt.Sprintf("，本月 %d 积分已到账", res.Granted)
		}
		toast(w, msg)
	} else {
		toast(w, fmt.Sprintf("兑换成功，%d 积分已到账", res.Credits))
	}
	redirect(w, r, "/settings#ai")
}

// ========== 网站 ==========

type siteFormView struct {
	Site     store.Site
	New      bool
	Props    []google.Property
	GSCSites []google.GSCSite
	LoadErr  string
	Exclude  string
}

func (s *Server) liveLists(ctx context.Context) ([]google.Property, []google.GSCSite, error) {
	type lists struct {
		P []google.Property
		G []google.GSCSite
	}
	var l lists
	if s.Store.CacheGet(ctx, listsKey(ctx), &l) {
		return l.P, l.G, nil
	}
	gc, err := s.Sync.Client(ctx)
	if err != nil {
		return nil, nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	l.P, err = gc.Properties(cctx)
	if err != nil {
		return nil, nil, err
	}
	l.G, _ = gc.GSCSites(cctx)
	s.Store.CacheSet(ctx, listsKey(ctx), l, 10*time.Minute)
	return l.P, l.G, nil
}

// listsKey 是“服务账号能看到哪些媒体资源”的缓存键：每个用户的服务账号不一样，键里必须带上用户。
func listsKey(ctx context.Context) string { return fmt.Sprintf("google:lists:%d", store.UserID(ctx)) }

func (s *Server) siteEditForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := siteFormView{}
	if id := pathID(r); id > 0 {
		site, err := s.Store.Site(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v.Site = site
	} else {
		v.New = true
		v.Site.Timezone = s.Store.DefaultTZ(ctx)
	}
	v.Exclude = strings.Join(v.Site.ExcludePaths, ", ")
	var err error
	v.Props, v.GSCSites, err = s.liveLists(ctx)
	if err != nil {
		v.LoadErr = err.Error()
	}
	s.part(w, "site-form", v)
}

func (s *Server) siteSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := pathID(r)
	var site store.Site
	if id > 0 {
		var err error
		if site, err = s.Store.Site(ctx, id); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	oldGA, oldGSC := site.GAProperty, site.GSCSite
	site.Name = strings.TrimSpace(r.FormValue("name"))
	site.Domain = strings.TrimSpace(r.FormValue("domain"))
	site.GAProperty = strings.TrimSpace(r.FormValue("ga_property"))
	site.GSCSite = strings.TrimSpace(r.FormValue("gsc_site"))
	site.Description = truncate(strings.TrimSpace(r.FormValue("description")), 1000) // 每次 AI 调用都会带上它
	if lv := r.FormValue("signal_level"); signals.ValidLevel(lv) {
		site.SignalLevel = lv
	}
	if site.SignalLevel == "" {
		site.SignalLevel = signals.LevelNormal
	}
	if tz := strings.TrimSpace(r.FormValue("timezone")); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			site.Timezone = tz
		}
	}
	if site.Timezone == "" {
		site.Timezone = s.Store.DefaultTZ(ctx)
	}
	site.ExcludePaths = nil
	for _, p := range strings.FieldsFunc(r.FormValue("exclude"), func(r rune) bool { return r == ',' || r == '，' || r == '\n' }) {
		if p = strings.TrimSpace(p); p != "" {
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			site.ExcludePaths = append(site.ExcludePaths, p)
		}
	}
	if site.Name == "" {
		site.Name = site.Domain
	}
	if site.Name == "" {
		toast(w, "请填写名称")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	gaChanged, gscChanged := id > 0 && oldGA != site.GAProperty, id > 0 && oldGSC != site.GSCSite
	if (gaChanged || gscChanged) && s.Sync.Running(id) {
		// 正在跑的同步用的还是旧数据源，这时候换的话，旧数据会在清理之后又被写回来
		toast(w, "这个网站正在同步，等同步结束后再更换数据源")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if id == 0 {
		if s.Store.SiteCount(ctx) >= s.maxSites(r) {
			toast(w, fmt.Sprintf("你的套餐最多 %d 个网站，升级套餐可以添加更多", s.maxSites(r)))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		nid, err := s.Store.CreateSite(ctx, site)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.Sched.SyncNow(nid)
		toast(w, "已添加，正在后台回填历史数据")
	} else {
		if err := s.Store.UpdateSite(ctx, site); err != nil {
			s.fail(w, r, err)
			return
		}
		if gaChanged || gscChanged {
			// 换了数据源：旧数据源的数据不能留着和新的混在一起
			if err := s.Store.ResetSiteData(ctx, site.ID, gaChanged, gscChanged); err != nil {
				s.fail(w, r, err)
				return
			}
			_ = s.M.ClearCache(ctx)
			s.Sched.SyncNow(site.ID)
			toast(w, "已保存。数据源换了，旧数据已清除，正在后台重新同步")
		} else {
			toast(w, "已保存。新的背景说明从下一次 AI 分析开始生效")
		}
	}
	redirect(w, r, "/settings#sites")
}

func (s *Server) siteDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteSite(r.Context(), pathID(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "已删除")
	redirect(w, r, "/settings#sites")
}

type importRow struct {
	P        google.Property
	Imported bool
	Match    string
}

func (s *Server) importForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	props, gsc, err := s.liveLists(ctx)
	v := map[string]any{"Error": "", "Rows": []importRow{}, "GSC": gsc, "New": 0, "Total": len(props)}
	if err != nil {
		v["Error"] = err.Error()
		s.part(w, "import-form", v)
		return
	}
	sites, _ := s.Store.Sites(ctx)
	have := map[string]bool{}
	for _, x := range sites {
		have[x.GAProperty] = true
	}
	var rows []importRow
	n := 0
	for _, p := range props {
		row := importRow{P: p, Imported: have[p.Property]}
		for _, g := range gsc {
			if p.Domain != "" && google.Host(g.SiteURL) == p.Domain {
				row.Match = g.SiteURL
				if strings.HasPrefix(g.SiteURL, "sc-domain:") {
					break // 域名资源优先
				}
			}
		}
		if !row.Imported {
			n++
		}
		rows = append(rows, row)
	}
	v["Rows"], v["New"] = rows, n
	s.part(w, "import-form", v)
}

func (s *Server) importSites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	props, _, err := s.liveLists(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byID := map[string]google.Property{}
	for _, p := range props {
		byID[p.Property] = p
	}
	// 已经导入过的媒体资源不再导入（重复提交表单、或者在另一个标签页里已经导入过）
	sites, err := s.Store.Sites(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	have := map[string]bool{}
	for _, x := range sites {
		have[x.GAProperty] = true
	}
	n, limit := 0, s.maxSites(r)-len(sites)
	for _, pid := range r.Form["ga"] {
		p, ok := byID[pid]
		if !ok || have[pid] {
			continue
		}
		if n >= limit {
			toast(w, fmt.Sprintf("已导入 %d 个网站。你的套餐最多 %d 个网站，其余的没有导入（升级套餐可以添加更多）", n, s.maxSites(r)))
			redirect(w, r, "/settings#sites")
			return
		}
		have[pid] = true
		tz := p.TimeZone
		if tz == "" {
			tz = s.Store.DefaultTZ(ctx)
		}
		gsc := r.FormValue("gsc_" + strings.TrimPrefix(pid, "properties/"))
		if gsc == "-" {
			gsc = ""
		}
		id, err := s.Store.CreateSite(ctx, store.Site{Name: p.DisplayName, Domain: p.Domain, GAProperty: pid, GSCSite: gsc, Timezone: tz})
		if err == nil {
			n++
			s.Sched.SyncNow(id)
		}
	}
	toast(w, fmt.Sprintf("已导入 %d 个网站，正在后台回填历史数据", n))
	redirect(w, r, "/settings#sites")
}

// ========== 报告、同步、用量 ==========

func (s *Server) saveReports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := func(k string) string { return strconv.FormatBool(r.FormValue(k) != "") }
	_ = s.Store.SetSetting(ctx, store.KeyReportWeekly, b("weekly"))
	_ = s.Store.SetSetting(ctx, store.KeyReportMonthly, b("monthly"))
	_ = s.Store.SetSetting(ctx, store.KeyReportPerSite, b("per_site"))
	_ = s.Store.SetSetting(ctx, store.KeyReportReview, b("review"))
	if _, ok := r.Form["digest_form"]; ok { // 邮件简报的开关只在付费套餐的表单里出现
		_ = s.Store.SetSetting(ctx, store.KeyDigestOff, strconv.FormatBool(r.FormValue("digest") == ""))
	}
	if d := r.FormValue("detail"); d == "brief" || d == "standard" || d == "detailed" {
		_ = s.Store.SetSetting(ctx, store.KeyReportDetail, d)
	}
	if tz := r.FormValue("tz"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			_ = s.Store.SetSetting(ctx, store.KeyReportTZ, tz)
		}
	}
	toast(w, "已保存，从下一份报告开始生效")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) syncNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := formID(r, "site")
	if _, err := s.Sync.Client(ctx); err != nil {
		toast(w, err.Error())
		w.WriteHeader(http.StatusNoContent)
		return
	}
	sites, _ := s.Store.Sites(ctx) // 只同步自己的网站
	plan := s.Store.PlanOf(ctx, store.UserID(ctx))
	for _, x := range sites {
		if (id == 0 || x.ID == id) && !plan.Paused(x) { // 超出套餐的网站不能手动同步
			s.Sched.SyncNow(x.ID)
		}
	}
	if id == 0 {
		toast(w, "开始同步全部网站")
	} else {
		toast(w, "开始同步")
	}
	time.Sleep(300 * time.Millisecond)
	s.syncTable(w, r)
}

func (s *Server) syncTable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows := s.siteRows(ctx)
	running := false
	for _, x := range rows {
		if x.Running || x.GA.Status == "running" || x.GSC.Status == "running" {
			running = true
		}
	}
	runs, _ := s.Store.RecentRuns(ctx, 20)
	s.part(w, "sync-table", map[string]any{"Sites": rows, "Running": running, "Runs": runs})
}

func (s *Server) clearCache(w http.ResponseWriter, r *http.Request) {
	if err := s.M.ClearCache(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "缓存已清除")
	w.WriteHeader(http.StatusNoContent)
}

// saveSignals 保存“想收到哪些类型的提醒”。表单里勾选的是要保留的，没勾的记为关闭。
func (s *Server) saveSignals(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	on := map[string]bool{}
	for _, k := range r.Form["kind"] {
		on[k] = true
	}
	var muted []string
	for _, k := range signals.Kinds {
		if !on[k.Key] {
			muted = append(muted, k.Key)
		}
	}
	if err := s.Store.SetSetting(r.Context(), store.KeySignalsMuted, strings.Join(muted, ",")); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "已保存，关闭的提醒类型不会再出现")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) renderSessions(w http.ResponseWriter, r *http.Request) {
	ss, _ := s.Store.Sessions(r.Context())
	cur, _ := r.Context().Value(sessionKey).(string)
	s.part(w, "sessions", map[string]any{"Sessions": ss, "Current": cur})
}
