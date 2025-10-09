package utils

import (
	"strconv"
	"strings"

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
		browser = ua.Name
		// if ua.Version != "" {
		// 	browser = fmt.Sprintf("%s %s", ua.Name, ua.Version)
		// } else {
		// 	browser = ua.Name
		// }
	} else {
		browser = "Unknown"
	}

	// 获取操作系统信息
	if ua.OS != "" {
		os = ua.OS
		// if ua.OSVersion != "" {
		// 	os = fmt.Sprintf("%s %s", ua.OS, ua.OSVersion)
		// } else {
		// 	os = ua.OS
		// }
	} else {
		os = "Unknown"
	}

	// 是否为机器人
	isBot = ua.Bot

	return device, browser, os, isBot
}

// DetectDevice 检测设备类型
// ua: User-Agent字符串
// resolution: 分辨率字符串，格式如 "1920*1080" 或 "1920x1080"
// 返回: iPhone, iPad, Mobile/Android, Tablet/Android, Laptop, Desktop
func DetectDevice(ua, resolution string) string {
	if ua == "" || resolution == "" {
		return "Unknown"
	}
	ua = strings.ToLower(ua)
	resolution = strings.ReplaceAll(resolution, "x", "*")
	parts := strings.Split(resolution, "*")

	if len(parts) != 2 {
		return "Unknown"
	}

	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))

	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return "Unknown"
	}

	min, max := w, h
	if h < w {
		min, max = h, w
	}

	if strings.Contains(ua, "iphone") {
		return "iPhone"
	}
	if strings.Contains(ua, "ipad") || (strings.Contains(ua, "macintosh") && max <= 1366) {
		return "iPad"
	}
	if strings.Contains(ua, "android") {
		if !strings.Contains(ua, "mobile") || min >= 600 {
			return "Tablet/Android"
		}
		return "Mobile/Android"
	}
	if strings.Contains(ua, "windows") || strings.Contains(ua, "macintosh") || strings.Contains(ua, "linux") {
		if max <= 1920 {
			return "Laptop"
		}
		return "Desktop"
	}
	return "Unknown"
}
