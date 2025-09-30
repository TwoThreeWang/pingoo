package services

import (
	"pingoo/models"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IncrementDailyPV 累加某个站点某类别某项在指定日期的 PV
func IncrementDailyPV(db *gorm.DB, siteID uint64, category, item string, date time.Time) error {
	stat := models.DailyStats{
		SiteID:   siteID,
		Category: category,
		Item:     item,
		PV:       1, // 每次事件加1
		Date:     date,
	}

	// PostgreSQL 或 MySQL 都可使用 OnConflict
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "site_id"}, {Name: "date"}, {Name: "category"}, {Name: "item"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"pv":         gorm.Expr("daily_stats.pv + 1"), // 累加 PV
			"updated_at": time.Now(),                      // 更新更新时间
		}),
	}).Create(&stat).Error
}

// UpsertDailyStatsBatch 批量更新 DailyStats，支持 PV 累加
func UpsertDailyStatsBatch(tx *gorm.DB, siteID uint64, updates []struct {
	Category string
	Item     string
	PVDelta  int64
}, date time.Time) error {

	var stats []models.DailyStats
	for _, u := range updates {
		stats = append(stats, models.DailyStats{
			SiteID:   siteID,
			Category: u.Category,
			Item:     u.Item,
			PV:       u.PVDelta,
			Date:     date,
		})
	}

	// 批量插入 + OnConflict 累加 PV
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "site_id"}, {Name: "date"}, {Name: "category"}, {Name: "item"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"pv":         gorm.Expr("daily_stats.pv + EXCLUDED.pv"), // EXCLUDED.pv 是新插入的值
			"updated_at": time.Now(),
		}),
	}).Create(&stats).Error
}
