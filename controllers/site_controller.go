package controllers

import (
	"strconv"

	"pingoo/middleware"
	"pingoo/models"
	"pingoo/services"
	"pingoo/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SiteController 站点控制器
type SiteController struct {
	siteService *services.SiteService
}

// NewSiteController 创建站点控制器
func NewSiteController(db *gorm.DB) *SiteController {
	return &SiteController{siteService: services.NewSiteService()}
}

// Create 创建站点
func (sc *SiteController) Create(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	var input models.SiteCreate
	if err := c.ShouldBindJSON(&input); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}

	site, err := sc.siteService.CreateSite(&input, userID)
	if err != nil {
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
	search := c.Query("search")
	sites, total, err := sc.siteService.GetSites(userID, page, limit, search)
	if err != nil {
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

	site, err := sc.siteService.GetSiteByID(siteID)
	if err != nil {
		utils.ServerError(c, "获取站点详情失败")
		return
	}
	if site.UserID != userID {
		utils.ServerError(c, "站点不存在")
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

	site, err := sc.siteService.UpdateSite(siteID, userID, &input)
	if err != nil {
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

	err = sc.siteService.DeleteSite(siteID, userID)
	if err != nil {
		utils.ServerError(c, "删除站点失败")
		return
	}

	utils.Success(c, "站点删除成功")
}

// ClearStats 删除网站所有统计数据
func (sc *SiteController) ClearStats(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		utils.ValidationError(c, "无效的站点ID")
		return
	}

	err = sc.siteService.ClearSiteStats(siteID, userID)
	if err != nil {
		utils.ServerError(c, err.Error())
		return
	}

	utils.Success(c, gin.H{"message": "统计数据已清空"})
}
