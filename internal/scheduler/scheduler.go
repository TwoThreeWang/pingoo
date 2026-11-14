// Package scheduler 是进程内的定时任务：每天同步 → 检测信号 → 生成洞察；周一生成周报，每月 1 日生成月报。
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"pingoo/internal/agent"
	"pingoo/internal/google"
	"pingoo/internal/mail"
	"pingoo/internal/period"
	"pingoo/internal/signals"
	"pingoo/internal/store"
	"pingoo/internal/syncer"
)

type Scheduler struct {
	Store    *store.Store
	Sync     *syncer.Syncer
	Detector *signals.Detector
	Agent    *agent.Agent
	Log      *slog.Logger
	Mail     *mail.Mailer // 邮件简报、套餐到期提醒；nil 表示不发

	trigger  chan int64
	syncQ    chan int64
	syncing  sync.Map // 正在排队或正在同步的网站 → *atomic.Bool（同步期间又被要求同步，结束后要再来一次）
	insightQ chan int64
	queued   sync.Map // 已经在排队生成洞察的网站
	reportQ  chan reportJob
	reportsQ sync.Map // 已经在排队（或正在生成）的报告
}

type reportJob struct {
	uid        int64 // 报告的主人
	siteID     *int64
	kind       string
	start, end time.Time
	detail     string
}

func (j reportJob) key() string {
	id := int64(0)
	if j.siteID != nil {
		id = *j.siteID
	}
	return fmt.Sprintf("%d:%d:%s:%s", j.uid, id, j.kind, period.D(j.start))
}

// quiet 是自动任务里不用记日志的“失败”：没有模型、积分不够、全站到上限，都是预期内的，下次再试。
func quiet(err error) bool {
	return err == nil || err == agent.ErrNoAI || err == agent.ErrBudget || err == agent.ErrNoCredits
}

// 自动报告失败后的重试：隔 reportRetryWait 再试，一共最多试 maxReportTries 次。
// 不设上限的话，AI 接口出问题的那天会每 10 分钟重新生成一遍所有报告，白白花 token。
const (
	maxReportTries  = 3
	reportRetryWait = time.Hour
)

// reportDue 判断某一期报告现在要不要自动生成。
func reportDue(have, failed int, lastFail, now time.Time) bool {
	if have > 0 || failed >= maxReportTries {
		return false
	}
	return failed == 0 || now.Sub(lastFail) >= reportRetryWait
}

// guard 接住后台任务里的 panic：调度器跑在自己的 goroutine 里，不接住的话一次 panic 会让整个进程退出。
func (s *Scheduler) guard(task string, fn func()) {
	defer func() {
		if v := recover(); v != nil {
			s.Log.Error("后台任务出错", "task", task, "panic", v, "stack", string(debug.Stack()))
		}
	}()
	fn()
}

// SyncHour 是每天同步的时刻（网站当地时间），给 GA 留出处理前一天数据的时间。
const SyncHour = 4

func (s *Scheduler) Run(ctx context.Context) {
	s.trigger = make(chan int64, 256)
	s.syncQ = make(chan int64, 4096)
	s.insightQ = make(chan int64, 64)
	s.reportQ = make(chan reportJob, 256)
	for i := 0; i < syncWorkers; i++ {
		go s.syncWorker(ctx)
	}
	go s.insightWorker(ctx)
	go s.reportWorker(ctx)
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	time.Sleep(5 * time.Second)
	housekeeping := func() {
		s.guard("housekeeping", func() {
			s.refreshGoogleUpdates(ctx)
			if n := s.Store.Cleanup(ctx) + s.Store.Prune(ctx); n > 0 {
				s.Log.Info("已清理过期的会话、缓存和历史记录", "rows", n)
			}
			if n := s.Store.GrantMonthly(ctx); n > 0 {
				s.Log.Info("已发放套餐每月积分", "users", n)
			}
		})
	}
	housekeeping()
	s.guard("tick", func() { s.tick(ctx, false) })
	lastUpdates := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.trigger:
			s.guard("sync", func() { s.syncOne(ctx, id) })
		case <-t.C:
			if time.Since(lastUpdates) > 6*time.Hour {
				housekeeping()
				lastUpdates = time.Now()
			}
			s.guard("tick", func() { s.tick(ctx, false) })
		}
	}
}

// syncWorkers 是同时同步的网站数。以前是一个一个来，新网站的首次回填（GA 90 天 + Search Console 16 个月）
// 要好几分钟，会挡住后面所有网站。每个用户上传自己的服务账号、GA 配额按各自项目单独算，并发同步不互相挤占配额，
// 所以用户/网站变多时可以调大（用 SYNC_WORKERS 环境变量），受限的是本机的 CPU、内存和出网带宽。默认 3，范围 1～64。
var syncWorkers = envInt("SYNC_WORKERS", 3, 1, 64)

func envInt(key string, def, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}

// enqueueSync 把一个网站放进同步队列，不会阻塞。已经在排队或正在同步的不重复放，
// 只记下“结束后再来一次”（例如同步到一半时站长换了数据源）。
func (s *Scheduler) enqueueSync(id int64) {
	if s.syncQ == nil {
		return
	}
	if v, dup := s.syncing.LoadOrStore(id, new(atomic.Bool)); dup {
		v.(*atomic.Bool).Store(true)
		return
	}
	select {
	case s.syncQ <- id:
	default:
		s.syncing.Delete(id) // 队列满了：下一轮 tick 会再放
	}
}

// finishSync 在一个网站同步结束后调用：同步期间有人又要求同步的话，再排一次。
func (s *Scheduler) finishSync(id int64) {
	if v, ok := s.syncing.LoadAndDelete(id); ok && v.(*atomic.Bool).Load() {
		s.enqueueSync(id)
	}
}

func (s *Scheduler) syncWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.syncQ:
			s.guard("sync", func() {
				if site, err := s.Store.SiteAny(ctx, id); err == nil {
					s.process(ctx, site)
				}
			})
			s.finishSync(id)
		}
	}
}

// SyncNow 让调度器尽快同步某个网站（0 表示全部）。不会阻塞。
func (s *Scheduler) SyncNow(siteID int64) {
	if s.trigger == nil {
		return
	}
	select {
	case s.trigger <- siteID:
	default:
	}
}

func (s *Scheduler) syncOne(ctx context.Context, siteID int64) {
	sites, err := s.Store.AllSites(ctx)
	if err != nil {
		return
	}
	for _, site := range sites {
		if siteID != 0 && site.ID != siteID {
			continue
		}
		s.enqueueSync(site.ID)
	}
}

// tick 检查每个网站是否到了该同步的时候。
func (s *Scheduler) tick(ctx context.Context, force bool) {
	sites, err := s.Store.AllSites(ctx)
	if err != nil {
		s.Log.Error("读取网站失败", "err", err)
		return
	}
	plans := map[int64]store.Plan{} // 每个用户的套餐，这一轮里只查一次
	for _, site := range sites {
		if site.Demo {
			continue
		}
		source := primarySource(site)
		if source == "" {
			continue // 什么都没连，没有可同步的
		}
		p, ok := plans[site.UserID]
		if !ok {
			p = s.Store.PlanOf(ctx, site.UserID)
			plans[site.UserID] = p
		}
		if p.Paused(site) {
			continue // 超出套餐网站数的网站暂停同步（数据不删，升级或续费后恢复）
		}
		st, _ := s.Store.SyncState(ctx, site.ID, source)
		if force || syncDue(st, time.Now(), site.Location(), p.SyncHours) {
			s.enqueueSync(site.ID)
		}
	}
	s.reports(ctx)
	s.mails(ctx)
	s.nightly(ctx)
}

// primarySource 返回用哪个数据源的同步状态代表这个网站：连了 GA 就看 GA，只连了 Search Console 的看 Search Console。
// （以前一律看 GA：只连 Search Console 的网站永远是“从未同步”，每 10 分钟就会重新同步一次。）
func primarySource(site store.Site) string {
	switch {
	case site.GAProperty != "":
		return "ga"
	case site.GSCSite != "":
		return "gsc"
	}
	return ""
}

// syncDue 判断一个网站现在要不要同步：还没同步到昨天，并且已经过了当地的 SyncHour（从没同步过的不用等）。
// everyHours > 0（付费套餐）时，已经同步到昨天了也每隔这么多小时再刷新一次：GA 前一天的数据要过一阵才完整，
// 提醒也能更早发现。这种刷新只重拉有变化的日子，没变化时只花一次很小的查询（见 syncer 的 gaUnchanged）。
func syncDue(st store.SyncState, now time.Time, loc *time.Location, everyHours int) bool {
	if st.LastSynced == nil {
		return !(st.Status == "error" && now.Sub(st.UpdatedAt) < time.Hour)
	}
	now = now.In(loc)
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	// LastSynced 是从 date 列读出来的，时刻是 UTC 零点；要先换成当地那一天的零点再比。
	// 直接比的话，在 UTC 以西的时区“已经同步到昨天”也会被当成没同步，每 10 分钟重拉一次。
	last := time.Date(st.LastSynced.Year(), st.LastSynced.Month(), st.LastSynced.Day(), 0, 0, 0, 0, loc)
	if !last.Before(yesterday) {
		return everyHours > 0 && st.Status != "running" && now.Sub(st.UpdatedAt) >= time.Duration(everyHours)*time.Hour
	}
	if now.Hour() < SyncHour {
		return false
	}
	return !(st.Status == "error" && now.Sub(st.UpdatedAt) < time.Hour) // 出错后一小时内不重试
}

// process 同步一个网站，然后检测信号，再把洞察放进队列。
// 洞察要等 AI，慢的模型要好几分钟，所以放到单独的队列里，不耽误下一个网站同步。
func (s *Scheduler) process(ctx context.Context, site store.Site) {
	ctx = store.WithUser(ctx, site.UserID) // 用网站主人的 Google 服务账号同步，写入他名下的记录
	if !site.Demo {
		source := primarySource(site)
		if source == "" {
			source = "ga"
		}
		before, _ := s.Store.SyncState(ctx, site.ID, source)
		if err := s.Sync.SyncSite(ctx, site); err != nil {
			s.Log.Warn("同步失败", "site", site.Name, "err", err)
			if err == syncer.ErrNoGoogle {
				return
			}
		}
		if before.LastSynced == nil {
			// 第一次回填完：“全部网站”的简报是在缺这个网站数据时写的，删掉让它重新生成
			s.Store.DropOverviewInsights(ctx)
		}
	}
	if s.Detector == nil {
		return
	}
	if !site.Demo {
		s.Detector.M.RefreshTotals(ctx, site)
	}
	s.Detector.Detect(ctx, site)
	s.queueInsight(site.ID)
}

func (s *Scheduler) queueInsight(siteID int64) {
	if s.insightQ == nil {
		return
	}
	if _, dup := s.queued.LoadOrStore(siteID, true); dup {
		return
	}
	select {
	case s.insightQ <- siteID:
	default:
		s.queued.Delete(siteID)
	}
}

// insightIdleDays：用户超过这么多天没打开过 Pingoo，就不再每天提前生成洞察。
const insightIdleDays = 3

// insightWorker 一次只生成一个网站的洞察，避免同时向 AI 发太多请求。
func (s *Scheduler) insightWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.insightQ:
			s.queued.Delete(id)
			s.guard("insight", func() {
				site, err := s.Store.SiteAny(ctx, id)
				if err != nil || !s.insightDue(ctx, site) {
					return
				}
				ctx := store.WithUser(ctx, site.UserID)
				r := period.Resolve(agent.DefaultInsightRange(&site), time.Now(), site.Location())
				if _, err := s.Agent.GenerateInsight(ctx, &site, r); !quiet(err) {
					s.Log.Warn("生成洞察失败", "site", site.Name, "err", err)
				}
			})
		}
	}
}

// insightDue 判断同步完之后要不要给这个网站自动写洞察。
func (s *Scheduler) insightDue(ctx context.Context, site store.Site) bool {
	if site.Demo || s.Store.PlanOf(ctx, site.UserID).Paused(site) {
		return false
	}
	// 自动洞察会花积分：只给打开了“自动生成 AI 内容”的用户生成
	if u, err := s.Store.User(ctx, site.UserID); err != nil || !u.AutoAI || u.Disabled || u.Credits <= 0 {
		return false
	}
	// 最近几天没打开过 Pingoo 的用户不提前生成：没人看的洞察每天每个网站都在白花积分。
	// 他回来打开页面时，过期的洞察会当场重新生成（见 web 的 loadInsight），内容一样，只是第一次要等半分钟。
	if seen := s.Store.LastSeen(ctx, site.UserID); time.Since(seen) > insightIdleDays*24*time.Hour {
		return false
	}
	ctx = store.WithUser(ctx, site.UserID)
	// 20 小时内已经有一份完成的洞察就不再生成（手动同步、重启后的同步都不会重复花 token）
	key := agent.DefaultInsightRange(&site)
	if ins, err := s.Store.LatestInsight(ctx, &site.ID, key); err == nil && ins.Status == "done" && time.Since(ins.CreatedAt) < agent.InsightTTL {
		return false
	}
	// 已经连续失败好几次了：不再自动重试，等站长手动点“重新生成”
	return s.Store.InsightFailStreak(ctx, &site.ID, key) < agent.InsightMaxFails
}

// queueReport 把一期报告放进队列（已经有了、失败次数太多、或者已经在排队的不放）。
func (s *Scheduler) queueReport(ctx context.Context, j reportJob) {
	if s.reportQ == nil {
		return
	}
	ctx = store.WithUser(ctx, j.uid)
	have, failed, lastFail, err := s.Store.ReportAttempts(ctx, j.siteID, j.kind, j.start)
	if err != nil || !reportDue(have, failed, lastFail, time.Now()) {
		return
	}
	if _, dup := s.reportsQ.LoadOrStore(j.key(), true); dup {
		return
	}
	select {
	case s.reportQ <- j:
	default:
		s.reportsQ.Delete(j.key())
	}
}

// reportWorker 一次只生成一份报告。生成完才把它从“排队中”里拿掉，所以同一期不会被重复放进队列。
func (s *Scheduler) reportWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.reportQ:
			s.guard("report", func() {
				defer s.reportsQ.Delete(j.key())
				ctx := store.WithUser(ctx, j.uid)
				have, failed, lastFail, err := s.Store.ReportAttempts(ctx, j.siteID, j.kind, j.start)
				if err != nil || !reportDue(have, failed, lastFail, time.Now()) {
					return
				}
				if _, err := s.Agent.GenerateReport(ctx, j.siteID, j.kind, j.start, j.end, j.detail); !quiet(err) {
					s.Log.Warn("生成报告失败", "kind", j.kind, "err", err)
				}
			})
		}
	}
}

// refreshGoogleUpdates 从 Google 搜索状态页读取算法更新和搜索故障，存成图上的标注。
func (s *Scheduler) refreshGoogleUpdates(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	list, err := google.SearchIncidents(cctx)
	if err != nil {
		s.Log.Warn("读取 Google 搜索状态页失败", "err", err)
		return
	}
	n := 0
	for _, x := range list {
		label := UpdateLabel(x.Service, x.Desc)
		if label == "" {
			continue
		}
		begin, err := time.Parse(time.RFC3339, x.Begin)
		if err != nil {
			continue
		}
		var end *time.Time
		if t, err := time.Parse(time.RFC3339, x.End); err == nil {
			end = &t
		}
		if err := s.Store.UpsertAnnotation(ctx, "google", x.ID, begin, end, label, x.Desc); err == nil {
			n++
		}
	}
	s.Log.Info("已更新 Google 搜索更新记录", "count", n)
}

// UpdateLabel 把状态页上的英文标题变成图上的中文短标签；无关的记录返回空。
func UpdateLabel(service, desc string) string {
	d := strings.ToLower(desc)
	switch {
	case strings.Contains(d, "core update"):
		return "Google 核心更新"
	case strings.Contains(d, "spam update"):
		return "Google 垃圾内容更新"
	case strings.Contains(d, "helpful content"):
		return "Google 有用内容更新"
	case strings.Contains(d, "review"):
		return "Google 评测内容更新"
	case strings.Contains(d, "discover"):
		return "Google Discover 更新"
	case service == "Ranking":
		return "Google 排名更新"
	case service == "Indexing" || service == "Crawling" || service == "Serving":
		return "Google 搜索故障"
	}
	return ""
}

// reports 给“上一个完整的周 / 月”的报告排队（已经有了、或者失败次数太多的不会再排，见 queueReport）。
//
// 不再限定“今天是周一 / 1 日”：那天服务没开、或者当天几次都失败的话，之后的任何一天都会补上这一期。
// 什么时候生成由 reportReady 决定——要等用到的网站都同步完这一期的最后一天。
func (s *Scheduler) reports(ctx context.Context) {
	if _, _, over := s.Agent.Budget(ctx); over {
		return // 今天全站的 AI 用量到上限了：明天再生成，不记成失败
	}
	// 自动报告会花积分：只给打开了“自动生成 AI 内容”、还有积分的用户生成
	users, err := s.Store.AutoAIUsers(ctx)
	if err != nil {
		return
	}
	states, err := s.Store.SyncStates(ctx)
	if err != nil {
		return
	}
	for _, u := range users {
		s.userReports(store.WithUser(ctx, u.ID), u.ID, states)
	}
}

func (s *Scheduler) userReports(ctx context.Context, uid int64, states map[int64]map[string]store.SyncState) {
	rs := s.Store.ReportSettings(ctx)
	loc, err := time.LoadLocation(rs.TZ)
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	all, err := s.Store.Sites(ctx)
	if err != nil {
		return
	}
	gen := func(kind string, start, end time.Time) {
		sites := reportSites(all, end)
		if len(sites) == 0 {
			return
		}
		if reportReady(sites, states, end, now) {
			s.queueReport(ctx, reportJob{uid: uid, kind: kind, start: start, end: end, detail: rs.Detail})
		}
		if rs.PerSite {
			for _, site := range sites {
				if id := site.ID; reportReady([]store.Site{site}, states, end, now) {
					s.queueReport(ctx, reportJob{uid: uid, siteID: &id, kind: kind, start: start, end: end, detail: rs.Detail})
				}
			}
		}
	}
	if rs.Weekly {
		a, b := agent.WeeklyPeriod(now)
		gen("weekly", a, b)
	}
	if rs.Monthly {
		a, b := agent.MonthlyPeriod(now)
		gen("monthly", a, b)
	}
}

// reportSites 返回某一期自动报告要覆盖的网站：
//   - 不含演示网站：给模拟数据写周报是白花 token，全站报告里也不该把它们和真实数据加在一起；
//   - 不含这一期结束之后才添加的网站：刚装好 Pingoo 时不会立刻补一份“上周”的报告，第一份从下一期开始
//     （想马上看可以手动生成）。
func reportSites(all []store.Site, end time.Time) []store.Site {
	var out []store.Site
	for _, site := range all {
		if site.Demo || period.D(site.CreatedAt.In(end.Location())) > period.D(end) {
			continue
		}
		out = append(out, site)
	}
	return out
}

// reportReady 判断现在能不能生成截至 end 那天的报告。
//   - 最早是 end 第二天的 SyncHour+1 点（报告时区），也就是页面上说的“周一 05:00”；
//   - 要等用到的网站都同步到 end 那天：各网站按自己当地时间同步，时区靠西的会晚几个小时，
//     不等的话报告里会缺它们最后一天的数据；
//   - 最晚等到 end 之后第二天的 06:00：某个网站同步一直出错时，不能让报告永远出不来。
func reportReady(sites []store.Site, states map[int64]map[string]store.SyncState, end, now time.Time) bool {
	loc := end.Location()
	if now.Before(time.Date(end.Year(), end.Month(), end.Day()+1, SyncHour+1, 0, 0, 0, loc)) {
		return false
	}
	if !now.Before(time.Date(end.Year(), end.Month(), end.Day()+2, 6, 0, 0, 0, loc)) {
		return true
	}
	for _, site := range sites {
		source := primarySource(site)
		if source == "" {
			continue // 没连数据源的网站没有可等的
		}
		st := states[site.ID][source]
		if st.LastSynced == nil || period.D(*st.LastSynced) < period.D(end) {
			return false
		}
	}
	return true
}
