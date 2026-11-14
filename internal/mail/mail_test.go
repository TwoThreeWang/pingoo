package mail

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCanReceive(t *testing.T) {
	var calls atomic.Int32
	m := &MX{Lookup: func(ctx context.Context, d string) ([]*net.MX, error) {
		calls.Add(1)
		switch d {
		case "gmail.com":
			return []*net.MX{{Host: "gmail-smtp-in.l.google.com.", Pref: 5}}, nil
		case "example.com":
			return []*net.MX{{Host: ".", Pref: 0}}, nil // Null MX
		case "nomx.dev", "gmial.con":
			return nil, &net.DNSError{Err: "no such host", Name: d, IsNotFound: true}
		case "slow.org":
			return nil, &net.DNSError{Err: "i/o timeout", Name: d, IsTimeout: true}
		}
		return nil, errors.New("unexpected")
	}}
	ctx := context.Background()
	cases := map[string]bool{
		"a@gmail.com": true, "a@GMAIL.com.": true, "a@example.com": false, "a@nomx.dev": false, "a@gmial.con": false,
		"a@localhost": false, "a@slow.org": true, "not-an-email": false,
	}
	for email, want := range cases {
		if got := m.CanReceive(ctx, email); got != want {
			t.Errorf("CanReceive(%q) = %v, want %v", email, got, want)
		}
	}
	before := calls.Load()
	m.CanReceive(ctx, "b@gmail.com")   // 有结论：走缓存
	m.CanReceive(ctx, "b@example.com") // 有结论：走缓存
	m.CanReceive(ctx, "b@slow.org")    // 临时故障不缓存：再查一次
	if got := calls.Load() - before; got != 1 {
		t.Fatalf("缓存不对：多查了 %d 次（应该只有超时的那个重查）", got)
	}
}

func TestValidAddressAndBuild(t *testing.T) {
	for in, want := range map[string]bool{"a@b.com": true, "A.B+c@x.io": true, "Evan <a@b.com>": false, "a@b.com\r\nBcc: x@y.z": false, "@b.com": false, "a@": false} {
		if ValidAddress(in) != want {
			t.Errorf("ValidAddress(%q) 应该是 %v", in, want)
		}
	}
	raw := string(build(Config{From: "noreply@pingoo.app", FromName: "Pingoo 小助手"}, Msg{To: "a@b.com", Subject: "你的初始密码", Body: "密码：abc"}))
	if !strings.Contains(raw, "Subject: =?UTF-8?b?") || !strings.Contains(raw, "From: =?UTF-8?b?") || !strings.Contains(raw, "<noreply@pingoo.app>") {
		t.Fatalf("邮件头没有正确编码：\n%s", raw)
	}
	if strings.Contains(raw, "Reply-To") {
		t.Error("没有 ReplyTo 时不应该写 Reply-To")
	}
	// 反馈邮件带用户的回复地址；不合格的地址（可能夹带换行注入邮件头）直接丢掉
	raw = string(build(Config{From: "noreply@pingoo.app"}, Msg{To: "admin@b.com", ReplyTo: "u@c.com", Subject: "反馈", Body: "x"}))
	if !strings.Contains(raw, "Reply-To: u@c.com\r\n") {
		t.Errorf("应该有 Reply-To：\n%s", raw)
	}
	raw = string(build(Config{From: "noreply@pingoo.app"}, Msg{To: "admin@b.com", ReplyTo: "u@c.com\r\nBcc: x@y.z", Subject: "反馈", Body: "x"}))
	if strings.Contains(raw, "Bcc") || strings.Contains(raw, "Reply-To") {
		t.Errorf("不合格的回复地址不能写进邮件头：\n%s", raw)
	}
}
