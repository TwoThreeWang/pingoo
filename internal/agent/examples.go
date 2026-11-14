package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pingoo/internal/metrics"
	"pingoo/internal/period"
	"pingoo/internal/store"
)

// ========== 演示账号的示例 ==========
//
// 演示账号不能调用 AI，但访客需要看到 AI 功能是什么样子。DemoExamples 按演示数据里的真实数字和“需要关注”
// 拼出几份示例：全部网站和每个网站“近 7 天”的洞察、上周的周报、每个网站一份诊断。不调用 AI、不花积分；
// 模型一栏写“示例”，正文里也说明是示例。演示数据每天刷新时会跟着重新生成。

// ExampleModel 是示例内容的“模型”名。
const ExampleModel = "示例（演示数据，不是 AI 生成）"

const exampleNote = "（演示示例：真实账号里，AI 会自己去查你的 GA 和 Search Console 数据，写出这样的分析。）"

// DemoExamples 为当前用户（演示账号）写入示例。ctx 里要带演示账号。
func DemoExamples(ctx context.Context, st *store.Store, m *metrics.Svc, sites []store.Site) error {
	if len(sites) == 0 {
		return nil
	}
	loc := sites[0].Location()
	type siteSum struct {
		site       store.Site
		cur, prev  int
		sigs       []store.Signal
		adRev, adP float64
	}
	var sums []siteSum
	for _, s := range sites {
		r := period.Resolve("d7", time.Now(), s.Location())
		cur, prev := m.Totals(ctx, s, r)
		sigs, _ := st.OpenSignals(ctx, s.ID, 30)
		x := siteSum{site: s, cur: cur.Users, prev: prev.Users, sigs: sigs}
		if m.HasAds(ctx, s.ID) {
			sq := r.Sequential()
			x.adRev, x.adP = m.AdTotals(ctx, s.ID, sq.Start, sq.End).Revenue, m.AdTotals(ctx, s.ID, sq.PrevStart, sq.PrevEnd).Revenue
		}
		sums = append(sums, x)

		// 网站的洞察
		content := InsightContent{
			Headline: fmt.Sprintf("近 7 天 %s 位访客，%s", metrics.Thousands(float64(cur.Users)), changeWords(float64(cur.Users), float64(prev.Users))),
			Lede:     siteLede(s, sigs) + exampleNote,
		}
		for _, sg := range topN(sigs, 3) {
			content.Items = append(content.Items, InsightItem{Type: itemType(sg.Severity), Title: sg.Title,
				Evidence: "规则检测 · " + period.D(sg.EventDate), SignalID: sg.ID, Section: sectionOf(sg.Kind)})
		}
		if x.adRev > 0 {
			content.Items = append(content.Items, InsightItem{Type: itemType2(x.adRev, x.adP), Section: "ads",
				Title:    fmt.Sprintf("广告收入 %.2f %s，%s", x.adRev, s.Currency, changeWords(x.adRev, x.adP)),
				Evidence: fmt.Sprintf("上周 %.2f · 先看每千次浏览收入有没有变，再看流量", x.adP)})
		}
		for _, sg := range topN(sigs, 2) {
			content.Todos = append(content.Todos, todoFor(sg))
		}
		if err := writeInsight(ctx, st, &s.ID, r, content); err != nil {
			return err
		}
		// 每个网站一份诊断：挑最值得查的那条提醒
		if sg := pickDiagnosable(sigs); sg != nil {
			if err := writeDiagnosis(ctx, st, s, *sg); err != nil {
				return err
			}
		}
	}

	// 全部网站的洞察
	r := period.Resolve("d7", time.Now(), loc)
	var total, totalPrev int
	best := sums[0]
	for _, x := range sums {
		total, totalPrev = total+x.cur, totalPrev+x.prev
		if x.cur-x.prev > best.cur-best.prev {
			best = x
		}
	}
	all := InsightContent{
		Headline: fmt.Sprintf("%d 个网站近 7 天共 %s 位访客，%s；%s 涨得最多", len(sums), metrics.Thousands(float64(total)),
			changeWords(float64(total), float64(totalPrev)), best.site.Name),
		Lede: "先看下面几条需要处理的变化，每条都能点进去看数据。" + exampleNote,
	}
	for _, x := range sums {
		if len(x.sigs) > 0 && len(all.Items) < 4 {
			sg := x.sigs[0]
			all.Items = append(all.Items, InsightItem{Type: itemType(sg.Severity), Title: x.site.Name + "：" + sg.Title,
				Evidence: "规则检测 · " + period.D(sg.EventDate), SignalID: sg.ID, Section: sectionOf(sg.Kind)})
			if len(all.Todos) < 3 {
				all.Todos = append(all.Todos, todoFor(sg))
			}
		}
	}
	if err := writeInsight(ctx, st, nil, r, all); err != nil {
		return err
	}
	return writeWeekly(ctx, st, m, sites, loc)
}

func changeWords(cur, prev float64) string {
	if prev <= 0 {
		return "上期没有数据"
	}
	p := (cur - prev) / prev * 100
	switch {
	case p >= 1:
		return fmt.Sprintf("比上期多 %.0f%%", p)
	case p <= -1:
		return fmt.Sprintf("比上期少 %.0f%%", -p)
	}
	return "和上期差不多"
}

func siteLede(s store.Site, sigs []store.Signal) string {
	if len(sigs) == 0 {
		return "这段时间整体平稳，没有需要特别处理的变化。"
	}
	return fmt.Sprintf("这段时间最值得注意的是：%s。", strings.TrimSuffix(sigs[0].Title, "。"))
}

func itemType(sev string) string {
	switch sev {
	case "critical", "warn":
		return "warn"
	case "good":
		return "good"
	}
	return "chance"
}

func itemType2(cur, prev float64) string {
	if cur >= prev {
		return "good"
	}
	return "warn"
}

// sectionOf 把提醒归到详情页的某一节（洞察里的“查看数据”会跳过去）。
func sectionOf(kind string) string {
	switch {
	case strings.HasPrefix(kind, "page_"):
		return "content"
	case strings.HasSuffix(kind, "_referrer"):
		return "sources"
	case kind == "rank_drop" || kind == "near_top" || kind == "low_ctr":
		return "search"
	case kind == "mobile_gap":
		return "audience"
	case strings.HasPrefix(kind, "ad_"):
		return "ads"
	}
	return "health"
}

// todoFor 按提醒的类型给一条能直接做的建议。
func todoFor(sg store.Signal) InsightTodo {
	why := strings.TrimSuffix(sg.Title, "。")
	text := map[string]string{
		"page_surge":      "趁热度还在，在这篇文章里加上相关文章和订阅入口，把一次性的访客留下来",
		"page_decline":    "更新这个页面：补上过时的内容，改一改标题和开头",
		"page_new":        "给这个新页面加几条站内链接，让搜索引擎更快收录",
		"new_referrer":    "去这个来源网站看看大家在讨论什么，必要时回复一下",
		"ai_referrer":     "检查被 AI 引用最多的页面，确认关键信息写得清楚、准确",
		"rank_drop":       "对比排名下滑的搜索词和页面内容，看看是不是被更新的文章超过了",
		"near_top":        "给这个搜索词对应的页面补充内容、多加几条内链，争取进入前 5",
		"low_ctr":         "改写这个页面在搜索结果里的标题和描述，让人更想点",
		"mobile_gap":      "用手机打开主要页面，检查排版、字号和弹窗",
		"not_found":       "给还在被访问的 404 页面加上跳转",
		"tracking_broken": "检查网站的统计代码还在不在、有没有被改动",
		"traffic_spike":   "找到这次访客暴涨的来源，想想怎么再来一次",
		"traffic_drop":    "看看访客下降集中在哪个来源、哪些页面",
		"ad_rpm_drop":     "检查最近有没有改动广告位、广告是否正常加载，再对比 AdSense 后台的单价",
		"ad_low_page":     "给这个页面加一个广告位，或者把现有广告挪到更显眼的位置",
	}[sg.Kind]
	if text == "" {
		text = "跟进这条变化"
	}
	return InsightTodo{Text: text, Why: why}
}

// pickDiagnosable 挑一条适合做诊断示例的提醒（异常、飙升、下滑优先）。
func pickDiagnosable(sigs []store.Signal) *store.Signal {
	for _, k := range []string{"traffic_spike", "traffic_drop", "page_surge", "ad_rpm_drop", "tracking_broken", "page_decline", "rank_drop"} {
		for i := range sigs {
			if sigs[i].Kind == k {
				return &sigs[i]
			}
		}
	}
	if len(sigs) > 0 {
		return &sigs[0]
	}
	return nil
}

func writeInsight(ctx context.Context, st *store.Store, siteID *int64, r period.Range, c InsightContent) error {
	id, err := st.CreateInsight(ctx, siteID, r.Key, r.Start, r.End)
	if err != nil {
		return err
	}
	return st.FinishInsight(ctx, id, c, "", ExampleModel, "")
}

func writeDiagnosis(ctx context.Context, st *store.Store, s store.Site, sg store.Signal) error {
	id, err := st.CreateDiagnosis(ctx, s.ID, "为什么："+sg.Title, map[string]any{"signal_id": sg.ID, "kind": sg.Kind, "date": period.D(sg.EventDate)})
	if err != nil {
		return err
	}
	steps := []store.Step{
		{Title: "确认变化有多大", Detail: "对比这一天和前 4 周同一天的访客、浏览量，确认不是统计误差", Done: true},
		{Title: "按来源拆开", Detail: "看变化集中在哪个渠道、哪个来源网站", Done: true},
		{Title: "按页面拆开", Detail: "看是哪几个页面带来的变化", Done: true},
		{Title: "对照搜索数据", Detail: "检查同期的搜索点击和排名有没有一起变", Done: true},
	}
	res := DiagnosisResult{Confidence: "medium",
		Conclusion:  strings.TrimSuffix(sg.Title, "。") + "。从规则给出的证据看，这次变化主要集中在少数来源和页面上，不是整站的问题。",
		Caveat:      "这是演示示例：真实账号里，AI 会按上面的步骤逐个调用数据，给出具体的数字和更确定的结论。",
		Suggestions: []string{todoFor(sg).Text}, Steps: steps}
	return st.FinishDiagnosis(ctx, id, steps, res, "", ExampleModel, "")
}

// writeWeekly 写一份上周（周一到周日）的全站周报。
func writeWeekly(ctx context.Context, st *store.Store, m *metrics.Svc, sites []store.Site, loc *time.Location) error {
	now := time.Now().In(loc)
	y := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	end := y.AddDate(0, 0, -int((y.Weekday()+7)%7)) // 最近的周日
	start := end.AddDate(0, 0, -6)
	r := period.Custom(start, end)
	snap := ReportSnapshot{PrevStart: period.D(r.PrevStart), PrevEnd: period.D(r.PrevEnd)}
	var content ReportContent
	var best ReportRow
	for _, s := range sites {
		row := snapshotRow(ctx, m, s, r)
		snap.Rows = append(snap.Rows, row)
		snap.Total.Visitors += row.Visitors
		snap.Total.PrevVis += row.PrevVis
		if row.HasSearch {
			snap.Total.HasSearch = true
			snap.Total.Clicks += row.Clicks
			snap.Total.PrevClicks += row.PrevClicks
		}
		if row.Visitors-row.PrevVis > best.Visitors-best.PrevVis || best.Name == "" {
			best = row
		}
		sigs, _ := st.OpenSignals(ctx, s.ID, 30)
		for _, sg := range sigs {
			if sg.EventDate.Before(start) || sg.EventDate.After(end.AddDate(0, 0, 1)) || len(content.Stories) >= 4 {
				continue
			}
			content.Stories = append(content.Stories, Story{Severity: sg.Severity, Site: s.Name, SiteID: s.ID, Title: sg.Title,
				Body:       "规则在 " + period.D(sg.EventDate) + " 检测到这条变化。真实的周报里，AI 会接着查清原因：是哪个来源、哪些页面、和上期比差在哪里，再说明它会不会持续。",
				Confidence: "medium", Basis: "规则检测"})
			if len(content.Next) < 3 {
				t := todoFor(sg)
				content.Next = append(content.Next, NextItem{Site: s.Name, SiteID: s.ID, Text: t.Text, Why: t.Why})
			}
		}
	}
	snap.Total.Name = "全部网站"
	content.Title = fmt.Sprintf("上周共 %s 位访客，%s", metrics.Thousands(float64(snap.Total.Visitors)), changeWords(float64(snap.Total.Visitors), float64(snap.Total.PrevVis)))
	content.Lede = fmt.Sprintf("%s 涨得最多（%s）。下面是上周最值得注意的几件事和这周可以做的事。%s",
		best.Name, changeWords(float64(best.Visitors), float64(best.PrevVis)), exampleNote)
	id, err := st.CreateReport(ctx, nil, "weekly", start, end)
	if err != nil {
		return err
	}
	return st.FinishReport(ctx, id, content, snap, "", ExampleModel, 0, 0, "")
}

// snapshotRow 是报告里一个网站的核心数字（访客、搜索点击、每日访客小图）。
func snapshotRow(ctx context.Context, m *metrics.Svc, s store.Site, r period.Range) ReportRow {
	cur, prev := m.Totals(ctx, s, r)
	row := ReportRow{SiteID: s.ID, Name: s.Name, Visitors: cur.Users, PrevVis: prev.Users}
	row.Clicks, row.PrevClicks, row.HasSearch = m.SearchTotals(ctx, s.ID, r, m.GSCLatest(ctx, s.ID))
	row.Spark = m.DailySeries(ctx, s.ID, r, "ga_daily", "sum(active_users)::float8").Cur
	row.Status = statusOf(row)
	return row
}
