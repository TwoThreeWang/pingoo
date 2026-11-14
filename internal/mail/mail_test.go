package mail

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"regexp"
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

// 邮件带纯文本和 HTML 两个版本，内容一样；HTML 版用所有邮件共用的那个模版。
func TestBuildMultipartAndHTML(t *testing.T) {
	msg := Msg{To: "a@b.com", Subject: "重新设置 Pingoo 密码", Title: "重新设置密码",
		Body:   "你申请了重新设置密码，请点下面的链接完成操作。链接 30 分钟内有效，只能用一次。如非本人操作，请忽略此邮件，原来的密码仍然有效。",
		Action: "重新设置密码", ActionURL: "https://pingoo.app/reset?t=abc.def&x=1"}
	raw := string(build(Config{From: "noreply@pingoo.app"}, msg))
	m := regexp.MustCompile(`boundary="([^"]+)"`).FindStringSubmatch(raw)
	if m == nil || !strings.Contains(raw, "Content-Type: multipart/alternative") {
		t.Fatalf("应该是 multipart/alternative：\n%.400s", raw)
	}
	parts := strings.Split(raw, "--"+m[1])
	if len(parts) != 4 || !strings.HasPrefix(parts[3], "--") || !strings.Contains(parts[1], "text/plain") || !strings.Contains(parts[2], "text/html") {
		t.Fatalf("应该先纯文本、后 HTML 两个部分，实际 %d 段", len(parts))
	}
	decode := func(part string) string {
		_, enc, _ := strings.Cut(part, "\r\n\r\n")
		b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(enc, "\r\n", ""))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// 纯文本版：正文、链接、落款都要有
	if text := decode(parts[1]); text != msg.Body+"\n\n重新设置密码：https://pingoo.app/reset?t=abc.def&x=1\n\n此邮件由 Pingoo 系统自动发送，请勿直接回复。" {
		t.Errorf("纯文本版不对：%q", text)
	}
	// HTML 版就是那个模版，一个字节都不多
	want := `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8"><title>重新设置密码 - Pingoo</title></head>` +
		`<body style="font-family: sans-serif; line-height: 1.6; color: #333;"><div style="max-width: 600px; margin: 0 auto; padding: 20px;">` +
		`<h2 style="margin-top: 0; margin-bottom: 16px;">重新设置密码</h2>` +
		`<div><p>` + msg.Body + `</p></div>` +
		`<p><a href="https://pingoo.app/reset?t=abc.def&amp;x=1" style="color: #2b59c3; text-decoration: none;">重新设置密码</a></p>` +
		`<p style="font-size: 12px; color: #999; margin-top: 24px;">此邮件由 Pingoo 系统自动发送，请勿直接回复。</p>` +
		`</div></body></html>`
	if h := decode(parts[2]); h != want {
		t.Errorf("HTML 版和模版不一致：\n得到 %s\n应该 %s", h, want)
	}
}

func TestMailTemplate(t *testing.T) {
	// 没有要用户去做的事：不出链接；没写标题：用邮件主题
	h := Msg{Subject: "你的 Pingoo 登录邮箱已修改", Body: "登录邮箱刚刚改了。"}.HTML()
	if strings.Contains(h, "<a ") || !strings.Contains(h, ">你的 Pingoo 登录邮箱已修改</h2>") {
		t.Errorf("没有链接的邮件不应该出现 <a>，标题用主题：%s", h)
	}
	// 缩进的“名字：值”（初始密码、验证码）：值加粗；标题和内容都要转义
	h = Msg{Title: "欢迎使用 <Pingoo>", Body: "你的账号已经开通。\n\n    登录邮箱：a@b.com\n    初始密码：Xy<12>&z"}.HTML()
	if !strings.Contains(h, "<p>登录邮箱：<b>a@b.com</b><br>初始密码：<b>Xy&lt;12&gt;&amp;z</b></p>") || !strings.Contains(h, "欢迎使用 &lt;Pingoo&gt;</h2>") {
		t.Errorf("账号信息的值应该加粗，标题和密码要转义：%s", h)
	}
	// 每日简报：网站名加粗，下面几行照常；正文里的网址变成链接，后面跟着的中文标点不能算进去
	h = Msg{Title: "每日简报", Body: "这是昨天的情况。\n\n▸ 我的<博客>\n  访客 120（比前一天 +20%）· 会话 150\n  需要关注：流量下滑了\n\n新周报：https://pingoo.app/reports/1。"}.HTML()
	if !strings.Contains(h, "<p><b>我的&lt;博客&gt;</b><br>访客 120（比前一天 +20%）· 会话 150<br>需要关注：流量下滑了</p>") {
		t.Errorf("简报里的网站名应该加粗：%s", h)
	}
	if !strings.Contains(h, `<a href="https://pingoo.app/reports/1" style="color: #2b59c3; text-decoration: none;">pingoo.app/reports/1</a>。`) {
		t.Errorf("正文里的网址应该变成链接，显示的文字就是网址本身：%s", h)
	}
	// 反馈邮件是让管理员直接回复的：落款不能写“请勿回复”；用户写的 HTML 必须显示成文字
	fb := Msg{ReplyTo: "u@c.com", Title: "用户反馈：建议", Body: "类型：建议\n\n<script>alert(1)</script> <a href=\"https://evil.example\">点我</a>"}
	h = fb.HTML()
	if strings.Contains(h, "<script>") || strings.Contains(h, `<a href="https://evil.example">点我`) {
		t.Errorf("用户写的 HTML 没有转义：%s", h)
	}
	if !strings.Contains(h, ">直接回复这封邮件就能回复用户。</p>") || strings.Contains(h, "请勿直接回复") || !strings.HasSuffix(fb.Text(), "直接回复这封邮件就能回复用户。") {
		t.Errorf("反馈邮件的落款不对：%s", h)
	}
}
