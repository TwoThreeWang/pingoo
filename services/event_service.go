package services

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"pingoo/database"
	"pingoo/models"
	"pingoo/utils"

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
		ISP:         eventCreate.Isp,
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

// GetEvents 根据站点ID获取事件列表
func (s *EventService) GetEventsDetail(query *models.EventQuery) ([]models.Event, int64, error) {
	if query.SiteID == 0 {
		return nil, 0, errors.New("站点ID不能为空")
	}
	db := database.GetDB().Model(&models.Event{}).Where("site_id = ?", query.SiteID)

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
		startTime, err := utils.ParseDate(query.StartTime)
		if err == nil {
			db = db.Where("created_at >= ?", startTime)
		}
	}
	if query.EndTime != "" {
		endTime, err := utils.ParseDate(query.EndTime)
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

	var events []models.Event
	offset := (page - 1) * pageSize
	if err := db.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&events).Error; err != nil {
		return nil, 0, fmt.Errorf("查询事件列表失败: %v", err)
	}

	return events, total, nil
}

// GetEventsSummary 获取网站下整体流量指标
func (s *EventService) GetEventsSummary(siteID uint64, startDate string, endDate string) (*models.SimpleSiteStats, error) {
	var stats models.SimpleSiteStats
	db := database.GetDB()

	// 解析日期
	start, err := utils.ParseDate(startDate)
	if err != nil {
		return nil, fmt.Errorf("开始日期格式错误: %v", err)
	}
	end, err := utils.ParseDate(endDate)
	if err != nil {
		return nil, fmt.Errorf("结束日期格式错误: %v", err)
	}
	end = end.Add(24 * time.Hour).Add(-time.Nanosecond)

	stats.SiteID = siteID
	stats.StartDate = startDate
	stats.EndDate = endDate

	// 同时查询PV（页面浏览量）、UV（独立访客数）和IPCount
	if err = db.Raw(`
		SELECT
			COUNT(*) as pv,
			COUNT(DISTINCT(session_id)) as uv,
			COUNT(DISTINCT(ip)) as ip_count
		FROM events
		WHERE site_id = ? AND event_type = 'page_view' AND created_at BETWEEN ? AND ?
	`, siteID, start, end).Row().Scan(&stats.PV, &stats.UV, &stats.IPCount); err != nil {
		return nil, fmt.Errorf("统计PV、UV和IP失败: %v", err.Error())
	}

	// 获取自定义事件数量
	if err = db.Model(&models.Event{}).
		Where("site_id = ? AND event_type = 'custom' AND created_at BETWEEN ? AND ?", siteID, start, end).
		Count(&stats.EventCount).Error; err != nil {
		return nil, fmt.Errorf("统计事件数量失败: %v", err.Error())
	}

	// 获取跳出率和平均访问时长
	var totalSessions int64
	var bounceSessions int64
	var totalDuration int64

	if err = db.Model(&models.Session{}).
		Select("COUNT(*) as session_count, COALESCE(SUM(duration), 0) as total_duration").
		Where("site_id = ? AND start_time BETWEEN ? AND ?", siteID, start, end).Row().
		Scan(&totalSessions, &totalDuration); err != nil {
		return nil, fmt.Errorf("统计会话数和访问时长失败: %v", err.Error())
	}

	// 获取跳出会话数（只访问了一个页面的会话）
	if err = db.Model(&models.Session{}).
		Where("site_id = ? AND start_time BETWEEN ? AND ? AND pages = 1", siteID, start, end).
		Count(&bounceSessions).Error; err != nil {
		return nil, fmt.Errorf("统计跳出会话失败: %v", err)
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

	// 本周IP和PV总量（基于传入的日期所在周）
	weekStart := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	for weekStart.Weekday() != time.Monday {
		weekStart = weekStart.AddDate(0, 0, -1)
	}
	weekEnd := weekStart.AddDate(0, 0, 7)
	if err = db.Model(&models.Event{}).
		Select("COUNT(*) as pv, COUNT(DISTINCT(ip)) as ips").
		Where("site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?", siteID, weekStart, weekEnd).Row().
		Scan(&stats.WeekPv, &stats.WeekIp); err != nil {
		return nil, fmt.Errorf("统计本周数据失败: %v", err.Error())
	}

	// 本月IP和PV总量（基于传入的日期所在周）
	monthStart := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
	monthEnd := monthStart.AddDate(0, 1, 0)
	if err = db.Model(&models.Event{}).
		Select("COUNT(*) as month_pv, COUNT(DISTINCT ip) as month_ip").
		Where("site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?", siteID, monthStart, monthEnd).
		Row().Scan(&stats.MonthPv, &stats.MonthIp); err != nil {
		return nil, fmt.Errorf("统计本月PV和IP失败: %v", err.Error())
	}

	// 小时流量分布
	if err := db.Raw(`
		SELECT EXTRACT(HOUR FROM created_at) as hour, COUNT(*) as count
		FROM events
		WHERE site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?
		GROUP BY EXTRACT(HOUR FROM created_at)
		ORDER BY hour
	`, siteID, start, end).Scan(&stats.HourlyStats).Error; err != nil {
		return nil, fmt.Errorf("统计小时流量分布失败: %v", err.Error())
	}

	return &stats, nil
}

// GetEventsRank 事件概览排行
func (s *EventService) GetEventsRank(siteID uint64, startDate, endDate, statType, eventType string, page, pageSize int) (*[]models.RankStats, int64, error) {
	var rankStats []models.RankStats
	db := database.GetDB()

	// 解析日期
	start, err := utils.ParseDate(startDate)
	if err != nil {
		return &rankStats, 0, fmt.Errorf("开始日期格式错误: %v", err)
	}
	end, err := utils.ParseDate(endDate)
	if err != nil {
		return &rankStats, 0, fmt.Errorf("结束日期格式错误: %v", err)
	}
	end = end.Add(24 * time.Hour).Add(-time.Nanosecond)

	// 获取排行数据
	sql := fmt.Sprintf(`
		SELECT %s AS key, COUNT(*) as count
		FROM events
		WHERE site_id = ? AND event_type = ? AND created_at >= ? AND created_at < ?
		GROUP BY %s
		ORDER BY count DESC
		LIMIT ? OFFSET ?
	`, statType, statType)
	db.Raw(sql, siteID, eventType, start, end, pageSize, (page-1)*pageSize).Scan(&rankStats)

	// 获取总量
	var total int64
	sqlTotal := fmt.Sprintf(`
		SELECT COUNT(distinct %s)
		FROM events
		WHERE site_id = ? AND event_type = ? AND created_at >= ? AND created_at < ?
	`, statType)
	db.Raw(sqlTotal, siteID, eventType, start, end).Scan(&total)

	return &rankStats, total, nil
}
