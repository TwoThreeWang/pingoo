package controllers

import (
	"log"
	"strconv"
	"time"

	"pingoo/middleware"
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
func (ec *EventController) CreateEvent(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	var eventCreate models.EventCreate
	if err := c.ShouldBindJSON(&eventCreate); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}
	// 验证用户是否有权限访问站点
	ss := services.NewSiteService()
	if hasAccess, err := ss.CheckUserAccess(eventCreate.SiteID, userID); err != nil || !hasAccess {
		utils.ValidationError(c, err.Error())
		return
	}

	// 自动获取客户端IP
	if eventCreate.IP == "" {
		eventCreate.IP = c.ClientIP()
	}
	// 从ip提取国家等信息
	ipInfo, err := utils.QueryIP(eventCreate.IP)
	if err != nil {
		log.Println(err)
	}
	eventCreate.Country = ipInfo.Country
	eventCreate.Subdivision = ipInfo.Region
	eventCreate.City = ipInfo.City
	eventCreate.Isp = ipInfo.ISP

	// 自动获取User-Agent
	if eventCreate.UserAgent == "" {
		eventCreate.UserAgent = c.GetHeader("User-Agent")
	}
	// 从UserAgent中提取Device、Browser、OS、IsBot
	eventCreate.Device, eventCreate.Browser, eventCreate.OS, eventCreate.IsBot = utils.ParseUserAgent(eventCreate.UserAgent)

	event, err := ec.eventService.CreateEvent(&eventCreate)
	if err != nil {
		utils.Fail(c, err.Error())
		return
	}

	utils.Success(c, event)
}

// GetEvents 根据站点ID获取事件列表
func (ec *EventController) GetEvents(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("site_id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}
	// 验证用户是否有权限访问站点
	ss := services.NewSiteService()
	if hasAccess, err := ss.CheckUserAccess(siteID, userID); err != nil || !hasAccess {
		utils.ValidationError(c, err.Error())
		return
	}

	var query models.EventQuery
	if err = c.ShouldBindQuery(&query); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}
	query.SiteID = siteID
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.PageSize <= 0 {
		query.PageSize = 10
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}

	events, total, err := ec.eventService.GetEventsDetail(&query)
	if err != nil {
		utils.ServerError(c, err.Error())
		return
	}

	utils.SuccessWithPage(c, events, total, query.Page, query.PageSize)
}

// GetEventsRank 获取事件统计信息
func (ec *EventController) GetEventsRank(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("site_id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}
	// 验证用户是否有权限访问站点
	ss := services.NewSiteService()
	if hasAccess, err := ss.CheckUserAccess(siteID, userID); err != nil || !hasAccess {
		utils.ValidationError(c, err.Error())
		return
	}
	// 获取查询日期参数，默认为当天
	dateStr := c.DefaultQuery("date", time.Now().Format("2006-01-02"))
	page := c.DefaultQuery("page", "1")
	pageInt, err := strconv.Atoi(page)
	if err != nil {
		pageInt = 1
	}
	pageSize := 10
	statType := c.DefaultQuery("stat_type", "url")
	eventType := c.DefaultQuery("event_type", "page_view")

	stats, total, err := ec.eventService.GetEventsRank(siteID, dateStr, dateStr, statType, eventType, pageInt, pageSize)
	if err != nil {
		utils.ServerError(c, err.Error())
		return
	}

	utils.SuccessWithPage(c, stats, total, pageInt, pageSize)
}

// GetEventsSummary 获取网站下整体流量指标
func (ec *EventController) GetEventsSummary(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("site_id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}
	// 验证用户是否有权限访问站点
	ss := services.NewSiteService()
	if hasAccess, err := ss.CheckUserAccess(siteID, userID); err != nil || !hasAccess {
		utils.ValidationError(c, err.Error())
		return
	}
	// 获取查询日期参数，默认为当天
	dateStr := c.DefaultQuery("date", time.Now().Format("2006-01-02"))

	stats, err := ec.eventService.GetEventsSummary(siteID, dateStr, dateStr)
	if err != nil {
		utils.ServerError(c, err.Error())
		return
	}

	utils.Success(c, stats)
}

// TrackCustomEvent 自定义事件追踪接口
func (ec *EventController) TrackCustomEvent(c *gin.Context) {
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

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}
	SiteID, err := strconv.ParseUint(req.SiteIDStr, 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的SiteID格式")
		return
	}

	// 验证站点存在
	siteService := services.NewSiteService()
	if _, err := siteService.GetSiteByID(SiteID); err != nil {
		utils.Fail(c, "站点不存在")
		return
	}
	UserAgent := c.GetHeader("User-Agent")
	// 从UserAgent中提取Device、Browser、OS、IsBot
	device, browser, os, isBot := utils.ParseUserAgent(UserAgent)
	ip := "36.112.118.66"
	// ip := "64.69.36.11"
	// ip := c.ClientIP()
	// 从ip提取国家等信息
	ipInfo, err := utils.QueryIP(ip)
	if err != nil {
		log.Println(err)
	}
	eventCreate := &models.EventCreate{
		SiteID:      SiteID,
		SessionID:   req.SessionID,
		UserID:      req.UserID,
		IP:          ip,
		URL:         req.URL,
		Referrer:    req.Referrer,
		Screen:      req.Screen,
		Device:      device,
		Browser:     browser,
		OS:          os,
		IsBot:       isBot,
		Country:     ipInfo.Country,
		Subdivision: ipInfo.Region,
		City:        ipInfo.City,
		Isp:         ipInfo.ISP,
		UserAgent:   UserAgent,
		EventType:   req.EventType,
		EventValue:  req.EventValue,
	}

	event, err := ec.eventService.CreateEvent(eventCreate)
	if err != nil {
		utils.Fail(c, err.Error())
		return
	}

	utils.Success(c, event)
}
