package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripThinking(t *testing.T) {
	cases := map[string]string{
		"一加一等于二。":                            "一加一等于二。",
		"<think>先想想</think>\n一加一等于二。":        "一加一等于二。",
		"想了很久</think>答案是二":                   "答案是二",
		"<think>a</think>甲<think>b</think>乙": "甲乙",
		"<think>被截断的思考":                      "",
	}
	for in, want := range cases {
		if got := StripThinking(in); got != want {
			t.Errorf("StripThinking(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractJSON(t *testing.T) {
	var v struct{ A int }
	if err := ExtractJSON("好的：\n```json\n{\"A\": 3}\n```", &v); err != nil || v.A != 3 {
		t.Fatalf("got %v %v", v, err)
	}
}

func TestChatRetriesEmptyBody(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			w.WriteHeader(200) // 空响应
		case 2:
			_, _ = w.Write([]byte(`{"error":{"message":"upstream timeout"}}`))
		default:
			_, _ = w.Write([]byte(`{"model":"m2","choices":[{"message":{"role":"assistant","content":"<think>x</think>好"},"finish_reason":"stop"}]}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "k", "m")
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || resp.Message.Content != "好" || resp.Model != "m2" {
		t.Fatalf("calls=%d content=%q model=%q", calls, resp.Message.Content, resp.Model)
	}
}

func TestUsageCached(t *testing.T) {
	var deepseek, openai, none Usage
	_ = json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":5,"prompt_cache_hit_tokens":64,"prompt_cache_miss_tokens":36}`), &deepseek)
	_ = json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":80}}`), &openai)
	_ = json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":null}`), &none)
	if deepseek.Cached() != 64 || openai.Cached() != 80 || none.Cached() != 0 {
		t.Fatalf("deepseek=%d openai=%d none=%d", deepseek.Cached(), openai.Cached(), none.Cached())
	}
}

func TestAgentMaxCalls(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		if step == 1 { // 一轮里并发调 4 次工具（参数各不相同）
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[` +
				`{"id":"a","type":"function","function":{"name":"t","arguments":"{\"n\":1}"}},{"id":"b","type":"function","function":{"name":"t","arguments":"{\"n\":2}"}},` +
				`{"id":"c","type":"function","function":{"name":"t","arguments":"{\"n\":3}"}},{"id":"d","type":"function","function":{"name":"t","arguments":"{\"n\":4}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"结论"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	ran := 0
	a := &Agent{Client: New(srv.URL, "k", "m"), MaxSteps: 5, MaxCalls: 2,
		Handlers: map[string]ToolFunc{"t": func(ctx context.Context, args json.RawMessage) (string, string, error) { ran++; return "ok", "t", nil }}}
	res, err := a.Run(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil || res.Content != "结论" || ran != 2 {
		t.Fatalf("err=%v content=%q ran=%d", err, res.Content, ran)
	}
	// 工具往来原样留在结果里：1 条发起调用的消息 + 4 条工具结果（没执行的也要有回复）
	if len(res.Messages) != 5 || res.Messages[0].Role != "assistant" || res.Messages[4].Role != "tool" {
		t.Fatalf("Messages = %+v", res.Messages)
	}
}

// 参数完全一样的查询只执行一次：第二次直接告诉模型结果在上面，不占查询次数，也不把同一份数据再发一遍。
func TestAgentSkipsRepeatedCalls(t *testing.T) {
	step := 0
	var lastTool string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		var body struct{ Messages []Message }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if n := len(body.Messages); n > 0 && body.Messages[n-1].Role == "tool" {
			lastTool = body.Messages[n-1].Content
		}
		if step <= 2 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"a","type":"function","function":{"name":"t","arguments":"{\"n\":1}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"结论"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	ran := 0
	a := &Agent{Client: New(srv.URL, "k", "m"), MaxSteps: 6,
		Handlers: map[string]ToolFunc{"t": func(ctx context.Context, args json.RawMessage) (string, string, error) {
			ran++
			return "数据", "t", nil
		}}}
	res, err := a.Run(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil || res.Content != "结论" || ran != 1 || lastTool != repeatedMsg {
		t.Fatalf("err=%v content=%q ran=%d lastTool=%q", err, res.Content, ran, lastTool)
	}
}

// 接口没有返回 usage 时按文字长度估算：中文一字一个 token，其他字符四个一个；工具调用的参数和思考过程也算输出。
func TestChatEstimatesMissingUsage(t *testing.T) {
	if n := EstimateTokens("你好abcdefg"); n != 4 {
		t.Fatalf("EstimateTokens = %d, want 4", n)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"好的","tool_calls":[{"id":"1","type":"function","function":{"name":"ping","arguments":"{}"}}]},"reasoning_content":"想一想","finish_reason":"tool_calls"}]}`))
	}))
	defer srv.Close()
	resp, err := New(srv.URL, "k", "m").Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "请调用 ping"}}})
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	// 输出：好的(2) + 想一想(3) + "ping{}"(6 个字符 → 2)
	if !u.Estimated || u.CompletionTokens != 7 || u.PromptTokens < 5 {
		t.Fatalf("usage = %+v", u)
	}
	var sum Usage
	sum.Add(Usage{PromptTokens: 10, CompletionTokens: 1, CacheHit: 4})
	sum.Add(u)
	if !sum.Estimated || sum.PromptTokens != 10+u.PromptTokens || sum.Cached() != 4 {
		t.Fatalf("Add = %+v", sum)
	}

	// 接口返回了用量就照用，不估算
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"好"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":1}}`))
	}))
	defer srv2.Close()
	resp, _ = New(srv2.URL, "k", "m").Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if resp.Usage.Estimated || resp.Usage.PromptTokens != 9 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

// 用量快到预扣额时（Budget 返回 true）先用一句话让模型收尾，工具定义照常带着（去掉会让提示缓存全部失效）；
// 模型不听、还要查，下一轮才真的不给工具。两种情况都不会把 10 轮跑满。
func TestAgentBudgetWrapsUp(t *testing.T) {
	run := func(obeys bool) (calls []bool, ran int, content string) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Tools    []any
				Messages []Message
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			hasTools := len(body.Tools) > 0
			calls = append(calls, hasTools)
			asked := false // 有没有被要求收尾
			for _, m := range body.Messages {
				asked = asked || m.Content == wrapUpMsg
			}
			if hasTools && !(asked && obeys) {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"1","type":"function","function":{"name":"q","arguments":"{\"n\":%d}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1000,"completion_tokens":10}}`, len(calls))))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"结论"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":10}}`))
		}))
		defer srv.Close()
		a := &Agent{Client: New(srv.URL, "k", "m"), MaxSteps: 10,
			Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "q", Parameters: map[string]any{"type": "object"}}}},
			Handlers: map[string]ToolFunc{"q": func(context.Context, json.RawMessage) (string, string, error) { ran++; return "数据", "q", nil }},
			Budget:   func(u Usage) bool { return u.PromptTokens >= 2000 }}
		res, err := a.Run(context.Background(), []Message{{Role: "user", Content: "问"}})
		if err != nil {
			t.Fatal(err)
		}
		return calls, ran, res.Content
	}
	// 听话的模型：第 3 次请求还带着工具，但直接给了结论
	if calls, ran, content := run(true); content != "结论" || len(calls) != 3 || !calls[2] || ran != 2 {
		t.Fatalf("听话的模型：calls=%v ran=%d content=%q", calls, ran, content)
	}
	// 不听话的模型：第 3 次还要查（不执行），第 4 次不给工具了
	if calls, ran, content := run(false); content != "结论" || len(calls) != 4 || !calls[2] || calls[3] || ran != 2 {
		t.Fatalf("不听话的模型：calls=%v ran=%d content=%q", calls, ran, content)
	}
}

// 思考强度换成各家服务商自己的参数；管理员在附加参数里写了同名参数时以他写的为准。
func TestThinkingParams(t *testing.T) {
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	cases := []struct{ style, url, effort, want string }{
		{"auto", "https://api.xiaomimimo.com/v1", EffortOff, `{"thinking":{"type":"disabled"}}`},
		{"auto", "https://api.xiaomimimo.com/v1", EffortLow, `{"thinking":{"type":"enabled"}}`},
		{"effort", "", EffortOff, `{"reasoning_effort":"low"}`},
		{"effort", "", EffortHigh, `{"reasoning_effort":"high"}`},
		{"auto", "https://openrouter.ai/api/v1", EffortMedium, `{"reasoning":{"effort":"medium"}}`},
		{"qwen", "", EffortOff, `{"enable_thinking":false}`},
		{"auto", "https://dashscope.aliyuncs.com/compatible-mode/v1", EffortLow, `{"enable_thinking":true,"thinking_budget":1024}`},
		{"qwen", "", EffortMedium, `{"enable_thinking":true,"thinking_budget":4096}`},
		{"qwen", "", EffortHigh, `{"enable_thinking":true}`},
		{"auto", "https://api.deepseek.com", EffortOff, `{"thinking":{"type":"disabled"}}`},
		{"deepseek", "", EffortLow, `{"reasoning_effort":"low","thinking":{"type":"enabled"}}`},
		{"deepseek", "", EffortMedium, `{"reasoning_effort":"high","thinking":{"type":"enabled"}}`},
		{"deepseek", "", EffortHigh, `{"reasoning_effort":"max","thinking":{"type":"enabled"}}`},
		{"auto", "https://example.com/v1", EffortOff, `null`}, // 认不出来的服务商不带思考参数
		{"none", "https://api.openai.com/v1", EffortLow, `null`},
		{"effort", "", "", `null`},
	}
	for _, c := range cases {
		if got := js(ThinkingParams(c.style, c.url, c.effort)); got != c.want {
			t.Errorf("ThinkingParams(%q,%q,%q) = %s, want %s", c.style, c.url, c.effort, got, c.want)
		}
	}
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"好"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k", "m")
	c.Thinking = map[string]any{"reasoning_effort": "medium"}
	ask := func(req Request) {
		req.Messages = []Message{{Role: "user", Content: "hi"}}
		if _, err := c.Chat(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if ask(Request{}); body["reasoning_effort"] != "medium" {
		t.Fatalf("应该带上任务的思考强度: %v", body)
	}
	if ask(Request{Thinking: map[string]any{"reasoning_effort": "low"}}); body["reasoning_effort"] != "low" {
		t.Fatalf("单次请求可以换思考强度: %v", body)
	}
	c.Extra = map[string]any{"reasoning_effort": "high"}
	if ask(Request{}); body["reasoning_effort"] != "high" {
		t.Fatalf("附加参数里写了就以附加参数为准: %v", body)
	}
}

// 输出上限先用 max_tokens；接口不认就换 max_completion_tokens，再不认就不带。管理员自己写了上限时不覆盖。
func TestChatOutputLimit(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if body["max_tokens"] != nil {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'max_tokens'. Use 'max_completion_tokens' instead."}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"好"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k", "m")
	req := Request{Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: 500}
	if _, err := c.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1]["max_completion_tokens"] != float64(500) || bodies[1]["max_tokens"] != nil {
		t.Fatalf("应该换成 max_completion_tokens 重试: %v", bodies)
	}
	bodies = nil
	c.Extra = map[string]any{"max_completion_tokens": 9}
	if _, err := c.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || bodies[0]["max_completion_tokens"] != float64(9) || bodies[0]["max_tokens"] != nil {
		t.Fatalf("管理员写了上限就不再加: %v", bodies)
	}
}

// 模型的附加参数原样加进请求，但不能覆盖 model、messages 这些字段。
func TestClientExtraParams(t *testing.T) {
	if _, err := ValidExtra(`{"model":"x"}`); err == nil {
		t.Error("附加参数不能覆盖 model")
	}
	if _, err := ValidExtra(`[1,2]`); err == nil {
		t.Error("附加参数要是 JSON 对象")
	}
	if v, err := ValidExtra(` {"reasoning_effort": "low"} `); err != nil || v != `{"reasoning_effort":"low"}` {
		t.Errorf("ValidExtra = %q %v", v, err)
	}
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"好"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k", "m")
	c.Extra = map[string]any{"reasoning_effort": "low", "model": "hijack"}
	if _, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if body["reasoning_effort"] != "low" || body["model"] != "m" {
		t.Fatalf("请求体不对: %v", body)
	}
}

// 被服务商的内容审核拦截：原样重试一次；还是被拦就返回 ErrContentFilter，那句拒绝提示不能当成回答。
func TestAgentContentFilter(t *testing.T) {
	run := func(blocked int) (*RunResult, error, int) {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls <= blocked {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"The request was rejected because it was considered high risk"},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":50,"completion_tokens":0}}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"结论"},"finish_reason":"stop"}],"usage":{"prompt_tokens":50,"completion_tokens":5}}`))
		}))
		defer srv.Close()
		res, err := (&Agent{Client: New(srv.URL, "k", "m"), MaxSteps: 4}).Run(context.Background(), []Message{{Role: "user", Content: "问"}})
		return res, err, calls
	}
	if res, err, calls := run(1); err != nil || res.Content != "结论" || calls != 2 || res.Usage.PromptTokens != 100 {
		t.Fatalf("拦一次应该重试成功，两次调用都计入用量: err=%v content=%q calls=%d usage=%+v", err, res.Content, calls, res.Usage)
	}
	// 接口没给用量的那次调用按字数估算输入；前面命中过缓存的话，和上一次相同的开头按缓存价算
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		switch step {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"a","type":"function","function":{"name":"t","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":30,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":10}}}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"rejected"},"finish_reason":"content_filter"}]}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"结论"},"finish_reason":"stop"}],"usage":{"prompt_tokens":60,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":30}}}`))
		}
	}))
	defer srv.Close()
	res, err := (&Agent{Client: New(srv.URL, "k", "m"), MaxSteps: 5,
		Handlers: map[string]ToolFunc{"t": func(context.Context, json.RawMessage) (string, string, error) { return "数据", "t", nil }},
	}).Run(context.Background(), []Message{{Role: "user", Content: "问"}})
	if err != nil || !res.Usage.Estimated || res.Usage.Cached() != 10+30+30 {
		t.Fatalf("估算的那次调用应该有 30 个 token 按缓存价算: err=%v usage=%+v", err, res.Usage)
	}
	if res, err, calls := run(5); !errors.Is(err, ErrContentFilter) || res.Content != "" || calls != 2 || res.Usage.PromptTokens != 100 {
		t.Fatalf("连着被拦应该返回 ErrContentFilter，用量照记: err=%v content=%q calls=%d usage=%+v", err, res.Content, calls, res.Usage)
	}
}

// 收尾那一轮：工具照常带着，tool_choice 是 none。接口不认 none（报 400）就退回 auto。
func TestAgentWrapUpToolChoice(t *testing.T) {
	var choices []any
	rejectNone := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if rejectNone && body["tool_choice"] == "none" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"tool_choice must be auto"}}`))
			return
		}
		choices = append(choices, body["tool_choice"])
		if body["tool_choice"] == "auto" && len(choices) < 3 {
			_, _ = w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"1","type":"function","function":{"name":"q","arguments":"{\"n\":%d}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`, len(choices))))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"结论"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	run := func() {
		choices = nil
		a := &Agent{Client: New(srv.URL, "k", "m"), MaxSteps: 4,
			Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "q", Parameters: map[string]any{"type": "object"}}}},
			Handlers: map[string]ToolFunc{"q": func(context.Context, json.RawMessage) (string, string, error) { return "数据", "q", nil }}}
		if res, err := a.Run(context.Background(), []Message{{Role: "user", Content: "问"}}); err != nil || res.Content != "结论" {
			t.Fatalf("err=%v res=%+v", err, res)
		}
	}
	// 4 轮：前 2 轮查数据，第 3 轮收尾（none）
	if run(); len(choices) != 3 || choices[0] != "auto" || choices[2] != "none" {
		t.Fatalf("收尾那一轮应该是 tool_choice=none: %v", choices)
	}
	rejectNone = true
	if run(); len(choices) != 3 || choices[2] != "auto" {
		t.Fatalf("接口不认 none 时退回 auto: %v", choices)
	}
}

// DeepSeek 这类要回传思考过程的服务商：思考内容留在消息里、下一轮原样发回去；其他服务商收到就丢掉。
// 接口因为缺思考内容报 400 时，这次任务后面的调用都不思考。
func TestKeepReasoning(t *testing.T) {
	var sent [][]Message
	var thinking []any
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Message
			Thinking any
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if th, _ := body.Thinking.(map[string]any); fail && th["type"] == "enabled" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"Missing reasoning_content field in the assistant message"}}`))
			return
		}
		sent = append(sent, body.Messages)
		thinking = append(thinking, body.Thinking)
		if len(sent) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"先查一下","tool_calls":[{"id":"1","type":"function","function":{"name":"q","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"结论","reasoning_content":"所以"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	run := func(keep bool) *RunResult {
		sent, thinking = nil, nil
		c := New(srv.URL, "k", "m")
		c.KeepReasoning = keep
		c.Thinking, c.Light = ThinkingParams("deepseek", "", EffortMedium), ThinkingParams("deepseek", "", EffortOff)
		res, err := (&Agent{Client: c, MaxSteps: 4,
			Handlers: map[string]ToolFunc{"q": func(context.Context, json.RawMessage) (string, string, error) { return "数据", "q", nil }},
		}).Run(context.Background(), []Message{{Role: "user", Content: "问"}})
		if err != nil || res.Content != "结论" {
			t.Fatalf("err=%v res=%+v", err, res)
		}
		return res
	}
	res := run(true)
	if got := sent[1][1].ReasoningContent; got != "先查一下" {
		t.Fatalf("第二轮要把第一轮的思考原样发回去: %q", got)
	}
	if n := len(res.Messages); n != 3 || res.Messages[2].ReasoningContent != "所以" {
		t.Fatalf("最终回答的思考也要留着: %+v", res.Messages)
	}
	res = run(false)
	if sent[1][1].ReasoningContent != "" || len(res.Messages) != 2 {
		t.Fatalf("不要求回传的服务商不留思考内容: %+v", res.Messages)
	}
	fail = true
	run(true)
	for _, th := range thinking {
		if m, _ := th.(map[string]any); m["type"] != "disabled" {
			t.Fatalf("报 400 之后这次任务都不思考: %v", thinking)
		}
	}
}
