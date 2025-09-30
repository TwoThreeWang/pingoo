package database

import (
	"fmt"
	"pingoo/models"

	"gorm.io/gorm"
)

// Migrate 执行数据库迁移
func Migrate() error {
	db := GetDB()

	// 自动迁移表结构
	if err := db.AutoMigrate(
		&models.User{},
		&models.Site{},
		&models.Event{},
		&models.Session{},
		&models.DailyStats{},
	); err != nil {
		return fmt.Errorf("数据库迁移失败: %v", err)
	}

	// 添加索引（仅在需要时）
	if err := addIndexes(db); err != nil {
		return fmt.Errorf("添加索引失败: %v", err)
	}

	return nil
}

// addIndexes 添加数据库索引
func addIndexes(db *gorm.DB) error {
	// 检查并创建用户表索引
	if !db.Migrator().HasIndex("users", "idx_users_email") {
		if err := db.Exec("CREATE INDEX idx_users_email ON users(email)").Error; err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex("users", "idx_users_username") {
		if err := db.Exec("CREATE INDEX idx_users_username ON users(username)").Error; err != nil {
			return err
		}
	}

	// 检查并创建站点表索引
	if !db.Migrator().HasIndex("sites", "idx_sites_id_user_deleted") {
		if err := db.Exec("CREATE INDEX idx_sites_id_user_deleted ON sites (id, user_id) WHERE deleted_at IS NULL").Error; err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex("sites", "idx_sites_user_id") {
		if err := db.Exec("CREATE INDEX idx_sites_user_id ON sites(user_id)").Error; err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex("sites", "idx_sites_domain") {
		if err := db.Exec("CREATE INDEX idx_sites_domain ON sites(domain)").Error; err != nil {
			return err
		}
	}

	// 检查并创建事件表索引
	if !db.Migrator().HasIndex("events", "idx_events_site_type_created") {
		if err := db.Exec("CREATE INDEX idx_events_site_type_created ON events (site_id, event_type, created_at) INCLUDE (session_id, ip)").Error; err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex("events", "idx_events_site_id") {
		if err := db.Exec("CREATE INDEX idx_events_site_id ON events(site_id)").Error; err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex("events", "idx_events_session_id") {
		if err := db.Exec("CREATE INDEX idx_events_session_id ON events(session_id)").Error; err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex("events", "idx_events_event_type") {
		if err := db.Exec("CREATE INDEX idx_events_event_type ON events(event_type)").Error; err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex("events", "idx_events_site_created_at") {
		if err := db.Exec("CREATE INDEX idx_events_site_created_at ON events(site_id, created_at)").Error; err != nil {
			return err
		}
	}
	// 检查并创建sessions表索引
	if !db.Migrator().HasIndex("sessions", "idx_sessions_site_start") {
		if err := db.Exec("CREATE INDEX idx_sessions_site_start ON sessions (start_time) WHERE site_id = 2 AND deleted_at IS NULL").Error; err != nil {
			return err
		}
	}
	// 检查并创建daily_stats表索引
	if !db.Migrator().HasIndex("daily_stats", "uniq_daily_stats") {
		if err := db.Exec("CREATE UNIQUE INDEX uniq_daily_stats ON daily_stats (site_id, date, category, item)").Error; err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex("daily_stats", "idx_daily_stats_site_category_date_pv") {
		if err := db.Exec("CREATE INDEX idx_daily_stats_site_category_date_pv ON daily_stats (site_id, category, date)").Error; err != nil {
			return err
		}
	}

	return nil
}
