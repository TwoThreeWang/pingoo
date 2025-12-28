package main

import (
	"fmt"
	"log"
	"net/http"
	"pingoo/config"
	"pingoo/database"
	"pingoo/routers"
	"pingoo/services"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	// 加载配置
	cfg := config.Load()

	// 时区设置
	loc, _ := time.LoadLocation(cfg.Database.TimeZone)
	time.Local = loc

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

	// 启动定时任务（保留最近60天数据，每天凌晨2:00清理）
	scheduler := services.NewScheduler(60)
	scheduler.Start()

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
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%s", port),
		Handler:           r,
		ReadTimeout:       30 * time.Second,  // 限制读取完整请求的时间（包括Body）
		WriteTimeout:      30 * time.Second,  // 限制写入响应的时间
		IdleTimeout:       120 * time.Second, // 限制空闲连接的保持时间（Keep-Alive）
		ReadHeaderTimeout: 10 * time.Second,  // 限制读取请求头的时间
		MaxHeaderBytes:    1 << 20,           // 1MB，限制请求头的最大大小
	}

	// 启动服务
	log.Printf("启动http服务,端口:%s,监听请求中...", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
