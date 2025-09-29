package utils

import (
	"fmt"

	"github.com/mileusna/useragent"
)

// ParseUserAgent 解析UserAgent字符串，提取设备、浏览器、操作系统
func ParseUserAgent(uaString string) (device, browser, os string, isBot bool) {
	ua := useragent.Parse(uaString)

	// 判断设备类型
	if ua.Mobile {
		device = "Mobile"
	} else if ua.Tablet {
		device = "Tablet"
	} else if ua.Desktop {
		device = "Desktop"
	} else {
		device = "Unknown"
	}

	// 获取浏览器信息
	if ua.Name != "" {
		if ua.Version != "" {
			browser = fmt.Sprintf("%s %s", ua.Name, ua.Version)
		} else {
			browser = ua.Name
		}
	} else {
		browser = "Unknown"
	}

	// 获取操作系统信息
	if ua.OS != "" {
		if ua.OSVersion != "" {
			os = fmt.Sprintf("%s %s", ua.OS, ua.OSVersion)
		} else {
			os = ua.OS
		}
	} else {
		os = "Unknown"
	}

	// 是否为机器人
	isBot = ua.Bot

	return device, browser, os, isBot
}
