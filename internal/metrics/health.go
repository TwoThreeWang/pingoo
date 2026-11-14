package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"pingoo/internal/period"
	"pingoo/internal/store"
)

type Check struct {
	Status string // ok / warn / info / error
	Title  string
	Detail string
	Items  [][2]string
}

func (m *Svc) Health(ctx context.Context, site store.Site) []Check {
	var out []Check
	latest := m.GALatest(ctx, site.ID)
	loc := site.Location()
	now := time.Now().In(loc)
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)

	// 1. 统计代码
	switch {
	case latest.IsZero():
		out = append(out, Check{Status: "info", Title: "还没有 GA 数据", Detail: "首次同步完成后，这里会检查统计代码是否正常。"})
	default:
		var y, median float64
		_ = m.Store.Pool.QueryRow(ctx, `SELECT coalesce((SELECT active_users FROM ga_daily WHERE site_id=$1 AND date=$2),0),
			coalesce((SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY active_users) FROM ga_daily WHERE site_id=$1 AND date BETWEEN $2::date-14 AND $2::date-1),0)`,
			site.ID, latest).Scan(&y, &median)
		lag := int(yesterday.Sub(time.Date(latest.Year(), latest.Month(), latest.Day(), 0, 0, 0, 0, loc)).Hours() / 24)
		switch {
		case median >= 50 && y < median*0.1:
			out = append(out, Check{Status: "error", Title: "统计代码可能失效了",
				Detail: fmt.Sprintf("%s 只有 %.0f 位访客，过去两周中位数是 %.0f。所有渠道同时下跌时，通常是统计代码被删掉或被 Cookie 弹窗拦截。", period.MD(latest), y, median)})
		case lag > 2:
			out = append(out, Check{Status: "warn", Title: "数据同步落后了", Detail: fmt.Sprintf("GA 数据只同步到 %s，已经落后 %d 天。去设置页看看同步记录里的错误。", period.MD(latest), lag)})
		default:
			out = append(out, Check{Status: "ok", Title: "统计代码正常", Detail: fmt.Sprintf("数据同步到 %s，最近每天都有访客记录。", period.MD(latest))})
		}
	}

	// 2. 404 页面
	rows, err := m.Store.Pool.Query(ctx, `SELECT path, sum(page_views) FROM ga_daily_pages
		WHERE site_id=$1 AND date >= $2::date - 28 AND (title ILIKE '%404%' OR title ILIKE '%not found%' OR title LIKE '%找不到%' OR title LIKE '%页面不存在%')
		GROUP BY path ORDER BY 2 DESC LIMIT 10`, site.ID, yesterday)
	if err == nil {
		var items [][2]string
		total := 0
		for rows.Next() {
			var p string
			var n int
			if rows.Scan(&p, &n) == nil {
				items = append(items, [2]string{p, Thousands(float64(n)) + " 次"})
				total += n
			}
		}
		rows.Close()
		if len(items) > 0 {
			out = append(out, Check{Status: "warn", Title: fmt.Sprintf("%d 个 404 页面仍有人访问", len(items)),
				Detail: "近 28 天共 " + Thousands(float64(total)) + " 次。可以把这些地址 301 跳转到对应的新页面。", Items: items})
		} else if !latest.IsZero() {
			out = append(out, Check{Status: "ok", Title: "没有发现被访问的 404 页面", Detail: "近 28 天没有标题像“404 / Not Found”的页面被访问。"})
		}
	}

	// 3. 标题重复
	rows, err = m.Store.Pool.Query(ctx, `SELECT title, array_agg(DISTINCT path) FROM ga_daily_pages
		WHERE site_id=$1 AND date >= $2::date - 28 AND title <> '' AND page_views > 0
		GROUP BY title HAVING count(DISTINCT path) > 1 ORDER BY sum(page_views) DESC LIMIT 5`, site.ID, yesterday)
	if err == nil {
		var items [][2]string
		for rows.Next() {
			var t string
			var paths []string
			if rows.Scan(&t, &paths) == nil {
				sort.Strings(paths)
				if len(paths) > 3 {
					paths = append(paths[:3], "…")
				}
				items = append(items, [2]string{strings.Join(paths, "  "), BookTitle(t, "")})
			}
		}
		rows.Close()
		if len(items) > 0 {
			out = append(out, Check{Status: "info", Title: fmt.Sprintf("%d 组页面标题相同", len(items)),
				Detail: "内容相近的页面会互相抢搜索排名；如果是带参数或草稿页，可以考虑设置 canonical 或排除。", Items: items})
		}
	}

	// 4. Search Console
	if site.GSCSite == "" && !site.Demo {
		out = append(out, Check{Status: "info", Title: "没有连接 Search Console", Detail: "连接后可以看到搜索词、排名和点击率。在设置里给这个网站选一个 Search Console 网站。"})
	} else {
		g := m.GSCLatest(ctx, site.ID)
		st, _ := m.Store.SyncState(ctx, site.ID, "gsc")
		switch {
		case st.Status == "error":
			out = append(out, Check{Status: "error", Title: "Search Console 同步失败", Detail: st.LastError})
		case g.IsZero():
			out = append(out, Check{Status: "info", Title: "Search Console 还没有数据", Detail: "首次同步完成后会显示。"})
		default:
			out = append(out, Check{Status: "ok", Title: "Search Console 已连接", Detail: fmt.Sprintf("数据截至 %s，Google 通常有 2～3 天延迟。", period.MD(g))})
		}
	}

	// 5. 机器人 / 刷量：来访不少，但几乎没有人认真看
	if !latest.IsZero() {
		var items [][2]string
		rows, err := m.Store.Pool.Query(ctx, `SELECT 'src', lower(source), sum(sessions), sum(engaged_sessions) FROM ga_daily_sources
			WHERE site_id=$1 AND date >= $2::date - 28 GROUP BY lower(source)
			HAVING sum(sessions) >= 200 AND sum(engaged_sessions)::float8 / sum(sessions) < 0.08
			UNION ALL
			SELECT 'geo', country, sum(sessions), sum(engaged_sessions) FROM ga_daily_geo
			WHERE site_id=$1 AND date >= $2::date - 28 GROUP BY country
			HAVING sum(sessions) >= 200 AND sum(engaged_sessions)::float8 / sum(sessions) < 0.08
			ORDER BY 3 DESC LIMIT 5`, site.ID, latest)
		if err == nil {
			for rows.Next() {
				var kind, name string
				var n, e float64
				if rows.Scan(&kind, &name, &n, &e) == nil {
					label := "来源 " + name
					if name == "(not set)" {
						label = "来源未知（GA 记为 not set，常见于脚本或机器人直接发的请求）"
					}
					if kind == "geo" {
						label = "地区 " + CountryName(name)
					}
					items = append(items, [2]string{label, fmt.Sprintf("%s 次访问，认真看的只有 %.0f%%", Thousands(n), ratio(e, n)*100)})
				}
			}
			rows.Close()
		}
		if len(items) > 0 {
			out = append(out, Check{Status: "warn", Title: "可能有机器人或刷量流量",
				Detail: "近 28 天有来源的访问几乎都是 0 秒离开。它们会拉低认真阅读比例、抬高访客数。可以在 GA「管理 → 数据过滤器」里排除，或在 GA 里确认后再决定。", Items: items})
		} else {
			out = append(out, Check{Status: "ok", Title: "没有发现异常的机器人流量", Detail: "近 28 天没有“访问很多、但几乎都 0 秒离开”的来源或地区。"})
		}
	}

	// 6. GA 同步错误
	if st, _ := m.Store.SyncState(ctx, site.ID, "ga"); st.Status == "error" {
		out = append(out, Check{Status: "error", Title: "GA 同步失败", Detail: st.LastError})
	}

	// 6. 排除路径
	if len(site.ExcludePaths) > 0 {
		out = append(out, Check{Status: "ok", Title: "已排除不统计的路径", Detail: strings.Join(site.ExcludePaths, "、") + " 不计入内容分析。"})
	}
	return out
}

func HealthSummary(cs []Check) string {
	n := map[string]int{}
	for _, c := range cs {
		n[c.Status]++
	}
	var parts []string
	if n["ok"] > 0 {
		parts = append(parts, fmt.Sprintf("%d 项正常", n["ok"]))
	}
	if n["error"]+n["warn"] > 0 {
		parts = append(parts, fmt.Sprintf("%d 项需处理", n["error"]+n["warn"]))
	}
	if n["info"] > 0 {
		parts = append(parts, fmt.Sprintf("%d 项提示", n["info"]))
	}
	return strings.Join(parts, " · ")
}

// Thousands 把数字格式化成 12,400。
func Thousands(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "−" + b.String()
	}
	return b.String()
}
