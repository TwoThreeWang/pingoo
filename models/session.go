package models

import (
	"time"

	"gorm.io/gorm"
)

// Session 会话表模型
type Session struct {
	gorm.Model           // 自动添加 ID、CreatedAt、UpdatedAt、DeletedAt 字段
	SessionID  string    `gorm:"column:session_id;type:varchar(64);primaryKey;comment:会话ID"` // 会话ID
	SiteID     uint64    `gorm:"column:site_id;type:int8;comment:关联站点ID"`                    // 关联站点ID
	UserID     string    `gorm:"column:user_id;type:varchar(64);comment:登录用户ID（可选）"`         // 登录用户ID（可选）
	IP         string    `gorm:"column:ip;type:varchar(64);comment:访客IP"`                    // 访客IP
	StartTime  time.Time `gorm:"column:start_time;comment:会话开始时间"`                           // 会话开始时间
	EndTime    time.Time `gorm:"column:end_time;comment:会话结束时间"`                             // 会话结束时间
	Pages      int       `gorm:"column:pages;comment:本次访问的页面数"`                              // 本次访问的页面数
	Duration   int       `gorm:"column:duration;comment:本次访问的总时长（秒）"`                        // 本次访问的总时长（秒）
}

// TableName 设置表名
func (Session) TableName() string {
	return "sessions"
}

// SessionCreate 创建会话的请求结构体
type SessionCreate struct {
	SessionID string `json:"session_id" binding:"required"`
	UserID    string `json:"user_id,omitempty"`
	IP        string `json:"ip" binding:"required"`
	StartTime string `json:"start_time" binding:"required"`
	EndTime   string `json:"end_time" binding:"required"`
	Pages     int    `json:"pages" binding:"required"`
	Duration  int    `json:"duration" binding:"required"`
}

// SessionQuery 查询会话的请求参数
type SessionQuery struct {
	SessionID string `form:"session_id"`
	UserID    string `form:"user_id"`
	IP        string `form:"ip"`
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
	Page      int    `form:"page,default=1"`
	PageSize  int    `form:"page_size,default=20"`
}

// SessionStats 会话统计信息
type SessionStats struct {
	TotalSessions int     `json:"total_sessions"`
	TotalUsers    int     `json:"total_users"`
	TotalPages    int     `json:"total_pages"`
	AvgDuration   float64 `json:"avg_duration"`
	AvgPages      float64 `json:"avg_pages"`
	StartDate     string  `json:"start_date"`
	EndDate       string  `json:"end_date"`
}
