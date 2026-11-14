package agent

import (
	"strings"
	"testing"

	"pingoo/internal/llm"
	"pingoo/internal/store"
)

// 最近一轮查到的原始数据原样带上（排在那一轮的回答前面）；原始数据已经清掉的轮次只留回答和一行备注。
func TestChatHistory(t *testing.T) {
	call := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function"}}}
	result := llm.Message{Role: "tool", ToolCallID: "c1", Content: "date,value\n2026-11-01,12"}
	history := []store.Message{
		{ID: 1, Role: "user", Content: "上周怎么样", Status: "done"},
		{ID: 2, Role: "assistant", Content: "涨了一成。", Status: "done", Trace: "概览 · 博客 · 11-01..11-07\n拆分 channels · 博客 · 11-01..11-07"},
		{ID: 3, Role: "user", Content: "搜索词呢", Status: "done"},
		{ID: 4, Role: "assistant", Content: "主要是两个词。", Status: "done", Trace: "拆分 queries · 博客 · 11-01..11-07"},
		{ID: 5, Role: "user", Content: "出错的那次", Status: "done"},
		{ID: 6, Role: "assistant", Content: "", Status: "error"},
		{ID: 7, Role: "user", Content: "继续", Status: "done"},
		{ID: 8, Role: "assistant", Status: "running"},
	}
	msgs := chatHistory(history, map[int64][]llm.Message{4: {call, result}}, 8)
	var roles []string
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	if got := strings.Join(roles, ","); got != "user,assistant,user,assistant,tool,assistant,user,user" {
		t.Fatalf("消息顺序不对: %s", got)
	}
	if !strings.Contains(msgs[1].Content, "【系统备注：这一轮查过 概览 · 博客 · 11-01..11-07；拆分 channels") {
		t.Errorf("原始数据清掉的轮次应该留一行备注: %q", msgs[1].Content)
	}
	if msgs[4].Content != result.Content || msgs[3].ToolCalls[0].ID != "c1" {
		t.Errorf("留着的工具往来要原样带上: %+v", msgs[3:5])
	}
	if msgs[5].Content != "主要是两个词。" {
		t.Errorf("带了原始数据的回答不用再加备注: %q", msgs[5].Content)
	}
}

// 历史太长时只带最近的一段，而且起点每 8 条才挪一次：每个问题都挪的话，前面的内容每次都变，提示缓存命不中。
func TestChatHistoryWindow(t *testing.T) {
	build := func(n int) []store.Message {
		var hs []store.Message
		for i := 1; i <= n; i++ {
			role := "user"
			if i%2 == 0 {
				role = "assistant"
			}
			hs = append(hs, store.Message{ID: int64(i), Role: role, Content: "第" + string(rune('0'+i%10)) + "条", Status: "done"})
		}
		return hs
	}
	if got := len(chatHistory(build(24), nil, 0)); got != 24 {
		t.Errorf("24 条以内全带: %d", got)
	}
	a, b := chatHistory(build(26), nil, 0), chatHistory(build(28), nil, 0)
	if len(a) != 18 || len(b) != 20 || a[0].Content != b[0].Content || a[0].Role != "user" {
		t.Errorf("多聊一轮，起点不应该动: %d %d %q %q", len(a), len(b), a[0].Content, b[0].Content)
	}
	if c := chatHistory(build(34), nil, 0); len(c) != 18 {
		t.Errorf("再多 8 条才挪一次起点: %d", len(c))
	}
}

// 留着的原始数据超过上限时，从最早的清起，一次清到下限；最新的一轮总是留着。
// 缓存便宜的模型多留几轮，缓存价是原价一两成的只留最近一两轮。
func TestStaleToolLogs(t *testing.T) {
	cheap, dear := toolLogBudget(true), toolLogBudget(false)
	if drop := staleToolLogs(map[int64]int{2: 9000, 4: 9000}, cheap); drop != nil {
		t.Errorf("没超过上限不清: %v", drop)
	}
	drop := staleToolLogs(map[int64]int{2: 9000, 4: 9000, 6: 5000, 8: 6000}, cheap)
	if len(drop) != 2 || drop[0] != 2 || drop[1] != 4 {
		t.Errorf("应该清掉最早的两轮（剩 11000）: %v", drop)
	}
	if drop := staleToolLogs(map[int64]int{2: 1000, 8: 30000}, cheap); len(drop) != 1 || drop[0] != 2 {
		t.Errorf("最新的一轮再大也留着: %v", drop)
	}
	// 缓存不便宜：两轮各 6000 字还留着，第三轮进来就只剩最新的一轮
	if drop := staleToolLogs(map[int64]int{2: 6000, 4: 6000}, dear); drop != nil {
		t.Errorf("12000 字以内不清: %v", drop)
	}
	if drop := staleToolLogs(map[int64]int{2: 6000, 4: 6000, 6: 6000}, dear); len(drop) != 2 || drop[0] != 2 || drop[1] != 4 {
		t.Errorf("缓存不便宜时只留最新的一轮: %v", drop)
	}
}

// 历史太长（按字数算）时再往后挪起点，免得上下文越滚越大。
func TestChatHistoryChars(t *testing.T) {
	long := strings.Repeat("字", 2500)
	var hs []store.Message
	for i := 1; i <= 20; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		hs = append(hs, store.Message{ID: int64(i), Role: role, Content: long, Status: "done"})
	}
	msgs := chatHistory(hs, nil, 0)
	total := 0
	for _, m := range msgs {
		total += m.Chars()
	}
	if len(msgs) != 12 || total > historyChars || msgs[0].Role != "user" {
		t.Errorf("20 条各 2500 字（共 5 万字）应该只带最近 12 条: %d 条 %d 字", len(msgs), total)
	}
}

// 要回传思考过程的服务商（DeepSeek）：工具往来的最后一条是带着思考的最终回答，下一轮要把思考原样带上，正文用存下来的。
func TestChatHistoryKeepsReasoning(t *testing.T) {
	log := []llm.Message{
		{Role: "assistant", ReasoningContent: "先查渠道", ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function"}}},
		{Role: "tool", ToolCallID: "c1", Content: "数据"},
		{Role: "assistant", Content: "结论\n追问建议：a|b", ReasoningContent: "数据说明……"},
	}
	history := []store.Message{
		{ID: 1, Role: "user", Content: "问", Status: "done"},
		{ID: 2, Role: "assistant", Content: "结论", Status: "done", Trace: "拆分 channels"},
	}
	msgs := chatHistory(history, map[int64][]llm.Message{2: log}, 0)
	if len(msgs) != 4 || msgs[1].ReasoningContent != "先查渠道" || msgs[3].Content != "结论" || msgs[3].ReasoningContent != "数据说明……" {
		t.Fatalf("思考过程要原样带上，最终回答不能重复: %+v", msgs)
	}
}
