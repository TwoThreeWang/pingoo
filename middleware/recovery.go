package middleware

import (
	"log"
	"os"
	"pingoo/utils"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// CustomRecovery恢复中间件，用于捕获HTTP请求处理过程中的panic错误，防止服务崩溃，并返回500错误给客户端
func CustomRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %v\n%s", err, debug.Stack())
				// 打印到日志文件
				f, fileErr := os.OpenFile("logs/panic.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if fileErr == nil {
					defer f.Close()
					logger := log.New(f, "", log.LstdFlags)
					logger.Printf("panic: %v\n%s", err, debug.Stack())
				}

				// 返回友好的错误响应
				utils.ServerError(c, "服务器内部错误，请稍后再试")
				c.Abort()
			}
		}()
		c.Next()
	}
}
