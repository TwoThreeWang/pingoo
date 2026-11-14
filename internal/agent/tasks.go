package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"pingoo/internal/llm"
	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/store"
)

// ========== 洞察 ==========

type InsightItem struct {
	Type     string `json:"type"` // good / warn / chance
	Title    string `json:"title"`
	Evidence string `json:"evidence"`
	Guess    bool   `json:"guess"`
	SignalID int64  `json:"signal_id,omitempty"`
	Section  string `json:"section,omitempty"` // content / sources / search / audience / health
}

type InsightTodo struct {
	Text string `json:"text"`
	Why  string `json:"why"`
	ID   int64  `json:"id,omitempty"`
}

type InsightContent struct {
	Headline string        `json:"headline"`
	Lede     string        `json:"lede"`
	Items    []InsightItem `json:"items"`
	Todos    []InsightTodo `json:"todos"`
}

// GenerateInsight 为某个网站（site 为 nil 表示全部网站）生成洞察。同步执行，调用方自己决定是否放到后台。
// InsightTTL 是洞察的有效期：超过这个时间，默认时间范围的洞察会自动重新生成。
const InsightTTL = 20 * time.Hour

// InsightMaxFails：洞察连续失败这么多次之后不再自动重试，等站长手动点“重新生成”（成功一次就重新计数）。
// 自动重试是每次同步后都来一次：模型或接口一直有问题、或者网站的内容总被服务商的审核拦截时，
// 不设上限就会每天白跑；被审核拦截的还照常计费。
const InsightMaxFails = 3

// DefaultInsightRange 返回会自动生成洞察的时间范围：全部网站和网站详情都是近 7 天（页面默认打开的范围）。
// 其他时间范围只在站长点“生成洞察”时生成，避免切换范围就花 token。
func DefaultInsightRange(site *store.Site) string {
	return "d7"
}

// InsightKey 是“这份洞察正在生成”的标记：网站洞察按网站区分，全部网站的简报按用户区分。
// 生成时占用它、页面用 Busy 判断是不是在生成，两边必须用同一个，否则页面会把正在生成的当成“被中断了”。
func InsightKey(uid int64, site *store.Site, rangeKey string) string {
	if site != nil {
		return fmt.Sprintf("insight:%d:%s", site.ID, rangeKey)
	}
	return fmt.Sprintf("insight:all:%d:%s", uid, rangeKey)
}

func (a *Agent) GenerateInsight(ctx context.Context, site *store.Site, r period.Range) (id int64, err error) {
	var sid *int64
	key := InsightKey(store.UserID(ctx), site, r.Key)
	if site != nil {
		ctx = store.WithUser(ctx, site.UserID) // 后台生成时 context 里可能还没有用户
		sid = &site.ID
	}
	if !a.claim(key) {
		return 0, nil
	}
	defer a.release(key)
	c, b, err := a.begin(ctx, "insight")
	if err != nil {
		return 0, err
	}
	id, err = a.Store.CreateInsight(ctx, sid, r.Key, r.Start, r.End)
	if err != nil {
		b.finish(llm.Usage{}, true)
		return 0, err
	}
	finish := func(content any, trace, errMsg string) error {
		fctx, cancel := finishCtx()
		defer cancel()
		return a.Store.FinishInsight(fctx, id, content, trace, c.Model, errMsg)
	}
	defer func() {
		if a.recovered("insight", recover()) {
			_ = finish(nil, "", errInternal)
			err = errors.New(errInternal)
		}
		b.finish(llm.Usage{}, true) // 正常结束时已经结算过，这里不会重复；出错、panic 时全额退回
	}()
	ctxText, err := a.insightContext(ctx, site, r)
	if err != nil {
		_ = finish(nil, "", err.Error())
		return id, err
	}
	var task string
	if site == nil {
		task = `任务：给站长写一段“全部网站”的简报开头。
各网站的核心数字和信号上面已经给了，不用再查；只在需要拆原因时（例如某个网站大涨大跌）再用工具查渠道、页面或每日数据。然后只输出 JSON：
{"headline":"一句话标题，30 字以内，点出最重要的一两件事","lede":"2~3 句话，说清楚整体怎么样、原因是什么、最该先做什么"}`
	} else {
		task = `任务：为这个网站写“洞察”。核心数字、渠道、页面涨跌、搜索词和信号上面已经给了，不用再查；
需要深挖时再用工具（例如某篇文章飙升，查它的流量从哪来；某个指标下滑，拆到具体来源/入口页/搜索词，或者看每日变化）。
最后只输出 JSON，不要输出别的文字：
{
 "headline": "一句话标题，35 字以内，点出最重要的变化",
 "lede": "2 句话：变化的原因，以及什么会持续、什么不会",
 "items": [
   {"type":"good|warn|chance", "title":"一句话结论，带关键数字", "evidence":"证据：2~3 个具体数字，用 · 分隔", "guess": false, "signal_id": 0, "section":"content|sources|search|audience|health"}
 ],
 "todos": [ {"text":"具体要做的事", "why":"为什么、怎么判断效果"} ]
}
要求：items 2~4 条（good=亮点，warn=问题，chance=机会）；guess=true 表示里面有推测；如果某条对应上面给出的信号，signal_id 填它的 id；todos 2~4 条，按优先级排序。`
	}
	msgs := []llm.Message{
		{Role: "system", Content: a.systemPrompt(ctx, "")},
		{Role: "user", Content: ctxText + "\n\n" + task},
	}
	runner := a.newRunner(c, b, nil)
	runner.Tools = insightTools()
	// 洞察是概括，数据大多已经给了：最多 5 轮、6 次查询就写结论，深挖交给诊断
	runner.MaxSteps = 7
	runner.MaxCalls = 6
	res, err := runner.Run(ctx, msgs)
	var content InsightContent
	parsed := false
	if err == nil {
		content, parsed = parseOrRepair(ctx, c, res, task, func(x InsightContent) bool { return x.Headline != "" })
	}
	b.settle(res.Usage, err, parsed)
	if err != nil {
		_ = finish(nil, joinTrace(res.Trace), err.Error())
		return id, err
	}
	if !parsed {
		msg := "AI 返回的格式不对，请重新生成"
		_ = finish(nil, joinTrace(res.Trace), msg)
		return id, errors.New(msg)
	}
	if site != nil {
		for i, t := range content.Todos {
			if strings.TrimSpace(t.Text) == "" {
				continue
			}
			tid, err := a.Store.AddTodo(ctx, store.Todo{SiteID: sid, Source: "insight", SourceID: id, Text: t.Text, Why: t.Why})
			if err == nil {
				content.Todos[i].ID = tid
			}
		}
	}
	return id, finish(content, joinTrace(res.Trace), "")
}

// insightContext 把规则引擎已经算好的东西交给 AI，省得它从头查。
func (a *Agent) insightContext(ctx context.Context, site *store.Site, r period.Range) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "时间范围：%s。\n", rangeText(r))
	var sigs []store.Signal
	var err error
	if site == nil {
		sites, _ := a.Store.Sites(ctx)
		sb.WriteString("\n各网站核心数字（访客为去重人数）：\n")
		for _, s := range sites {
			sr := period.Resolve(r.Key, time.Now(), s.Location())
			cur, prev := a.M.Totals(ctx, s, sr)
			c1, c2, ok := a.M.SearchTotals(ctx, s.ID, sr, a.M.GSCLatest(ctx, s.ID))
			fmt.Fprintf(&sb, "- %s（id=%d）：访客 %s", s.Name, s.ID, countLine(cur.Users, prev.Users))
			fmt.Fprintf(&sb, "，浏览量 %s", countLine(cur.PV, prev.PV))
			if cur.Sessions > 0 {
				_, _, ch := tileText(metrics.Tile{Kind: "pct", Cur: cur.EngRate(), Prev: prev.EngRate()})
				fmt.Fprintf(&sb, "，认真阅读比例 本期 %.1f%%，上期 %.1f%%（%s）", cur.EngRate()*100, prev.EngRate()*100, ch)
			}
			if ok {
				fmt.Fprintf(&sb, "，搜索点击 %s", countLine(c1, c2))
			}
			if !sr.Hourly && a.M.HasAds(ctx, s.ID) {
				sq := sr.Sequential()
				t := metrics.AdTiles(a.M.AdTotals(ctx, s.ID, sq.Start, sq.End), a.M.AdTotals(ctx, s.ID, sq.PrevStart, sq.PrevEnd))[0]
				c, p, ch := tileText(t)
				fmt.Fprintf(&sb, "，广告收入 本期 %s，上期 %s（%s，%s）", c, p, ch, s.Currency)
			}
			sb.WriteString("\n")
		}
		sigs, err = a.Store.OpenSignals(ctx, 0, 14)
	} else {
		fmt.Fprintf(&sb, "网站：%s（id=%d，%s）。\n", site.Name, site.ID, site.Domain)
		if site.Description != "" {
			fmt.Fprintf(&sb, "站长写的背景：%s\n", site.Description)
		}
		sb.WriteString("\n核心数字：\n")
		for _, t := range a.M.Tiles(ctx, *site, r) {
			if !t.HasVal {
				fmt.Fprintf(&sb, "- %s：%s\n", t.Label, t.Note)
				continue
			}
			c, p, ch := tileText(t)
			fmt.Fprintf(&sb, "- %s：本期 %s，上期 %s（%s）\n", t.Label, c, p, ch)
		}
		sb.WriteString(a.siteDetails(ctx, *site, r))
		sb.WriteString(a.adDetails(ctx, *site, r))
		sigs, err = a.Store.OpenSignals(ctx, site.ID, 30)
	}
	if err != nil {
		return "", err
	}
	sb.WriteString(a.googleUpdates(ctx, r.Start, r.End))
	if len(sigs) > 0 {
		sb.WriteString("\n规则引擎检测到的信号（数字是准确的）：\n")
		for _, s := range topN(sigs, 12) {
			fmt.Fprintf(&sb, "- [id=%d][%s][%s][%s] %s\n", s.ID, s.Severity, s.SiteName, period.D(s.EventDate), s.Title)
		}
	} else {
		sb.WriteString("\n规则引擎这段时间没有检测到明显的信号。\n")
	}
	return sb.String(), nil
}

// adDetails 是网站上广告收益（AdSense 等）的摘要：合计、每千次浏览收入、收入最高的页面、流量大收入低的页面。
// 没有广告数据的网站返回空。
func (a *Agent) adDetails(ctx context.Context, site store.Site, r period.Range) string {
	if r.Hourly || !a.M.HasAds(ctx, site.ID) {
		return ""
	}
	var sb strings.Builder
	top := a.M.Ads(ctx, site, r, "top")
	fmt.Fprintf(&sb, "\n广告收益（网站上展示的广告，单位 %s）：\n", site.Currency)
	for _, t := range metrics.AdTiles(top.Cur, top.Prev) {
		c, p, ch := tileText(t)
		fmt.Fprintf(&sb, "- %s：本期 %s，上期 %s（%s）\n", t.Label, c, p, ch)
	}
	if len(top.Pages) > 0 {
		sb.WriteString("收入最高的页面：\n")
		for _, p := range topN(top.Pages, 5) {
			fmt.Fprintf(&sb, "- %s：收入 %.2f（上期 %.2f），浏览 %d，每千次浏览收入 %.2f\n", p.Path, p.Revenue, p.PrevRevenue, p.Views, p.RPM)
		}
	}
	if low := a.M.Ads(ctx, site, r, "low").Pages; len(low) > 0 {
		sb.WriteString("浏览多但广告收入低的页面（每千次浏览收入不到全站一半）：\n")
		for _, p := range topN(low, 3) {
			fmt.Fprintf(&sb, "- %s：浏览 %d，每千次浏览收入 %.2f（全站的 %.0f%%）\n", p.Path, p.Views, p.RPM, p.RPMVsSite*100)
		}
	}
	return sb.String()
}

// ========== 诊断 ==========

type DiagnosisResult struct {
	Steps       []store.Step `json:"steps"`
	Confidence  string       `json:"confidence"` // high / medium / low
	Conclusion  string       `json:"conclusion"`
	Caveat      string       `json:"caveat"`
	Suggestions []string     `json:"suggestions"`
}

// diagnosisFormat 是诊断结果的 JSON 格式（提示词里用一次，格式不对让模型整理时再用一次）。
const diagnosisFormat = `{"steps":[{"title":"这一步做了什么，6~10 个字","detail":"发现了什么，带具体数字，一两句话"}],
 "confidence":"high|medium|low",
 "conclusion":"一两句话的结论",
 "caveat":"如果结论里有推测或数据的局限，写在这里；没有就留空",
 "suggestions":["具体、可执行的建议，2~4 条"]}`

// StartDiagnosis 创建诊断记录并在后台执行，立刻返回 id。
func (a *Agent) StartDiagnosis(ctx context.Context, site store.Site, title, question string, target map[string]any) (int64, error) {
	ctx = store.WithUser(ctx, site.UserID)
	c, b, err := a.begin(ctx, "diagnose")
	if err != nil {
		return 0, err
	}
	id, err := a.Store.CreateDiagnosis(ctx, site.ID, title, target)
	if err != nil {
		b.finish(llm.Usage{}, true)
		return 0, err
	}
	go func() {
		ctx, cancel := bg(ctx)
		defer cancel()
		defer b.finish(llm.Usage{}, true) // 出错、panic 时全额退回；正常结算后不会重复
		tr := &stepTracker{save: func(s []store.Step) { a.Store.SetDiagnosisSteps(ctx, id, s) }}
		finish := func(steps []store.Step, result any, trace, errMsg string) {
			fctx, cancel := finishCtx()
			defer cancel()
			_ = a.Store.FinishDiagnosis(fctx, id, steps, result, trace, c.Model, errMsg)
		}
		defer func() {
			if a.recovered("diagnose", recover()) {
				finish(tr.snapshot(), nil, "", errInternal)
			}
		}()
		prompt := fmt.Sprintf(`请诊断这个问题：%s
网站：%s（id=%d）。%s

下面是这个网站现成的数据，每次诊断都要先看这几样，所以直接给你了，不用再查：
%s
诊断思路：
1. 先用上面的数据排除统计问题（是不是所有渠道同时归零、搜索点击是否正常），看变化集中在哪个渠道、从哪天开始；
2. 再用工具往下拆：具体的来源、页面、搜索词、国家或设备。要看“哪天开始、出在哪里”时用 daily_breakdown；
3. 搜索相关的变化，用 Search Console 交叉验证：是排名变了、曝光变了，还是点击率变了；
4. 排除周期因素（周末、节假日）。
上面的时间范围不合适时（例如问题说的是更早的日期），换成合适的日期再查。最多可以查 %d 次，互不依赖的查询一轮里一起发。

最后只输出 JSON（steps 写 3~6 步，只写对结论有用的）：
%s`, question, site.Name, site.ID, describeTarget(target), a.diagnosisContext(ctx, site, target), diagnoseMaxCalls, diagnosisFormat)
		msgs := []llm.Message{{Role: "system", Content: a.systemPrompt(ctx, "")}, {Role: "user", Content: prompt}}
		runner := a.newRunner(c, b, tr.on)
		runner.MaxCalls = diagnoseMaxCalls
		res, err := runner.Run(ctx, msgs)
		var out DiagnosisResult
		parsed := false
		if err == nil {
			out, parsed = parseOrRepair(ctx, c, res, diagnosisFormat, func(x DiagnosisResult) bool { return x.Conclusion != "" })
		}
		b.settle(res.Usage, err, parsed)
		if err != nil {
			finish(tr.snapshot(), nil, joinTrace(res.Trace), err.Error())
			return
		}
		if !parsed {
			finish(tr.snapshot(), nil, joinTrace(res.Trace), "AI 返回的格式不对，请重试")
			return
		}
		for i := range out.Steps {
			out.Steps[i].Done = true
		}
		finish(out.Steps, out, joinTrace(res.Trace), "")
	}()
	return id, nil
}

// diagnoseMaxCalls 是一次诊断最多查几次数据。开头必查的几样已经写进提示里了，剩下的次数用来深挖；
// 没有上限的时候出现过一次诊断查 18 次、其中好几次是同一个维度换日期重查。
const diagnoseMaxCalls = 12

// diagnosisContext 是诊断开头每次都要查的数据：核心数字、每天的访客和搜索点击、渠道、页面涨跌、入口页、搜索词、信号。
// 直接写进提示，省掉开头两三轮工具调用（每多一轮，模型都要多想一遍、多发一遍前面的内容）。
// 时间范围用近 28 天对比再往前 28 天：够看出趋势，也盖得住规则引擎报信号的日期。
func (a *Agent) diagnosisContext(ctx context.Context, site store.Site, target map[string]any) string {
	r := period.Resolve("d28", time.Now(), site.Location())
	var sb strings.Builder
	tiles := func(title string, r period.Range) {
		fmt.Fprintf(&sb, "%s（%s）：\n", title, rangeText(r))
		for _, t := range a.M.Tiles(ctx, site, r) {
			if !t.HasVal {
				fmt.Fprintf(&sb, "- %s：%s\n", t.Label, t.Note)
				continue
			}
			c, p, ch := tileText(t)
			fmt.Fprintf(&sb, "- %s：本期 %s，上期 %s（%s）\n", t.Label, c, p, ch)
		}
	}
	// 近 7 天的也给：页面默认看近 7 天，洞察和提醒里的数字多半是这个范围的，问题里引用的数字要能对得上
	tiles("近 7 天的核心数字", period.Resolve("d7", time.Now(), site.Location()))
	sb.WriteString("\n")
	tiles("近 28 天的核心数字", r)
	fmt.Fprintf(&sb, "\n下面的明细都是近 28 天的（%s）。\n", rangeText(r))
	sb.WriteString(a.dailyTable(ctx, site, r))
	sb.WriteString(a.siteDetails(ctx, site, r))
	sb.WriteString(a.landingTop(ctx, site, r))
	if kind, _ := target["kind"].(string); strings.HasPrefix(kind, "ad_") {
		sb.WriteString(a.adDetails(ctx, site, r))
	}
	sb.WriteString(a.googleUpdates(ctx, r.PrevStart, r.End))
	if sigs, err := a.Store.OpenSignals(ctx, site.ID, 30); err == nil && len(sigs) > 0 {
		sb.WriteString("\n规则引擎检测到的信号（数字是准确的）：\n")
		for _, s := range topN(sigs, 12) {
			fmt.Fprintf(&sb, "- [%s][%s] %s\n", s.Severity, period.D(s.EventDate), s.Title)
		}
	}
	return sb.String()
}

// dailyTable 是每天的访客和搜索点击，上期和本期连在一起：变化从哪天开始，一眼能看出来。
func (a *Agent) dailyTable(ctx context.Context, site store.Site, r period.Range) string {
	both := period.Custom(r.PrevStart, r.End)
	vis := a.M.DailySeries(ctx, site.ID, both, "ga_daily", "sum(active_users)::float8")
	hasSearch := site.GSCSite != "" || site.Demo
	var clicks []float64
	gscLatest := a.M.GSCLatest(ctx, site.ID)
	if hasSearch {
		clicks = a.M.DailySeries(ctx, site.ID, both, "gsc_daily", "sum(clicks)::float8").Cur
	}
	var sb strings.Builder
	sb.WriteString("\n每天的数（上期 + 本期）：\ndate,访客")
	if hasSearch {
		sb.WriteString(",搜索点击")
	}
	sb.WriteString("\n")
	for i, d := range vis.Dates {
		fmt.Fprintf(&sb, "%s,%v", period.D(d), round(vis.Cur[i]))
		if hasSearch {
			if i < len(clicks) && !d.After(gscLatest) {
				fmt.Fprintf(&sb, ",%v", round(clicks[i]))
			} else {
				sb.WriteString(",") // 搜索数据有两三天延迟，还没有的日子留空，不能写成 0
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// landingTop 是本期访问次数最多的入口页。
func (a *Agent) landingTop(ctx context.Context, site store.Site, r period.Range) string {
	rows, err := a.M.Content(ctx, site, r, "landing")
	if err != nil || len(rows) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n访问次数最多的入口页（本期）：\n")
	for _, p := range topN(rows, 8) {
		fmt.Fprintf(&sb, "- %s：%s 次，认真阅读 %.0f%%\n", p.Path, metrics.Thousands(float64(p.Sessions)), p.EngRate*100)
	}
	return sb.String()
}

func describeTarget(t map[string]any) string {
	if len(t) == 0 {
		return ""
	}
	b, _ := json.Marshal(t)
	return "相关信息：" + string(b)
}

// ========== 问答 ==========

// Answer 在后台回答对话里最新的问题，把结果写进 msgID 那条助手消息。ctx 里要有对话的主人。
func (a *Agent) Answer(ctx context.Context, conv store.Conversation, msgID int64) {
	go func() {
		ctx, cancel := bg(ctx)
		defer cancel()
		msg, err := a.Store.Message(ctx, msgID)
		if err != nil {
			return
		}
		// 最后的结果用单独的 ctx 写：ctx 超时后再用它写会失败，消息就一直是“回答中”，这个对话再也问不了新问题
		save := func() {
			fctx, cancel := finishCtx()
			defer cancel()
			_ = a.Store.UpdateMessage(fctx, msg)
		}
		fail := func(e error) {
			msg.Status, msg.Error = "error", e.Error()
			save()
		}
		defer func() {
			if a.recovered("chat", recover()) {
				fail(errors.New(errInternal))
			}
		}()
		c, b, err := a.begin(ctx, "chat")
		if err != nil {
			fail(err)
			return
		}
		defer b.finish(llm.Usage{}, true) // 出错、panic 时全额退回；正常结算后不会重复
		history, err := a.Store.Messages(ctx, conv.ID)
		if err != nil {
			fail(err)
			return
		}
		scope := "这次对话针对全部网站。"
		if conv.SiteID != nil {
			scope = fmt.Sprintf("这次对话主要针对网站「%s」（id=%d），除非用户问到别的网站。", conv.SiteName, *conv.SiteID)
		}
		extra := scope + `
回答格式：用 Markdown。先给结论，再给依据；需要对比多个数字时用表格。不要太长，能 300 字说清就不要写 800 字。
回答的最后一行单独写“追问建议：”，后面跟 2~3 个用户接下来可能想问的问题，用 | 分隔。
前面几轮查到的数据还留在对话里，能直接用的就不要重查。历史回答末尾的【系统备注】是系统自动加的，说明那一轮查过哪些数据（原始结果已经省略，需要时可以重查），你自己的回答里不要写这种备注。`
		logs := a.toolLogs(ctx, conv.ID)
		msgs := append([]llm.Message{{Role: "system", Content: a.systemPrompt(ctx, extra)}}, chatHistory(history, logs, msgID)...)
		tr := &stepTracker{save: func(s []store.Step) {
			msg.Steps = s
			_ = a.Store.UpdateMessage(ctx, msg)
		}}
		res, err := a.newRunner(c, b, tr.on).Run(ctx, msgs)
		b.settle(res.Usage, err, true)
		msg.Trace = joinTrace(res.Trace)
		if err != nil {
			fail(err)
			return
		}
		content, followups := splitFollowups(res.Content)
		msg.Content, msg.Followups, msg.Status, msg.Steps = content, followups, "done", tr.snapshot()
		save()
		a.keepToolLog(ctx, msg.ID, res.Messages, logs, b.m.CacheCheap())
	}()
}

// ---------- 问答的上下文 ----------
//
// 每个问题都要把之前的对话重新发一遍。怎么带，既影响花费，也影响回答的质量：
//   - 最近几轮查到的原始数据原样带上。追问（“继续”“那搜索词呢”“展开讲第二点”）十有八九要用，不带的话
//     AI 只能把上一轮的查询重做一遍；原样带上还能命中提示缓存（和上一轮发出去的内容一字不差），价格只有原价的零头。
//   - 更早的轮次只留回答和一行“查过什么”。回答本身就是那批数据里和问题有关的部分的浓缩，
//     原始表格里剩下的大多和后面的问题无关，带着只会让上下文越来越长、AI 越来越抓不住重点。
//   - 什么时候把一轮从“原样”降成“一行备注”是存下来的状态（清空那一轮的 tool_log），而且一次多清几轮：
//     要是每个问题都重新划一次线，前面的内容每次都在变，提示缓存就一直命不中。
//   - 带多少看缓存有多便宜。命中缓存的输入几乎不要钱的模型（MiMo 是原价的 2%）多带几轮；
//     缓存价是原价一两成的（通义千问 20%、DeepSeek 10% 左右）只带最近一两轮：每一次调用都要为带着的数据付这一两成，
//     带得太多，反而比偶尔重查一次贵。
const (
	historyHigh  = 24 // 历史消息超过这么多条时，只带最近的 16～23 条（每 8 条才挪一次起点，同样是为了缓存）
	historyKeep  = 16
	historyChars = 30000 // 历史（回答加上带着的原始数据）最多这么多字，超过就再往后挪起点。
	// 上下文太长，AI 抓不住重点；有的模型还按单次请求的输入长度分档计价（qwen3.7-flash 超过 32K token 贵 3 倍）。
)

// logBudget 是对话里留多少工具原始数据：超过 high 就清掉最早的几轮，一直清到不超过 low；
// 单独一轮就超过 max 的不留（查了太多东西，下一问带着它不划算）。单位是字。
type logBudget struct{ high, low, max int }

func toolLogBudget(cacheCheap bool) logBudget {
	if cacheCheap {
		return logBudget{high: 24000, low: 12000, max: 40000}
	}
	return logBudget{high: 12000, low: 4000, max: 20000}
}

// toolLogs 读出对话里还留着的工具往来。读不出来就当没有（只是省钱的手段，不能因为它让问答失败）。
func (a *Agent) toolLogs(ctx context.Context, convID int64) map[int64][]llm.Message {
	raw, err := a.Store.ToolLogs(ctx, convID)
	if err != nil {
		a.Log.Warn("读取问答的工具记录失败", "conv", convID, "err", err)
		return nil
	}
	out := map[int64][]llm.Message{}
	for id, b := range raw {
		var ms []llm.Message
		if json.Unmarshal(b, &ms) == nil && len(ms) > 0 {
			out[id] = ms
		}
	}
	return out
}

// chatHistory 把之前的对话变成发给模型的消息。skipID 是正在回答的那条（还没有内容）。
func chatHistory(history []store.Message, logs map[int64][]llm.Message, skipID int64) []llm.Message {
	var usable []store.Message
	for _, h := range history {
		if h.ID == skipID || h.Status != "done" || strings.TrimSpace(h.Content) == "" {
			continue
		}
		usable = append(usable, h)
	}
	start := 0
	if n := len(usable); n > historyHigh {
		start = (n - historyKeep) / 8 * 8
	}
	size := func(h store.Message) int { return min(len([]rune(h.Content)), 3000) + logChars(logs[h.ID]) }
	total := 0
	for _, h := range usable[start:] {
		total += size(h)
	}
	for total > historyChars && start+8 < len(usable) { // 太长了：一次往后挪 8 条
		for _, h := range usable[start : start+8] {
			total -= size(h)
		}
		start += 8
	}
	if start > 0 && usable[start].Role != "user" && start+1 < len(usable) {
		start++ // 从一个提问开始，不要从半截回答开始
	}
	var msgs []llm.Message
	for _, h := range usable[start:] {
		// 历史消息每条最多 3000 字：每个问题都会把整段对话重新发一遍，不截断的话越聊越贵
		m := llm.Message{Role: h.Role, Content: clipRunes(h.Content, 3000)}
		if h.Role == "assistant" {
			if log := logs[h.ID]; len(log) > 0 {
				// 要回传思考过程的服务商，工具往来的最后一条是最终回答（带着那一段思考）：正文用存下来的，思考原样带上
				if last := log[len(log)-1]; last.Role == "assistant" && len(last.ToolCalls) == 0 {
					m.ReasoningContent = last.ReasoningContent
					log = log[:len(log)-1]
				}
				msgs = append(msgs, log...)
			} else if note := traceNote(h.Trace); note != "" {
				m.Content += "\n\n" + note
			}
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// traceNote 把一轮查过的数据写成一行备注（原始结果已经不在对话里了）。
func traceNote(trace string) string {
	var items []string
	for _, l := range strings.Split(trace, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			items = append(items, clipRunes(l, 80))
		}
	}
	if len(items) == 0 {
		return ""
	}
	more := ""
	if len(items) > 12 {
		more = fmt.Sprintf(" 等 %d 项", len(items))
		items = items[:12]
	}
	return "【系统备注：这一轮查过 " + strings.Join(items, "；") + more + "。原始结果已经省略。】"
}

func logChars(ms []llm.Message) (n int) {
	for _, m := range ms {
		n += m.Chars()
	}
	return n
}

// staleToolLogs 决定清掉哪几轮的原始数据。sizes 是每一轮的字数（消息 id → 字数，包括刚回答完的这一轮）。
// 总量没超过 b.high 就都留着；超过了从最早的清起，清到不超过 b.low，但最新的一轮总是留着。
func staleToolLogs(sizes map[int64]int, b logBudget) (drop []int64) {
	ids := make([]int64, 0, len(sizes))
	total := 0
	for id, n := range sizes {
		ids = append(ids, id)
		total += n
	}
	if total <= b.high {
		return nil
	}
	slices.Sort(ids)
	for _, id := range ids[:len(ids)-1] {
		if total <= b.low {
			break
		}
		total -= sizes[id]
		drop = append(drop, id)
	}
	return drop
}

// keepToolLog 存下这一轮的工具往来，供下一个问题接着用；留着的原始数据太多时清掉最早的几轮。
func (a *Agent) keepToolLog(ctx context.Context, msgID int64, log []llm.Message, prev map[int64][]llm.Message, cacheCheap bool) {
	fctx, cancel := finishCtx()
	defer cancel()
	fctx = store.WithUser(fctx, store.UserID(ctx))
	budget := toolLogBudget(cacheCheap)
	sizes := map[int64]int{}
	for id, ms := range prev {
		sizes[id] = logChars(ms)
	}
	if n := logChars(log); len(log) > 0 && n <= budget.max {
		raw, _ := json.Marshal(log)
		if err := a.Store.SetToolLog(fctx, msgID, raw); err != nil {
			a.Log.Warn("保存问答的工具记录失败", "msg", msgID, "err", err)
			return
		}
		sizes[msgID] = n
	}
	for _, id := range staleToolLogs(sizes, budget) {
		_ = a.Store.SetToolLog(fctx, id, nil)
	}
}

func splitFollowups(s string) (string, []string) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-3; i-- {
		l := strings.TrimSpace(strings.Trim(lines[i], "*_ "))
		for _, p := range []string{"追问建议：", "追问建议:"} {
			if strings.HasPrefix(l, p) {
				var out []string
				for _, q := range strings.Split(strings.TrimPrefix(l, p), "|") {
					if q = strings.TrimSpace(q); q != "" {
						out = append(out, q)
					}
				}
				return strings.TrimSpace(strings.Join(lines[:i], "\n")), topN(out, 3)
			}
		}
	}
	return strings.TrimSpace(s), nil
}

// TitleFor 用问题本身生成对话标题。
func TitleFor(q string) string {
	q = strings.TrimSpace(strings.ReplaceAll(q, "\n", " "))
	r := []rune(q)
	if len(r) > 28 {
		return string(r[:28]) + "…"
	}
	return q
}

func topN[T any](xs []T, n int) []T {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
