package services

import (
	"errors"
	"fmt"
	"pingoo/database"
	"pingoo/models"

	"gorm.io/gorm"
)

type SiteService struct{}

// NewSiteService 创建站点服务实例
func NewSiteService() *SiteService {
	return &SiteService{}
}

// CreateSite 创建站点
func (s *SiteService) CreateSite(siteCreate *models.SiteCreate, userID uint64) (*models.Site, error) {
	if siteCreate.Name == "" || siteCreate.Domain == "" {
		return nil, errors.New("站点名称和域名不能为空")
	}

	site := &models.Site{
		Name:   siteCreate.Name,
		Domain: siteCreate.Domain,
		UserID: userID,
	}

	db := database.GetDB()
	if err := db.Create(site).Error; err != nil {
		return nil, fmt.Errorf("创建站点失败: %v", err)
	}

	return site, nil
}

// GetSites 获取用户站点列表
func (s *SiteService) GetSites(userID uint64, page, pageSize int, name string) ([]models.Site, int64, error) {
	db := database.GetDB().Model(&models.Site{}).Where("user_id = ?", userID)

	// 搜索
	if name != "" {
		db = db.Where("name LIKE ? OR domain LIKE ?", "%"+name+"%", "%"+name+"%")
	}

	// 统计总数
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计站点数量失败: %v", err)
	}

	// 分页查询
	var sites []models.Site
	if err := db.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&sites).Error; err != nil {
		return nil, 0, fmt.Errorf("查询站点列表失败: %v", err)
	}

	return sites, total, nil
}

// GetSiteByID 根据ID获取站点详情
func (s *SiteService) GetSiteByID(id uint64) (*models.Site, error) {
	var site models.Site
	db := database.GetDB()
	if err := db.First(&site, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("站点不存在")
		}
		return nil, fmt.Errorf("查询站点失败: %v", err)
	}
	return &site, nil
}

// UpdateSite 更新站点信息
func (s *SiteService) UpdateSite(id uint64, userID uint64, siteUpdate *models.SiteUpdate) (*models.Site, error) {
	site, err := s.GetSiteByID(id)
	if err != nil {
		return nil, err
	}

	// 验证权限
	if site.UserID != userID {
		return nil, errors.New("无权限修改此站点")
	}

	// 更新字段
	if siteUpdate.Name != "" {
		site.Name = siteUpdate.Name
	}
	if siteUpdate.Domain != "" {
		site.Domain = siteUpdate.Domain
	}

	db := database.GetDB()
	if err := db.Save(site).Error; err != nil {
		return nil, fmt.Errorf("更新站点失败: %v", err)
	}

	return site, nil
}

// DeleteSite 删除站点
func (s *SiteService) DeleteSite(id uint64, userID uint64) error {
	site, err := s.GetSiteByID(id)
	if err != nil {
		return err
	}

	// 验证权限
	if site.UserID != userID {
		return errors.New("无权限删除此站点")
	}

	db := database.GetDB()
	// 先清空数据
	err = s.ClearSiteStats(id, userID)
	if err != nil {
		return fmt.Errorf("删除站点失败: %v", err)
	}

	if err := db.Unscoped().Delete(&models.Site{}, id).Error; err != nil {
		return fmt.Errorf("删除站点失败: %v", err)
	}

	return nil
}

// CheckUserAccess 检查用户是否有权限访问站点
func (s *SiteService) CheckUserAccess(siteID uint64, userID uint64) (bool, error) {
	var count int64
	db := database.GetDB().Model(&models.Site{})
	if err := db.Where("id = ? AND user_id = ?", siteID, userID).Count(&count).Error; err != nil {
		return false, fmt.Errorf("检查权限失败: %v", err)
	}
	if count <= 0 {
		return false, errors.New("权限检查失败")
	}
	return count > 0, nil
}

func (s *SiteService) ClearSiteStats(siteID uint64, userID uint64) error {
	site, err := s.GetSiteByID(siteID)
	if err != nil {
		return err
	}

	// 验证权限
	if site.UserID != userID {
		return errors.New("无权限删除此站点")
	}

	db := database.GetDB()
	// 使用事务删除所有统计数据
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 删除events
	if err := tx.Unscoped().Where("site_id = ?", siteID).Delete(&models.Event{}).Error; err != nil {
		tx.Rollback()
		return errors.New("删除events统计数据失败")
	}

	// 删除sessions
	if err := tx.Unscoped().Where("site_id = ?", siteID).Delete(&models.Session{}).Error; err != nil {
		tx.Rollback()
		return errors.New("删除session统计数据失败")
	}

	// 删除daily_stats
	if err := tx.Unscoped().Where("site_id = ?", siteID).Delete(&models.DailyStats{}).Error; err != nil {
		tx.Rollback()
		return errors.New("删除daily_stats统计数据失败")
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return errors.New("事务提交失败")
	}
	return nil
}
