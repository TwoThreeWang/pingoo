package store

import (
	"context"
	"time"
)

// DayTotals 是一个网站某一天的总数（邮件简报用）。
type DayTotals struct {
	Users, Sessions, Views int
	HasGA                  bool
	Clicks, Impressions    int
	GSCDay                 time.Time // Search Console 最近有数据的那天（它有两三天延迟）
	HasGSC                 bool
}

// Totals 返回网站在 day 这一天的 GA 总数，以及 day 当天或之前最近一天的 Search Console 总数。
func (s *Store) Totals(ctx context.Context, siteID int64, day time.Time) DayTotals {
	var t DayTotals
	d := day.Format("2006-01-02")
	t.HasGA = s.Pool.QueryRow(ctx, `SELECT active_users, sessions, page_views FROM ga_daily WHERE site_id=$1 AND date=$2::date`, siteID, d).
		Scan(&t.Users, &t.Sessions, &t.Views) == nil
	t.HasGSC = s.Pool.QueryRow(ctx, `SELECT clicks, impressions, date FROM gsc_daily WHERE site_id=$1 AND date <= $2::date AND date > $2::date - 5
		ORDER BY date DESC LIMIT 1`, siteID, d).Scan(&t.Clicks, &t.Impressions, &t.GSCDay) == nil
	return t
}
