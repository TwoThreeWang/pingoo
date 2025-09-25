package models

// SimpleSiteStats 详细网站统计信息
type SimpleSiteStats struct {
	SiteID      uint64  `json:"site_id"`
	PV          int64   `json:"pv"`           // 页面浏览量
	UV          int64   `json:"uv"`           // 独立访客数
	IPCount     int64   `json:"ip_count"`     // 独立IP数
	EventCount  int64   `json:"event_count"`  // 事件数
	BounceRate  float64 `json:"bounce_rate"`  // 跳出率
	AvgDuration float64 `json:"avg_duration"` // 平均访问时长（秒）
	WeekIp      int64   `json:"week_ip"`      // 本周IP
	WeekPv      int64   `json:"week_pv"`      // 本周PV
	MonthIp     int64   `json:"month_ip"`     // 本月IP
	MonthPv     int64   `json:"month_pv"`     // 本月PV
	HourlyStats []struct {
		Hour  int   `json:"hour"`
		Count int64 `json:"count"`
	} `json:"hourly_stats"` // 按小时流量分布
	StartDate string `json:"start_time"` // 开始时间
	EndDate   string `json:"end_time"`   // 结束时间
}

type RankStats struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}
