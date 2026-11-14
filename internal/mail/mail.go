// Package mail 发送通知邮件（Amazon SES 等任何 SMTP 服务），并在发信前检查收件域名能不能收信。
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"pingoo/internal/store"
)

// Config 是发信服务的配置，在管理后台里设置（密码加密保存）。
type Config struct {
	Host, Port, User, Password string
	From, FromName             string
	DryRun                     bool // 不真的发，只写日志（本地开发和测试用，来自 .env 的 MAIL_DRYRUN）
}

func (c Config) Enabled() bool { return c.DryRun || (c.Host != "" && c.From != "") }

type Mailer struct {
	DryRun bool
	Store  *store.Store
	Log    *slog.Logger
	MX     *MX
	// Sent 在测试里记录发出的邮件
	Sent func(Msg)
}

// Config 读取当前的发信配置（每次发信都读，后台改了马上生效，不用重启）。
func (m *Mailer) Config(ctx context.Context) Config {
	get := func(k, def string) string { return m.Store.SettingOr(ctx, k, def) }
	return Config{Host: get(store.KeySMTPHost, ""), Port: get(store.KeySMTPPort, "587"), User: get(store.KeySMTPUser, ""),
		Password: get(store.KeySMTPPassword, ""), From: get(store.KeySMTPFrom, ""), FromName: get(store.KeySMTPFromName, "Pingoo"),
		DryRun: m.DryRun}
}

type Msg struct {
	Kind    string // register / reset / email_code / notice / feedback
	To      string
	ReplyTo string // 用户反馈：管理员直接点“回复”就能回给用户
	Subject string
	Body    string
	IP      string
	UserID  int64
}

var (
	ErrNotConfigured = errors.New("还没有配置发信服务（SMTP）")
	ErrUndeliverable = errors.New("该邮箱域名无法接收邮件，请检查是否拼写错误")
	ErrDailyLimit    = errors.New("今天的发信额度已经用完，请明天再试")
)

// ValidAddress 检查邮箱格式（只接受纯地址，不接受“名字 <地址>”这种写法）。
func ValidAddress(email string) bool {
	if len(email) > 254 || strings.ContainsAny(email, " \t\r\n<>\"") {
		return false
	}
	a, err := mail.ParseAddress(email)
	return err == nil && a.Address == email && Domain(email) != ""
}

// Send 发一封邮件。所有邮件都走这里：先兜底检查收件域名（收不了信的跳过并记日志），再记一条发信记录，最后发送。
// 发信记录在发送前写，所以限额按“尝试次数”算，发送失败也计数（防止有人靠失败反复触发）。
func (m *Mailer) Send(ctx context.Context, msg Msg) error {
	cfg := m.Config(ctx)
	if !cfg.Enabled() {
		return ErrNotConfigured
	}
	// 全站 24 小时发信硬上限：所有邮件都在这里把关（注册、找回密码、验证码、反馈……），哪个入口被刷都超不过它
	if limit := m.Store.MailLimit(ctx); m.Store.MailCount(ctx, "", "", "", 0, time.Now().Add(-24*time.Hour)) >= limit {
		m.Log.Error("已达到 24 小时发信上限，邮件没有发出", "kind", msg.Kind, "limit", limit)
		return ErrDailyLimit
	}
	if m.MX != nil && !m.MX.CanReceive(ctx, msg.To) {
		m.Log.Warn("收件域名无法接收邮件，跳过", "kind", msg.Kind, "domain", Domain(msg.To))
		return ErrUndeliverable
	}
	id := m.Store.LogMail(ctx, msg.Kind, msg.To, msg.IP, msg.UserID)
	if m.Sent != nil {
		m.Sent(msg)
	}
	if err := m.deliver(cfg, msg); err != nil {
		m.Log.Error("发信失败", "kind", msg.Kind, "domain", Domain(msg.To), "err", err)
		m.Store.MailFailed(context.WithoutCancel(ctx), id, err.Error())
		return err
	}
	return nil
}

func (m *Mailer) deliver(cfg Config, msg Msg) error {
	raw := build(cfg, msg)
	if cfg.DryRun {
		// 只在本地开发时打开：正文里可能有初始密码，正式环境不要用
		m.Log.Info("【试运行，没有真的发出】", "to", msg.To, "subject", msg.Subject, "body", msg.Body)
		return nil
	}
	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	d := &net.Dialer{Timeout: 10 * time.Second}
	if cfg.Port == "465" { // 隐式 TLS
		conn, err = tls.DialWithDialer(d, "tcp", addr, tlsCfg)
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("连接发信服务失败：%w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if cfg.Port != "465" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("发信服务不支持 STARTTLS，拒绝明文发送")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if cfg.User != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("发信服务登录失败：%w", err)
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return err
	}
	if err := c.Rcpt(msg.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// build 拼出邮件原文：UTF-8 纯文本，正文 base64 编码。
func build(cfg Config, msg Msg) []byte {
	from := cfg.From
	if cfg.FromName != "" {
		from = mime.BEncoding.Encode("UTF-8", cfg.FromName) + " <" + cfg.From + ">"
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := Domain(cfg.From)
	if domain == "" {
		domain = "pingoo.local"
	}
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + msg.To + "\r\n")
	if msg.ReplyTo != "" && ValidAddress(msg.ReplyTo) {
		b.WriteString("Reply-To: " + msg.ReplyTo + "\r\n")
	}
	b.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", msg.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: <" + hex.EncodeToString(id) + "@" + domain + ">\r\n")
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(msg.Body))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return []byte(b.String())
}
