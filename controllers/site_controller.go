package controllers

import (
	"strconv"
	"time"

	"pingoo/middleware"
	"pingoo/models"
	"pingoo/services"
	"pingoo/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SiteController 站点控制器
type SiteController struct {
	db *gorm.DB
}

// NewSiteController 创建站点控制器
func NewSiteController(db *gorm.DB) *SiteController {
	return &SiteController{db: db}
}

// Create 创建站点
func (sc *SiteController) Create(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	var input models.SiteCreate
	if err := c.ShouldBindJSON(&input); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}

	// 检查域名是否已存在
	var existingSite models.Site
	if err := sc.db.Where("domain = ?", input.Domain).First(&existingSite).Error; err == nil {
		utils.Fail(c, "域名已存在")
		return
	}

	// 创建站点
	site := models.Site{
		Name:   input.Name,
		Domain: input.Domain,
		UserID: userID,
	}

	if err := sc.db.Create(&site).Error; err != nil {
		utils.ServerError(c, "创建站点失败")
		return
	}

	// 返回站点信息
	siteResponse := models.SiteResponse{
		ID:        uint64(site.ID),
		Name:      site.Name,
		Domain:    site.Domain,
		UserID:    site.UserID,
		CreatedAt: site.CreatedAt,
		UpdatedAt: site.UpdatedAt,
	}

	utils.Success(c, siteResponse)
}

// List 获取用户站点列表
func (sc *SiteController) List(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	// 获取分页参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 10
	}

	offset := (page - 1) * limit

	// 查询站点列表
	var sites []models.Site
	var total int64

	query := sc.db.Where("user_id = ?", userID)

	// 搜索条件
	if search := c.Query("search"); search != "" {
		query = query.Where("name LIKE ? OR domain LIKE ?", "%"+search+"%", "%"+search+"%")
	}

	// 获取总数
	query.Model(&models.Site{}).Count(&total)

	// 获取分页数据
	if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&sites).Error; err != nil {
		utils.ServerError(c, "获取站点列表失败")
		return
	}

	// 转换为响应格式
	var siteResponses []models.SiteResponse
	for _, site := range sites {
		siteResponses = append(siteResponses, models.SiteResponse{
			ID:        uint64(site.ID),
			Name:      site.Name,
			Domain:    site.Domain,
			UserID:    site.UserID,
			CreatedAt: site.CreatedAt,
			UpdatedAt: site.UpdatedAt,
		})
	}

	utils.SuccessWithPage(c, siteResponses, total, page, limit)
}

// Get 获取站点详情
func (sc *SiteController) Get(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}

	var site models.Site
	if err := sc.db.Where("id = ? AND user_id = ?", siteID, userID).First(&site).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			utils.NotFound(c, "站点不存在")
		} else {
			utils.ServerError(c, "获取站点详情失败")
		}
		return
	}

	// 返回站点信息
	siteResponse := models.SiteResponse{
		ID:        uint64(site.ID),
		Name:      site.Name,
		Domain:    site.Domain,
		UserID:    site.UserID,
		CreatedAt: site.CreatedAt,
		UpdatedAt: site.UpdatedAt,
	}

	utils.Success(c, siteResponse)
}

// Update 更新站点信息
func (sc *SiteController) Update(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}

	var input models.SiteUpdate
	if err := c.ShouldBindJSON(&input); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}

	var site models.Site
	if err := sc.db.Where("id = ? AND user_id = ?", siteID, userID).First(&site).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			utils.NotFound(c, "站点不存在")
		} else {
			utils.ServerError(c, "获取站点详情失败")
		}
		return
	}

	// 检查域名是否被其他站点使用
	if input.Domain != site.Domain {
		var existingSite models.Site
		if err := sc.db.Where("domain = ? AND id != ?", site.Domain, siteID).First(&existingSite).Error; err == nil {
			utils.Fail(c, "域名已被其他站点使用")
			return
		}
		site.Domain = input.Domain
	}

	// 更新站点信息
	if input.Name != "" {
		site.Name = input.Name
	}

	if err := sc.db.Save(&site).Error; err != nil {
		utils.ServerError(c, "更新站点失败")
		return
	}

	siteResponse := models.SiteResponse{
		ID:        uint64(site.ID),
		Name:      site.Name,
		Domain:    site.Domain,
		UserID:    site.UserID,
		CreatedAt: site.CreatedAt,
		UpdatedAt: site.UpdatedAt,
	}

	utils.Success(c, siteResponse)
}

// Delete 删除站点
func (sc *SiteController) Delete(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}

	var site models.Site
	if err := sc.db.Where("id = ? AND user_id = ?", siteID, userID).First(&site).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			utils.FailWithCode(c, 200, "站点不存在")
		} else {
			utils.ServerError(c, "获取站点详情失败")
		}
		return
	}

	// 物理删除站点
	if err := sc.db.Unscoped().Delete(&site).Error; err != nil {
		utils.ServerError(c, "删除站点失败")
		return
	}

	utils.Success(c, "站点删除成功")
}

// GetStats 获取站点统计信息
func (sc *SiteController) GetStats(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}

	var site models.Site
	if err := sc.db.Where("id = ? AND user_id = ?", siteID, userID).First(&site).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			utils.NotFound(c, "站点不存在")
		} else {
			utils.ServerError(c, "获取站点详情失败")
		}
		return
	}

	// 获取查询日期参数，默认为当天
	dateStr := c.Query("date")
	var targetDate time.Time
	if dateStr == "" {
		targetDate = time.Now()
	} else {
		targetDate, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			targetDate = time.Now()
		}
	}

	// 设置日期范围（当天0点到24点）
	startDate := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, targetDate.Location())
	endDate := startDate.Add(24 * time.Hour)

	// 基础统计信息
	var weekIp int64
	var weekPv int64
	var monthIp int64
	var monthPv int64

	// 本周IP和PV总量（基于传入的日期所在周）
	weekStart := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, targetDate.Location())
	for weekStart.Weekday() != time.Monday {
		weekStart = weekStart.AddDate(0, 0, -1)
	}
	weekEnd := weekStart.AddDate(0, 0, 7)
	sc.db.Model(&models.Event{}).Where("site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?", siteID, weekStart, weekEnd).Count(&weekPv)
	sc.db.Model(&models.Event{}).Where("site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?", siteID, weekStart, weekEnd).Distinct("ip").Count(&weekIp)

	// 本月IP和PV总量（基于传入的日期所在周）
	monthStart := time.Date(targetDate.Year(), targetDate.Month(), 1, 0, 0, 0, 0, targetDate.Location())
	monthEnd := monthStart.AddDate(0, 1, 0)
	sc.db.Model(&models.Event{}).Where("site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?", siteID, monthStart, monthEnd).Count(&monthPv)
	sc.db.Model(&models.Event{}).Where("site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?", siteID, monthStart, monthEnd).Distinct("ip").Count(&monthIp)

	// 小时流量分布
	type HourlyStats struct {
		Hour  int   `json:"hour"`
		Count int64 `json:"count"`
	}
	var hourlyStats []HourlyStats
	sc.db.Raw(`
		SELECT EXTRACT(HOUR FROM created_at) as hour, COUNT(*) as count
		FROM events
		WHERE site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?
		GROUP BY EXTRACT(HOUR FROM created_at)
		ORDER BY hour
	`, siteID, startDate, endDate).Scan(&hourlyStats)

	// 访问网页Top10
	type PageStats struct {
		URL   string `json:"url"`
		Count int64  `json:"count"`
	}
	var topPages []PageStats
	sc.db.Raw(`
		SELECT url, COUNT(*) as count
		FROM events
		WHERE site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?
		GROUP BY url
		ORDER BY count DESC
		LIMIT 10
	`, siteID, startDate, endDate).Scan(&topPages)

	// 来源Top10
	type ReferrerStats struct {
		Referrer string `json:"referrer"`
		Count    int64  `json:"count"`
	}
	var topReferrers []ReferrerStats
	sc.db.Raw(`
		SELECT referrer, COUNT(*) as count
		FROM events
		WHERE site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ? AND referrer != ''
		GROUP BY referrer
		ORDER BY count DESC
		LIMIT 10
	`, siteID, startDate, endDate).Scan(&topReferrers)

	// 操作系统Top10
	type OSStats struct {
		OS    string `json:"os"`
		Count int64  `json:"count"`
	}
	var topOS []OSStats
	sc.db.Raw(`
		SELECT os, COUNT(*) as count
		FROM events
		WHERE site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ? AND os != ''
		GROUP BY os
		ORDER BY count DESC
		LIMIT 10
	`, siteID, startDate, endDate).Scan(&topOS)

	// 设备类型分布
	type DeviceStats struct {
		Device string `json:"device"`
		Count  int64  `json:"count"`
	}
	var deviceTypes []DeviceStats
	sc.db.Raw(`
		SELECT device, COUNT(*) as count
		FROM events
		WHERE site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ? AND device != ''
		GROUP BY device
		ORDER BY count DESC
	`, siteID, startDate, endDate).Scan(&deviceTypes)

	// 访问地区TOP10（基于IP解析）
	type LocationStats struct {
		Location string `json:"location"`
		Count    int64  `json:"count"`
	}
	var topLocations []LocationStats
	sc.db.Raw(`
		SELECT Country as location, COUNT(*) as count
		FROM events
		WHERE site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?
		GROUP BY Country
		ORDER BY count DESC
		LIMIT 10
	`, siteID, startDate, endDate).Scan(&topLocations)

	// 运营商TOP10
	type ISPStats struct {
		ISP   string `json:"isp"`
		Count int64  `json:"count"`
	}
	var topISP []ISPStats
	sc.db.Raw(`
		SELECT ISP as isp, COUNT(*) as count
		FROM events
		WHERE site_id = ? AND event_type = 'page_view' AND created_at >= ? AND created_at < ?
		GROUP BY ISP
		ORDER BY count DESC
		LIMIT 10
	`, siteID, startDate, endDate).Scan(&topISP)

	stats := struct {
		SiteID       uint64          `json:"site_id"`
		WeekIp       int64           `json:"week_ip"`
		WeekPv       int64           `json:"week_pv"`
		MonthIp      int64           `json:"month_ip"`
		MonthPv      int64           `json:"month_pv"`
		TargetDate   string          `json:"target_date"`
		HourlyStats  []HourlyStats   `json:"hourly_stats"`
		TopPages     []PageStats     `json:"top_pages"`
		TopReferrers []ReferrerStats `json:"top_referrers"`
		TopOS        []OSStats       `json:"top_os"`
		DeviceTypes  []DeviceStats   `json:"device_types"`
		TopLocations []LocationStats `json:"top_locations"`
		TopISP       []ISPStats      `json:"top_isp"`
	}{
		SiteID:       siteID,
		WeekIp:       weekIp,
		WeekPv:       weekPv,
		MonthIp:      monthIp,
		MonthPv:      monthPv,
		TargetDate:   targetDate.Format("2006-01-02"),
		HourlyStats:  hourlyStats,
		TopPages:     topPages,
		TopReferrers: topReferrers,
		TopOS:        topOS,
		DeviceTypes:  deviceTypes,
		TopLocations: topLocations,
		TopISP:       topISP,
	}

	utils.Success(c, stats)
}

// GetSimpleStats 获取网站统计信息
func (sc *SiteController) GetSimpleStats(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}

	// 验证站点存在且属于当前用户
	var site models.Site
	if err := sc.db.Where("id = ? AND user_id = ?", siteID, userID).First(&site).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			utils.NotFound(c, "站点不存在")
		} else {
			utils.ServerError(c, "获取站点详情失败")
		}
		return
	}

	// 获取时间范围参数，默认获取当天的数据
	startDate := c.DefaultQuery("date", time.Now().Format("2006-01-02"))
	endDate := startDate

	// 获取站点统计信息
	siteService := services.NewSiteService()
	stats, err := siteService.GetSimpleSiteStats(siteID, userID, startDate, endDate)
	if err != nil {
		utils.ServerError(c, err.Error())
		return
	}

	utils.Success(c, stats)
}

// ClearStats 删除网站所有统计数据
func (sc *SiteController) ClearStats(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}

	// 验证站点存在且属于当前用户
	var site models.Site
	if err := sc.db.Where("id = ? AND user_id = ?", siteID, userID).First(&site).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			utils.NotFound(c, "站点不存在")
		} else {
			utils.ServerError(c, "获取站点详情失败")
		}
		return
	}

	// 使用事务删除所有统计数据
	tx := sc.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 删除events
	if err := tx.Unscoped().Where("site_id = ?", siteID).Delete(&models.Event{}).Error; err != nil {
		tx.Rollback()
		utils.ServerError(c, "删除events统计数据失败")
		return
	}

	// 删除sessions
	if err := tx.Unscoped().Where("site_id = ?", siteID).Delete(&models.Session{}).Error; err != nil {
		tx.Rollback()
		utils.ServerError(c, "删除session统计数据失败")
		return
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		utils.ServerError(c, "事务提交失败")
		return
	}

	utils.Success(c, gin.H{"message": "统计数据已清空"})
}
