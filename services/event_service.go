package services

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"pingoo/database"
	"pingoo/models"

	"gorm.io/gorm"
)

type EventService struct{}

// NewEventService 创建事件服务实例
func NewEventService() *EventService {
	return &EventService{}
}

// CreateEvent 创建事件
func (s *EventService) CreateEvent(eventCreate *models.EventCreate) (*models.Event, error) {
	if eventCreate.SessionID == "" || eventCreate.URL == "" || eventCreate.EventType == "" {
		return nil, errors.New("缺少必需参数")
	}

	event := &models.Event{
		SessionID:   eventCreate.SessionID,
		SiteID:      eventCreate.SiteID,
		UserID:      eventCreate.UserID,
		IP:          eventCreate.IP,
		URL:         eventCreate.URL,
		Referrer:    eventCreate.Referrer,
		UserAgent:   eventCreate.UserAgent,
		Device:      eventCreate.Device,
		Browser:     eventCreate.Browser,
		OS:          eventCreate.OS,
		Screen:      eventCreate.Screen,
		IsBot:       eventCreate.IsBot,
		Country:     eventCreate.Country,
		City:        eventCreate.City,
		Subdivision: eventCreate.Subdivision,
		EventType:   eventCreate.EventType,
		EventValue:  eventCreate.EventValue,
	}

	db := database.GetDB()

	// 使用事务处理
	err := db.Transaction(func(tx *gorm.DB) error {
		// 创建事件
		if err := tx.Create(event).Error; err != nil {
			return fmt.Errorf("创建事件失败: %v", err)
		}

		// 查找现有会话
		var session models.Session
		err := tx.Where("session_id = ? AND site_id = ?", event.SessionID, event.SiteID).First(&session).Error

		now := time.Now()
		AfterthirtyMinutes := now.Add(30 * time.Minute)

		if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
			// 会话不存在，创建新会话
			newSession := models.Session{
				SessionID: event.SessionID,
				SiteID:    event.SiteID,
				UserID:    event.UserID,
				IP:        event.IP,
				StartTime: now,
				EndTime:   AfterthirtyMinutes,
				Pages:     1,
				Duration:  0,
			}
			if err := tx.Create(&newSession).Error; err != nil {
				return fmt.Errorf("创建会话失败: %v", err)
			}
		} else if err == nil {
			// 会话存在，更新现有会话
			updates := map[string]interface{}{
				"pages":    session.Pages + 1,
				"end_time": AfterthirtyMinutes,
				"duration": int(now.Sub(session.StartTime).Seconds()),
			}
			if err := tx.Model(&session).Updates(updates).Error; err != nil {
				return fmt.Errorf("更新会话失败: %v", err)
			}
		} else if err != nil {
			return fmt.Errorf("查询会话失败: %v", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return event, nil
}

// GetEvents 获取事件列表
func (s *EventService) GetEvents(query *models.EventQuery) ([]models.Event, int64, error) {
	db := database.GetDB().Model(&models.Event{})

	// 构建查询条件
	if query.SessionID != "" {
		db = db.Where("session_id = ?", query.SessionID)
	}
	if query.UserID != "" {
		db = db.Where("user_id = ?", query.UserID)
	}
	if query.IP != "" {
		db = db.Where("ip = ?", query.IP)
	}
	if query.URL != "" {
		db = db.Where("url LIKE ?", "%"+query.URL+"%")
	}
	if query.Device != "" {
		db = db.Where("device = ?", query.Device)
	}
	if query.Browser != "" {
		db = db.Where("browser = ?", query.Browser)
	}
	if query.OS != "" {
		db = db.Where("os = ?", query.OS)
	}
	if query.EventType != "" {
		db = db.Where("event_type = ?", query.EventType)
	}
	if query.IsBot != "" {
		isBot, err := strconv.ParseBool(query.IsBot)
		if err == nil {
			db = db.Where("is_bot = ?", isBot)
		} else {
			log.Printf("解析IsBot参数失败: %v", err)
		}
	}

	// 时间范围查询
	if query.StartTime != "" {
		startTime, err := time.Parse("2006-01-02", query.StartTime)
		if err == nil {
			db = db.Where("created_at >= ?", startTime)
		}
	}
	if query.EndTime != "" {
		endTime, err := time.Parse("2006-01-02", query.EndTime)
		if err == nil {
			endTime = endTime.Add(24 * time.Hour) // 包含当天
			db = db.Where("created_at <= ?", endTime)
		}
	}

	// 统计总数
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计事件数量失败: %v", err)
	}

	// 分页查询
	page := query.Page
	pageSize := query.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	var events []models.Event
	offset := (page - 1) * pageSize
	if err := db.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&events).Error; err != nil {
		return nil, 0, fmt.Errorf("查询事件列表失败: %v", err)
	}

	return events, total, nil
}

// GetEventStats 获取事件统计信息
func (s *EventService) GetEventStats(query *models.EventQuery) (*models.EventStats, error) {
	if query.SiteID == 0 {
		return nil, errors.New("站点ID不能为空")
	}

	db := database.GetDB().Model(&models.Event{}).Where("site_id = ?", query.SiteID)

	// 时间范围查询
	if query.StartTime != "" {
		startTime, err := time.Parse("2006-01-02", query.StartTime)
		if err == nil {
			db = db.Where("created_at >= ?", startTime)
		}
	}
	if query.EndTime != "" {
		endTime, err := time.Parse("2006-01-02", query.EndTime)
		if err == nil {
			endTime = endTime.Add(24 * time.Hour)
			db = db.Where("created_at <= ?", endTime)
		}
	}

	// 统计总PV（页面浏览量）
	var totalPV int64
	if err := db.Count(&totalPV).Error; err != nil {
		return nil, fmt.Errorf("统计PV失败: %v", err)
	}

	// 统计总UV（独立访客）
	var totalUV int64
	if err := db.Distinct("session_id").Count(&totalUV).Error; err != nil {
		return nil, fmt.Errorf("统计UV失败: %v", err)
	}

	// 计算跳出率（只访问了一个页面的会话数/总会话数）
	var bounceRate float64
	if totalUV > 0 {
		var singlePageSessions int64
		subQuery := db.Select("session_id").Group("session_id").Having("COUNT(*) = 1")
		if err := db.Model(&models.Event{}).Where("session_id IN (?)", subQuery).Distinct("session_id").Count(&singlePageSessions).Error; err != nil {
			return nil, fmt.Errorf("统计跳出率失败: %v", err)
		}
		bounceRate = float64(singlePageSessions) / float64(totalUV) * 100
	}

	stats := &models.EventStats{
		TotalPV:    totalPV,
		TotalUV:    totalUV,
		BounceRate: bounceRate,
	}

	return stats, nil
}

// GetEventByID 根据ID获取事件
func (s *EventService) GetEventByID(id uint64) (*models.Event, error) {
	var event models.Event
	db := database.GetDB()
	if err := db.First(&event, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("事件不存在")
		}
		return nil, fmt.Errorf("查询事件失败: %v", err)
	}
	return &event, nil
}

// GetSimpleStats 获取详细网站统计信息
func (s *EventService) GetSimpleStats(siteID uint64, startDate string, endDate string) (*models.SimpleSiteStats, error) {
	var stats models.SimpleSiteStats
	db := database.GetDB()

	// 解析日期
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, fmt.Errorf("开始日期格式错误: %v", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, fmt.Errorf("结束日期格式错误: %v", err)
	}
	end = end.Add(24 * time.Hour).Add(-time.Nanosecond)

	stats.SiteID = siteID
	stats.StartDate = startDate
	stats.EndDate = endDate

	// 获取PV（页面浏览量）
	if err := db.Model(&models.Event{}).
		Where("site_id = ? AND event_type = 'page_view' AND created_at BETWEEN ? AND ?", siteID, start, end).
		Count(&stats.PV).Error; err != nil {
		return nil, fmt.Errorf("统计PV失败: %v", err)
	}

	// 获取UV（独立访客数）
	if err := db.Model(&models.Event{}).
		Where("site_id = ? AND created_at BETWEEN ? AND ?", siteID, start, end).
		Distinct("session_id").
		Count(&stats.UV).Error; err != nil {
		return nil, fmt.Errorf("统计UV失败: %v", err)
	}

	// 获取访客IP数量
	if err := db.Model(&models.Event{}).
		Where("site_id = ? AND created_at BETWEEN ? AND ?", siteID, start, end).
		Distinct("ip").
		Count(&stats.IPCount).Error; err != nil {
		return nil, fmt.Errorf("统计IP数量失败: %v", err)
	}

	// 获取跳出率和平均访问时长
	var totalSessions int64
	var bounceSessions int64
	var totalDuration int64

	// 获取总会话数
	if err := db.Model(&models.Session{}).
		Where("site_id = ? AND start_time BETWEEN ? AND ?", siteID, start, end).
		Count(&totalSessions).Error; err != nil {
		return nil, fmt.Errorf("统计会话数失败: %v", err)
	}

	// 获取跳出会话数（只访问了一个页面的会话）
	if err := db.Model(&models.Session{}).
		Where("site_id = ? AND start_time BETWEEN ? AND ? AND pages = 1", siteID, start, end).
		Count(&bounceSessions).Error; err != nil {
		return nil, fmt.Errorf("统计跳出会话失败: %v", err)
	}

	// 获取总访问时长
	if err := db.Model(&models.Session{}).
		Where("site_id = ? AND start_time BETWEEN ? AND ?", siteID, start, end).
		Select("COALESCE(SUM(duration), 0)").
		Scan(&totalDuration).Error; err != nil {
		return nil, fmt.Errorf("统计访问时长失败: %v", err)
	}

	// 计算跳出率
	if totalSessions > 0 {
		stats.BounceRate = float64(bounceSessions) / float64(totalSessions) * 100
	} else {
		stats.BounceRate = 0
	}

	// 计算平均访问时长
	if totalSessions > 0 {
		stats.AvgDuration = float64(totalDuration) / float64(totalSessions)
	} else {
		stats.AvgDuration = 0
	}

	return &stats, nil
}
