package routers

import (
	"pingoo/controllers"

	"github.com/gin-gonic/gin"
)

// SetupRouter 设置路由
func SetupRouter(router *gin.Engine) *gin.Engine {

	// 设置中间件
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	// 设置CORS
	router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// 创建控制器实例
	eventController := controllers.NewEventController()

	// API路由组
	api := router.Group("/api/v1")
	{
		// 事件相关路由
		events := api.Group("/events")
		{
			events.GET("", eventController.GetEvents)           // 获取事件列表
			events.POST("", eventController.CreateEvent)        // 创建事件
			events.GET("/stats", eventController.GetEventStats) // 获取事件统计
			events.GET("/:id", eventController.GetEventByID)    // 获取单个事件
		}

		// 追踪路由（简化版，用于前端埋点）
		track := api.Group("/track")
		{
			track.POST("/pageview", eventController.TrackPageView)    // 页面浏览追踪
			track.POST("/event", eventController.TrackCustomEvent)    // 自定义事件追踪
			track.GET("/pageview.gif", eventController.TrackPageView) // 图片方式页面追踪
		}
	}

	// 健康检查路由
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"message": "服务运行正常",
		})
	})

	// 根路径
	router.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"name":    "Pingoo Analytics",
			"version": "1.0.0",
			"message": "网站统计分析系统API服务",
		})
	})

	return router
}

// SetupAdminRouter 设置管理后台路由（可选）
func SetupAdminRouter() *gin.Engine {
	router := gin.Default()

	// 设置静态文件服务
	router.Static("/static", "./public")
	router.LoadHTMLGlob("templates/*")

	// 管理后台路由
	admin := router.Group("/admin")
	{
		admin.GET("/dashboard", func(c *gin.Context) {
			c.HTML(200, "dashboard.html", gin.H{
				"title": "Pingoo Analytics - 管理后台",
			})
		})

		admin.GET("/events", func(c *gin.Context) {
			c.HTML(200, "events.html", gin.H{
				"title": "事件列表 - Pingoo Analytics",
			})
		})

		admin.GET("/stats", func(c *gin.Context) {
			c.HTML(200, "stats.html", gin.H{
				"title": "统计分析 - Pingoo Analytics",
			})
		})
	}

	return router
}
