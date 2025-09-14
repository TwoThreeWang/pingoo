package main

import (
	"log"
	"pingoo/config"
	"pingoo/database"
	"pingoo/routers"

	"github.com/gin-gonic/gin"
)

func main() {
	// 加载配置
	cfg := config.Load()

	// 初始化数据库
	db, err := database.Initialize(cfg.Database)
	if err != nil {
		log.Fatal("数据库连接失败:", err)
	}
	log.Println("数据库连接成功", db)

	// 执行数据库迁移
	if err := database.Migrate(); err != nil {
		log.Fatal("数据库迁移失败:", err)
	}

	// 添加测试数据（开发环境）
	if cfg.Server.Mode == "development" {
		if err := database.SeedData(); err != nil {
			log.Printf("添加测试数据失败: %v", err)
		}
	}

	// 设置Gin模式
	gin.SetMode(cfg.Server.Mode)

	// 创建Gin引擎
	r := gin.Default()

	// 配置静态文件服务
	r.Static("/public", "./public")

	// 初始化路由
	routers.SetupRouter(r, db, cfg)

	// 启动服务器
	port := cfg.Server.Port
	log.Printf("服务器启动在端口: %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal("服务器启动失败:", err)
	}
}
