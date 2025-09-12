package controllers

import (
	"log"
	"strconv"

	"pingoo/models"
	"pingoo/services"
	"pingoo/utils"

	"github.com/gin-gonic/gin"
)

type EventController struct {
	eventService *services.EventService
}

// NewEventController 创建事件控制器实例
func NewEventController() *EventController {
	return &EventController{
		eventService: services.NewEventService(),
	}
}

// CreateEvent 创建事件
func (c *EventController) CreateEvent(ctx *gin.Context) {
	var eventCreate models.EventCreate
	if err := ctx.ShouldBindJSON(&eventCreate); err != nil {
		utils.ValidationError(ctx, err.Error())
		return
	}

	// 自动获取客户端IP
	if eventCreate.IP == "" {
		eventCreate.IP = ctx.ClientIP()
	}

	// 自动获取User-Agent
	if eventCreate.UserAgent == "" {
		eventCreate.UserAgent = ctx.GetHeader("User-Agent")
	}

	event, err := c.eventService.CreateEvent(&eventCreate)
	if err != nil {
		utils.Fail(ctx, err.Error())
		return
	}

	utils.Success(ctx, event)
}

// GetEvents 获取事件列表
func (c *EventController) GetEvents(ctx *gin.Context) {
	var query models.EventQuery
	if err := ctx.ShouldBindQuery(&query); err != nil {
		utils.ValidationError(ctx, err.Error())
		return
	}

	events, total, err := c.eventService.GetEvents(&query)
	if err != nil {
		utils.ServerError(ctx, err.Error())
		return
	}

	utils.SuccessWithPage(ctx, events, total, query.Page, query.PageSize)
}

// GetEventStats 获取事件统计信息
func (c *EventController) GetEventStats(ctx *gin.Context) {
	var query models.EventQuery
	if err := ctx.ShouldBindQuery(&query); err != nil {
		utils.ValidationError(ctx, err.Error())
		return
	}

	stats, err := c.eventService.GetEventStats(&query)
	if err != nil {
		utils.ServerError(ctx, err.Error())
		return
	}

	utils.Success(ctx, stats)
}

// GetEventByID 根据ID获取事件详情
func (c *EventController) GetEventByID(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		utils.ValidationError(ctx, "无效的ID格式")
		return
	}

	event, err := c.eventService.GetEventByID(id)
	if err != nil {
		if err.Error() == "事件不存在" {
			utils.NotFound(ctx, err.Error())
		} else {
			utils.ServerError(ctx, err.Error())
		}
		return
	}

	utils.Success(ctx, event)
}

// TrackPageView 页面浏览追踪接口（简化版）
func (c *EventController) TrackPageView(ctx *gin.Context) {
	var req struct {
		SessionID string `json:"session_id" binding:"required"`
		UserID    string `json:"user_id"`
		URL       string `json:"url" binding:"required"`
		Referrer  string `json:"referrer"`
		Title     string `json:"title"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		utils.ValidationError(ctx, err.Error())
		return
	}

	eventCreate := &models.EventCreate{
		SessionID:  req.SessionID,
		UserID:     req.UserID,
		IP:         ctx.ClientIP(),
		URL:        req.URL,
		Referrer:   req.Referrer,
		UserAgent:  ctx.GetHeader("User-Agent"),
		EventType:  "page_view",
		EventValue: req.Title,
	}

	event, err := c.eventService.CreateEvent(eventCreate)
	if err != nil {
		utils.Fail(ctx, err.Error())
		return
	}
	log.Printf("创建事件成功: %v", event)

	// 返回1x1透明GIF像素
	ctx.Data(200, "image/gif", []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x1, 0x0, 0x1, 0x0, 0x80, 0x0, 0x0, 0xff, 0xff, 0xff, 0x0, 0x0, 0x0, 0x2c, 0x0, 0x0, 0x0, 0x0, 0x1, 0x0, 0x1, 0x0, 0x0, 0x2, 0x2, 0x44, 0x1, 0x0, 0x3b})
}

// TrackCustomEvent 自定义事件追踪接口
func (c *EventController) TrackCustomEvent(ctx *gin.Context) {
	var req struct {
		SessionID  string `json:"session_id" binding:"required"`
		UserID     string `json:"user_id"`
		URL        string `json:"url" binding:"required"`
		EventType  string `json:"event_type" binding:"required"`
		EventValue string `json:"event_value"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		utils.ValidationError(ctx, err.Error())
		return
	}

	eventCreate := &models.EventCreate{
		SessionID:  req.SessionID,
		UserID:     req.UserID,
		IP:         ctx.ClientIP(),
		URL:        req.URL,
		UserAgent:  ctx.GetHeader("User-Agent"),
		EventType:  req.EventType,
		EventValue: req.EventValue,
	}

	event, err := c.eventService.CreateEvent(eventCreate)
	if err != nil {
		utils.Fail(ctx, err.Error())
		return
	}

	utils.Success(ctx, event)
}
