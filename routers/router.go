package routers

import (
	"net/http"
	"pingoo/config"
	"pingoo/controllers"
	"pingoo/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SetupRouter 设置路由
func SetupRouter(router *gin.Engine, db *gorm.DB, cfg *config.Config) *gin.Engine {
	// 设置中间件
	// 恢复中间件，用于捕获HTTP请求处理过程中的panic错误，防止服务崩溃，并返回500错误给客户端
	router.Use(gin.Recovery())
	// 日志中间件，用于记录所有HTTP请求的详细信息，包括请求方法、路径、状态码、响应时间等
	router.Use(gin.Logger())
	// 设置CORS
	router.Use(func(c *gin.Context) {
		// 允许所有来源
		c.Header("Access-Control-Allow-Origin", "*")
		// 允许的HTTP方法
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		// 允许的请求头
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})
	// 创建事件控制器实例
	eventController := controllers.NewEventController()
	// API路由组
	api := router.Group("/api")
	{
		// 事件相关路由
		events := api.Group("/events")
		{
			events.GET("", middleware.AuthMiddleware(), eventController.GetEvents)           // 获取事件列表
			events.POST("", middleware.AuthMiddleware(), eventController.CreateEvent)        // 创建事件
			events.GET("/stats", middleware.AuthMiddleware(), eventController.GetEventStats) // 获取事件统计
			events.GET("/:id", middleware.AuthMiddleware(), eventController.GetEventByID)    // 获取单个事件
		}

		// 创建认证控制器实例
		authController := controllers.NewAuthController(db, cfg)
		// 用户认证路由
		auth := api.Group("/auth")
		{
			auth.POST("/register", authController.Register)                                   // 注册
			auth.POST("/login", authController.Login)                                         // 登录
			auth.POST("/refresh", authController.RefreshToken)                                // 刷新令牌
			auth.GET("/me", middleware.AuthMiddleware(), authController.Me)                   // 获取当前用户信息
			auth.PUT("/profile", middleware.AuthMiddleware(), authController.UpdateProfile)   // 更新用户资料
			auth.PUT("/password", middleware.AuthMiddleware(), authController.ChangePassword) // 更新用户密码
		}

		// 创建站点控制器实例
		siteController := controllers.NewSiteController(db)
		// 站点管理路由
		sites := api.Group("/sites")
		sites.Use(middleware.AuthMiddleware())
		{
			sites.GET("", siteController.List)                            // 获取站点列表
			sites.POST("", siteController.Create)                         // 创建站点
			sites.GET("/:id", siteController.Get)                         // 获取站点详情
			sites.PUT("/:id", siteController.Update)                      // 更新站点信息
			sites.DELETE("/:id", siteController.Delete)                   // 删除站点
			sites.DELETE("/:id/stats", siteController.ClearStats)         // 删除网站所有统计数据
			sites.GET("/:id/stats", siteController.GetStats)              // 获取站点统计信息
			sites.GET("/:id/simple-stats", siteController.GetSimpleStats) // 获取站点统计信息概览
		}
	}

	// 健康检查路由
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"message": "服务运行正常",
		})
	})

	// 静态文件路由 - 追踪脚本
	// 访问 /pingoo.js 和配置的 TRACKER_SCRIPT_NAME 时都返回 /public/js/pingoo.js
	pingooScriptHandler := func(c *gin.Context) {
		// 设置缓存头，缓存文件1天
		c.Header("Cache-Control", "public, max-age=86400")
		c.File("./public/js/pingoo.js")
	}
	router.GET("/pingoo.js", pingooScriptHandler)
	if cfg.Site.TrackerScriptName != "" && cfg.Site.TrackerScriptName != "pingoo.js" {
		router.GET("/"+cfg.Site.TrackerScriptName, pingooScriptHandler)
	}
	router.POST("/send", eventController.TrackCustomEvent) // 统一事件追踪接口

	// 使用多模板渲染器
	router.HTMLRender = controllers.LoadLocalTemplates("./templates")
	// 创建前端页面控制器实例
	webController := controllers.NewWebController()
	// 前端页面路由
	router.GET("/", webController.Index)                        // 首页
	router.GET("/login", webController.Login)                   // 登录页
	router.GET("/register", webController.Register)             // 注册页
	router.GET("/dashboard", webController.Dashboard)           // 仪表盘页
	router.GET("/websites/:id", webController.Websites)         // 网站详情页
	router.GET("/profile", webController.Profile)               // 用户中心页
	router.GET("/docs/:name", webController.RenderMarkdownFile) // 文档解析

	// 404路由错误
	router.NoRoute(func(c *gin.Context) {
		c.HTML(http.StatusNotFound, "404", controllers.OutputCommonSession(c, gin.H{
			"Title": "404，页面不存在",
		}))
	})

	return router
}
