// Package agent 组织 AI 的四种任务：洞察、诊断、报告、问答。
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"pingoo/internal/llm"
	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/store"
	"pingoo/internal/syncer"
)

type Agent struct {
	Store *store.Store
	M     *metrics.Svc
	Sync  *syncer.Syncer
	Log   *slog.Logger

	mu      sync.Mutex
	running map[string]bool
	stops   map[int64]context.CancelCauseFunc // 正在跑的诊断：诊断 id → 让它停下来的函数
}

// errStopped 是用户自己停掉任务（关掉了诊断窗口）时的取消原因，用来和超时、服务关闭区分开。
var errStopped = errors.New("已停止")

// StopDiagnosis 让正在跑的诊断停下来（用户关掉了诊断窗口，结果已经没人看了）。诊断不在跑时什么也不做。
// 只负责发出停止信号：把记录标成“已停止”、结算积分由诊断自己的后台任务收尾时做。
func (a *Agent) StopDiagnosis(id int64) bool {
	a.mu.Lock()
	stop := a.stops[id]
	a.mu.Unlock()
	if stop == nil {
		return false
	}
	stop(errStopped)
	return true
}

func New(st *store.Store, m *metrics.Svc, sy *syncer.Syncer, log *slog.Logger) *Agent {
	return &Agent{Store: st, M: m, Sync: sy, Log: log, running: map[string]bool{}, stops: map[int64]context.CancelCauseFunc{}}
}

var ErrNoAI = errors.New("平台还没有可用的 AI 模型，请联系管理员")

// ErrNoCredits 表示积分不够这一次任务的预扣额。
var ErrNoCredits = errors.New("积分不足，充值后才能使用 AI 功能（设置 → AI 与积分）")

// ErrBudget 表示全站今天的 token 用量已经到了管理员设的上限。
var ErrBudget = errors.New("今天全站的 AI 用量已经到上限了，明天会自动恢复")

// Budget 返回今天已经用掉的 token、每日上限（0 表示不限），以及是不是已经到上限。
// 上限在每个任务开始前检查：正在跑的任务不会被打断，所以实际用量可能比上限多出一个任务的量。
func (a *Agent) Budget(ctx context.Context) (used, limit int64, over bool) {
	limit = a.Store.DailyTokenCap(ctx)
	if limit <= 0 {
		return 0, 0, false
	}
	loc := time.Local // 全站每日上限按后台设置的默认时区算“今天”
	if l, err := time.LoadLocation(a.Store.DefaultTZ(ctx)); err == nil {
		loc = l
	}
	now := time.Now().In(loc)
	used = a.Store.TokensSince(ctx, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc))
	return used, limit, used >= limit
}

// claim 防止同一件事被重复触发（例如同时点了两次“重新生成”）。
func (a *Agent) claim(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running[key] {
		return false
	}
	a.running[key] = true
	return true
}

func (a *Agent) release(key string) {
	a.mu.Lock()
	delete(a.running, key)
	a.mu.Unlock()
}

func (a *Agent) Busy(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running[key]
}

// bill 是一次 AI 任务的计费：开始时按这类任务的上限预扣积分，结束时按实际用量结算、退回多余，失败全额退回。
type bill struct {
	a      *Agent
	id     int64
	m      store.AIModel
	budget int64 // 这次任务最多付得起多少积分：预扣的 + 预扣后还剩的余额
	done   bool
}

// near 表示用量快到用户付得起的上限了（八成）：该收尾了——不再查数据，用已经查到的直接给结论，
// 剩下两成留给最后一轮回答。余额充足的用户基本碰不到（任务本身最多 10 轮），分析不会被提前打断；
// 只有余额不够继续查的时候才会提前收尾，免得余额被扣成很大的负数。免费模型（价格是 0）永远不会提前收尾。
func (b *bill) near(u llm.Usage) bool {
	if b == nil {
		return false
	}
	used := b.m.RawCost(u.PromptTokens, u.CompletionTokens, u.Cached())
	return used > 0 && used >= 0.8*float64(b.budget)
}

// begin 为当前用户（context 里的用户）开始一次 AI 任务：选模型、检查全站上限、预扣积分。
func (a *Agent) begin(ctx context.Context, kind string) (*llm.Client, *bill, error) {
	uid := store.UserID(ctx)
	m, err := a.Store.ModelFor(ctx, uid)
	if err != nil || m.BaseURL == "" || m.Model == "" {
		return nil, nil, ErrNoAI
	}
	if _, _, over := a.Budget(ctx); over {
		return nil, nil, ErrBudget
	}
	hold := m.Hold(kind)
	id, err := a.Store.Reserve(ctx, uid, kind, m.Name, hold)
	budget := hold
	if u, uerr := a.Store.User(ctx, uid); err == nil && uerr == nil && u.Credits > 0 {
		budget += u.Credits // 预扣之后还剩的余额也付得起
	}
	if errors.Is(err, store.ErrNoCredits) {
		return nil, nil, ErrNoCredits
	}
	if err != nil {
		return nil, nil, err
	}
	c := llm.New(m.BaseURL, m.APIKey, m.Model)
	c.Extra = m.Extra()
	c.Thinking, c.Light = m.Thinking(kind), m.Thinking("repair") // 思考强度按任务类型定
	c.KeepReasoning = m.KeepReasoning()
	c.Log = a.Log
	return c, &bill{a: a, id: id, m: m, budget: budget}, nil
}

// settle 按任务的结果结算：成功照常扣，失败全额退回。ok 表示拿到了能用的结果（例如回答解析成功）。
// 例外是被服务商内容审核拦截的任务：没有结果，但查数据用掉的 token 已经实实在在花出去了，照常计费，并在明细里注明原因。
func (b *bill) settle(u llm.Usage, err error, ok bool) {
	if errors.Is(err, llm.ErrContentFilter) {
		b.close(u, false, "被服务商的内容审核拦截，没有结果；已经用掉的 token 照常计费")
		return
	}
	b.finish(u, err != nil || !ok)
}

// finish 结算。failed 为 true 时全额退回（用户不为失败的任务付费）；重复调用只算第一次。
func (b *bill) finish(u llm.Usage, failed bool) { b.close(u, failed, "") }

func (b *bill) close(u llm.Usage, failed bool, note string) {
	if b == nil || b.done {
		return
	}
	b.done = true
	ctx, cancel := finishCtx()
	defer cancel()
	charge := b.m.Cost(u.PromptTokens, u.CompletionTokens, u.Cached())
	switch {
	case failed:
		charge, note = 0, "没有成功，已全额退回"
	case note == "" && u.Estimated:
		note = "接口没有返回用量，按字数估算"
	}
	if err := b.a.Store.Settle(ctx, b.id, u.PromptTokens, u.CompletionTokens, u.Cached(), charge, note); err != nil {
		b.a.Log.Error("结算积分失败", "ledger", b.id, "err", err)
	}
}

// maxOutput 是每一次调用最多输出多少 token（思考也算在内）。正常的一次调用用不到它的三分之一，
// 只用来挡住偶尔失控的输出：出现过一份洞察“想”了 6 万多 token 的情况。
const maxOutput = 16000

// newRunner 默认最多 11 轮：前 9 轮可以查数据，第 10 轮要求收尾（工具还带着，保住提示缓存），
// 模型不听的话第 11 轮不给工具。
func (a *Agent) newRunner(c *llm.Client, b *bill, onTool func(name, trace string, done bool)) *llm.Agent {
	return &llm.Agent{Client: c, Tools: toolDefs(), Handlers: a.handlers(), MaxSteps: 11, MaxOutput: maxOutput, OnTool: onTool, Budget: b.near}
}

// insightTools 是生成洞察时给 AI 的工具：去掉 site_overview / list_signals，
// 这些内容已经直接写在提示里了，再调一次只会让后面每一轮都多发一遍。
func insightTools() []llm.Tool {
	var out []llm.Tool
	for _, t := range toolDefs() {
		switch t.Function.Name {
		case "site_overview", "list_signals":
			continue
		}
		out = append(out, t)
	}
	return out
}

// systemPrompt 是所有任务共用的规则。
func (a *Agent) systemPrompt(ctx context.Context, extra string) string {
	sites, _ := a.Store.Sites(ctx)
	var sb strings.Builder
	now := time.Now()
	// 不变的部分（身份、规则）放最前面，日期、网站列表这些会变的放后面：服务商按“开头相同的部分”命中提示缓存，
	// 开头一样的话，不同任务、不同用户的第一次调用也能命中，缓存部分的价格一般只有原价的 1/4～1/10。
	sb.WriteString(`你是 Pingoo 的数据分析助手，帮一位独立开发者看懂他的网站数据。

规则：
1. 只用中文回答，说人话：用“访客”“认真阅读比例”“搜索点击”这样的词，不要用 GA 的英文术语；必须用术语时加一句解释。
2. 所有数字都必须来自已经给出的数据或工具返回的数据，不能编造。引用数字时带上对比（比上期多/少多少）。
   数字写成人能直接读的样子：比例写“41.7%”，不要写“0.417”；大数字写“12,400”。变化幅度直接引用给出的结果，不要自己换算；
   “腰斩”“翻倍”“暴跌”这类词只在真的减半、翻倍、跌一半以上时用，比例的变化说“下降了 12.6 个百分点”。
3. 区分“事实”和“推测”。推测原因时要说明依据，并标明是推测。
4. 你只能看到这位站长自己网站的 GA 和 Search Console 数据，看不到竞争对手和其他网站。被问到这类问题时如实说明，再从已有数据里找线索。
5. 查数据要省：已经给出或查过的直接用，不重复查；先想清楚要验证什么，把互不依赖的查询放在同一轮里一起发（一次调用多个工具）。
   优先用本地数据的工具（又快又省配额），不够时再用 ga_report / gsc_query。
   想知道“变化从哪天开始、出在哪个渠道 / 来源 / 页面”，用 daily_breakdown 一次查出每天的数，不要把日期切成几段反复查。
   工具是你自己用的：写给站长看的内容里不要出现工具的名字，要他自己去看的话说“在来源页看每天的变化”这样的人话。
6. 搜索数据（Search Console）通常有 2~3 天延迟。
7. 建议要具体、可执行，说清楚做什么、为什么、怎么判断有没有效果。
8. 网站有广告收益（AdSense 等）的数据时，收入的变化要拆成两部分讲：流量（浏览量）的变化，和每千次浏览收入（RPM）的变化。RPM 变了、流量没变，问题多半在广告本身（单价、广告位、加载），不在内容。
`)
	fmt.Fprintf(&sb, "\n今天是 %s（%s）。\n\n站长的网站：\n", now.Format("2006-01-02"), []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[now.Weekday()])
	// 写明每个网站的 id、数据截至哪天、有没有 Search Console：AI 不用再花一次调用去“列出网站”，也不会去查没连的 Search Console
	for _, s := range sites {
		fmt.Fprintf(&sb, "- id=%d %s（%s）", s.ID, s.Name, s.Domain)
		if s.Demo {
			sb.WriteString("（演示数据）")
		}
		if t := a.M.GALatest(ctx, s.ID); !t.IsZero() {
			fmt.Fprintf(&sb, "；GA 数据截至 %s", period.D(t))
		}
		if s.GSCSite == "" && !s.Demo {
			sb.WriteString("；没有连接 Search Console（不要查它的搜索数据）")
		} else if t := a.M.GSCLatest(ctx, s.ID); !t.IsZero() {
			fmt.Fprintf(&sb, "；Search Console 数据截至 %s", period.D(t))
		}
		if s.Description != "" {
			fmt.Fprintf(&sb, "。站长写的背景：%s", s.Description)
		}
		sb.WriteString("\n")
	}
	if extra != "" {
		sb.WriteString("\n" + extra)
	}
	return sb.String()
}

// finishCtx 专门用来把任务结果写回数据库。任务自己的 ctx 这时可能已经超时或被取消，
// 再用它写会立刻失败，记录就一直停在“进行中”（问答会因此拒绝后面所有的新问题）。
func finishCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

// recovered 在后台任务的 defer 里调用：任务里出现 panic 时记下日志并返回 true，由调用方把记录标成失败。
// 后台 goroutine 没有 HTTP 那层 recover，不接住的话一次 panic 会让整个进程退出。
func (a *Agent) recovered(task string, v any) bool {
	if v == nil {
		return false
	}
	a.Log.Error("后台任务出错", "task", task, "panic", v, "stack", string(debug.Stack()))
	return true
}

// parseOrRepair 把模型的最终回答解析成 T。解析不出来、或者缺了关键字段（valid 返回 false）时，
// 把这份回答连同格式要求再发给模型一次，只让它整理格式。模型偶尔会在 JSON 外面加解释、漏括号，
// 或者干脆用文字回答；这时数据已经查完了，多花一次不带工具的小调用，
// 比直接报“格式不对”、让站长点“重新生成”再从头查一遍数据划算得多。
// 整理用掉的 token 会加进 res.Usage。
func parseOrRepair[T any](ctx context.Context, c *llm.Client, res *llm.RunResult, format string, valid func(T) bool) (T, bool) {
	var out T
	if llm.ExtractJSON(res.Content, &out) == nil && valid(out) {
		return out, true
	}
	var zero T
	resp, err := c.Chat(ctx, llm.Request{Thinking: c.Light, MaxTokens: maxOutput, Messages: []llm.Message{
		{Role: "system", Content: "你负责把一段分析结论整理成指定的 JSON 格式。只做格式整理：不要增加、删除或改动任何数字和结论。只输出 JSON，不要输出别的文字。"},
		{Role: "user", Content: "格式要求：\n" + format + "\n\n需要整理的回答：\n" + res.Content},
	}})
	if err != nil {
		return zero, false
	}
	res.Usage.Add(resp.Usage)
	var fixed T
	if llm.ExtractJSON(resp.Message.Content, &fixed) == nil && valid(fixed) {
		return fixed, true
	}
	return zero, false
}

const errInternal = "内部出错了，请重试；如果反复出现，请查看服务日志"

// humanTrace 把工具调用变成“正在查什么”的中文短句。
func humanTrace(name, trace string) string {
	switch name {
	case "site_overview":
		return "读取核心数字"
	case "daily_metric":
		return "查看每天的变化"
	case "daily_breakdown":
		return "按维度查看每天的变化"
	case "breakdown":
		if i := strings.Index(trace, "拆分 "); i >= 0 {
			rest := trace[i+len("拆分 "):]
			dim := strings.Fields(rest)
			if len(dim) > 0 {
				return "按" + map[string]string{"pages": "页面", "landing": "入口页", "channels": "渠道", "sources": "来源网站", "countries": "国家",
					"devices": "设备", "browsers": "浏览器", "queries": "搜索词", "search_pages": "搜索页面"}[dim[0]] + "拆分"
			}
		}
		return "拆分数据"
	case "list_signals":
		return "读取需要关注的信号"
	case "ga_report":
		return "实时查询 GA"
	case "gsc_query":
		return "实时查询 Search Console"
	}
	return name
}

// stepTracker 把工具调用实时写成步骤，供页面轮询显示。
type stepTracker struct {
	mu    sync.Mutex
	steps []store.Step
	save  func([]store.Step)
}

func (t *stepTracker) on(name, trace string, done bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !done {
		t.steps = append(t.steps, store.Step{Title: humanTrace(name, trace), Detail: trace})
	} else if n := len(t.steps); n > 0 {
		t.steps[n-1].Done = true
		t.steps[n-1].Detail = trace
	}
	if t.save != nil {
		t.save(append([]store.Step(nil), t.steps...))
	}
}

func (t *stepTracker) snapshot() []store.Step {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]store.Step(nil), t.steps...)
}

func joinTrace(lines []string) string { return strings.Join(lines, "\n") }

// rangeText 给提示词用的日期描述。
func rangeText(r period.Range) string {
	return fmt.Sprintf("本期 %s ~ %s，对比上期 %s ~ %s", period.D(r.Start), period.D(r.End), period.D(r.PrevStart), period.D(r.PrevEnd))
}

// bg 给后台 goroutine 用：不跟着请求结束而取消，但带上同一个用户。
func bg(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(store.WithUser(context.Background(), store.UserID(ctx)), 6*time.Minute)
}

// googleUpdates 列出这段时间 Google 搜索的算法更新和故障，供 AI 排查原因时参考。
func (a *Agent) googleUpdates(ctx context.Context, from, to time.Time) string {
	gs, err := a.Store.Annotations(ctx, 0, from.AddDate(0, 0, -14), to)
	if err != nil || len(gs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n这段时间（含之前两周）Google 搜索的更新和故障，来自 Google 搜索状态页：\n")
	for _, g := range gs {
		end := "进行中"
		if g.EndDate != nil {
			end = period.D(*g.EndDate)
		}
		fmt.Fprintf(&sb, "- %s ~ %s：%s（%s）\n", period.D(g.Date), end, g.Detail, g.Label)
	}
	sb.WriteString("搜索流量的变化如果和这些时间重合，要考虑是算法更新的影响。\n")
	return sb.String()
}
