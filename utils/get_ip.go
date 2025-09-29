package utils

import (
	"fmt"
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

// GetRealIP 从Gin Context获取真实客户端IP地址
func GetRealIP(c *gin.Context) string {
	// 检查 CF-Connecting-IP (Cloudflare)
	if ip := c.GetHeader("CF-Connecting-IP"); ip != "" {
		if realIP := validateIP(ip); realIP != "" {
			return realIP
		}
	}

	// 检查 X-Real-IP (Nginx常用)
	if ip := c.GetHeader("X-Real-IP"); ip != "" {
		if realIP := validateIP(ip); realIP != "" {
			return realIP
		}
	}

	// 检查 X-Forwarded-For (最常见的代理头)
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		for _, ip := range ips {
			ip = strings.TrimSpace(ip)
			if realIP := validateIP(ip); realIP != "" && !isPrivateIP(realIP) {
				return realIP
			}
		}
	}

	// 检查 True-Client-IP (Akamai, Cloudflare)
	if ip := c.GetHeader("True-Client-IP"); ip != "" {
		if realIP := validateIP(ip); realIP != "" {
			return realIP
		}
	}

	// 检查 X-Client-IP
	if ip := c.GetHeader("X-Client-IP"); ip != "" {
		if realIP := validateIP(ip); realIP != "" {
			return realIP
		}
	}

	// 使用 Gin 的 ClientIP (已实现了基本的代理头检查)
	return c.ClientIP()
}

// GetRealIPWithTrust 获取真实IP（带信任代理验证）
// 信任验证就是只信任特定代理服务器发送的IP头信息，防止恶意用户伪造IP地址。
func GetRealIPWithTrust(c *gin.Context, trustedProxies []string) string {
	remoteIP := c.ClientIP()

	// 如果直连IP不在信任列表中，直接返回
	if !isTrustedProxy(remoteIP, trustedProxies) {
		ip, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		return ip
	}

	// 如果在信任列表中，则检查代理头
	return GetRealIP(c)
}

// validateIP 验证IP格式是否正确
func validateIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) != nil {
		return ip
	}
	return ""
}

// isPrivateIP 判断是否为内网IP
func isPrivateIP(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}

	for _, cidr := range privateRanges {
		_, ipNet, _ := net.ParseCIDR(cidr)
		if ipNet.Contains(parsedIP) {
			return true
		}
	}

	return false
}

// isTrustedProxy 检查IP是否在信任的代理列表中
func isTrustedProxy(ip string, trustedProxies []string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	for _, trusted := range trustedProxies {
		if strings.Contains(trusted, "/") {
			_, ipNet, err := net.ParseCIDR(trusted)
			if err == nil && ipNet.Contains(parsedIP) {
				return true
			}
		} else {
			if ip == trusted {
				return true
			}
		}
	}

	return false
}

// AnonymizeIP 匿名化 IP 地址：IPv4 截断到 /24，IPv6 截断到 /64。
// 返回匿名化后的 IP 字符串。
func AnonymizeIP(ipStr string) (string, error) {
	// 1. 解析 IP 地址字符串
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ipStr, fmt.Errorf("无效的 IP 地址格式: %s", ipStr)
	}

	// 2. 检查 IP 类型并进行截断 (Masking)
	// 尝试转换为 IPv4 4字节格式。如果成功，说明是 IPv4。
	if ip4 := ip.To4(); ip4 != nil {
		// --- IPv4 截断到 /24 ---
		// 子网掩码 255.255.255.0
		mask := net.IPv4Mask(255, 255, 255, 0)
		// 应用掩码（位与操作）
		anonIP := ip4.Mask(mask)
		return anonIP.String(), nil
	}

	// 如果不是 IPv4，则假设是 IPv6
	if len(ip) == net.IPv6len {
		// --- IPv6 截断到 /64 ---
		// IPv6 地址是 16 字节（128 位）。/64 意味着保留前 8 个字节（64 位）。
		// 掩码前 8 字节为 255，后 8 字节为 0。
		maskBytes := make([]byte, net.IPv6len)
		// 设置前 8 字节为 255 (保留网络前缀)
		for i := 0; i < 8; i++ {
			maskBytes[i] = 0xFF
		}

		// 创建 IPv6 掩码
		mask := net.IPMask(maskBytes)

		// 应用掩码
		anonIP := ip.Mask(mask)
		return anonIP.String(), nil
	}

	// 既不是标准的 IPv4，也不是标准的 IPv6
	return ipStr, fmt.Errorf("不支持的 IP 地址类型或长度: %s", ipStr)
}

// ===== 使用示例 =====

func test() {
	r := gin.Default()

	// 方式1: 基础用法
	r.GET("/analytics", func(c *gin.Context) {
		realIP := GetRealIP(c)

		c.JSON(200, gin.H{
			"ip": realIP,
		})
	})

	// 方式2: 带信任代理验证（推荐生产环境）
	r.GET("/analytics-secure", func(c *gin.Context) {
		trustedProxies := []string{
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			// 添加你的Nginx/CDN IP
		}

		realIP := GetRealIPWithTrust(c, trustedProxies)

		c.JSON(200, gin.H{
			"ip": realIP,
		})
	})

	// 方式3: 作为中间件使用
	r.Use(RealIPMiddleware())

	r.GET("/track", func(c *gin.Context) {
		// 从Context中获取中间件设置的IP
		ip := c.GetString("real_ip")

		c.JSON(200, gin.H{
			"tracked_ip": ip,
		})
	})

	r.Run(":8080")
}

// RealIPMiddleware 真实IP中间件
func RealIPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		realIP := GetRealIP(c)
		c.Set("real_ip", realIP)
		c.Next()
	}
}

// RealIPMiddlewareWithTrust 带信任验证的中间件
func RealIPMiddlewareWithTrust(trustedProxies []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		realIP := GetRealIPWithTrust(c, trustedProxies)
		c.Set("real_ip", realIP)
		c.Next()
	}
}
