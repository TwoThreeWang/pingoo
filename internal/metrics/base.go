// Package metrics 把本地的每日数据（和少量实时 GA 查询）整理成页面需要的样子。
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"pingoo/internal/google"
	"pingoo/internal/period"
	"pingoo/internal/store"
	"pingoo/internal/syncer"
)

type Svc struct {
	Store *store.Store
	Sync  *syncer.Syncer
	Log   *slog.Logger

	mu  sync.Mutex
	mem map[string]memItem
}

type memItem struct {
	v   any
	exp time.Time
}

func New(st *store.Store, sy *syncer.Syncer, log *slog.Logger) *Svc {
	return &Svc{Store: st, Sync: sy, Log: log, mem: map[string]memItem{}}
}

func (m *Svc) memGet(key string) (any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.mem[key]
	if !ok || time.Now().After(it.exp) {
		return nil, false
	}
	return it.v, true
}

func (m *Svc) memSet(key string, v any, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.mem) > 2000 {
		m.mem = map[string]memItem{}
	}
	m.mem[key] = memItem{v: v, exp: time.Now().Add(ttl)}
}

// ClearCache 清空内存和数据库里的实时查询缓存。
func (m *Svc) ClearCache(ctx context.Context) error {
	m.mu.Lock()
	m.mem = map[string]memItem{}
	m.mu.Unlock()
	return m.Store.CacheClear(ctx)
}

// google 返回 GA 客户端；没有配置时返回 nil。
func (m *Svc) google(ctx context.Context) *google.Client {
	gc, err := m.Sync.Client(ctx)
	if err != nil {
		return nil
	}
	return gc
}

// AIRegex 识别来自 AI 助手的来源（GA 默认把它们算进“外链”）。
var AIRegex = `(^|\.)(chatgpt\.com|chat\.openai\.com|openai\.com|perplexity\.ai|claude\.ai|gemini\.google\.com|bard\.google\.com|copilot\.microsoft\.com|copilot\.com|you\.com|phind\.com|poe\.com|kimi\.moonshot\.cn|kimi\.com|chat\.deepseek\.com|deepseek\.com|doubao\.com|yiyan\.baidu\.com|tongyi\.aliyun\.com|chatglm\.cn|metaso\.cn|grok\.com|meta\.ai|chat\.mistral\.ai)$`

var aiRe = regexp.MustCompile(AIRegex)

func IsAISource(source string) bool { return aiRe.MatchString(strings.ToLower(source)) }

// excludePatterns 把排除路径转成 LIKE 模式。
func excludePatterns(site store.Site) []string {
	// GA 记不到落地页时写“(not set)”，超出表格上限时合并成“(other)”：都不是真实页面，不参与内容排行
	out := []string{"(not set)", "(other)"}
	for _, p := range site.ExcludePaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(p)
		out = append(out, p+"%")
	}
	return out
}

// ========== 汇总数字 ==========

type Totals struct {
	Users    int
	NewUsers int
	Sessions int
	Engaged  int
	PV       int
	EngSec   float64
	Approx   bool // true 表示访客数是按天相加的近似值
}

func (t Totals) EngRate() float64  { return ratio(float64(t.Engaged), float64(t.Sessions)) }
func (t Totals) PPV() float64      { return ratio(float64(t.PV), float64(t.Sessions)) }
func (t Totals) AvgDur() float64   { return ratio(t.EngSec, float64(t.Users)) }
func (t Totals) NewShare() float64 { return ratio(float64(t.NewUsers), float64(t.Users)) }
func (t Totals) Returning() int {
	if t.Users > t.NewUsers {
		return t.Users - t.NewUsers
	}
	return 0
}

func ratio(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

// localTotals 从每日表里求和（访客数是近似值）。
func (m *Svc) localTotals(ctx context.Context, siteID int64, from, to time.Time) Totals {
	var t Totals
	_ = m.Store.Pool.QueryRow(ctx, `SELECT coalesce(sum(active_users),0),coalesce(sum(new_users),0),coalesce(sum(sessions),0),
		coalesce(sum(engaged_sessions),0),coalesce(sum(page_views),0),coalesce(sum(engagement_seconds),0)
		FROM ga_daily WHERE site_id=$1 AND date BETWEEN $2 AND $3`, siteID, from, to).
		Scan(&t.Users, &t.NewUsers, &t.Sessions, &t.Engaged, &t.PV, &t.EngSec)
	t.Approx = true
	return t
}

// localHourTotals 用小时表估算“昨天同一时段”。
func (m *Svc) localHourTotals(ctx context.Context, siteID int64, day time.Time, untilHour int) Totals {
	var t Totals
	_ = m.Store.Pool.QueryRow(ctx, `SELECT coalesce(sum(users),0),coalesce(sum(sessions),0) FROM ga_hourly WHERE site_id=$1 AND date=$2 AND hour<=$3`,
		siteID, day, untilHour).Scan(&t.Users, &t.Sessions)
	t.Approx = true
	return t
}

var totalMetrics = []string{"activeUsers", "newUsers", "sessions", "engagedSessions", "screenPageViews", "userEngagementDuration"}

func totalsFromRow(ms []float64) Totals {
	return Totals{Users: int(ms[0]), NewUsers: int(ms[1]), Sessions: int(ms[2]), Engaged: int(ms[3]), PV: int(ms[4]), EngSec: ms[5]}
}

// totalsTTL 是汇总数字的缓存时间。范围截止到昨天，数字只会在下一次同步（GA 修正最近几天的数据）时变，
// 同步完成后 RefreshTotals 会主动刷新，所以可以缓存得久一些；缓存键里带日期，过了一天自然就换了。
const totalsTTL = 24 * time.Hour

// RefreshTotals 在每次同步之后调用：丢掉这个网站缓存的汇总数字，再把默认范围（近 7 天、近 28 天）提前算好。
// 汇总数字要实时查 GA，每个网站可能要等几秒；不提前算的话，每天第一次打开概览页要等所有网站都查完。
func (m *Svc) RefreshTotals(ctx context.Context, site store.Site) {
	m.Store.CacheDeletePrefix(ctx, fmt.Sprintf("totals:%d:", site.ID))
	if site.Demo || site.GAProperty == "" || m.google(ctx) == nil {
		return
	}
	for _, key := range []string{"d7", "d28"} {
		m.Totals(ctx, site, m.SyncedRange(ctx, site, key))
	}
}

// Totals 返回本期和对比期的汇总。访客数用 GA 实时查询得到真正的去重人数（缓存到下一次同步，最长 totalsTTL）；
// 查不到时退回到本地按天相加的近似值。
func (m *Svc) Totals(ctx context.Context, site store.Site, r period.Range) (cur, prev Totals) {
	key := fmt.Sprintf("totals:%d:%s:%s:%s:%s", site.ID, r.Key, period.D(r.Start), period.D(r.End), period.D(r.PrevStart))
	type pair struct{ Cur, Prev Totals }
	if r.Live {
		if v, ok := m.memGet(key); ok {
			p := v.(pair)
			return p.Cur, p.Prev
		}
	} else {
		var p pair
		if m.Store.CacheGet(ctx, key, &p) {
			return p.Cur, p.Prev
		}
	}
	gc := m.google(ctx)
	if gc != nil && !site.Demo && site.GAProperty != "" {
		ranges := []google.DateRange{{StartDate: period.D(r.Start), EndDate: period.D(r.End), Name: "cur"}}
		if !r.Live {
			ranges = append(ranges, google.DateRange{StartDate: period.D(r.PrevStart), EndDate: period.D(r.PrevEnd), Name: "prev"})
		}
		qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		rep, err := gc.RunReport(qctx, site.GAProperty, google.ReportRequest{Ranges: ranges, Metrics: totalMetrics})
		cancel()
		if err == nil {
			for _, row := range rep.Rows {
				name := "cur"
				if len(row.Dims) > 0 {
					name = row.Dims[0]
				}
				if name == "prev" {
					prev = totalsFromRow(row.Metrics)
				} else {
					cur = totalsFromRow(row.Metrics)
				}
			}
			if r.Live {
				prev = m.localHourTotals(ctx, site.ID, r.PrevEnd, r.NowHour)
				m.memSet(key, pair{cur, prev}, 5*time.Minute)
			} else {
				m.Store.CacheSet(ctx, key, pair{cur, prev}, totalsTTL)
			}
			return cur, prev
		}
		m.Log.Warn("实时查询 GA 失败，使用本地近似值", "site", site.Name, "err", err)
	}
	if r.Live {
		return Totals{Approx: true}, m.localHourTotals(ctx, site.ID, r.PrevEnd, r.NowHour)
	}
	return m.localTotals(ctx, site.ID, r.Start, r.End), m.localTotals(ctx, site.ID, r.PrevStart, r.PrevEnd)
}

// Realtime 返回过去 30 分钟在线人数（缓存 1 分钟）；查不到返回 -1。
func (m *Svc) Realtime(ctx context.Context, site store.Site) int {
	if site.Demo {
		return demoLive(site.ID)
	}
	key := fmt.Sprintf("rt:%d", site.ID)
	if v, ok := m.memGet(key); ok {
		return v.(int)
	}
	gc := m.google(ctx)
	if gc == nil || site.GAProperty == "" {
		return -1
	}
	qctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	n, err := gc.Realtime(qctx, site.GAProperty)
	if err != nil {
		m.memSet(key, -1, 30*time.Second)
		return -1
	}
	m.memSet(key, n, time.Minute)
	return n
}

func demoLive(id int64) int {
	return []int{8, 14, 0, 6, 3, 2, 5, 4}[int(id)%8]
}

// ========== 时间序列 ==========

// Series 是图表需要的一条线（本期 + 对比期）。
type Series struct {
	Cur, Prev          []float64
	Labels, PrevLabels []string
	Dates              []time.Time
}

// dailySeries 从某张每日表里按日期取一个表达式的值，缺失的日期补 0。
func (m *Svc) dailySeries(ctx context.Context, table, expr string, siteID int64, dates []time.Time) []float64 {
	out := make([]float64, len(dates))
	if len(dates) == 0 {
		return out
	}
	rows, err := m.Store.Pool.Query(ctx, `SELECT date, `+expr+` FROM `+table+` WHERE site_id=$1 AND date BETWEEN $2 AND $3 GROUP BY date`,
		siteID, dates[0], dates[len(dates)-1])
	if err != nil {
		m.Log.Error("查询序列失败", "table", table, "err", err)
		return out
	}
	defer rows.Close()
	idx := map[string]int{}
	for i, d := range dates {
		idx[period.D(d)] = i
	}
	for rows.Next() {
		var d time.Time
		var v *float64
		if rows.Scan(&d, &v) == nil && v != nil {
			if i, ok := idx[period.D(d)]; ok {
				out[i] = *v
			}
		}
	}
	return out
}

func (m *Svc) hourlySeries(ctx context.Context, siteID int64, day time.Time, hours int) []float64 {
	out := make([]float64, hours)
	rows, err := m.Store.Pool.Query(ctx, `SELECT hour, users FROM ga_hourly WHERE site_id=$1 AND date=$2 AND hour < $3`, siteID, day, hours)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var h int16
		var v int
		if rows.Scan(&h, &v) == nil && int(h) < hours {
			out[h] = float64(v)
		}
	}
	return out
}

// liveTodayHours 实时查 GA 今天每小时的访客（缓存 5 分钟）。
func (m *Svc) liveTodayHours(ctx context.Context, site store.Site, hours int) []float64 {
	out := make([]float64, hours)
	if site.Demo {
		return out
	}
	key := fmt.Sprintf("today-hours:%d", site.ID)
	if v, ok := m.memGet(key); ok {
		cached := v.([]float64)
		copy(out, cached)
		return out
	}
	gc := m.google(ctx)
	if gc == nil || site.GAProperty == "" {
		return out
	}
	qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rep, err := gc.RunReport(qctx, site.GAProperty, google.ReportRequest{
		Ranges: []google.DateRange{{StartDate: "today", EndDate: "today"}}, Dimensions: []string{"hour"}, Metrics: []string{"activeUsers"}})
	if err != nil {
		return out
	}
	full := make([]float64, 24)
	for _, r := range rep.Rows {
		var h int
		fmt.Sscanf(r.Dims[0], "%d", &h)
		if h >= 0 && h < 24 {
			full[h] = r.Metrics[0]
		}
	}
	m.memSet(key, full, 5*time.Minute)
	copy(out, full)
	return out
}

// VisitorSeries 返回访客趋势（今天/昨天按小时，其他按天）。
func (m *Svc) VisitorSeries(ctx context.Context, site store.Site, r period.Range) Series {
	if r.Hourly {
		n := 24
		if r.Live {
			n = r.NowHour + 1
		}
		s := Series{}
		for h := 0; h < n; h++ {
			s.Labels = append(s.Labels, fmt.Sprintf("%s %02d:00", r.Label, h))
			s.PrevLabels = append(s.PrevLabels, fmt.Sprintf("%s %02d:00", map[bool]string{true: "昨天", false: "前天"}[r.Live], h))
		}
		if r.Live {
			s.Cur = m.liveTodayHours(ctx, site, n)
		} else {
			s.Cur = m.hourlySeries(ctx, site.ID, r.Start, n)
		}
		s.Prev = m.hourlySeries(ctx, site.ID, r.PrevStart, n)
		return s
	}
	return m.DailySeries(ctx, site.ID, r, "ga_daily", "sum(active_users)::float8")
}

func (m *Svc) DailySeries(ctx context.Context, siteID int64, r period.Range, table, expr string) Series {
	dates, prevDates := r.Dates(), r.PrevDates()
	s := Series{Dates: dates}
	s.Cur = m.dailySeries(ctx, table, expr, siteID, dates)
	s.Prev = m.dailySeries(ctx, table, expr, siteID, prevDates)
	for _, d := range dates {
		s.Labels = append(s.Labels, period.MD(d))
	}
	for _, d := range prevDates {
		if r.Compare == period.CompareYoY {
			s.PrevLabels = append(s.PrevLabels, period.YMD(d)) // 去年同期要带年份，不然和本期的日期看着差不多
		} else {
			s.PrevLabels = append(s.PrevLabels, period.MD(d))
		}
	}
	return s
}

// HasDaily 报告某段时间有没有 GA 的每日数据（用来判断“去年同期”有没有东西可比）。
func (m *Svc) HasDaily(ctx context.Context, siteID int64, from, to time.Time) bool {
	var ok bool
	_ = m.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ga_daily WHERE site_id=$1 AND date BETWEEN $2 AND $3)`, siteID, from, to).Scan(&ok)
	return ok
}

// GSCLatest 返回 Search Console 数据最新的日期（零值表示没有数据）。
func (m *Svc) GSCLatest(ctx context.Context, siteID int64) time.Time {
	var t *time.Time
	_ = m.Store.Pool.QueryRow(ctx, `SELECT max(date) FROM gsc_daily WHERE ($1=0 OR site_id=$1) AND impressions>0`, siteID).Scan(&t)
	if t == nil {
		return time.Time{}
	}
	return *t
}

// GALatest 返回 GA 已同步的最新日期。
func (m *Svc) GALatest(ctx context.Context, siteID int64) time.Time {
	var t *time.Time
	_ = m.Store.Pool.QueryRow(ctx, `SELECT max(date) FROM ga_daily WHERE ($1=0 OR site_id=$1)`, siteID).Scan(&t)
	if t == nil {
		return time.Time{}
	}
	return *t
}

// LastSyncAt 返回当前用户的网站最近一次成功同步的时间。
func (m *Svc) LastSyncAt(ctx context.Context) time.Time {
	var t *time.Time
	_ = m.Store.Pool.QueryRow(ctx, `SELECT max(r.finished_at) FROM sync_runs r JOIN sites st ON st.id=r.site_id
		WHERE r.status='ok' AND st.user_id=$1`, store.UserID(ctx)).Scan(&t)
	if t == nil {
		return time.Time{}
	}
	return *t
}

// ========== 通用小工具 ==========

func pct(cur, prev float64) float64 {
	if prev == 0 {
		if cur == 0 {
			return 0
		}
		return math.Inf(1)
	}
	return (cur - prev) / prev
}

func sumF(a []float64) float64 {
	s := 0.0
	for _, v := range a {
		s += v
	}
	return s
}

func topN[T any](xs []T, n int) []T {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

func sortBy[T any](xs []T, less func(a, b T) bool) {
	sort.SliceStable(xs, func(i, j int) bool { return less(xs[i], xs[j]) })
}

// ExpectedCTR 是各排名的大致平均点击率，用于判断“高曝光低点击”和估算提升空间。
func ExpectedCTR(pos float64) float64 {
	table := []float64{0.28, 0.15, 0.11, 0.08, 0.065, 0.05, 0.04, 0.032, 0.028, 0.025}
	if pos < 1 {
		pos = 1
	}
	i := int(math.Round(pos)) - 1
	if i < len(table) {
		return table[i]
	}
	if pos <= 20 {
		return 0.012
	}
	return 0.005
}

// BookTitle 给页面标题加上书名号；标题为空时用路径，标题本身已经以书名号开头时不再重复加。
func BookTitle(title, path string) string {
	if title == "" || title == "(not set)" {
		return path
	}
	if strings.HasPrefix(title, "《") {
		return title
	}
	return "《" + title + "》"
}
