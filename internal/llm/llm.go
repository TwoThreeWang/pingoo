// Package llm 是一个极简的 OpenAI 兼容客户端（/chat/completions + 工具调用）。
// 兼容 OpenAI、DeepSeek、通义千问、OpenRouter、Ollama 等。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	// ReasoningContent 是模型的思考过程。只有要求把它原样传回去的服务商才保留（见 Client.KeepReasoning），
	// 其他服务商收到回答就丢掉：传回去要么被忽略，要么白白多算输入。
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// Chars 是这条消息大致占多少字（正文、思考过程加上工具调用的参数），用来估算一段对话有多长。
func (m Message) Chars() int {
	n := len([]rune(m.Content)) + len([]rune(m.ReasoningContent))
	for _, tc := range m.ToolCalls {
		n += len(tc.Function.Name) + len([]rune(tc.Function.Arguments))
	}
	return n
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	// 命中提示缓存的输入 token：DeepSeek 叫 prompt_cache_hit_tokens，OpenAI / OpenRouter 放在 prompt_tokens_details 里
	CacheHit int `json:"prompt_cache_hit_tokens"`
	Details  *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
	// Estimated 表示接口没有返回用量，token 数是按文字长度估算的（见 EstimateTokens）
	Estimated bool `json:"-"`
}

// Add 把另一次调用的用量累加进来（缓存命中统一记到 CacheHit）。
func (u *Usage) Add(o Usage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.CacheHit = u.Cached() + o.Cached()
	u.Details = nil
	u.Estimated = u.Estimated || o.Estimated
}

// EstimateTokens 粗略估算一段文字的 token 数：中日韩文字一个字算 1 个，其他字符 4 个算 1 个。
// 只在接口没有返回用量时用来计费，宁可略多一点也不漏算（各家分词器不同，中文通常 1 字 ≈ 0.6～1 个 token）。
func EstimateTokens(s string) int {
	cjk, other := 0, 0
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// Cached 返回命中缓存的输入 token 数（不同服务商的字段不一样，取较大的那个）。
func (u Usage) Cached() int {
	n := u.CacheHit
	if u.Details != nil && u.Details.CachedTokens > n {
		n = u.Details.CachedTokens
	}
	return n
}

type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
	// Extra 是附加到每次请求里的参数（管理员在后台给模型配置），例如 {"reasoning_effort": "low"}。
	// 不能覆盖 model、messages、tools 这些由程序决定的字段。
	Extra map[string]any
	// Thinking 是控制“思考强度”的参数，按任务类型定（见 ThinkingParams）。Extra 里有同名参数时以 Extra 为准。
	Thinking map[string]any
	// Light 是不需要推理的小调用（例如只整理格式）用的思考参数，通常是“不思考”。
	Light map[string]any
	// KeepReasoning 表示这家服务商要求把之前每一轮的思考过程原样传回去（DeepSeek 的思考模式带工具时不传会报 400）。
	KeepReasoning bool

	noThinking bool // 这次任务里接口因为思考内容对不上拒绝过请求：后面的调用都不思考
}

// reservedKeys 是请求里由程序决定、附加参数不能覆盖的字段。
var reservedKeys = map[string]bool{"model": true, "messages": true, "tools": true, "tool_choice": true, "stream": true, "n": true}

// ValidExtra 检查附加参数：要是一个 JSON 对象，不能包含 model、messages 这些字段。返回规范化后的 JSON（空输入返回空）。
func ValidExtra(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "", errors.New("附加参数要是一个 JSON 对象，例如 {\"reasoning_effort\": \"low\"}")
	}
	for k := range v {
		if reservedKeys[k] {
			return "", fmt.Errorf("附加参数不能包含 %q", k)
		}
	}
	b, _ := json.Marshal(v)
	return string(b), nil
}

func New(baseURL, apiKey, model string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		Model:   strings.TrimSpace(model),
		HTTP:    &http.Client{Timeout: 180 * time.Second},
	}
}

type Request struct {
	Messages    []Message
	Tools       []Tool
	Temperature float64
	// MaxTokens 是这一次调用最多输出多少 token（思考也算在内），0 表示不限。
	MaxTokens int
	// Thinking 不是 nil 时，这一次请求用它代替 Client.Thinking（空 map 表示不带思考参数）。
	Thinking map[string]any
	// ToolChoice 为空时是 auto。收尾那一轮用 "none"：工具定义照常带着（保住提示缓存），但不许模型再调用。
	ToolChoice string
}

// 思考强度的四档。服务商的写法各不相同，ThinkingParams 负责换成具体的请求参数。
const (
	EffortOff    = "off"    // 不思考（服务商关不掉时用最低一档）
	EffortLow    = "low"    // 少想
	EffortMedium = "medium" // 正常
	EffortHigh   = "high"   // 多想
)

// ThinkingStyles 是服务商控制思考的几种写法（后台“思考参数的写法”下拉框）。
var ThinkingStyles = []struct{ Key, Label string }{
	{"auto", "自动（按接口地址判断）"},
	{"none", "不控制（不是推理模型，或者不确定）"},
	{"effort", "reasoning_effort（OpenAI、Gemini、Grok）"},
	{"qwen", "enable_thinking + thinking_budget（通义千问 / 阿里百炼）"},
	{"deepseek", "thinking.type + reasoning_effort（DeepSeek）"},
	{"switch", "thinking.type 开 / 关（小米 MiMo、智谱 GLM、火山豆包）"},
	{"openrouter", "reasoning.effort（OpenRouter）"},
}

// ValidThinkingStyle 把表单里的值规范成 ThinkingStyles 里的一个；不认识的当作 auto。
func ValidThinkingStyle(s string) string {
	for _, x := range ThinkingStyles {
		if x.Key == s {
			return s
		}
	}
	return "auto"
}

// ValidEffort 把思考强度规范成四档之一；空或不认识的返回空（表示用这类任务的默认值）。
func ValidEffort(s string) string {
	switch s {
	case EffortOff, EffortLow, EffortMedium, EffortHigh:
		return s
	}
	return ""
}

// GuessThinkingStyle 按接口地址猜服务商控制思考的写法；认不出来返回 none（不带任何思考参数，最稳妥）。
func GuessThinkingStyle(baseURL string) string {
	u := strings.ToLower(baseURL)
	has := func(xs ...string) bool {
		for _, x := range xs {
			if strings.Contains(u, x) {
				return true
			}
		}
		return false
	}
	switch {
	case has("openrouter.ai"):
		return "openrouter"
	case has("xiaomimimo.com", "bigmodel.cn", "volces.com"):
		return "switch"
	case has("dashscope"):
		return "qwen"
	case has("deepseek.com"):
		return "deepseek"
	case has("api.openai.com", "generativelanguage.googleapis.com", "api.x.ai"):
		return "effort"
	}
	return "none"
}

// 通义千问的思考预算（thinking_budget，单位 token）：少想和正常各给一个上限，多想不设上限。
// 实测一次调用正常思考 400～2,500 token，这两个上限平时碰不到，只挡住偶尔想个没完的情况。
const (
	qwenBudgetLow    = 1024
	qwenBudgetMedium = 4096
)

// resolveStyle 把 auto / 空换成按接口地址猜出来的写法。
func resolveStyle(style, baseURL string) string {
	if style == "" || style == "auto" {
		return GuessThinkingStyle(baseURL)
	}
	return style
}

// KeepsReasoning 表示这家服务商要求把思考过程原样传回去。目前只有 DeepSeek：
// 思考模式下只要请求里带了工具，之前每一轮的 reasoning_content 都要传回去，不传就报 400。
func KeepsReasoning(style, baseURL string) bool { return resolveStyle(style, baseURL) == "deepseek" }

// ThinkingParams 把思考强度换成这家服务商的请求参数；不控制思考时返回 nil。
//   - effort（OpenAI、Gemini、Grok）、openrouter：只有强度没有开关，off 用最低一档 low。
//   - qwen：off 是关；少想、正常是开并且限制思考预算；多想是开、不限预算。
//   - deepseek：off 是关；强度只有 low / high / max 三档，默认是 high，所以少想 = low、正常 = high、多想 = max。
//   - switch（MiMo、智谱、豆包）：只有开和关，off 是关，其余都是开。
func ThinkingParams(style, baseURL, effort string) map[string]any {
	style = resolveStyle(style, baseURL)
	if effort = ValidEffort(effort); effort == "" {
		return nil
	}
	level := effort
	if level == EffortOff {
		level = EffortLow
	}
	on := map[string]any{"type": "enabled"}
	off := map[string]any{"type": "disabled"}
	switch style {
	case "effort":
		return map[string]any{"reasoning_effort": level}
	case "openrouter":
		return map[string]any{"reasoning": map[string]any{"effort": level}}
	case "switch":
		if effort == EffortOff {
			return map[string]any{"thinking": off}
		}
		return map[string]any{"thinking": on}
	case "deepseek":
		switch effort {
		case EffortOff:
			return map[string]any{"thinking": off}
		case EffortLow:
			return map[string]any{"thinking": on, "reasoning_effort": "low"}
		case EffortHigh:
			return map[string]any{"thinking": on, "reasoning_effort": "max"}
		}
		return map[string]any{"thinking": on, "reasoning_effort": "high"}
	case "qwen":
		switch effort {
		case EffortOff:
			return map[string]any{"enable_thinking": false}
		case EffortLow:
			return map[string]any{"enable_thinking": true, "thinking_budget": qwenBudgetLow}
		case EffortMedium:
			return map[string]any{"enable_thinking": true, "thinking_budget": qwenBudgetMedium}
		}
		return map[string]any{"enable_thinking": true}
	}
	return nil
}

type Response struct {
	Message      Message
	FinishReason string
	Usage        Usage
	Model        string // 实际回答的模型（经过 OpenRouter 等路由时可能和请求的不同）
}

// Chat 发送一次请求（不处理工具调用循环）。
func (c *Client) Chat(ctx context.Context, req Request) (*Response, error) {
	body := map[string]any{
		"model":    c.Model,
		"messages": req.Messages,
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
		body["tool_choice"] = "auto"
		if req.ToolChoice != "" {
			body["tool_choice"] = req.ToolChoice
		}
	}
	thinking := c.Thinking
	if req.Thinking != nil {
		thinking = req.Thinking
	}
	if c.noThinking {
		thinking = c.Light
	}
	for k, v := range thinking {
		if !reservedKeys[k] {
			body[k] = v
		}
	}
	for k, v := range c.Extra {
		if !reservedKeys[k] {
			body[k] = v
		}
	}
	// 输出上限只是防失控的护栏。管理员在附加参数里自己写了上限的话，以他写的为准。
	limitKey := ""
	if req.MaxTokens > 0 && body["max_tokens"] == nil && body["max_completion_tokens"] == nil {
		limitKey = "max_tokens"
		body[limitKey] = req.MaxTokens
	}
	raw, _ := json.Marshal(body)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*3) * time.Second):
			}
		}
		hreq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		hreq.Header.Set("Content-Type", "application/json")
		hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
		resp, err := c.HTTP.Do(hreq)
		if err != nil {
			lastErr = fmt.Errorf("连接 AI 接口失败: %w", err)
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = apiErr(resp.StatusCode, data)
			continue
		}
		if resp.StatusCode == 400 && body["temperature"] != nil && bytes.Contains(bytes.ToLower(data), []byte("temperature")) {
			// 有些推理模型（如 OpenAI o 系列）不接受 temperature，去掉后重试
			delete(body, "temperature")
			raw, _ = json.Marshal(body)
			attempt--
			continue
		}
		if resp.StatusCode == 400 && body["tool_choice"] != nil && body["tool_choice"] != "auto" && bytes.Contains(bytes.ToLower(data), []byte("tool_choice")) {
			// 有的接口只认 auto：退回 auto，由调用方处理模型还要调工具的情况
			body["tool_choice"] = "auto"
			raw, _ = json.Marshal(body)
			attempt--
			continue
		}
		if resp.StatusCode == 400 && !c.noThinking && len(thinking) > 0 && bytes.Contains(bytes.ToLower(data), []byte("reasoning_content")) {
			// 接口要求带上之前每一轮的思考过程，而对话里有几轮没有（例如更早的问答只存了回答）：
			// 这次任务后面的调用都不思考，不思考就没有这个要求。比整个任务失败强。
			c.noThinking = true
			for k := range thinking {
				delete(body, k)
			}
			for k, v := range c.Light {
				if !reservedKeys[k] {
					body[k] = v
				}
			}
			for k, v := range c.Extra {
				if !reservedKeys[k] {
					body[k] = v
				}
			}
			raw, _ = json.Marshal(body)
			attempt--
			continue
		}
		if resp.StatusCode == 400 && limitKey != "" && bytes.Contains(bytes.ToLower(data), []byte("max_tokens")) {
			// 新的推理模型只认 max_completion_tokens；换了还不行就不带上限
			delete(body, limitKey)
			if limitKey == "max_tokens" {
				limitKey = "max_completion_tokens"
				body[limitKey] = req.MaxTokens
			} else {
				limitKey = ""
			}
			raw, _ = json.Marshal(body)
			attempt--
			continue
		}
		if resp.StatusCode >= 300 {
			return nil, apiErr(resp.StatusCode, data)
		}
		var out struct {
			Model   string `json:"model"`
			Choices []struct {
				Message      Message `json:"message"`
				FinishReason string  `json:"finish_reason"`
				// 推理模型的思考过程（DeepSeek 叫 reasoning_content，OpenRouter 叫 reasoning），只用来估算用量
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
			} `json:"choices"`
			Usage Usage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		// 网关或免费模型偶尔返回空内容、半截 JSON，或者状态码 200 但正文是错误：都当作临时故障重试
		if err := json.Unmarshal(data, &out); err != nil {
			lastErr = fmt.Errorf("AI 接口返回的内容不完整（%s），已重试 3 次", describeBody(data))
			continue
		}
		if out.Error != nil && out.Error.Message != "" {
			lastErr = fmt.Errorf("AI 接口出错：%s", out.Error.Message)
			continue
		}
		if len(out.Choices) == 0 {
			lastErr = errors.New("AI 接口没有返回结果")
			continue
		}
		ch := out.Choices[0]
		m := ch.Message
		u := out.Usage
		if u.PromptTokens == 0 && u.CompletionTokens == 0 {
			// 有的网关、自建服务不返回 usage：按发出去和收回来的文字长度估算，不能当作 0 不计费
			gen := m.Content + m.ReasoningContent + ch.ReasoningContent + ch.Reasoning
			for _, tc := range m.ToolCalls {
				gen += tc.Function.Name + tc.Function.Arguments
			}
			u = Usage{PromptTokens: EstimateTokens(string(raw)), CompletionTokens: EstimateTokens(gen), Estimated: true}
		}
		m.Content = StripThinking(m.Content)
		if !c.KeepReasoning {
			m.ReasoningContent = ""
		}
		return &Response{Message: m, FinishReason: ch.FinishReason, Usage: u, Model: out.Model}, nil
	}
	return nil, lastErr
}

// StripThinking 去掉部分推理模型写在正文里的 <think>…</think> 思考过程。
func StripThinking(s string) string {
	for {
		i := strings.Index(s, "<think>")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "</think>")
		if j < 0 {
			s = s[:i] // 思考没写完就被截断了
			break
		}
		s = s[:i] + s[i+j+len("</think>"):]
	}
	// 有的服务只返回结尾的 </think>
	if j := strings.Index(s, "</think>"); j >= 0 {
		s = s[j+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

func describeBody(data []byte) string {
	t := strings.TrimSpace(string(data))
	if t == "" {
		return "空响应"
	}
	if len(t) > 120 {
		t = t[:120] + "…"
	}
	return t
}

func apiErr(status int, data []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	switch status {
	case 401, 403:
		return fmt.Errorf("AI 接口拒绝访问（%d）：API Key 可能不对。%s", status, msg)
	case 404:
		return fmt.Errorf("AI 接口地址或模型名不对（404）：%s", msg)
	case 429:
		return fmt.Errorf("AI 接口限流或额度用完（429）：%s", msg)
	}
	return fmt.Errorf("AI 接口错误 %d：%s", status, msg)
}

// ToolFunc 执行一次工具调用，返回给模型看的文本，以及给用户看的一行说明。
type ToolFunc func(ctx context.Context, args json.RawMessage) (result string, trace string, err error)

type Agent struct {
	Client   *Client
	Tools    []Tool
	Handlers map[string]ToolFunc
	MaxSteps int
	// MaxCalls 是整个任务最多执行几次工具调用（0 表示不限）。有的模型一轮会并发调好几个工具，只限轮数挡不住。
	MaxCalls int
	// MaxOutput 是每一次调用最多输出多少 token（思考也算在内），0 表示不限。只用来挡住偶尔失控的输出。
	MaxOutput int
	// OnTool 在每次工具调用前后回调，用来实时显示“AI 正在查什么”。
	OnTool func(name, trace string, done bool)
	// Budget 收到目前为止的累计用量，返回 true 表示快到用户付得起的上限了：
	// 该收尾了，让模型用已经查到的数据直接给出最终回答，免得余额被扣成很大的负数。
	Budget func(Usage) bool
}

type RunResult struct {
	Content string
	Trace   []string
	Usage   Usage
	// Messages 是这次任务里新增的工具往来（模型发起调用的那几条消息和工具结果），原样保留。
	// 问答把它存下来，下一个问题接着用：不用重查，而且和上一轮发出去的内容一字不差，能命中提示缓存。
	Messages []Message
}

// ErrContentFilter 表示服务商的内容安全审核拦截了请求（finish_reason 是 content_filter），重试一次还是被拦。
// 这时返回的“回答”只是一句拒绝提示，不能当成分析结果；但前面查数据用掉的 token 是实打实花了的，调用方照常计费。
var ErrContentFilter = errors.New("AI 服务商的内容安全审核拦截了这次请求（网站的页面标题、搜索词里可能有它认为敏感的内容），没有拿到分析结果。已经用掉的 token 照常计费。可以再试一次，或者在设置里换一个模型")

const (
	wrapUpMsg   = "数据已经足够了，请不要再调用工具，直接给出最终回答。"
	notRunMsg   = "没有执行：这次分析的查询次数已经用完。请根据已经查到的数据直接给出结论。"
	repeatedMsg = "这个查询前面已经查过，参数完全一样，结果就在上面的对话里。直接用那份结果，不要重复查。"
)

// Run 执行“模型 → 工具 → 模型”的循环，直到模型给出最终回答。
func (a *Agent) Run(ctx context.Context, msgs []Message) (*RunResult, error) {
	res := &RunResult{}
	start := len(msgs)
	empties := 0
	calls := 0
	max := a.MaxSteps
	if max <= 0 {
		max = 10
	}
	// 收尾分两步。先让模型别再查（wrapped）：加一句话，并且把 tool_choice 设成 none，工具定义照常带着——
	// 工具定义排在提示的前面，一去掉，整段对话就和前几轮对不上，提示缓存全部失效，而收尾这一轮恰好是最长的。
	// 接口不认 none、模型还要查，下一轮才真的不给工具（forced）。
	wrapped, forced := false, false
	filtered := false         // 被内容审核拦过一次了
	lastPrompt := 0           // 上一次调用的输入 token 数（接口返回的）
	cachedBefore := false     // 这次任务里有没有命中过提示缓存
	seen := map[string]bool{} // 已经成功执行过的查询（工具名 + 参数）
	for step := 0; step < max; step++ {
		last := step == max-1 || forced
		if !wrapped && (last || (step > 0 && (step == max-2 || (a.Budget != nil && a.Budget(res.Usage))))) {
			msgs = append(msgs, Message{Role: "user", Content: wrapUpMsg})
			wrapped = true
		}
		req := Request{Messages: msgs, Tools: a.Tools, Temperature: 0.3, MaxTokens: a.MaxOutput}
		switch {
		case last:
			req.Tools = nil
		case wrapped:
			req.ToolChoice = "none" // 大多数接口认它（通义千问、DeepSeek、OpenAI）；不认的（MiMo 会忽略）靠下面的 forced 兜底
		}
		resp, err := a.Client.Chat(ctx, req)
		if err != nil {
			return res, err
		}
		if u := &resp.Usage; u.Estimated && cachedBefore {
			// 这次调用接口没给用量（例如被内容审核拦截），输入是按字数估的。但它的开头和上一次调用发出去的一字不差，
			// 而这家接口的提示缓存是生效的：那一段按缓存价算，不能整段都按原价。
			u.CacheHit = min(lastPrompt, u.PromptTokens)
		} else if !u.Estimated {
			lastPrompt, cachedBefore = u.PromptTokens, cachedBefore || u.Cached() > 0
		}
		res.Usage.Add(resp.Usage)
		m := resp.Message
		if resp.FinishReason == "content_filter" && len(m.ToolCalls) == 0 {
			// 审核不是每次都拦同样的内容：原样再发一次常常就过了，比整个任务作废划算
			if !filtered {
				filtered = true
				step--
				continue
			}
			return res, ErrContentFilter
		}
		if len(m.ToolCalls) == 0 {
			res.Content = strings.TrimSpace(m.Content)
			if res.Content == "" {
				// 推理模型偶尔只想不说，或者输出被截断：先原样再问一次，再不行就明确要求给出最终回答
				if empties < 2 {
					empties++
					if empties == 2 {
						msgs = append(msgs, Message{Role: "user", Content: "请根据已经查到的数据，直接按要求的格式给出最终回答。"})
					}
					step--
					continue
				}
				who := resp.Model
				if who == "" {
					who = a.Client.Model
				}
				if resp.FinishReason == "length" {
					return res, fmt.Errorf("AI 的回答被截断了（模型 %s 输出长度超出限制），可以换一个模型再试", who)
				}
				return res, fmt.Errorf("AI 连续 3 次没有给出回答（模型 %s，结束原因 %s），可以稍后再试或换一个模型", who, resp.FinishReason)
			}
			for _, x := range msgs[start:] {
				if x.Role != "user" { // 催模型收尾的那几句不算工具往来
					res.Messages = append(res.Messages, x)
				}
			}
			if m.ReasoningContent != "" {
				// 要回传思考过程的服务商：最终回答的那一段思考也得留着，下一个问题要原样带上
				m.Role = "assistant"
				res.Messages = append(res.Messages, m)
			}
			return res, nil
		}
		if m.Role == "" {
			m.Role = "assistant"
		}
		msgs = append(msgs, m)
		for _, tc := range m.ToolCalls {
			// 每个调用都要回一条 tool 消息（哪怕没执行），否则接口会报错
			reply := func(content string) {
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: content})
			}
			if wrapped {
				reply(notRunMsg)
				forced = true
				continue
			}
			key := tc.Function.Name + "\x00" + strings.TrimSpace(tc.Function.Arguments)
			if seen[key] {
				reply(repeatedMsg)
				continue
			}
			if a.MaxCalls > 0 && calls >= a.MaxCalls {
				reply(notRunMsg)
				continue
			}
			calls++
			h, ok := a.Handlers[tc.Function.Name]
			var out, trace string
			if !ok {
				out = "错误：没有这个工具"
				trace = tc.Function.Name + "（未知工具）"
			} else {
				args := json.RawMessage(tc.Function.Arguments)
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				if a.OnTool != nil {
					a.OnTool(tc.Function.Name, describeCall(tc.Function.Name, args), false)
				}
				var err error
				out, trace, err = h(ctx, args)
				if err != nil {
					out = "错误：" + err.Error()
					if trace == "" {
						trace = tc.Function.Name
					}
					trace += "  → 失败：" + err.Error()
				} else {
					seen[key] = true
				}
				if a.OnTool != nil {
					a.OnTool(tc.Function.Name, trace, true)
				}
			}
			res.Trace = append(res.Trace, trace)
			reply(clip(out, maxToolResult))
		}
	}
	return res, errors.New("AI 调用工具的次数太多，已停止")
}

// maxToolResult 是单次工具结果最多给模型看的字数。结果会留在对话里、后面每一轮都要重发，太长就很费 token。
const maxToolResult = 8000

// clip 按字符（不是字节）截断，避免把一个汉字切成两半。
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…（结果太长，已截断。需要的话缩小日期范围或加过滤条件再查）"
}

func describeCall(name string, args json.RawMessage) string {
	s := string(args)
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return name + " " + s
}

// ExtractJSON 从模型回答里取出 JSON（兼容 ```json 代码块和前后多余文字）。
func ExtractJSON(s string, v any) error {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		if j := strings.Index(rest, "```"); j >= 0 {
			s = strings.TrimSpace(rest[:j])
		}
	}
	start := strings.IndexAny(s, "{[")
	end := strings.LastIndexAny(s, "}]")
	if start < 0 || end <= start {
		return errors.New("AI 的回答里没有找到 JSON")
	}
	return json.Unmarshal([]byte(s[start:end+1]), v)
}
