package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"pingoo/internal/agent"
	"pingoo/internal/store"
)

type convGroup struct {
	Label string
	Items []store.Conversation
}

type chatData struct {
	Groups   []convGroup
	Conv     *store.Conversation
	Messages []store.Message
	Sites    []store.Site
	ScopeID  int64
	Q        string
	AIReady  bool
	AIMsg    string
	Suggest  [][2]string
}

func groupConvs(cs []store.Conversation) []convGroup {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	gs := []convGroup{{Label: "今天"}, {Label: "最近 7 天"}, {Label: "更早"}}
	for _, c := range cs {
		switch {
		case !c.UpdatedAt.Before(today):
			gs[0].Items = append(gs[0].Items, c)
		case c.UpdatedAt.After(today.AddDate(0, 0, -7)):
			gs[1].Items = append(gs[1].Items, c)
		default:
			gs[2].Items = append(gs[2].Items, c)
		}
	}
	var out []convGroup
	for _, g := range gs {
		if len(g.Items) > 0 {
			out = append(out, g)
		}
	}
	return out
}

func (s *Server) chatPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := chatData{Q: r.URL.Query().Get("q")}
	cs, err := s.Store.Conversations(ctx, d.Q, 100)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Groups = groupConvs(cs)
	d.Sites, _ = s.Store.Sites(ctx)
	st := s.aiStatus(r, "chat")
	d.AIReady, d.AIMsg = st.Ready, st.Msg
	if id := pathID(r); id > 0 {
		c, err := s.Store.Conversation(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		d.Conv = &c
		if c.SiteID != nil {
			d.ScopeID = *c.SiteID
		}
		d.Messages, _ = s.Store.Messages(ctx, id)
	} else if sid, _ := strconv.ParseInt(r.URL.Query().Get("site"), 10, 64); sid > 0 {
		d.ScopeID = sid
	}
	d.Suggest = s.suggestions(d.Sites, d.ScopeID)
	if isHX(r) && r.URL.Query().Get("partial") == "list" {
		s.part(w, "chat-list", d)
		return
	}
	title := "问答"
	if d.Conv != nil {
		title = d.Conv.Title
	}
	s.page(w, r, "chat", title, "chat", d)
}

func (s *Server) suggestions(sites []store.Site, scope int64) [][2]string {
	name := "全部网站"
	for _, x := range sites {
		if x.ID == scope {
			name = x.Name
		}
	}
	if scope > 0 {
		return [][2]string{
			{"这个网站的读者都从哪来？哪个来源质量最好？", name}, {"有哪些文章该更新了？", name},
			{"手机和电脑的读者差别大吗？", name}, {"哪些搜索词离首页只差一点？", name},
			{"最近 28 天最大的变化是什么，原因是什么？", name}, {"接下来一周我最应该做哪三件事？", name},
		}
	}
	return [][2]string{
		{"这周哪个网站最值得我花时间？", name}, {"哪个网站的搜索流量最有潜力？", name},
		{"有没有网站的数据看起来不正常？", name}, {"手机和电脑的读者差别大吗？", name},
		{"哪些搜索词离首页只差一点？", name}, {"这个月和上个月比，最大的变化是什么？", name},
	}
}

// maxQuestion 是问答里一个问题最多多少字。
const maxQuestion = 2000

func (s *Server) chatSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// 问题有长度上限：每一轮都要把整段对话重新发给 AI，超长的问题会让一次问答的用量翻好几倍
	if utf8.RuneCountInString(text) > maxQuestion {
		toast(w, fmt.Sprintf("问题太长了，请控制在 %d 字以内", maxQuestion))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if st := s.aiStatus(r, "chat"); !st.Ready {
		toast(w, st.Msg)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	id := pathID(r)
	isNew := id == 0
	if isNew {
		var sid *int64
		if v := formID(r, "scope"); v > 0 {
			sid = &v
		}
		var err error
		if id, err = s.Store.CreateConversation(ctx, sid, agent.TitleFor(text)); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	conv, err := s.Store.Conversation(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// 上一个回答还没结束时，不接受新问题；但早就该结束（超过 answerTimeout）还停在“回答中”的，
	// 说明后台任务没能把结果写回来，标成失败后放行，不然这个对话就再也问不了了
	if msgs, _ := s.Store.Messages(ctx, id); len(msgs) > 0 && msgs[len(msgs)-1].Status == "running" {
		last := msgs[len(msgs)-1]
		if time.Since(last.CreatedAt) < answerTimeout {
			toast(w, "上一个问题还在回答中")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		last.Status, last.Error = "error", "回答超时了，请重试"
		_ = s.Store.UpdateMessage(ctx, last)
	}
	if _, err := s.Store.AddMessage(ctx, store.Message{ConversationID: id, Role: "user", Content: text}); err != nil {
		s.fail(w, r, err)
		return
	}
	mid, err := s.Store.AddMessage(ctx, store.Message{ConversationID: id, Role: "assistant", Status: "running"})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Agent.Answer(ctx, conv, mid)
	if isNew {
		redirect(w, r, "/chat/"+strconv.FormatInt(id, 10))
		return
	}
	user := store.Message{Role: "user", Content: text}
	m, _ := s.Store.Message(ctx, mid)
	s.part(w, "chat-append", map[string]any{"User": user, "M": m})
}

// answerTimeout 比后台回答任务自己的时限（6 分钟）长：过了这个时间还是“回答中”，任务一定已经不在了。
const answerTimeout = 8 * time.Minute

func (s *Server) messagePoll(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.Message(r.Context(), pathID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if m.Status == "running" && time.Since(m.CreatedAt) > answerTimeout {
		m.Status, m.Error = "error", "回答超时了，请重试"
		_ = s.Store.UpdateMessage(r.Context(), m)
	}
	s.part(w, "chat-message", m)
}

func (s *Server) chatScope(w http.ResponseWriter, r *http.Request) {
	var sid *int64
	if v := formID(r, "scope"); v > 0 {
		sid = &v
	}
	if err := s.Store.SetConversationSite(r.Context(), pathID(r), sid); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "范围已更新，从下一个问题开始生效")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) chatDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteConversation(r.Context(), pathID(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/chat")
}

// messageSave 把一条 AI 回答保存起来，下一期报告会附上。
func (s *Server) messageSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, err := s.Store.Message(ctx, pathID(r))
	if err != nil || m.Role != "assistant" || m.Status != "done" {
		http.Error(w, "只能保存已经完成的回答", 400)
		return
	}
	conv, err := s.Store.Conversation(ctx, m.ConversationID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	title := conv.Title
	if msgs, err := s.Store.Messages(ctx, conv.ID); err == nil {
		for i, x := range msgs {
			if x.ID == m.ID && i > 0 && msgs[i-1].Role == "user" {
				title = agent.TitleFor(msgs[i-1].Content)
			}
		}
	}
	if err := s.Store.AddNote(ctx, store.Note{SiteID: conv.SiteID, Source: "chat", SourceID: m.ID, Title: title, Content: m.Content}); err != nil {
		s.fail(w, r, err)
		return
	}
	toast(w, "已保存，下一期报告会附上这条分析")
	w.Write([]byte(`<button type="button" disabled>已保存到报告 ✓</button>`))
}
