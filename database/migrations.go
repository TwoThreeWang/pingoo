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
	); err != nil {
		return fmt.Errorf("数据库迁移失败: %v", err)
	}

	// 添加索引
	if err := addIndexes(db); err != nil {
		return fmt.Errorf("添加索引失败: %v", err)
	}

	return nil
}

// addIndexes 添加数据库索引
func addIndexes(db *gorm.DB) error {
	// 用户表索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)").Error; err != nil {
		return err
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)").Error; err != nil {
		return err
	}

	// 站点表索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_sites_user_id ON sites(user_id)").Error; err != nil {
		return err
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_sites_domain ON sites(domain)").Error; err != nil {
		return err
	}

	// 事件表索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_events_site_id ON events(site_id)").Error; err != nil {
		return err
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_events_session_id ON events(session_id)").Error; err != nil {
		return err
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_events_event_type ON events(event_type)").Error; err != nil {
		return err
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_events_created_at ON events(created_at)").Error; err != nil {
		return err
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_events_site_created_at ON events(site_id, created_at)").Error; err != nil {
		return err
	}

	return nil
}

// DropTables 删除所有表（用于开发环境重置）
func DropTables() error {
	db := GetDB()

	// 先删除外键约束
	if err := db.Migrator().DropTable(
		"events",
		"sites",
		"users",
	); err != nil {
		return fmt.Errorf("删除表失败: %v", err)
	}

	return nil
}

// ResetDatabase 重置数据库（删除并重新创建表）
func ResetDatabase() error {
	if err := DropTables(); err != nil {
		return err
	}

	if err := Migrate(); err != nil {
		return err
	}

	return nil
}

// SeedData 添加测试数据
func SeedData() error {
	db := GetDB()

	// 检查是否已有数据
	var count int64
	if err := db.Model(&models.User{}).Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		return nil // 已有数据，跳过
	}

	// 创建测试用户
	user := &models.User{
		Username: "admin",
		Email:    "admin@admin.com",
		Password: "$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi", // password
		Role:     "admin",
	}

	if err := db.Create(user).Error; err != nil {
		return fmt.Errorf("创建测试用户失败: %v", err)
	}

	// 创建测试站点
	site := &models.Site{
		Name:   "Pingoo",
		Domain: "example.com",
		UserID: uint64(user.ID),
	}

	if err := db.Create(site).Error; err != nil {
		return fmt.Errorf("创建测试站点失败: %v", err)
	}

	return nil
}
