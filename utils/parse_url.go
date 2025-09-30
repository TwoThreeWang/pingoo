package utils

import (
	"net/url"
	"strings"
)

// NormalizeReferrer 如果为空显示为直接访问，如果不为空只保留主域名
func NormalizeReferrer(referrer string) string {
	if referrer == "" {
		return "direct" // 或者 "直接访问"
	}

	u, err := url.Parse(referrer)
	if err != nil || u.Host == "" {
		return referrer // URL 解析失败，原样返回
	}

	host := strings.ToLower(u.Hostname())

	return host
}
