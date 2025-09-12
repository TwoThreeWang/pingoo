package services

import (
	"errors"
	"fmt"
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
		SessionID:  eventCreate.SessionID,
		UserID:     eventCreate.UserID,
		IP:         eventCreate.IP,
		URL:        eventCreate.URL,
		Referrer:   eventCreate.Referrer,
		UserAgent:  eventCreate.UserAgent,
		Device:     eventCreate.Device,
		Browser:    eventCreate.Browser,
		OS:         eventCreate.OS,
		Screen:     eventCreate.Screen,
		EventType:  eventCreate.EventType,
		EventValue: eventCreate.EventValue,
	}

	db := database.GetDB()
	if err := db.Create(event).Error; err != nil {
		return nil, fmt.Errorf("创建事件失败: %v", err)
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
	db := database.GetDB().Model(&models.Event{})

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
