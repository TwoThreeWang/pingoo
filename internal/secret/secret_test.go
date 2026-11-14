package secret

import (
	"strings"
	"testing"
)

func TestEncryptRoundTrip(t *testing.T) {
	b := New("0123456789abcdef0123456789abcdef")
	for _, in := range []string{"", "sk-test-123", `{"type":"service_account","private_key":"-----BEGIN"}`} {
		enc, err := b.Encrypt(in)
		if err != nil {
			t.Fatal(err)
		}
		if in != "" && enc == in {
			t.Fatalf("未加密: %q", enc)
		}
		out, err := b.Decrypt(enc)
		if err != nil {
			t.Fatal(err)
		}
		if out != in {
			t.Fatalf("解密结果不一致: %q != %q", out, in)
		}
	}
}

func TestDecryptWithOtherSecretFails(t *testing.T) {
	enc, _ := New("0123456789abcdef0123456789abcdef").Encrypt("hello")
	if _, err := New("ffffffffffffffffffffffffffffffff").Decrypt(enc); err == nil {
		t.Fatal("换了 APP_SECRET 应该无法解密")
	}
}

func TestSignVerify(t *testing.T) {
	b := New("0123456789abcdef0123456789abcdef")
	sig := b.Sign("data")
	if !b.Verify("data", sig) || b.Verify("data2", sig) {
		t.Fatal("签名校验结果不对")
	}
}

func TestMask(t *testing.T) {
	if got := Mask("sk-abcdefghijkl3f9a"); got != "sk-••••••••••••••••3f9a" {
		t.Fatalf("Mask = %q", got)
	}
	if Mask("") != "" || Mask("short") != "••••••••" {
		t.Fatal("短密钥处理不对")
	}
}

func TestPasswordHash(t *testing.T) {
	h := HashPassword("correct horse")
	if !CheckPassword(h, "correct horse") || CheckPassword(h, "wrong") || CheckPassword("", "") || CheckPassword("plain", "plain") {
		t.Fatal("密码校验不对")
	}
	if HashPassword("x") == HashPassword("x") {
		t.Fatal("同一个密码两次哈希应该不同（加盐）")
	}
}

func TestRedeemCodeFormat(t *testing.T) {
	c := RedeemCode()
	if len(c) != 23 || NormalizeCode(strings.ToLower(strings.ReplaceAll(c, "-", " "))) != c {
		t.Fatalf("卡密格式不对: %s", c)
	}
	if NormalizeCode("PGO-1234") != "" {
		t.Fatal("位数不对的卡密应该无效")
	}
	if p := RandomPassword(); len(p) != 12 {
		t.Fatalf("初始密码长度不对: %s", p)
	}
}
