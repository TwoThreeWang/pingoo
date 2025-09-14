package utils

import (
	"fmt"

	"github.com/mssola/user_agent"
)

// ParseUserAgent 解析UserAgent字符串，提取设备、浏览器、操作系统
func ParseUserAgent(uaString string) (device, browser, os string, isBot bool) {
	ua := user_agent.New(uaString)

	// 操作系统
	os = ua.OS()

	// 浏览器
	name, version := ua.Browser()
	browser = fmt.Sprintf("%s %s", name, version)

	// 设备类型（粗分：mobile/desktop/bot）
	isMobile := ua.Mobile()
	if isMobile {
		device = "mobile"
	} else {
		device = "desktop"
	}
	isBot = ua.Bot()
	return
}
