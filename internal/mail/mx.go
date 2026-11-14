package mail

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// MX 判断一个邮箱的域名能不能收信（只用标准库）：
//   - 域名不存在、没有 MX 记录、Null MX（只有一条 "."，RFC 7505）、单段域名（如 localhost）都算收不了；
//   - DNS 超时或网络出错时放行，不误伤正常用户；
//   - 有结论的结果缓存 1 小时，临时故障不缓存；每次查询最多等 3 秒。
type MX struct {
	Resolver *net.Resolver
	Lookup   func(ctx context.Context, domain string) ([]*net.MX, error) // 测试里替换
	mu       sync.Mutex
	cache    map[string]mxResult
}

type mxResult struct {
	ok  bool
	exp time.Time
}

const (
	mxTTL     = time.Hour
	mxTimeout = 3 * time.Second
)

// Domain 取出邮箱的域名（小写，去掉末尾的点）；格式不对返回空。
func Domain(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(email[at+1:])), ".")
}

// CanReceive 返回这个邮箱的域名能不能收信。
func (m *MX) CanReceive(ctx context.Context, email string) bool {
	d := Domain(email)
	if d == "" || !strings.Contains(d, ".") {
		return false
	}
	m.mu.Lock()
	if m.cache == nil {
		m.cache = map[string]mxResult{}
	}
	if r, ok := m.cache[d]; ok && time.Now().Before(r.exp) {
		m.mu.Unlock()
		return r.ok
	}
	m.mu.Unlock()

	lookup := m.Lookup
	if lookup == nil {
		res := m.Resolver
		if res == nil {
			res = net.DefaultResolver
		}
		lookup = res.LookupMX
	}
	cctx, cancel := context.WithTimeout(ctx, mxTimeout)
	defer cancel()
	recs, err := lookup(cctx, d)
	var ok bool
	switch {
	case err != nil:
		var de *net.DNSError
		if !errors.As(err, &de) || !de.IsNotFound {
			return true // 超时、网络出错：放行，不缓存
		}
		ok = false // 域名不存在，或者存在但没有 MX 记录
	case len(recs) == 0:
		ok = false
	case len(recs) == 1 && (recs[0].Host == "." || recs[0].Host == ""):
		ok = false // Null MX：明确声明不收信
	default:
		ok = true
	}
	m.mu.Lock()
	if len(m.cache) > 50000 {
		m.cache = map[string]mxResult{} // 防止被大量随机域名撑大
	}
	m.cache[d] = mxResult{ok: ok, exp: time.Now().Add(mxTTL)}
	m.mu.Unlock()
	return ok
}
