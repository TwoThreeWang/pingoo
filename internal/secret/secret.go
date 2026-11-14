// Package secret 负责加密存储敏感配置（API Key、服务账号密钥）和签名。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Box struct {
	aead   cipher.AEAD
	macKey []byte
}

// New 从 APP_SECRET 派生出两把互相独立的密钥：一把加密，一把签名。
func New(appSecret string) *Box {
	encKey := sha256.Sum256([]byte("pingoo/encrypt/v1|" + appSecret))
	macKey := sha256.Sum256([]byte("pingoo/sign/v1|" + appSecret))
	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		panic(err) // 32 字节密钥不会出错
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &Box{aead: aead, macKey: macKey[:]}
}

// Encrypt 返回 base64(nonce || ciphertext)。
func (b *Box) Encrypt(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (b *Box) Decrypt(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	n := b.aead.NonceSize()
	if len(raw) < n {
		return "", errors.New("密文太短")
	}
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("解密失败：APP_SECRET 可能被修改过")
	}
	return string(plain), nil
}

func (b *Box) Sign(data string) string {
	m := hmac.New(sha256.New, b.macKey)
	m.Write([]byte(data))
	return hex.EncodeToString(m.Sum(nil))
}

func (b *Box) Verify(data, sig string) bool {
	return hmac.Equal([]byte(b.Sign(data)), []byte(sig))
}

// RandomToken 生成 URL 安全的随机令牌。
func RandomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// Hash 用于把会话令牌存进数据库：数据库泄露也拿不到可用的令牌。
func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Mask 把密钥显示成 sk-••••3f9a 的样子。
func Mask(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= 8 {
		return "••••••••"
	}
	prefix := ""
	if len(r) > 3 && string(r[:3]) == "sk-" {
		prefix = "sk-"
	}
	return prefix + "••••••••••••••••" + string(r[len(r)-4:])
}

// ========== 密码 ==========

// 密码用 PBKDF2-SHA256 加盐哈希（标准库，不引入依赖）。迭代次数按 OWASP 的建议；存成
// "pbkdf2$迭代次数$盐$哈希"，以后调高迭代次数，旧密码照样能校验。
const pbkdf2Iter = 600000

func HashPassword(pw string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	return hashWith(pw, salt, pbkdf2Iter)
}

func hashWith(pw string, salt []byte, iter int) string {
	k, _ := pbkdf2.Key(sha256.New, pw, salt, iter, 32)
	return fmt.Sprintf("pbkdf2$%d$%s$%s", iter, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(k))
}

// CheckPassword 比对密码；stored 为空（还没设过密码）一律不通过。
func CheckPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashWith(pw, salt, iter)), []byte(stored)) == 1
}

// RandomPassword 生成初始密码：去掉容易看错的字符，12 位。
func RandomPassword() string {
	return randomFrom("abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789", 12)
}

// randomFrom 从 alphabet 里均匀地随机取 n 个字符（拒绝采样，避免取模带来的偏差）。
func randomFrom(alphabet string, n int) string {
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, 1)
	for len(out) < n {
		_, _ = rand.Read(buf)
		if int(buf[0]) < limit {
			out = append(out, alphabet[int(buf[0])%len(alphabet)])
		}
	}
	return string(out)
}

// RedeemCode 生成一个卡密，例如 PGO-7KQ2-M9XD-4HTW-ZC8E（20 位，约 100 位熵，猜不中）。
func RedeemCode() string {
	s := randomFrom("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 16)
	return "PGO-" + s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}

// NormalizeCode 把用户输入的卡密整理成标准格式（忽略大小写、空格和多余的横线）。
func NormalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	c := strings.TrimPrefix(b.String(), "PGO")
	if len(c) != 16 {
		return ""
	}
	return "PGO-" + c[0:4] + "-" + c[4:8] + "-" + c[8:12] + "-" + c[12:16]
}
