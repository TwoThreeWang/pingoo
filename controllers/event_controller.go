package controllers

import (
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
	// 从UserAgent中提取Device、Browser、OS、IsBot
	eventCreate.Device, eventCreate.Browser, eventCreate.OS, eventCreate.IsBot = utils.ParseUserAgent(eventCreate.UserAgent)

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

// TrackCustomEvent 自定义事件追踪接口
func (c *EventController) TrackCustomEvent(ctx *gin.Context) {
	var req struct {
		SessionID  string `json:"session_id"`
		UserID     string `json:"user_id"`
		URL        string `json:"url"`
		Referrer   string `json:"referrer"`
		EventType  string `json:"event_type"`
		EventValue string `json:"event_value"`
		SiteIDStr  string `json:"site_id"`
		Screen     string `json:"screen"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		utils.ValidationError(ctx, err.Error())
		return
	}
	SiteID, err := strconv.ParseUint(req.SiteIDStr, 10, 64)
	if err != nil {
		utils.ValidationError(ctx, "无效的SiteID格式")
		return
	}

	// 验证站点存在
	siteService := services.NewSiteService()
	if _, err := siteService.GetSiteByID(SiteID); err != nil {
		utils.Fail(ctx, "站点不存在")
		return
	}
	UserAgent := ctx.GetHeader("User-Agent")
	// 从UserAgent中提取Device、Browser、OS、IsBot
	device, browser, os, isBot := utils.ParseUserAgent(UserAgent)

	eventCreate := &models.EventCreate{
		SiteID:     SiteID,
		SessionID:  req.SessionID,
		UserID:     req.UserID,
		IP:         ctx.ClientIP(),
		URL:        req.URL,
		Referrer:   req.Referrer,
		Screen:     req.Screen,
		Device:     device,
		Browser:    browser,
		OS:         os,
		IsBot:      isBot,
		UserAgent:  UserAgent,
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
