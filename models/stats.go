package models

import (
	"time"

	"gorm.io/gorm"
)

// DailyStats 表结构：支持 OS、浏览器、来源、页面等多维度统计
type DailyStats struct {
	gorm.Model           // 自动添加 ID、CreatedAt、UpdatedAt、DeletedAt 字段
	SiteID     uint64    `gorm:"not null"`           // 网站ID
	Category   string    `gorm:"size:50;not null"`   // 分类 (os, browser, region, referrer, page)
	Item       string    `gorm:"size:255;not null"`  // 分类下的具体项 (如 "Windows", "Chrome", "CN-Guangdong")
	PV         int64     `gorm:"not null;default:0"` // 浏览量
	Date       time.Time `gorm:"type:date;not null"` // 统计日期 (按天)
}

// 表名
func (DailyStats) TableName() string {
	return "daily_stats"
}

// SimpleSiteStats 详细网站统计信息
type SimpleSiteStats struct {
	SiteID      uint64  `json:"site_id"`
	PV          int64   `json:"pv"`           // 页面浏览量
	UV          int64   `json:"uv"`           // 独立访客数
	IPCount     int64   `json:"ip_count"`     // 独立IP数
	EventCount  int64   `json:"event_count"`  // 事件数
	BounceRate  float64 `json:"bounce_rate"`  // 跳出率
	AvgDuration float64 `json:"avg_duration"` // 平均访问时长（秒）
	WeekUv      int64   `json:"week_uv"`      // 本周UV
	WeekPv      int64   `json:"week_pv"`      // 本周PV
	MonthUv     int64   `json:"month_uv"`     // 本月UV
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
