package pow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 固定测试向量：用 Python hashlib 独立算出来的，保证 Go、浏览器里的 JS 和标准实现一致。
var vectors = []struct {
	in, hex string
	zeros   int
}{
	{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", 0},
	{"abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", 0},
	{"0123456789abcdef0123456789abcdef:0", "ca6f1a8710df6d0d4232f2ea641de69c9807039d58a19336eaacf90510e55478", 0},
	{"00112233445566778899aabbccddeeff:123456789", "c8629c816fbbe0861d4af977d273e6bd22a0df571d002a6fcc8db2cadb127509", 0},
	{strings.Repeat("a", 55), "9f4390f8d30c2dd92ec9f095b65e2b9ae9b0a925a5258e241c9f1e910f734318", 0},
	{"00112233445566778899aabbccddeeff:140894", "0000b94175c1997145715c7da012d1866ee143737993b8915d9d5e04edfc93d9", 16},
}

const (
	solvedSalt  = "00112233445566778899aabbccddeeff"
	solvedNonce = "140894" // 第一个让前 16 位为 0 的 nonce
)

func TestVectorsGo(t *testing.T) {
	for _, v := range vectors {
		sum := sha256.Sum256([]byte(v.in))
		if hex.EncodeToString(sum[:]) != v.hex || LeadingZeroBits(sum) != v.zeros {
			t.Errorf("%q: %x（%d 个 0 位）", v.in, sum, LeadingZeroBits(sum))
		}
	}
}

// TestVectorsJS 用 Node 跑浏览器里的 pow.js，确认前端算法和后端一致。没装 Node 时跳过。
func TestVectorsJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("没有 Node，跳过前端算法的测试")
	}
	js, _ := filepath.Abs("../../web/static/js/pow.js")
	if _, err := os.Stat(js); err != nil {
		t.Fatal(err)
	}
	var cases []string
	for _, v := range vectors {
		cases = append(cases, `[`+jsString(v.in)+`,"`+v.hex+`",`+itoa(v.zeros)+`]`)
	}
	script := `const p = require(` + jsString(js) + `);
const cases = [` + strings.Join(cases, ",") + `];
for (const [s, h, z] of cases) {
  const w = p.sha256(s);
  if (p.hex(w) !== h || p.leadingZeroBits(w) !== z) { console.error("mismatch", JSON.stringify(s), p.hex(w), p.leadingZeroBits(w)); process.exit(1); }
}
const n = p.search("` + solvedSalt + `", 16, 0, 1000000);
if (n !== "` + solvedNonce + `") { console.error("search got", n); process.exit(1); }
console.log("ok");`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("JS 和后端算法不一致：%v\n%s", err, out)
	}
}

func jsString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func itoa(n int) string { return strconv.Itoa(n) }

// solve 用来在测试里找 nonce。
func solve(token string) string {
	parts := strings.Split(token, ".")
	need := 0
	for _, c := range parts[2] {
		need = need*10 + int(c-'0')
	}
	for n := 0; ; n++ {
		s := itoaN(n)
		if LeadingZeroBits(sha256.Sum256([]byte(parts[3]+":"+s))) >= need {
			return s
		}
	}
}

func itoaN(n int) string { return strconv.Itoa(n) }

func TestVerify(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	p := NewWithKey([]byte("test-key-0123456789abcdef0123456"))
	p.Now = func() time.Time { return now }
	tok := p.Issue("register", 8)
	n := solve(tok)

	at := func(d time.Duration) { p.Now = func() time.Time { return now.Add(d) } }
	at(time.Second)
	if err := p.Verify(tok, n, "register", 8); !errors.Is(err, ErrTooFast) {
		t.Fatalf("1 秒内提交应该算脚本，得到 %v", err)
	}
	at(3 * time.Second)
	if err := p.Verify(tok, n, "login", 8); !errors.Is(err, ErrScope) {
		t.Fatalf("注册页的挑战不能用来登录，得到 %v", err)
	}
	if err := p.Verify(tok, n, "register", 10); !errors.Is(err, ErrTooEasy) {
		t.Fatalf("难度低于当前要求应该拒绝，得到 %v", err)
	}
	if err := p.Verify(tok[:len(tok)-2]+"xx", n, "register", 8); !errors.Is(err, ErrSignature) {
		t.Fatalf("签名被改过应该拒绝，得到 %v", err)
	}
	if err := p.Verify(strings.Replace(tok, ".8.", ".1.", 1), n, "register", 1); !errors.Is(err, ErrSignature) {
		t.Fatalf("改低难度会让签名失效，得到 %v", err)
	}
	if err := p.Verify(tok, "abc", "register", 8); !errors.Is(err, ErrMalformed) {
		t.Fatalf("nonce 只能是数字，得到 %v", err)
	}
	wrong := "0"
	for LeadingZeroBits(sha256.Sum256([]byte(strings.Split(tok, ".")[3]+":"+wrong))) >= 8 {
		wrong += "1"
	}
	if err := p.Verify(tok, wrong, "register", 8); !errors.Is(err, ErrWork) {
		t.Fatalf("没算对应该拒绝，得到 %v", err)
	}
	if err := p.Verify(tok, n, "register", 8); err != nil {
		t.Fatalf("正确的答案应该通过：%v", err)
	}
	if err := p.Verify(tok, n, "register", 8); !errors.Is(err, ErrReused) {
		t.Fatalf("同一个挑战只能用一次，得到 %v", err)
	}
	tok2 := p.Issue("register", 8) // 签发时间是 now+3s
	at(3*time.Second + TTL + time.Second)
	if err := p.Verify(tok2, solve(tok2), "register", 8); !errors.Is(err, ErrExpired) {
		t.Fatalf("超过 30 分钟应该过期，得到 %v", err)
	}
	// 重启后（换了密钥）旧挑战全部失效
	if err := NewWithKey([]byte("another-key-0123456789abcdef0123")).Verify(tok2, solve(tok2), "register", 8); !errors.Is(err, ErrSignature) {
		t.Fatalf("换了密钥旧挑战应该失效，得到 %v", err)
	}
}

// 密钥不变时（由 APP_SECRET 派生），重启前签发的挑战，重启后的新签发器照样认。
func TestChallengeSurvivesRestart(t *testing.T) {
	key := []byte("derived-from-app-secret-0123456789")
	before := NewWithKey(key)
	tok := before.IssueAt("register", 8, time.Now().Add(-time.Minute))
	after := NewWithKey(key) // 模拟重启：新的签发器，已用记录是空的
	if err := after.Verify(tok, Solve(tok), "register", 8); err != nil {
		t.Fatalf("重启后旧挑战应该还能用: %v", err)
	}
	if err := NewWithKey([]byte("another-key")).Verify(tok, Solve(tok), "register", 8); err == nil {
		t.Fatal("换了密钥的挑战不应该通过")
	}
}
