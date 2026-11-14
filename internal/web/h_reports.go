package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"pingoo/internal/agent"
	"pingoo/internal/period"
	"pingoo/internal/store"
)

type reportItem struct {
	R       store.Report
	C       agent.ReportContent
	Snap    agent.ReportSnapshot
	Month   string
	Label   string
	Running bool
}

type reportsData struct {
	Items      []reportItem
	Sites      []store.Site
	Scope      string
	Kind       string
	NextWeekly time.Time
	AIReady    bool
	AIMsg      string
	Weekly     [2]time.Time
	Monthly    [2]time.Time
	ThisWeek   [2]time.Time
}

func toItem(rp store.Report) reportItem {
	it := reportItem{R: rp, Month: rp.PeriodEnd.Format("2006 年 1 月"), Running: rp.Status == "running"}
	_ = json.Unmarshal(rp.Content, &it.C)
	_ = json.Unmarshal(rp.Snapshot, &it.Snap)
	switch rp.Kind {
	case "weekly":
		_, w := rp.PeriodStart.ISOWeek()
		it.Label = "第 " + strconv.Itoa(w) + " 周"
	case "monthly":
		it.Label = strconv.Itoa(int(rp.PeriodStart.Month())) + " 月"
	default:
		it.Label = "自选时段"
	}
	if it.Running && time.Since(rp.CreatedAt) > 10*time.Minute {
		it.Running = false
		it.R.Status, it.R.Error = "error", "生成超时了"
	}
	return it
}

func (s *Server) reportsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := reportsData{Scope: r.URL.Query().Get("scope"), Kind: r.URL.Query().Get("kind")}
	filter := int64(-1)
	switch d.Scope {
	case "", "all":
		d.Scope = "all"
	case "summary":
		filter = 0
	default:
		filter, _ = strconv.ParseInt(d.Scope, 10, 64)
	}
	if d.Kind != "weekly" && d.Kind != "monthly" {
		d.Kind = ""
	}
	reps, err := s.Store.Reports(ctx, filter, d.Kind, 200)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, rp := range reps {
		d.Items = append(d.Items, toItem(rp))
	}
	d.Sites, _ = s.Store.Sites(ctx)
	st := s.aiStatus(r, "report")
	d.AIReady, d.AIMsg = st.Ready, st.Msg
	rs := s.Store.ReportSettings(ctx)
	loc, err := time.LoadLocation(rs.TZ)
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	a, b := agent.WeeklyPeriod(now)
	d.Weekly = [2]time.Time{a, b}
	a, b = agent.MonthlyPeriod(now)
	d.Monthly = [2]time.Time{a, b}
	_, sunday := agent.WeeklyPeriod(now.AddDate(0, 0, 7))
	d.ThisWeek = [2]time.Time{sunday.AddDate(0, 0, -6), time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)}
	if rs.Weekly {
		d.NextWeekly = d.Weekly[1].AddDate(0, 0, 8).Add(5 * time.Hour)
	}
	if isHX(r) && r.URL.Query().Get("partial") == "list" {
		s.part(w, "report-list", d)
		return
	}
	s.page(w, r, "reports", "报告", "reports", d)
}

func (s *Server) reportGenerate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rs := s.Store.ReportSettings(ctx)
	loc, err := time.LoadLocation(rs.TZ)
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	kind := r.FormValue("kind")
	var start, end time.Time
	switch kind {
	case "monthly":
		start, end = agent.MonthlyPeriod(now)
	case "custom":
		_, sunday := agent.WeeklyPeriod(now.AddDate(0, 0, 7))
		start = sunday.AddDate(0, 0, -6)
		end = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
		if end.Before(start) {
			start, end = agent.WeeklyPeriod(now)
			kind = "weekly"
		}
	default:
		kind = "weekly"
		start, end = agent.WeeklyPeriod(now)
	}
	var sid *int64
	if id := formID(r, "site"); id > 0 {
		sid = &id
	}
	if _, err := s.Agent.StartReport(ctx, sid, kind, start, end, rs.Detail); err != nil {
		if !isHX(r) { // 普通表单提交（没有 JS）：至少回到报告列表，不留一张空白页
			redirect(w, r, "/reports")
			return
		}
		toast(w, "没能开始生成："+err.Error())
		w.WriteHeader(http.StatusOK)
		return
	}
	toast(w, "开始生成，大约需要 30 秒")
	redirect(w, r, "/reports")
}

type reportView struct {
	reportItem
	Total     agent.ReportRow
	Range     period.Range
	SiteNames map[string]int64
}

func (s *Server) reportPage(w http.ResponseWriter, r *http.Request) {
	rp, err := s.Store.Report(r.Context(), pathID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v := reportView{reportItem: toItem(rp), Range: period.Custom(rp.PeriodStart, rp.PeriodEnd)}
	v.Total = v.Snap.Total
	v.SiteNames = map[string]int64{}
	for _, row := range v.Snap.Rows {
		v.SiteNames[row.Name] = row.SiteID
	}
	title := "报告"
	if v.C.Title != "" {
		title = v.Label + " · " + truncate(v.C.Title, 20)
	}
	s.page(w, r, "report", title, "reports", v)
}

func (s *Server) reportStatus(w http.ResponseWriter, r *http.Request) {
	rp, err := s.Store.Report(r.Context(), pathID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if rp.Status != "running" {
		redirect(w, r, "/reports/"+strconv.FormatInt(rp.ID, 10))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
