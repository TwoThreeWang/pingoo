package database

import (
	"fmt"
	"pingoo/config"
	"sync"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	DB   *gorm.DB
	once sync.Once
)

func Initialize(config config.DatabaseConfig) (*gorm.DB, error) {
	var err error
	once.Do(func() {
		dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=%s",
			config.Host, config.User, config.Password, config.DBName, config.Port, config.SSLMode, config.TimeZone)

		db, e := gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger:                 logger.Default.LogMode(logger.Warn), // 开发环境可以改成Info
			SkipDefaultTransaction: true,                                // 跳过默认事务
			DisableAutomaticPing:   true,                                // 关闭自动检测
			AllowGlobalUpdate:      false,                               // 禁止全表操作，防止误删/误更新
		})
		if e != nil {
			err = fmt.Errorf("无法连接数据库: %w", e)
			return
		}

		// 配置连接池
		sqlDB, e := db.DB()
		if e != nil {
			err = fmt.Errorf("获取数据库实例失败: %w", e)
			return
		}

		// 设置连接池参数
		sqlDB.SetMaxIdleConns(5)                   // 设置空闲连接池中的最大连接数
		sqlDB.SetMaxOpenConns(50)                  // 设置打开数据库连接的最大数量
		sqlDB.SetConnMaxLifetime(time.Hour)        // 设置连接可复用的最大时间
		sqlDB.SetConnMaxIdleTime(time.Minute * 10) // 设置空闲连接最大存活时间

		DB = db
	})
	return DB, err
}

func GetDB() *gorm.DB {
	return DB
}
