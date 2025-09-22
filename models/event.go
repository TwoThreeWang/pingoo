package models

import (
	"gorm.io/gorm"
)

type Event struct {
	gorm.Model         // 自动添加 ID、CreatedAt、UpdatedAt、DeletedAt 字段
	SiteID      uint64 `gorm:"index;not null" json:"site_id"`             // 关联站点ID
	SessionID   string `gorm:"type:varchar(64);index" json:"session_id"`  // 会话ID
	UserID      string `gorm:"type:varchar(64);index" json:"user_id"`     // 用户ID
	IP          string `gorm:"type:varchar(64);index" json:"ip"`          // IP
	URL         string `gorm:"type:text" json:"url"`                      // 网址
	Referrer    string `gorm:"type:text" json:"referrer"`                 // 来源
	UserAgent   string `gorm:"type:text" json:"user_agent"`               // 浏览器标识
	Device      string `gorm:"type:varchar(32);index" json:"device"`      // 设备
	Browser     string `gorm:"type:varchar(32);index" json:"browser"`     // 浏览器
	OS          string `gorm:"type:varchar(32);index" json:"os"`          // 操作系统
	Screen      string `gorm:"type:varchar(16)" json:"screen"`            // 屏幕尺寸
	IsBot       bool   `gorm:"type:boolean" json:"is_bot"`                // 是否爬虫
	Country     string `gorm:"type:varchar(32);index" json:"country"`     // 国家
	Subdivision string `gorm:"type:varchar(32);index" json:"subdivision"` // 省份
	City        string `gorm:"type:varchar(32);index" json:"city"`        // 城市
	ISP         string `gorm:"type:varchar(32);index" json:"isp"`         // 运营商
	EventType   string `gorm:"type:varchar(32);index" json:"event_type"`  // 事件类型
	EventValue  string `gorm:"type:text" json:"event_value"`              // 事件值

	// 关联关系
	Site Site `gorm:"foreignKey:SiteID" json:"site,omitempty"`
}

// TableName 设置表名
func (Event) TableName() string {
	return "events"
}

// EventCreate 创建事件的结构体
type EventCreate struct {
	SiteID      uint64 `json:"site_id" binding:"required"` // 站点ID
	SessionID   string `json:"session_id" binding:"required"`
	UserID      string `json:"user_id"`
	IP          string `json:"ip"`
	URL         string `json:"url" binding:"required"`
	Referrer    string `json:"referrer"`
	UserAgent   string `json:"user_agent"`
	Device      string `json:"device"`
	Browser     string `json:"browser"`
	OS          string `json:"os"`
	Screen      string `json:"screen"`
	IsBot       bool   `json:"is_bot"`
	Country     string `json:"country"`
	Subdivision string `json:"subdivision"`
	City        string `json:"city"`
	Isp         string `json:"isp"`
	EventType   string `json:"event_type" binding:"required"`
	EventValue  string `json:"event_value"`
}

// EventQuery 查询事件的结构体
type EventQuery struct {
	SiteID      uint64 `json:"site_id" form:"site_id"` // 站点ID
	SessionID   string `json:"session_id" form:"session_id"`
	UserID      string `json:"user_id" form:"user_id"`
	IP          string `json:"ip" form:"ip"`
	URL         string `json:"url" form:"url"`
	Device      string `json:"device" form:"device"`
	Browser     string `json:"browser" form:"browser"`
	OS          string `json:"os" form:"os"`
	Screen      string `json:"screen" form:"screen"`
	IsBot       string `json:"is_bot" form:"is_bot"`
	Country     string `json:"country" form:"country"`
	Subdivision string `json:"subdivision" form:"subdivision"`
	City        string `json:"city" form:"city"`
	Isp         string `json:"isp" form:"isp"`
	EventType   string `json:"event_type" form:"event_type"`
	StartTime   string `json:"start_time" form:"start_time"`
	EndTime     string `json:"end_time" form:"end_time"`
	Page        int    `json:"page" form:"page"`
	PageSize    int    `json:"page_size" form:"page_size"`
}

// EventStats 事件统计结构体
type EventStats struct {
	TotalPV     int64   `json:"total_pv"`
	TotalUV     int64   `json:"total_uv"`
	BounceRate  float64 `json:"bounce_rate"`
	AvgDuration float64 `json:"avg_duration"`
}

// TypeStat 指标统计
type TypeStat struct {
	TypeData string `json:"type_data"`
	Count    int64  `json:"count"`
}
