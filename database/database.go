package database

import (
	"fmt"
	"pingoo/config"
	"pingoo/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Initialize(config config.DatabaseConfig) (*gorm.DB, error) {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=%s",
		config.Host, config.User, config.Password, config.DBName, config.Port, config.SSLMode, config.TimeZone)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("无法连接数据库: %w", err)
	}

	// 自动迁移数据库表结构
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	DB = db
	return db, nil
}

func migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.Event{},
	)
}

func GetDB() *gorm.DB {
	return DB
}
