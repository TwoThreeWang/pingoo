package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sse 把几段内容按流式的格式写出去，每段之后立刻发送。
func sse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", c)
		w.(http.Flusher).Flush()
	}
}

func fastRetry(t *testing.T, idle time.Duration) {
	t.Helper()
	oldIdle, oldWait := idleTimeout, retryWait
	idleTimeout, retryWait = idle, 10*time.Millisecond
	t.Cleanup(func() { idleTimeout, retryWait = oldIdle, oldWait })
}

// 流式返回：正文、思考、工具调用都是一段段来的，要拼成和一次性返回一样的结果；用量在最后单独一段。
func TestChatStream(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		sse(w,
			`{"model":"m-real","choices":[{"delta":{"role":"assistant","reasoning_content":"先想"}}]}`,
			`{"choices":[{"delta":{"reasoning_content":"一下"}}]}`,
			`: keep-alive`,
			`{"choices":[{"delta":{"content":"好"}}]}`,
			`{"choices":[{"delta":{"content":"的","tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"ping","arguments":"{\"n\""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":1}"}},{"index":1,"id":"b","function":{"name":"pong","arguments":"{}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":100}}}`,
			`[DONE]`)
	}))
	defer srv.Close()
	c := New(srv.URL, "k", "m")
	c.KeepReasoning = true
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got["stream"] != true || got["stream_options"] == nil {
		t.Errorf("请求应该要求流式返回并带上用量: %v", got)
	}
	m := resp.Message
	if m.Role != "assistant" || m.Content != "好的" || m.ReasoningContent != "先想一下" {
		t.Errorf("正文或思考拼得不对: %+v", m)
	}
	if len(m.ToolCalls) != 2 || m.ToolCalls[0].ID != "a" || m.ToolCalls[0].Function.Name != "ping" || m.ToolCalls[0].Function.Arguments != `{"n":1}` ||
		m.ToolCalls[1].ID != "b" || m.ToolCalls[1].Function.Name != "pong" || m.ToolCalls[1].Type != "function" {
		t.Errorf("工具调用拼得不对: %+v", m.ToolCalls)
	}
	if resp.FinishReason != "tool_calls" || resp.Model != "m-real" {
		t.Errorf("finish=%q model=%q", resp.FinishReason, resp.Model)
	}
	if u := resp.Usage; u.PromptTokens != 120 || u.CompletionTokens != 30 || u.Cached() != 100 || u.Estimated {
		t.Errorf("用量不对: %+v", u)
	}
}

// 慢但一直在输出的回答不能被掐断：总耗时远超 idleTimeout，只要每一段之间没有停太久就行。
func TestChatStreamSlowButAlive(t *testing.T) {
	fastRetry(t, 150*time.Millisecond)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		for i := 0; i < 10; i++ {
			sse(w, `{"choices":[{"delta":{"reasoning_content":"想"}}]}`)
			time.Sleep(50 * time.Millisecond)
		}
		sse(w, `{"choices":[{"delta":{"content":"答案"},"finish_reason":"stop"}]}`, `[DONE]`)
	}))
	defer srv.Close()
	resp, err := New(srv.URL, "k", "m").Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil || resp.Message.Content != "答案" || calls != 1 {
		t.Fatalf("content=%v err=%v calls=%d", resp, err, calls)
	}
}

// 接口卡住（开了头就没有下文）、或者回答中途断开：算临时故障，重试。
func TestChatStreamStallAndCut(t *testing.T) {
	fastRetry(t, 100*time.Millisecond)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1: // 卡住
			sse(w, `{"choices":[{"delta":{"content":"半"}}]}`)
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
		case 2: // 没说完就断开
			sse(w, `{"choices":[{"delta":{"content":"半截"}}]}`)
		default:
			sse(w, `{"choices":[{"delta":{"content":"完整"},"finish_reason":"stop"}]}`, `[DONE]`)
		}
	}))
	defer srv.Close()
	began := time.Now()
	resp, err := New(srv.URL, "k", "m").Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil || resp.Message.Content != "完整" || calls != 3 {
		t.Fatalf("resp=%+v err=%v calls=%d", resp, err, calls)
	}
	if d := time.Since(began); d > 2*time.Second {
		t.Errorf("卡住的那次应该在 idleTimeout 后就放弃，实际用了 %v", d)
	}

	// 一直卡住：报错要说清楚是接口没有输出
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer stuck.Close()
	_, err = New(stuck.URL, "k", "m").Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil || !strings.Contains(err.Error(), "没有任何输出") {
		t.Errorf("一直卡住时的报错不对: %v", err)
	}
}

// 整个任务的时限到了：返回看得懂的 ErrTimeout，不是 context deadline exceeded，也不再重试。
func TestChatTaskDeadline(t *testing.T) {
	fastRetry(t, time.Second)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		for r.Context().Err() == nil {
			sse(w, `{"choices":[{"delta":{"reasoning_content":"想"}}]}`)
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := New(srv.URL, "k", "m").Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if !errors.Is(err, ErrTimeout) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

// 不认 stream_options 的接口：去掉再试；连 stream 都不认的：退回一次性返回。
func TestChatStreamFallback(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		switch {
		case b["stream_options"] != nil:
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`))
		case b["stream"] != nil:
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"stream is not supported"}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"好"},"finish_reason":"stop"}]}`))
		}
	}))
	defer srv.Close()
	resp, err := New(srv.URL, "k", "m").Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil || resp.Message.Content != "好" || len(bodies) != 3 {
		t.Fatalf("resp=%+v err=%v calls=%d", resp, err, len(bodies))
	}
}

func TestMergeToolCall(t *testing.T) {
	idx := func(i int) *int { return &i }
	d := func(i *int, id, name, args string) toolCallDelta {
		x := toolCallDelta{Index: i, ID: id}
		x.Function.Name, x.Function.Arguments = name, args
		return x
	}
	// 没有 index：带 id 的是新调用，不带的接在上一个后面
	var calls []ToolCall
	for _, x := range []toolCallDelta{d(nil, "a", "t", `{"n"`), d(nil, "", "", `:1}`), d(nil, "b", "u", `{}`)} {
		calls = mergeToolCall(calls, x)
	}
	if len(calls) != 2 || calls[0].Function.Arguments != `{"n":1}` || calls[1].Function.Name != "u" {
		t.Errorf("没有 index 时拼错了: %+v", calls)
	}
	// index 都是 0 但 id 不同（Gemini 兼容层）：是两个调用
	calls = nil
	for _, x := range []toolCallDelta{d(idx(0), "a", "t", `{"n":1}`), d(idx(0), "b", "t", `{"n":2}`)} {
		calls = mergeToolCall(calls, x)
	}
	if len(calls) != 2 || calls[1].Function.Arguments != `{"n":2}` {
		t.Errorf("index 重复时拼错了: %+v", calls)
	}
}
