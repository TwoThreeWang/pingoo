package models

import (
	"time"

	"gorm.io/gorm"
)

type Event struct {
	ID         uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID  string         `gorm:"type:varchar(64);index" json:"session_id"`
	UserID     string         `gorm:"type:varchar(64);index" json:"user_id"`
	IP         string         `gorm:"type:varchar(64);index" json:"ip"`
	URL        string         `gorm:"type:text" json:"url"`
	Referrer   string         `gorm:"type:text" json:"referrer"`
	UserAgent  string         `gorm:"type:text" json:"user_agent"`
	Device     string         `gorm:"type:varchar(32);index" json:"device"`
	Browser    string         `gorm:"type:varchar(32);index" json:"browser"`
	OS         string         `gorm:"type:varchar(32);index" json:"os"`
	Screen     string         `gorm:"type:varchar(16)" json:"screen"`
	EventType  string         `gorm:"type:varchar(32);index" json:"event_type"`
	EventValue string         `gorm:"type:text" json:"event_value"`
	CreatedAt  time.Time      `gorm:"index" json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 设置表名
func (Event) TableName() string {
	return "events"
}

// EventCreate 创建事件的结构体
type EventCreate struct {
	SessionID  string `json:"session_id" binding:"required"`
	UserID     string `json:"user_id"`
	IP         string `json:"ip"`
	URL        string `json:"url" binding:"required"`
	Referrer   string `json:"referrer"`
	UserAgent  string `json:"user_agent"`
	Device     string `json:"device"`
	Browser    string `json:"browser"`
	OS         string `json:"os"`
	Screen     string `json:"screen"`
	EventType  string `json:"event_type" binding:"required"`
	EventValue string `json:"event_value"`
}

// EventQuery 查询事件的结构体
type EventQuery struct {
	SessionID string `json:"session_id" form:"session_id"`
	UserID    string `json:"user_id" form:"user_id"`
	IP        string `json:"ip" form:"ip"`
	URL       string `json:"url" form:"url"`
	Device    string `json:"device" form:"device"`
	Browser   string `json:"browser" form:"browser"`
	OS        string `json:"os" form:"os"`
	EventType string `json:"event_type" form:"event_type"`
	StartTime string `json:"start_time" form:"start_time"`
	EndTime   string `json:"end_time" form:"end_time"`
	Page      int    `json:"page" form:"page"`
	PageSize  int    `json:"page_size" form:"page_size"`
}

// EventStats 事件统计结构体
type EventStats struct {
	TotalPV     int64   `json:"total_pv"`
	TotalUV     int64   `json:"total_uv"`
	BounceRate  float64 `json:"bounce_rate"`
	AvgDuration float64 `json:"avg_duration"`
}