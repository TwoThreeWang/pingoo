package metrics

import (
	"context"
	"time"

	"pingoo/internal/period"
	"pingoo/internal/store"
)

type SourceRow struct {
	Name      string // 渠道或来源
	Sub       string // 英文渠道名 / 次要信息
	Sessions  int
	Prev      int
	EngRate   float64
	PPV       float64
	Dwell     float64 // 每次访问平均停留秒数
	Note      string
	FirstSeen time.Time
	Landing   string
	IsNew     bool
}

var SourceTabs = []struct{ Key, Label string }{
	{"channels", "渠道对比"}, {"referrers", "新外链"}, {"ai", "AI 来源"}, {"top", "外链排行"}, {"utm", "推广活动"},
}

var channelNames = map[string]string{
	"Organic Search": "自然搜索", "Direct": "直接访问", "Referral": "外链", "Organic Social": "社交", "Email": "邮件",
	"Paid Search": "付费搜索", "Paid Social": "付费社交", "Organic Video": "视频", "Display": "展示广告", "Unassigned": "未分类",
	"Cross-network": "跨网络广告", "Organic Shopping": "购物", "Affiliates": "联盟推广", "SMS": "短信", "Mobile Push Notifications": "推送通知",
	"AI": "AI 助手",
}

func ChannelName(c string) string {
	if v, ok := channelNames[c]; ok {
		return v
	}
	return c
}

func (m *Svc) Sources(ctx context.Context, site store.Site, r period.Range, tab string) ([]SourceRow, error) {
	if r.Hourly {
		r = period.Resolve("d7", time.Now(), site.Location())
	}
	switch tab {
	case "referrers":
		return m.newReferrers(ctx, site, r)
	case "ai":
		return m.sourceList(ctx, site, r, `source ~* $6`, AIRegex, 10)
	case "top":
		return m.sourceList(ctx, site, r, `channel IN ('Referral','Organic Social') AND NOT source ~* $6`, AIRegex, 12)
	case "utm":
		return m.campaigns(ctx, site, r)
	}
	return m.channels(ctx, site, r)
}

func (m *Svc) channels(ctx context.Context, site store.Site, r period.Range) ([]SourceRow, error) {
	rows, err := m.Store.Pool.Query(ctx, `
		SELECT CASE WHEN source ~* $6 THEN 'AI' ELSE channel END AS ch,
		  coalesce(sum(sessions) FILTER (WHERE date BETWEEN $2 AND $3),0), coalesce(sum(sessions) FILTER (WHERE date BETWEEN $4 AND $5),0),
		  coalesce(sum(engaged_sessions) FILTER (WHERE date BETWEEN $2 AND $3),0), coalesce(sum(page_views) FILTER (WHERE date BETWEEN $2 AND $3),0),
		  coalesce(sum(engagement_seconds) FILTER (WHERE date BETWEEN $2 AND $3),0)
		FROM ga_daily_sources WHERE site_id=$1 AND date BETWEEN $4 AND $3 GROUP BY ch ORDER BY 2 DESC`,
		site.ID, r.Start, r.End, r.PrevStart, r.PrevEnd, AIRegex)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceRow
	for rows.Next() {
		var x SourceRow
		var eng, pv int
		var sec float64
		if err := rows.Scan(&x.Sub, &x.Sessions, &x.Prev, &eng, &pv, &sec); err != nil {
			return nil, err
		}
		if x.Sessions == 0 && x.Prev == 0 {
			continue
		}
		x.Name = ChannelName(x.Sub)
		if x.Sub == "AI" {
			x.Sub = "chatgpt / perplexity 等"
		}
		x.EngRate = ratio(float64(eng), float64(x.Sessions))
		x.PPV = ratio(float64(pv), float64(x.Sessions))
		x.Dwell = ratio(sec, float64(x.Sessions))
		out = append(out, x)
	}
	annotateChannels(out)
	return out, rows.Err()
}

// annotateChannels 给每个渠道写一句话：谁来得多、谁质量高、谁来了就走。
func annotateChannels(rows []SourceRow) {
	if len(rows) == 0 {
		return
	}
	best, bestRate := -1, 0.0
	for i, x := range rows {
		if x.Sessions >= 30 && x.EngRate > bestRate {
			best, bestRate = i, x.EngRate
		}
	}
	for i := range rows {
		x := &rows[i]
		low := x.Sessions >= 30 && x.EngRate < 0.42
		fast := x.Prev > 0 && float64(x.Sessions) > float64(x.Prev)*1.5 && x.Sessions >= 30
		switch {
		case i == best:
			x.Note = "读者质量最高"
		case i == 0 && x.EngRate >= 0.5:
			x.Note = "来得最多，质量也不错"
		case i == 0 && low:
			x.Note = "来得最多，但大多看完就走"
		case fast && low:
			x.Note = "增长很快，但大多看完就走"
		case low:
			x.Note = "大多看完就走"
		case fast:
			x.Note = "增长很快"
		case x.Prev >= 30 && float64(x.Sessions) < float64(x.Prev)*0.7:
			x.Note = "明显减少"
		}
	}
}

func (m *Svc) sourceList(ctx context.Context, site store.Site, r period.Range, where, arg string, limit int) ([]SourceRow, error) {
	rows, err := m.Store.Pool.Query(ctx, `
		SELECT lower(source),
		  coalesce(sum(sessions) FILTER (WHERE date BETWEEN $2 AND $3),0), coalesce(sum(sessions) FILTER (WHERE date BETWEEN $4 AND $5),0),
		  coalesce(sum(engaged_sessions) FILTER (WHERE date BETWEEN $2 AND $3),0),
		  coalesce(sum(engagement_seconds) FILTER (WHERE date BETWEEN $2 AND $3),0)
		FROM ga_daily_sources WHERE site_id=$1 AND date BETWEEN $4 AND $3 AND `+where+`
		GROUP BY 1 HAVING coalesce(sum(sessions) FILTER (WHERE date BETWEEN $2 AND $3),0) > 0 ORDER BY 2 DESC LIMIT `+itoa(int64(limit)),
		site.ID, r.Start, r.End, r.PrevStart, r.PrevEnd, arg)
	if err != nil {
		return nil, err
	}
	var out []SourceRow
	for rows.Next() {
		var x SourceRow
		var eng int
		var sec float64
		if err := rows.Scan(&x.Name, &x.Sessions, &x.Prev, &eng, &sec); err != nil {
			rows.Close()
			return nil, err
		}
		x.EngRate = ratio(float64(eng), float64(x.Sessions))
		x.Dwell = ratio(sec, float64(x.Sessions))
		x.IsNew = x.Prev == 0
		out = append(out, x)
	}
	rows.Close()
	m.fillLanding(ctx, site.ID, r, out)
	return out, nil
}

// fillLanding 找出每个来源带来的访客最常落在哪个页面。
func (m *Svc) fillLanding(ctx context.Context, siteID int64, r period.Range, rows []SourceRow) {
	for i := range rows {
		var path string
		_ = m.Store.Pool.QueryRow(ctx, `SELECT l.path FROM ga_daily_landing l WHERE l.site_id=$1 AND lower(l.source)=$2 AND l.date BETWEEN $3 AND $4 AND l.path NOT IN ('(not set)','(other)')
			GROUP BY l.path ORDER BY sum(l.sessions) DESC LIMIT 1`, siteID, rows[i].Name, r.Start, r.End).Scan(&path)
		if path != "" {
			var title string
			_ = m.Store.Pool.QueryRow(ctx, `SELECT title FROM ga_daily_pages WHERE site_id=$1 AND path=$2 AND title<>'' ORDER BY date DESC LIMIT 1`, siteID, path).Scan(&title)
			if title != "" {
				rows[i].Landing = BookTitle(title, rows[i].Landing)
			} else {
				rows[i].Landing = path
			}
		}
	}
}

// newReferrers 是本期第一次出现、且带来一定访客的外链网站。
func (m *Svc) newReferrers(ctx context.Context, site store.Site, r period.Range) ([]SourceRow, error) {
	minSess := max(20, r.Days)
	rows, err := m.Store.Pool.Query(ctx, `
		SELECT lower(source), min(date) FILTER (WHERE date BETWEEN $2 AND $3),
		  coalesce(sum(sessions) FILTER (WHERE date BETWEEN $2 AND $3),0),
		  coalesce(sum(engaged_sessions) FILTER (WHERE date BETWEEN $2 AND $3),0),
		  coalesce(sum(engagement_seconds) FILTER (WHERE date BETWEEN $2 AND $3),0)
		FROM ga_daily_sources
		WHERE site_id=$1 AND date BETWEEN $2::date - 90 AND $3 AND channel IN ('Referral','Organic Social') AND source NOT IN ('(not set)','(direct)')
		GROUP BY 1
		HAVING coalesce(sum(sessions) FILTER (WHERE date < $2),0) = 0 AND coalesce(sum(sessions) FILTER (WHERE date BETWEEN $2 AND $3),0) >= $4
		ORDER BY 3 DESC LIMIT 10`, site.ID, r.Start, r.End, minSess)
	if err != nil {
		return nil, err
	}
	var out []SourceRow
	for rows.Next() {
		var x SourceRow
		var first *time.Time
		var eng int
		var sec float64
		if err := rows.Scan(&x.Name, &first, &x.Sessions, &eng, &sec); err != nil {
			rows.Close()
			return nil, err
		}
		if first != nil {
			x.FirstSeen = *first
		}
		x.EngRate = ratio(float64(eng), float64(x.Sessions))
		x.Dwell = ratio(sec, float64(x.Sessions))
		x.IsNew = true
		out = append(out, x)
	}
	rows.Close()
	m.fillLanding(ctx, site.ID, r, out)
	return out, nil
}

func (m *Svc) campaigns(ctx context.Context, site store.Site, r period.Range) ([]SourceRow, error) {
	rows, err := m.Store.Pool.Query(ctx, `
		SELECT campaign, string_agg(DISTINCT lower(source), ' / '),
		  coalesce(sum(sessions) FILTER (WHERE date BETWEEN $2 AND $3),0), coalesce(sum(sessions) FILTER (WHERE date BETWEEN $4 AND $5),0),
		  coalesce(sum(engaged_sessions) FILTER (WHERE date BETWEEN $2 AND $3),0),
		  coalesce(sum(engagement_seconds) FILTER (WHERE date BETWEEN $2 AND $3),0)
		FROM ga_daily_sources WHERE site_id=$1 AND date BETWEEN $4 AND $3
		  AND campaign NOT IN ('(not set)','(direct)','(organic)','(referral)','') GROUP BY campaign
		HAVING coalesce(sum(sessions) FILTER (WHERE date BETWEEN $2 AND $3),0) > 0 ORDER BY 3 DESC LIMIT 15`,
		site.ID, r.Start, r.End, r.PrevStart, r.PrevEnd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceRow
	for rows.Next() {
		var x SourceRow
		var eng int
		var sec float64
		if err := rows.Scan(&x.Name, &x.Sub, &x.Sessions, &x.Prev, &eng, &sec); err != nil {
			return nil, err
		}
		x.EngRate = ratio(float64(eng), float64(x.Sessions))
		x.Dwell = ratio(sec, float64(x.Sessions))
		out = append(out, x)
	}
	return out, rows.Err()
}
