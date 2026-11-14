// Package config 从环境变量（和可选的 .env 文件）读取运行配置。
package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"
)

// Config 只放程序启动前就要用到、或者保护数据库本身的配置。站点地址、时区、发信这些能在运行中修改的，
// 都在管理后台里设置。
type Config struct {
	ListenAddr    string // 监听地址，例如 :5004
	DatabaseURL   string // postgres://user:pass@host:5432/db?sslmode=disable
	AppSecret     string // 加密密钥和签名会话用，至少 32 个字符
	AdminEmail    string // 站长（管理员）账号的邮箱：首次启动时用它和 ADMIN_PASSWORD 建管理员
	AdminPassword string
	// TrustedProxies 是可信反向代理的地址（TRUSTED_PROXIES，逗号分隔的 IP 或 CIDR）。
	// 只有请求直接来自这些地址时，才会读 X-Forwarded-For 里的客户端 IP；留空表示不信任任何代理。
	TrustedProxies []netip.Prefix
	// RealIPHeader 是前面的 CDN 写入访客真实 IP 的请求头（REAL_IP_HEADER，例如 Cloudflare 的 CF-Connecting-IP）。
	// 设了它，并且请求直接来自 TRUSTED_PROXIES 里的地址时，优先用这个头里的 IP，不用把整条代理链都列进 TRUSTED_PROXIES。
	RealIPHeader string
	MailDryRun   bool // MAIL_DRYRUN=true：不真的发信，只写日志（本地开发用）
}

func Load() (*Config, error) {
	loadDotEnv(".env")
	c := &Config{
		ListenAddr:    getenv("LISTEN_ADDR", ""),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		AppSecret:     os.Getenv("APP_SECRET"),
		AdminEmail:    strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		MailDryRun:    getenv("MAIL_DRYRUN", "false") == "true",
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":" + getenv("SERVER_PORT", "5004")
	}
	if c.DatabaseURL == "" {
		c.DatabaseURL = buildDSN()
	}
	var errs []string
	if c.AdminEmail == "" || !strings.Contains(c.AdminEmail, "@") {
		errs = append(errs, "ADMIN_EMAIL 不能为空：填站长（管理员）的邮箱，首次启动时会用它和 ADMIN_PASSWORD 建管理员账号")
	}
	tp, err := ParseProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		errs = append(errs, "TRUSTED_PROXIES 格式不对："+err.Error())
	}
	c.TrustedProxies = tp
	c.RealIPHeader = strings.TrimSpace(os.Getenv("REAL_IP_HEADER"))
	if c.RealIPHeader != "" && err == nil && len(tp) == 0 {
		errs = append(errs, "设置了 REAL_IP_HEADER 但 TRUSTED_PROXIES 是空的：只有请求来自可信代理时才会读这个头，请把紧挨着 Pingoo 的那层代理的 IP 或网段填进 TRUSTED_PROXIES")
	}
	if len(c.AppSecret) < 32 {
		errs = append(errs, "APP_SECRET 至少需要 32 个字符（可以用 `openssl rand -hex 32` 生成）")
	}
	switch {
	case c.AdminPassword == "":
		errs = append(errs, "ADMIN_PASSWORD 不能为空")
	case c.AdminPassword == "change-me":
		errs = append(errs, "ADMIN_PASSWORD 还是示例值 change-me，请换成自己的密码")
	case len([]rune(c.AdminPassword)) < MinPasswordLen:
		errs = append(errs, fmt.Sprintf("ADMIN_PASSWORD 太短，至少需要 %d 个字符", MinPasswordLen))
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "；"))
	}
	return c, nil
}

// MinPasswordLen 是密码的最短长度（管理员的初始密码和用户自己设的密码都一样）。
const MinPasswordLen = 8

// ParseProxies 解析 TRUSTED_PROXIES：逗号分隔，每一项是一个 IP（如 127.0.0.1）或一个网段（如 172.16.0.0/12）。
func ParseProxies(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(v, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("%q 不是合法的网段", item)
			}
			out = append(out, netip.PrefixFrom(p.Addr().Unmap(), p.Bits()).Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("%q 不是合法的 IP", item)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func buildDSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(getenv("DB_USER", "postgres"), getenv("DB_PASSWORD", "postgres")),
		Host:   fmt.Sprintf("%s:%s", getenv("DB_HOST", "localhost"), getenv("DB_PORT", "5432")),
		Path:   "/" + getenv("DB_NAME", "pingoo"),
	}
	q := url.Values{}
	q.Set("sslmode", getenv("DB_SSLMODE", "disable"))
	u.RawQuery = q.Encode()
	return u.String()
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// loadDotEnv 是一个极简的 .env 解析器：KEY=VALUE，支持 # 注释和引号。
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}
