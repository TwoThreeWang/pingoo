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

	// 设置Gin模式
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 创建Gin引擎
	r := gin.Default()

	// 配置静态文件服务
	r.Static("/static", "./public")
	r.LoadHTMLGlob("templates/*")

	// 设置路由
	// 初始化路由
	routers.SetupRouter(r)

	// 启动服务器
	log.Printf("服务器启动在端口: %s", cfg.Server.Port)
	if err := r.Run(":" + cfg.Server.Port); err != nil {
		log.Fatal("服务器启动失败:", err)
	}
}
