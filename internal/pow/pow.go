// Package pow 是注册、登录、找回密码用的人机校验：工作量证明（Proof of Work），不依赖任何第三方验证码服务。
//
// 规则：浏览器找一个 nonce，使 SHA-256(salt + ":" + nonce) 的前 bits 位都是 0。
// 服务端签发的挑战是 scope.签发时间.bits.salt.签名，签名密钥由 APP_SECRET 派生（重启前后不变）：
// 重启前打开的注册、登录页面重启后照样能提交。“已用记录”在内存里，重启后清空，
// 所以重启前 30 分钟内已经用过的挑战还能再用一次——只是多一次提交机会，登录锁定、发信限额照样生效。
package pow

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/bits"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	TTL    = 30 * time.Minute // 挑战的有效期
	MinAge = 2 * time.Second  // 签发后不足 2 秒就提交，视为脚本
)

var (
	ErrMalformed = errors.New("挑战格式不对")
	ErrSignature = errors.New("挑战签名不对")
	ErrScope     = errors.New("挑战用错了地方")
	ErrExpired   = errors.New("挑战过期了")
	ErrTooFast   = errors.New("提交得太快")
	ErrTooEasy   = errors.New("挑战难度低于当前要求")
	ErrWork      = errors.New("计算结果不对")
	ErrReused    = errors.New("挑战已经用过了")
)

type Issuer struct {
	key  []byte
	Now  func() time.Time // 测试里可以替换
	mu   sync.Mutex
	used map[string]time.Time // salt → 过期时间
	last time.Time            // 上次清理已用记录的时间
}

func NewWithKey(key []byte) *Issuer {
	return &Issuer{key: key, Now: time.Now, used: map[string]time.Time{}}
}

func (p *Issuer) sign(body string) string {
	m := hmac.New(sha256.New, p.key)
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Issue 签发一个挑战。scope 只能是字母（例如 login、register），不能含点号。
func (p *Issuer) Issue(scope string, difficulty int) string {
	return p.IssueAt(scope, difficulty, p.Now())
}

// IssueAt 按指定的签发时间签发（测试里用来造一个“已经过了 2 秒”的挑战）。
func (p *Issuer) IssueAt(scope string, difficulty int, at time.Time) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	body := scope + "." + strconv.FormatInt(at.Unix(), 10) + "." + strconv.Itoa(difficulty) + "." + hex.EncodeToString(salt)
	return body + "." + p.sign(body)
}

// Solve 在服务端找一个答案（只给测试用：真实的计算在浏览器里）。
func Solve(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 5 {
		return ""
	}
	need, _ := strconv.Atoi(parts[2])
	for n := 0; ; n++ {
		s := strconv.Itoa(n)
		if LeadingZeroBits(sha256.Sum256([]byte(parts[3]+":"+s))) >= need {
			return s
		}
	}
}

// Verify 校验浏览器交回来的挑战和 nonce。minBits 是当前要求的最低难度：
// 熔断后调高难度，之前囤下的低难度挑战一律不收。通过后这个挑战就作废，同一个挑战只能用一次。
func (p *Issuer) Verify(token, nonce, scope string, minBits int) error {
	parts := strings.Split(token, ".")
	if len(parts) != 5 || len(nonce) == 0 || len(nonce) > 20 || !digits(nonce) {
		return ErrMalformed
	}
	body := strings.Join(parts[:4], ".")
	if !hmac.Equal([]byte(p.sign(body)), []byte(parts[4])) {
		return ErrSignature
	}
	if parts[0] != scope {
		return ErrScope
	}
	ts, err1 := strconv.ParseInt(parts[1], 10, 64)
	need, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil {
		return ErrMalformed
	}
	now := p.Now()
	age := now.Sub(time.Unix(ts, 0))
	switch {
	case age > TTL:
		return ErrExpired
	case age < MinAge:
		return ErrTooFast
	case need < minBits:
		return ErrTooEasy
	}
	salt := parts[3]
	if LeadingZeroBits(sha256.Sum256([]byte(salt+":"+nonce))) < need {
		return ErrWork
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now.Sub(p.last) > time.Minute {
		p.last = now
		for k, exp := range p.used {
			if now.After(exp) {
				delete(p.used, k)
			}
		}
	}
	if _, ok := p.used[salt]; ok {
		return ErrReused
	}
	p.used[salt] = time.Unix(ts, 0).Add(TTL) // 记到挑战过期为止，过期之后它本来也不能用了
	return nil
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// LeadingZeroBits 返回哈希开头有几个连续的 0 位。
func LeadingZeroBits(sum [32]byte) int {
	n := 0
	for _, b := range sum {
		if b == 0 {
			n += 8
			continue
		}
		return n + bits.LeadingZeros8(b)
	}
	return n
}
