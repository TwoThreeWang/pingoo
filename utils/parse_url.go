package utils

import (
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// NormalizeReferrer 如果为空显示为直接访问，如果不为空只保留主域名
func NormalizeReferrer(referrer string) string {
	if referrer == "" {
		return "direct" // 或者 "直接访问"
	}

	u, err := url.Parse(referrer)
	if err != nil || u.Host == "" {
		return "direct" // URL 解析失败，也算直接访问
	}

	host := u.Hostname()
	eTLDPlusOne, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host // 出错就返回原 host
	}

	return strings.ToLower(eTLDPlusOne)
}
