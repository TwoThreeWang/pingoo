package web

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pingoo/internal/demo"
	"pingoo/internal/llm"
	"pingoo/internal/mail"
	"pingoo/internal/store"
)

// 管理后台：用户与积分、AI 模型和价格、卡密、全站设置、发信情况。只有管理员能进（auth 中间件里拦着）。

type adminData struct {
	Q          string
	Users      []store.User
	UserCount  int
	Models     []store.AIModel
	ModelNames map[int64]string
	Usage      []store.UsageRow
	Batches    []store.CodeBatch
	Mail       []store.MailStat
	MailDay    int
	MailLimit  int
	Breaker    bool
	RegHour    int
	Settings   map[string]string
	SiteURL    string // 现在生效的站点地址
	LocalURL   bool   // 站点地址还是 localhost：邮件里的链接别人打不开
	HasSMTPPw  bool
	TZs        []string
	HasDemo    bool
	PerYuan    int
	Plans      store.PlanConfig
	Now        time.Time
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := adminData{Q: strings.TrimSpace(r.URL.Query().Get("q")), MailLimit: s.Store.MailLimit(ctx), PerYuan: store.CreditsPerYuan, Plans: s.Store.Plans(ctx), Now: time.Now(),
		ModelNames: map[int64]string{}}
	d.Users, _ = s.Store.Users(ctx, d.Q, 100)
	_ = s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE NOT demo`).Scan(&d.UserCount)
	d.Models, _ = s.Store.Models(ctx, false)
	for _, m := range d.Models {
		d.ModelNames[m.ID] = m.Name
	}
	now := time.Now()
	d.Usage, _ = s.Store.UsageByModel(ctx, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()))
	d.Batches, _ = s.Store.CodeBatches(ctx)
	d.Mail, _ = s.Store.MailStats(ctx)
	d.MailDay = s.Store.MailCount(ctx, "", "", "", 0, now.Add(-24*time.Hour))
	d.RegHour = s.Store.MailCount(ctx, "register", "", "", 0, now.Add(-time.Hour))
	d.Breaker = d.RegHour > registerAlert
	d.Settings = map[string]string{}
	for _, k := range []string{store.KeySignupBonus, store.KeySignupOff, store.KeyAIDailyCap,
		store.KeySiteURL, store.KeySMTPHost, store.KeySMTPPort, store.KeySMTPUser, store.KeySMTPFrom, store.KeySMTPFromName} {
		d.Settings[k] = s.Store.SettingOr(ctx, k, "")
	}
	d.Settings[store.KeyDefaultTZ] = s.Store.DefaultTZ(ctx)
	d.HasSMTPPw = s.Store.SettingOr(ctx, store.KeySMTPPassword, "") != ""
	d.SiteURL = s.siteURL(ctx)
	host := ""
	if u, err := url.Parse(d.SiteURL); err == nil {
		host = u.Hostname()
	}
	d.LocalURL = host == "localhost" || host == "127.0.0.1" || host == "::1"
	d.TZs = tzChoices
	_, err := s.Store.DemoUserID(ctx, false)
	d.HasDemo = err == nil
	s.page(w, r, "admin", "管理后台", "admin", d)
}

func (s *Server) adminSaveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	num := func(key string, min int64) {
		if _, ok := r.Form[key]; !ok {
			return // 表单里没有这一项：不动（空字符串才表示清空）
		}
		v := strings.TrimSpace(strings.ReplaceAll(r.FormValue(key), ",", ""))
		if v == "" {
			_ = s.Store.SetSetting(ctx, key, "")
			return
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= min {
			_ = s.Store.SetSetting(ctx, key, strconv.FormatInt(n, 10))
		}
	}
	// 文本项：表单里有才改；有问题的不保存，并告诉管理员哪一项不对
	var problems []string
	text := func(key string, check func(string) (string, string)) {
		if _, ok := r.Form[key]; !ok {
			return
		}
		v, problem := check(strings.TrimSpace(r.FormValue(key)))
		if problem != "" {
			problems = append(problems, problem)
			return
		}
		_ = s.Store.SetSetting(ctx, key, v)
	}
	plain := func(v string) (string, string) { return v, "" }
	num(store.KeySignupBonus, 0)
	num(store.KeyAIDailyCap, 0)
	num(store.KeyMailLimit, 1)
	text(store.KeySiteURL, func(v string) (string, string) {
		v = strings.TrimRight(v, "/")
		if u, err := url.Parse(v); v != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "") {
			return "", "站点地址要像 https://pingoo.example.com 这样，只写协议和域名"
		}
		return v, ""
	})
	text(store.KeyDefaultTZ, func(v string) (string, string) {
		if _, err := time.LoadLocation(v); err != nil || v == "" {
			return "", "时区不对"
		}
		return v, ""
	})
	text(store.KeySMTPHost, plain)
	text(store.KeySMTPUser, plain)
	text(store.KeySMTPFromName, plain)
	text(store.KeySMTPPort, func(v string) (string, string) {
		if n, err := strconv.Atoi(v); v != "" && (err != nil || n < 1 || n > 65535) {
			return "", "SMTP 端口不对（一般是 587 或 465）"
		}
		return v, ""
	})
	text(store.KeySMTPFrom, func(v string) (string, string) {
		if v != "" && !mail.ValidAddress(v) {
			return "", "发件地址格式不对"
		}
		return v, ""
	})
	if v := r.FormValue(store.KeySMTPPassword); v != "" { // 留空表示不修改
		_ = s.Store.SetSetting(ctx, store.KeySMTPPassword, v)
	}
	if _, ok := r.Form["signup_form"]; ok { // “暂停注册”是复选框，只在它所在的表单里才处理
		_ = s.Store.SetSetting(ctx, store.KeySignupOff, strconv.FormatBool(r.FormValue(store.KeySignupOff) != ""))
	}

	if len(problems) > 0 {
		toast(w, "其余已保存；"+strings.Join(problems, "；"))
	} else {
		toast(w, "已保存")
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminMailTest 给管理员自己发一封测试邮件，确认发信配置能用。
func (s *Server) adminMailTest(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	err := s.Mail.Send(r.Context(), mail.Msg{Kind: "notice", To: u.Email, IP: rateKey(s.clientIP(r)), UserID: u.ID, Subject: "Pingoo 测试邮件",
		Body: "你好，\n\n这是一封测试邮件：Pingoo 的发信配置可以正常使用。\n\n站点地址：" + s.siteURL(r.Context()) + "\n\n— Pingoo"})
	if err != nil {
		toast(w, "没发出去："+err.Error())
	} else {
		toast(w, "测试邮件已发到 "+u.Email+"，请查收")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminCredits(w http.ResponseWriter, r *http.Request) {
	delta, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("delta")), 10, 64)
	if err != nil || delta == 0 {
		toast(w, "请填一个非 0 的整数，负数表示扣减")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	note := strings.TrimSpace(r.FormValue("note"))
	if note == "" {
		note = "管理员调整"
	}
	if err := s.Store.AddCredits(r.Context(), pathID(r), delta, "admin", truncate(note, 100)); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "积分已调整")
	redirect(w, r, "/admin#users")
}

func (s *Server) adminDisable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := s.Store.User(ctx, pathID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if u.IsAdmin {
		toast(w, "不能停用管理员")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.Store.SetDisabled(ctx, u.ID, !u.Disabled); err != nil {
		s.fail(w, r, err)
		return
	}
	if u.Disabled {
		toast(w, "已恢复 "+u.Email)
	} else {
		toast(w, "已停用 "+u.Email+"，他的登录已全部退出")
	}
	redirect(w, r, "/admin#users")
}

// ========== AI 模型 ==========

func (s *Server) adminModelForm(w http.ResponseWriter, r *http.Request) {
	m := store.AIModel{Multiplier: 1, Enabled: true}
	if id := pathID(r); id > 0 {
		var err error
		if m, err = s.Store.Model(r.Context(), id); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	// 每类任务的思考强度：Set 是管理员选的（空表示跟默认），Default 是程序里的默认值
	type effortRow struct{ Key, Label, Set, Default string }
	var set map[string]string
	_ = json.Unmarshal([]byte(m.TaskEffort), &set)
	var efforts []effortRow
	for _, t := range store.EffortTasks {
		efforts = append(efforts, effortRow{t.Key, t.Label, llm.ValidEffort(set[t.Key]), store.DefaultEffort[t.Key]})
	}
	style := m.ThinkingStyle
	if style == "" {
		style = "auto"
	}
	s.part(w, "admin-model-form", map[string]any{"M": m, "HasKey": m.APIKey != "", "PerYuan": store.CreditsPerYuan,
		"Styles": llm.ThinkingStyles, "Style": style, "Efforts": efforts})
}

func (s *Server) adminModelSave(w http.ResponseWriter, r *http.Request) {
	f := func(k string) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue(k)), 64)
		return max(v, 0)
	}
	m := store.AIModel{ID: pathID(r), Name: strings.TrimSpace(r.FormValue("name")), Model: strings.TrimSpace(r.FormValue("model")),
		BaseURL: strings.TrimRight(strings.TrimSpace(r.FormValue("base_url")), "/"), APIKey: strings.TrimSpace(r.FormValue("api_key")),
		PriceIn: f("price_in"), PriceOut: f("price_out"), PriceCache: f("price_cache"), Multiplier: f("multiplier"),
		Enabled: r.FormValue("enabled") != "", IsDefault: r.FormValue("is_default") != ""}
	extra, extraErr := llm.ValidExtra(r.FormValue("extra_params"))
	m.ExtraParams = extra
	m.ThinkingStyle = llm.ValidThinkingStyle(r.FormValue("thinking_style"))
	efforts := map[string]string{} // 只存管理员明确选了的，没选的跟着程序里的默认值走
	for _, t := range store.EffortTasks {
		if e := llm.ValidEffort(r.FormValue("effort_" + t.Key)); e != "" {
			efforts[t.Key] = e
		}
	}
	if len(efforts) > 0 {
		b, _ := json.Marshal(efforts)
		m.TaskEffort = string(b)
	}
	switch {
	case extraErr != nil:
		toast(w, extraErr.Error())
	case m.Name == "" || m.Model == "" || m.BaseURL == "":
		toast(w, "名称、模型 ID 和接口地址都要填")
	case !strings.HasPrefix(m.BaseURL, "https://") && !strings.HasPrefix(m.BaseURL, "http://"):
		toast(w, "接口地址要以 https:// 开头")
	case m.Multiplier <= 0:
		toast(w, "倍率要大于 0")
	case m.ID == 0 && m.APIKey == "":
		toast(w, "新模型要填 API Key")
	default:
		if _, err := s.Store.SaveModel(r.Context(), m); err != nil {
			s.fail(w, r, err)
			return
		}
		toast(w, "已保存")
		redirect(w, r, "/admin#models")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminModelDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteModel(r.Context(), pathID(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "已删除。选了这个模型的用户会自动改用默认模型")
	redirect(w, r, "/admin#models")
}

// adminModelTest 测试一个模型：连不连得上、支不支持工具调用、中文回答正不正常。
func (s *Server) adminModelTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	m, err := s.Store.Model(ctx, pathID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var res []testResult
	c := llm.New(m.BaseURL, m.APIKey, m.Model)
	c.Extra = m.Extra()                // 测试时带上附加参数，才能看出它们是否被接口接受、对回答有没有影响
	c.Thinking = m.Thinking("insight") // 也带上思考参数（洞察那一档，通常是最低的），接口不认的话这里就会报错
	start := time.Now()
	resp, err := c.Chat(ctx, llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "请调用 ping 工具，参数 word 填“你好”。"}},
		Tools: []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "ping", Description: "连通性测试",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"word": map[string]any{"type": "string"}}, "required": []string{"word"}}}}},
	})
	if err != nil {
		s.part(w, "test-results", []testResult{{"error", "连接失败", err.Error()}})
		return
	}
	who := "模型：" + m.Model
	if resp.Model != "" && resp.Model != m.Model {
		who += "（这次实际由 " + resp.Model + " 回答）"
	}
	res = append(res, testResult{"ok", fmt.Sprintf("连接成功，响应 %.1f 秒", time.Since(start).Seconds()), who})
	if len(resp.Message.ToolCalls) > 0 {
		res = append(res, testResult{"ok", "支持工具调用", "可以用于洞察、诊断、问答和报告"})
	} else {
		res = append(res, testResult{"error", "不支持工具调用", "这个模型没有调用工具。洞察、诊断、问答和报告都需要工具调用，请换一个支持 function calling 的模型"})
	}
	resp2, err := c.Chat(ctx, llm.Request{Messages: []llm.Message{{Role: "user", Content: "用一句中文回答：1+1 等于几？"}}})
	if err == nil {
		if strings.ContainsAny(resp2.Message.Content, "二2") {
			res = append(res, testResult{"ok", "中文回答正常", truncate(strings.TrimSpace(resp2.Message.Content), 40)})
		} else {
			got := truncate(resp2.Message.Content, 60)
			if strings.TrimSpace(got) == "" {
				got = "模型没有返回正文（结束原因：" + resp2.FinishReason + "）"
			}
			res = append(res, testResult{"warn", "回答看起来不太对", got})
		}
	}
	s.part(w, "test-results", res)
}

// ========== 套餐 ==========

// adminPlans 保存各档套餐的名称、价格、网站数、保留天数、每月积分。
func (s *Server) adminPlans(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg := s.Store.Plans(ctx)
	if c := strings.TrimSpace(r.FormValue("currency")); c != "" {
		cfg.Currency = truncate(c, 4)
	}
	atoi := func(k string, def int) int {
		if n, err := strconv.Atoi(strings.TrimSpace(strings.ReplaceAll(r.FormValue(k), ",", ""))); err == nil {
			return n
		}
		return def
	}
	for _, id := range store.PlanIDs {
		p := cfg.Plans[id]
		if v := strings.TrimSpace(r.FormValue(id + "_name")); v != "" {
			p.Name = truncate(v, 20)
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue(id+"_price")), 64); err == nil {
			p.Price = v
		}
		p.Sites = atoi(id+"_sites", p.Sites)
		p.DetailDays = atoi(id+"_detail_days", p.DetailDays)
		p.TotalDays = atoi(id+"_total_days", p.TotalDays)
		p.MonthlyCredits = int64(atoi(id+"_monthly_credits", int(p.MonthlyCredits)))
		if p.Paid() {
			p.BuyURL = strings.TrimSpace(r.FormValue(id + "_buy_url"))
		}
		cfg.Plans[id] = p
	}
	if err := s.Store.SavePlans(ctx, cfg); err != nil {
		toast(w, err.Error())
		w.WriteHeader(http.StatusNoContent)
		return
	}
	toast(w, "套餐已保存")
	w.WriteHeader(http.StatusNoContent)
}

// adminUserPlan 直接设置一个用户的套餐（客服用：补偿、试用等）。天数从现在起算；选免费版表示取消付费套餐。
func (s *Server) adminUserPlan(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("days")))
	plan := r.FormValue("plan")
	if plan != store.PlanFree && (days < 1 || days > 3660) {
		toast(w, "请填 1～3660 天")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.Store.SetPlan(r.Context(), pathID(r), plan, days); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "套餐已设置")
	redirect(w, r, "/admin#users")
}

// ========== 卡密 ==========

// adminCodes 生成一批卡密，直接下载成 CSV（原文只在这时能拿到，数据库里只存哈希）。
func (s *Server) adminCodes(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.FormValue("count"))
	credits, _ := strconv.ParseInt(r.FormValue("credits"), 10, 64)
	days, _ := strconv.Atoi(r.FormValue("valid_days"))
	planDays, _ := strconv.Atoi(r.FormValue("plan_days"))
	batch := strings.TrimSpace(r.FormValue("batch"))
	plan := r.FormValue("kind") // credits / basic / pro
	cfg := s.Store.Plans(r.Context())
	content := ""
	switch plan {
	case store.PlanBasic, store.PlanPro:
		if planDays < 1 || planDays > 3660 {
			http.Error(w, "套餐天数要在 1～3660 之间", http.StatusBadRequest)
			return
		}
		credits, content = 0, fmt.Sprintf("%s %d 天", cfg.Get(plan).Name, planDays)
	default:
		plan, planDays = "", 0
		if credits < 1 || credits > 100000000 {
			http.Error(w, "面额要大于 0", http.StatusBadRequest)
			return
		}
		content = fmt.Sprintf("%d 积分", credits)
	}
	if n < 1 || n > 1000 {
		http.Error(w, "数量要在 1～1000 之间", http.StatusBadRequest)
		return
	}
	if batch == "" {
		batch = time.Now().Format("20060102-1504")
	}
	var exp *time.Time
	if days > 0 {
		t := time.Now().AddDate(0, 0, days)
		exp = &t
	}
	codes, err := s.Store.CreateCodes(r.Context(), n, credits, plan, planDays, truncate(batch, 40), exp)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pingoo-codes-%d.csv"`, time.Now().Unix()))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("\xEF\xBB\xBF")) // 让 Excel 按 UTF-8 打开
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"卡密", "内容", "批次", "兑换截止"})
	expS := "长期有效"
	if exp != nil {
		expS = exp.Format("2006-01-02")
	}
	for _, c := range codes {
		_ = cw.Write([]string{c, content, batch, expS})
	}
	cw.Flush()
}

func (s *Server) adminCodesDelete(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.DeleteUnusedCodes(r.Context(), r.FormValue("batch"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, fmt.Sprintf("已作废 %d 张没用过的卡密", n))
	redirect(w, r, "/admin#codes")
}

// adminDemo 生成、重新生成或删除共享的演示账号。
func (s *Server) adminDemo(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context())
	var err error
	switch r.FormValue("action") {
	case "remove":
		_, err = demo.Remove(ctx, s.Store)
	case "reset":
		err = demo.Reset(ctx, s.Store, s.Detector)
	default:
		err = demo.Seed(ctx, s.Store, s.Detector)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "演示账号已更新")
	redirect(w, r, "/admin#settings")
}
