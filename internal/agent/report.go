package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"pingoo/internal/llm"
	"pingoo/internal/period"
	"pingoo/internal/store"
)

type ReportRow struct {
	SiteID     int64     `json:"site_id"`
	Name       string    `json:"name"`
	Visitors   int       `json:"visitors"`
	PrevVis    int       `json:"prev_visitors"`
	Clicks     int       `json:"clicks"`
	PrevClicks int       `json:"prev_clicks"`
	HasSearch  bool      `json:"has_search"`
	Status     string    `json:"status"`
	Spark      []float64 `json:"spark"`
}

type QueryMove struct {
	Query string  `json:"query"`
	Site  string  `json:"site"`
	From  float64 `json:"from"`
	To    float64 `json:"to"`
}

type ReportSnapshot struct {
	Rows      []ReportRow `json:"rows"`
	Total     ReportRow   `json:"total"`
	Up        []QueryMove `json:"up"`
	Down      []QueryMove `json:"down"`
	GSCLatest string      `json:"gsc_latest"`
	PrevStart string      `json:"prev_start"`
	PrevEnd   string      `json:"prev_end"`
}

type Story struct {
	Severity   string `json:"severity"`
	Site       string `json:"site"`
	SiteID     int64  `json:"site_id"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Confidence string `json:"confidence"`
	Basis      string `json:"basis"`
}

type Review struct {
	TodoID  int64  `json:"todo_id"`
	Text    string `json:"text"`
	Site    string `json:"site"`
	Status  string `json:"status"`
	DoneAt  string `json:"done_at"`
	Verdict string `json:"verdict"` // effective / unclear / ineffective / pending
	Before  string `json:"before"`
	After   string `json:"after"`
	Note    string `json:"note"`
}

type NextItem struct {
	Site   string `json:"site"`
	SiteID int64  `json:"site_id"`
	Text   string `json:"text"`
	Why    string `json:"why"`
}

type SavedNote struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Source  string `json:"source"` // chat / diagnosis
	Site    string `json:"site"`
	Date    string `json:"date"`
}

type ReportContent struct {
	Title   string      `json:"title"`
	Lede    string      `json:"lede"`
	Stories []Story     `json:"stories"`
	Reviews []Review    `json:"reviews"`
	Next    []NextItem  `json:"next"`
	Notes   []SavedNote `json:"notes,omitempty"`
}

// GenerateReport 创建一份报告并生成，写完才返回。调度器用它排队，一次只生成一份：
// 同时启动十几份报告很容易撞上 AI 接口的限流。
func (a *Agent) GenerateReport(ctx context.Context, siteID *int64, kind string, start, end time.Time, detail string) (int64, error) {
	c, b, err := a.begin(ctx, "report")
	if err != nil {
		return 0, err
	}
	id, err := a.Store.CreateReport(ctx, siteID, kind, start, end)
	if err != nil {
		b.finish(llm.Usage{}, true)
		return 0, err
	}
	bctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	a.buildReport(bctx, c, b, id, siteID, kind, start, end, detail)
	return id, nil
}

// StartReport 创建一份报告并在后台生成（站长手动点“立即生成”时用）。
func (a *Agent) StartReport(ctx context.Context, siteID *int64, kind string, start, end time.Time, detail string) (int64, error) {
	c, b, err := a.begin(ctx, "report")
	if err != nil {
		return 0, err
	}
	id, err := a.Store.CreateReport(ctx, siteID, kind, start, end)
	if err != nil {
		b.finish(llm.Usage{}, true)
		return 0, err
	}
	go func() {
		ctx, cancel := bg(ctx)
		defer cancel()
		a.buildReport(ctx, c, b, id, siteID, kind, start, end, detail)
	}()
	return id, nil
}

func (a *Agent) buildReport(ctx context.Context, c *llm.Client, b *bill, id int64, siteID *int64, kind string, start, end time.Time, detail string) {
	defer b.finish(llm.Usage{}, true) // 出错、panic 时全额退回；正常结算后不会重复
	r := period.Custom(start, end)
	sites, _ := a.Store.Sites(ctx)
	if siteID != nil {
		var only []store.Site
		for _, s := range sites {
			if s.ID == *siteID {
				only = append(only, s)
			}
		}
		sites = only
	} else {
		sites = RealSites(sites)
	}
	included := map[int64]bool{}
	for _, s := range sites {
		included[s.ID] = true
	}
	snap := ReportSnapshot{PrevStart: period.D(r.PrevStart), PrevEnd: period.D(r.PrevEnd)}
	// 结果用单独的 ctx 写回：ctx 超时后再用它写会失败，报告就一直显示“正在生成”
	finish := func(content any, trace string, u llm.Usage, errMsg string) {
		fctx, cancel := finishCtx()
		defer cancel()
		_ = a.Store.FinishReport(fctx, id, content, snap, trace, c.Model, u.PromptTokens, u.CompletionTokens, errMsg)
	}
	defer func() {
		if a.recovered("report", recover()) {
			finish(nil, "", llm.Usage{}, errInternal)
		}
	}()
	var sb strings.Builder
	fmt.Fprintf(&sb, "报告类型：%s。%s。\n\n核心数字（访客为去重人数，代码已经算好，直接引用）：\n", map[string]string{"weekly": "周报", "monthly": "月报"}[kind], rangeText(r))
	for _, s := range sites {
		row := snapshotRow(ctx, a.M, s, r)
		snap.Rows = append(snap.Rows, row)
		snap.Total.Visitors += row.Visitors
		snap.Total.PrevVis += row.PrevVis
		if row.HasSearch {
			snap.Total.HasSearch = true
			snap.Total.Clicks += row.Clicks
			snap.Total.PrevClicks += row.PrevClicks
		}
		fmt.Fprintf(&sb, "- %s（id=%d）：访客 %s", s.Name, s.ID, countLine(row.Visitors, row.PrevVis))
		if row.HasSearch {
			fmt.Fprintf(&sb, "；搜索点击 %s", countLine(row.Clicks, row.PrevClicks))
		}
		sb.WriteString("\n")
		// 搜索排名变化
		if rows, err := a.M.Search(ctx, s, r, "top"); err == nil {
			for _, q := range rows {
				if q.PrevPos > 0 && q.Impr >= 50 {
					mv := QueryMove{Query: q.Query, Site: s.Name, From: q.PrevPos, To: q.Pos}
					if q.PrevPos-q.Pos >= 1 {
						snap.Up = append(snap.Up, mv)
					} else if q.Pos-q.PrevPos >= 1 {
						snap.Down = append(snap.Down, mv)
					}
				}
			}
		}
		if rows, err := a.M.Search(ctx, s, r, "drops"); err == nil {
			for _, q := range rows {
				snap.Down = append(snap.Down, QueryMove{Query: q.Query, Site: s.Name, From: q.PrevPos, To: q.Pos})
			}
		}
	}
	sb.WriteString(a.reportDetails(ctx, sites, snap.Rows, r))
	snap.Up = topMoves(snap.Up, true)
	snap.Down = topMoves(snap.Down, false)
	if t := a.M.GSCLatest(ctx, 0); !t.IsZero() {
		snap.GSCLatest = period.D(t)
	}

	// 这段时间的信号
	sigs, _ := a.Store.OpenSignals(ctx, 0, int(time.Since(start).Hours()/24)+1)
	sb.WriteString("\n这段时间规则引擎检测到的信号：\n")
	n := 0
	for _, s := range sigs {
		// 按日期字符串比较：EventDate 从数据库读出来是 UTC 零点，start/end 是报告时区的零点，直接比时刻在西半球时区会差一天
		if d := period.D(s.EventDate); d < period.D(start) || d > period.D(end) || !included[s.SiteID] {
			continue
		}
		fmt.Fprintf(&sb, "- [%s][%s][%s] %s\n", s.Severity, s.SiteName, period.D(s.EventDate), s.Title)
		n++
	}
	if n == 0 {
		sb.WriteString("（没有）\n")
	}

	sb.WriteString(a.googleUpdates(ctx, start, end))

	// 上期建议
	var reviews []Review
	if prev, err := a.Store.PreviousReport(ctx, siteID, kind, start); err == nil {
		todos, _ := a.Store.TodosCreatedBetween(ctx, siteID, prev.CreatedAt.Add(-time.Minute), time.Now())
		if len(todos) > 0 {
			sb.WriteString("\n上一期以来的建议（请评估效果）：\n")
			for _, t := range topN(todos, 8) {
				rv := Review{TodoID: t.ID, Text: t.Text, Site: t.SiteName, Status: t.Status, Verdict: "pending"}
				if t.DoneAt != nil {
					rv.DoneAt = period.D(*t.DoneAt)
				}
				reviews = append(reviews, rv)
				fmt.Fprintf(&sb, "- [todo_id=%d][%s][%s] %s", t.ID, t.SiteName, map[string]string{"done": "已完成 " + rv.DoneAt, "open": "未完成"}[t.Status], t.Text)
				sb.WriteString("\n")
			}
		}
	}

	// 站长从问答、诊断里保存的分析
	notes, _ := a.Store.PendingNotes(ctx, siteID)
	var noteIDs []int64
	var saved []SavedNote
	if len(notes) > 0 {
		sb.WriteString("\n站长从问答和诊断里保存的分析（请判断现在是否仍然成立，必要时在 stories 里引用或更新）：\n")
		for _, n := range notes {
			body := n.Content
			if r := []rune(body); len(r) > 600 {
				body = string(r[:600]) + "…"
			}
			fmt.Fprintf(&sb, "- [%s][%s] %s：%s\n", period.D(n.CreatedAt), n.SiteName, n.Title, strings.ReplaceAll(body, "\n", " "))
			noteIDs = append(noteIDs, n.ID)
			saved = append(saved, SavedNote{Title: n.Title, Content: n.Content, Source: n.Source, Site: n.SiteName, Date: period.MD(n.CreatedAt)})
		}
	}

	length := map[string]string{
		"brief":    "写得简短：stories 最多 2 条，next 最多 3 条。",
		"standard": "stories 3~5 条，next 3~4 条。",
		"detailed": "写得详细：stories 5~7 条，每个网站至少提一句，next 4~6 条。",
	}[detail]
	task := fmt.Sprintf(`任务：写这一期的报告。数字以上面给出的为准；上面已经有的（核心数字、每天的变化、渠道、页面涨跌、搜索词、信号）不用再查，
只在需要解释某个重要变化的原因时用工具深挖，最多查 `+fmt.Sprint(reportMaxCalls)+` 次，互不依赖的查询一轮里一起发。%s
最后只输出 JSON：
{
 "title": "一句话结论，40 字以内",
 "lede": "2~3 句话：这一期最值得花时间的事，按优先级说",
 "stories": [{"severity":"critical|warn|good|chance|info","site":"网站名","site_id":0,"title":"一句话","body":"3~5 句话，说清发生了什么、证据、原因","confidence":"high|medium|low","basis":"依据了哪些数据，例如：GA 每日访客 · GSC 搜索词"}],
 "reviews": [{"todo_id":0,"verdict":"effective|unclear|ineffective|pending","before":"改动前的关键数字","after":"现在的数字","note":"一句话判断"}],
 "next": [{"site":"网站名","site_id":0,"text":"下期要做的事","why":"为什么、怎么判断效果"}]
}
stories 按重要程度排序；reviews 只针对上面列出的建议，没有就给空数组。`, length)
	msgs := []llm.Message{{Role: "system", Content: a.systemPrompt(ctx, "")}, {Role: "user", Content: sb.String() + "\n" + task}}
	runner := a.newRunner(c, b, nil)
	runner.MaxCalls = reportMaxCalls
	res, err := runner.Run(ctx, msgs)
	var content ReportContent
	parsed := false
	if err == nil {
		content, parsed = parseOrRepair(ctx, c, res, task, func(x ReportContent) bool { return x.Title != "" })
	}
	b.settle(res.Usage, err, parsed)
	if err != nil {
		finish(nil, joinTrace(res.Trace), res.Usage, err.Error())
		return
	}
	if !parsed {
		finish(nil, joinTrace(res.Trace), res.Usage, "AI 返回的格式不对，请重新生成")
		return
	}
	// 用数据库里的待办信息补全评估结果
	byID := map[int64]Review{}
	for _, rv := range content.Reviews {
		byID[rv.TodoID] = rv
	}
	for i, rv := range reviews {
		if got, ok := byID[rv.TodoID]; ok {
			reviews[i].Verdict, reviews[i].Before, reviews[i].After, reviews[i].Note = got.Verdict, got.Before, got.After, got.Note
		}
	}
	content.Reviews = reviews
	content.Notes = saved
	finish(content, joinTrace(res.Trace), res.Usage, "")
	fctx, cancel := finishCtx()
	defer cancel()
	a.Store.MarkNotesUsed(fctx, noteIDs, id)
}

// reportMaxCalls 是写一份报告最多查几次数据。每次都要看的明细已经写进提示里了，剩下的次数用来解释原因。
const reportMaxCalls = 12

// reportDetailSites 是全站报告里最多给几个网站附上明细（按访客变化的绝对值排）。
const reportDetailSites = 5

// reportDetails 是写报告时 AI 每次都会先查一遍的明细：每天的访客和搜索点击、渠道、页面涨跌、搜索词、入口页。
// 直接写进提示，省掉开头好几轮工具调用（没有它的时候，一份单站周报要先查 15 次才开始写）。
// 单个网站的报告给全；全站报告只给访客变化最大的几个网站，其余的 AI 需要时自己查。
func (a *Agent) reportDetails(ctx context.Context, sites []store.Site, rows []ReportRow, r period.Range) string {
	var sb strings.Builder
	if len(sites) == 1 {
		sb.WriteString(a.dailyTable(ctx, sites[0], r))
		sb.WriteString(a.siteDetails(ctx, sites[0], r))
		sb.WriteString(a.landingTop(ctx, sites[0], r))
		return sb.String()
	}
	change := func(i int) int {
		d := rows[i].Visitors - rows[i].PrevVis
		if d < 0 {
			d = -d
		}
		return d
	}
	idx := make([]int, 0, len(rows))
	for i := range rows {
		if i < len(sites) && change(i) > 0 {
			idx = append(idx, i)
		}
	}
	slices.SortStableFunc(idx, func(x, y int) int { return change(y) - change(x) })
	for _, i := range topN(idx, reportDetailSites) {
		if d := a.siteDetails(ctx, sites[i], r); d != "" {
			fmt.Fprintf(&sb, "\n===== %s（id=%d）的明细 =====%s", sites[i].Name, sites[i].ID, d)
		}
	}
	if sb.Len() > 0 {
		sb.WriteString("\n（以上是访客变化最大的几个网站；其他网站需要时用工具查。）\n")
	}
	return sb.String()
}

// RealSites 去掉演示网站：全站报告里不应该把模拟数据和真实数据加在一起。
// 只有演示网站时（还在试用）原样返回，这样手动生成报告仍然能看到效果。
func RealSites(sites []store.Site) []store.Site {
	var real []store.Site
	for _, s := range sites {
		if !s.Demo {
			real = append(real, s)
		}
	}
	if len(real) == 0 {
		return sites
	}
	return real
}

func statusOf(r ReportRow) string {
	d := 0.0
	if r.PrevVis > 0 {
		d = float64(r.Visitors)/float64(r.PrevVis) - 1
	}
	switch {
	case r.PrevVis >= 30 && d < -0.1:
		return "down"
	case r.PrevVis >= 30 && d > 0.1:
		return "up"
	}
	return "flat"
}

func topMoves(xs []QueryMove, up bool) []QueryMove {
	seen := map[string]bool{}
	var out []QueryMove
	for _, x := range xs {
		k := x.Site + "|" + x.Query
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, x)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1].From-out[j-1].To, out[j].From-out[j].To
			if (up && b > a) || (!up && b < a) {
				out[j-1], out[j] = out[j], out[j-1]
			}
		}
	}
	return topN(out, 3)
}

// WeeklyPeriod 返回 now 所在周之前一整周（周一到周日）。
func WeeklyPeriod(now time.Time) (time.Time, time.Time) {
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	wd := int(d.Weekday())
	if wd == 0 {
		wd = 7
	}
	thisMonday := d.AddDate(0, 0, -(wd - 1))
	return thisMonday.AddDate(0, 0, -7), thisMonday.AddDate(0, 0, -1)
}

// MonthlyPeriod 返回上一个自然月。
func MonthlyPeriod(now time.Time) (time.Time, time.Time) {
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return first.AddDate(0, -1, 0), first.AddDate(0, 0, -1)
}
