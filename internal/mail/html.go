package mail

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// 所有邮件共用这一个模版：标题、正文、一个主题色的文字链接（可以没有）、一行灰色小字的落款。
// 没有图片、样式表、底色、边框和按钮，一封邮件的 HTML 只有几百字节。
// 链接的颜色是网站的主题色（base.css 里的 --accent）。
const siteName = "Pingoo"

var tpl = template.Must(template.New("mail").Parse(`<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8"><title>{{.Title}} - {{.SiteName}}</title></head>` +
	`<body style="font-family: sans-serif; line-height: 1.6; color: #333;"><div style="max-width: 600px; margin: 0 auto; padding: 20px;">` +
	`<h2 style="margin-top: 0; margin-bottom: 16px;">{{.Title}}</h2>` +
	`<div>{{.Content}}</div>` +
	`{{if .ActionURL}}<p><a href="{{.ActionURL}}" style="` + linkStyle + `">{{.ActionText}}</a></p>{{end}}` +
	`<p style="font-size: 12px; color: #999; margin-top: 24px;">{{.Footer}}</p>` +
	`</div></body></html>`))

const linkStyle = "color: #2b59c3; text-decoration: none;"

// Title 是邮件正文里的大标题：没有单独写的话用邮件主题。
func (m Msg) title() string {
	if m.Title != "" {
		return m.Title
	}
	return m.Subject
}

// footer 是最后那行小字。带回复地址的邮件（用户反馈）是让收件人直接回复的，不能写“请勿回复”。
func (m Msg) footer() string {
	if m.ReplyTo != "" {
		return "直接回复这封邮件就能回复用户。"
	}
	return "此邮件由 " + siteName + " 系统自动发送，请勿直接回复。"
}

// Text 是邮件的纯文本版：正文、操作链接、落款。不显示 HTML 的客户端看到的是它，本地试运行时写进日志的也是它。
func (m Msg) Text() string {
	s := strings.TrimSpace(m.Body)
	if m.ActionURL != "" {
		s += "\n\n" + m.Action + "：" + m.ActionURL
	}
	return s + "\n\n" + m.footer()
}

// HTML 是邮件的 HTML 版。
func (m Msg) HTML() string {
	action := m.Action
	if action == "" {
		action = m.title()
	}
	var b strings.Builder
	_ = tpl.Execute(&b, map[string]any{"Title": m.title(), "SiteName": siteName, "Content": template.HTML(content(m.Body)),
		"ActionURL": m.ActionURL, "ActionText": action, "Footer": m.footer()})
	return b.String()
}

// 只认 ASCII 的网址：中文正文里网址后面常常直接跟着全角标点或汉字，不能一起算进去。
var urlRe = regexp.MustCompile(`https?://[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+`)

// content 把纯文本正文排成几个段落：空行分段，段里的换行保留。内容全部转义（反馈邮件的正文是用户写的）。
// 只有两处加粗：缩进的“名字：值”里的值（初始密码、验证码），和以 “▸ ” 开头的小标题（每日简报里的网站名）。
func content(body string) string {
	var b strings.Builder
	open := false
	for _, l := range strings.Split(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			if open {
				b.WriteString("</p>")
				open = false
			}
			continue
		}
		if open {
			b.WriteString("<br>")
		} else {
			b.WriteString("<p>")
			open = true
		}
		k, v, isKV := strings.Cut(t, "：")
		switch {
		case strings.HasPrefix(t, "▸ "):
			b.WriteString("<b>" + html.EscapeString(strings.TrimPrefix(t, "▸ ")) + "</b>")
		case isKV && strings.HasPrefix(l, "    "):
			b.WriteString(html.EscapeString(k) + "：<b>" + html.EscapeString(v) + "</b>")
		default:
			b.WriteString(inline(t))
		}
	}
	if open {
		b.WriteString("</p>")
	}
	return b.String()
}

// inline 转义一行文字，并把里面的网址变成链接。链接显示的文字就是网址本身（去掉 https://），
// 不会出现“显示的是一个地址、点开是另一个地址”的情况。
func inline(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range urlRe.FindAllStringIndex(s, -1) {
		u := strings.TrimRight(s[m[0]:m[1]], ".,;:!?)'")
		b.WriteString(html.EscapeString(s[last:m[0]]))
		b.WriteString(`<a href="` + html.EscapeString(u) + `" style="` + linkStyle + `">` + html.EscapeString(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")) + `</a>`)
		last = m[0] + len(u)
	}
	b.WriteString(html.EscapeString(s[last:]))
	return b.String()
}
