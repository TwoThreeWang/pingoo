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
func (s *SiteService) GetSites(userID uint64, query *models.SiteQuery) ([]models.Site, int64, error) {
	db := database.GetDB().Model(&models.Site{}).Where("user_id = ?", userID)

	// 搜索
	if query.Name != "" {
		db = db.Where("name LIKE ? OR domain LIKE ?", "%"+query.Name+"%", "%"+query.Name+"%")
	}

	// 统计总数
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计站点数量失败: %v", err)
	}

	// 分页查询
	var sites []models.Site
	if err := db.Order("created_at DESC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&sites).Error; err != nil {
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
	if err := db.Delete(&models.Site{}, id).Error; err != nil {
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
	return count > 0, nil
}

// GetSiteStats 获取站点统计信息
func (s *SiteService) GetSiteStats(siteID uint64, userID uint64) (*models.SiteStats, error) {
	// 验证权限
	if hasAccess, err := s.CheckUserAccess(siteID, userID); err != nil || !hasAccess {
		return nil, errors.New("无权限访问此站点")
	}

	var stats models.SiteStats

	// 获取站点基本信息
	_, err := s.GetSiteByID(siteID)
	if err != nil {
		return nil, err
	}
	// 获取事件统计
	eventService := NewEventService()
	query := &models.EventQuery{
		SiteID: siteID,
	}
	_, err = eventService.GetEventStats(query)
	if err != nil {
		return nil, err
	}

	return &stats, nil
}

// GetSimpleSiteStats 获取站点统计信息概览
func (s *SiteService) GetSimpleSiteStats(siteID uint64, userID uint64, startDate string, endDate string) (*models.SimpleSiteStats, error) {
	// 验证权限
	if hasAccess, err := s.CheckUserAccess(siteID, userID); err != nil || !hasAccess {
		return nil, errors.New("无权限访问此站点")
	}

	// 使用事件服务获取详细统计信息
	eventService := NewEventService()
	stats, err := eventService.GetSimpleStats(siteID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("获取统计信息失败: %v", err)
	}

	return stats, nil
}

// GetTrackingCode 获取追踪代码
func (s *SiteService) GetTrackingCode(siteID uint64, userID uint64) (string, error) {
	// 验证权限
	if hasAccess, err := s.CheckUserAccess(siteID, userID); err != nil || !hasAccess {
		return "", errors.New("无权限访问此站点")
	}

	site, err := s.GetSiteByID(siteID)
	if err != nil {
		return "", err
	}

	trackingCode := fmt.Sprintf(`
<!-- Pingoo -->
<script>
(function() {
    var siteId = %d;
    var trackingId = '%s';

    function trackEvent(eventType, data) {
        var xhr = new XMLHttpRequest();
        xhr.open('POST', '/api/events/track', true);
        xhr.setRequestHeader('Content-Type', 'application/json');
        xhr.send(JSON.stringify({
            session_id: sessionStorage.getItem('pingoo_session') || 'anonymous',
            site_id: siteId,
            event_type: eventType,
            ...data
        }));
    }

    // 页面浏览
    trackEvent('page_view', {
        url: window.location.href,
        referrer: document.referrer,
        title: document.title
    });

    // 点击事件
    document.addEventListener('click', function(e) {
        if (e.target.matches('[data-track]')) {
            trackEvent('click', {
                element: e.target.getAttribute('data-track'),
                url: window.location.href
            });
        }
    });
})();
</script>
<!-- End Pingoo -->`, site.ID)

	return trackingCode, nil
}
