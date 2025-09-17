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
	StartDate   string  `json:"start_time"`   // 开始时间
	EndDate     string  `json:"end_time"`     // 结束时间
}
