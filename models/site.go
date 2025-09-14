package models

import (
	"time"

	"gorm.io/gorm"
)

// Site 站点模型
type Site struct {
	gorm.Model        // 自动添加 ID、CreatedAt、UpdatedAt、DeletedAt 字段
	UserID     uint64 `gorm:"index;not null" json:"user_id"` // 所属用户ID
	Name       string `gorm:"type:varchar(100);not null" json:"name"`
	Domain     string `gorm:"type:varchar(255);uniqueIndex;not null" json:"domain"`

	// 关联关系
	User   User    `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Events []Event `gorm:"foreignKey:SiteID" json:"events,omitempty"`
}

// TableName 设置表名
func (Site) TableName() string {
	return "sites"
}

// SiteCreate 创建站点结构体
type SiteCreate struct {
	Name   string `json:"name" binding:"required,min=1,max=100"`
	Domain string `json:"domain" binding:"required,url"`
}

// SiteUpdate 更新站点结构体
type SiteUpdate struct {
	Name   string `json:"name" binding:"max=100"`
	Domain string `json:"domain" binding:"required,url"`
}

// SiteResponse 站点响应结构体
type SiteResponse struct {
	ID         uint64    `json:"id"`
	UserID     uint64    `json:"user_id"`
	Name       string    `json:"name"`
	Domain     string    `json:"domain"`
	TrackingID string    `json:"tracking_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	EventCount  int64 `json:"event_count,omitempty"`  // 事件总数
	UserCount   int64 `json:"user_count,omitempty"`   // 用户数
	PageView    int64 `json:"page_view,omitempty"`    // 页面浏览量
	UniqueUsers int64 `json:"unique_users,omitempty"` // 独立用户数
}

// SiteQuery 查询站点结构体
type SiteQuery struct {
	UserID   uint64 `json:"user_id" form:"user_id"`
	Name     string `json:"name" form:"name"`
	Domain   string `json:"domain" form:"domain"`
	Page     int    `json:"page" form:"page"`
	PageSize int    `json:"page_size" form:"page_size"`
}

// JSONB 自定义JSONB类型
type JSONB map[string]interface{}

// SiteStats 站点统计结构体
type SiteStats struct {
	SiteID       uint64          `json:"site_id"`
	TotalEvents  int64           `json:"total_events"`
	TotalUsers   int64           `json:"total_users"`
	TotalPV      int64           `json:"total_pv"`
	TotalUV      int64           `json:"total_uv"`
	BounceRate   float64         `json:"bounce_rate"`
	AvgDuration  float64         `json:"avg_duration"`
	TopPages     []PageStats     `json:"top_pages"`
	TopReferrers []ReferrerStats `json:"top_referrers"`
}

// PageStats 页面统计
type PageStats struct {
	URL    string `json:"url"`
	PV     int64  `json:"pv"`
	UV     int64  `json:"uv"`
	Bounce int64  `json:"bounce"`
}

// ReferrerStats 来源统计
type ReferrerStats struct {
	Referrer string `json:"referrer"`
	Count    int64  `json:"count"`
}
