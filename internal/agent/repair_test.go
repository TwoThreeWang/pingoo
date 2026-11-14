package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"pingoo/internal/llm"
	"pingoo/internal/store"
)

// fakeAI 依次返回 replies 里的回答，并记下每次请求的内容。
func fakeAI(t *testing.T, replies ...string) (*llm.Client, *atomic.Int32, *[]string) {
	t.Helper()
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var all []string
		for _, m := range req.Messages {
			all = append(all, m.Content)
		}
		bodies = append(bodies, strings.Join(all, "\n"))
		i := int(calls.Add(1)) - 1
		if i >= len(replies) {
			i = len(replies) - 1
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": replies[i]}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 20},
		})
	}))
	t.Cleanup(srv.Close)
	return llm.New(srv.URL, "k", "fake"), &calls, &bodies
}

func TestParseOrRepair(t *testing.T) {
	valid := func(x InsightContent) bool { return x.Headline != "" }
	const format = `{"headline":"一句话标题","lede":"两句话"}`
	ctx := context.Background()

	// 第一次就是合法的 JSON：不多调用
	c, calls, _ := fakeAI(t, "不应该被调用")
	res := &llm.RunResult{Content: "```json\n{\"headline\":\"访客涨了\",\"lede\":\"原因是搜索。\"}\n```"}
	if got, ok := parseOrRepair(ctx, c, res, format, valid); !ok || got.Headline != "访客涨了" || calls.Load() != 0 {
		t.Fatalf("合法的回答应该直接解析: ok=%v got=%+v calls=%d", ok, got, calls.Load())
	}

	// 模型用文字回答：让它整理一次，把原来的回答和格式要求都带上，token 也要记上
	c, calls, bodies := fakeAI(t, `{"headline":"访客涨了 12%","lede":"主要来自搜索。"}`)
	res = &llm.RunResult{Content: "这段时间访客涨了 12%，主要来自搜索。", Usage: llm.Usage{PromptTokens: 1000, CompletionTokens: 50}}
	got, ok := parseOrRepair(ctx, c, res, format, valid)
	if !ok || got.Headline != "访客涨了 12%" || calls.Load() != 1 {
		t.Fatalf("文字回答应该被整理成 JSON: ok=%v got=%+v calls=%d", ok, got, calls.Load())
	}
	if b := (*bodies)[0]; !strings.Contains(b, "访客涨了 12%，主要来自搜索。") || !strings.Contains(b, format) {
		t.Errorf("整理请求里应该带上原来的回答和格式要求: %s", b)
	}
	if res.Usage.PromptTokens != 1100 || res.Usage.CompletionTokens != 70 {
		t.Errorf("整理用掉的 token 应该加进用量: %+v", res.Usage)
	}

	// JSON 合法但缺关键字段，也要整理
	c, calls, _ = fakeAI(t, `{"headline":"补上了","lede":"x"}`)
	if got, ok := parseOrRepair(ctx, c, &llm.RunResult{Content: `{"lede":"只有导语"}`}, format, valid); !ok || got.Headline != "补上了" || calls.Load() != 1 {
		t.Fatalf("缺关键字段时应该整理: ok=%v got=%+v", ok, got)
	}

	// 整理之后还是不对：只试一次就放弃，返回空值
	c, calls, _ = fakeAI(t, "抱歉，我还是没法输出 JSON。")
	if got, ok := parseOrRepair(ctx, c, &llm.RunResult{Content: "文字回答"}, format, valid); ok || got.Headline != "" || calls.Load() != 1 {
		t.Fatalf("整理失败应该返回 false，且只多调用一次: ok=%v got=%+v calls=%d", ok, got, calls.Load())
	}
}

func TestRealSites(t *testing.T) {
	demo1, demo2 := store.Site{ID: 1, Demo: true}, store.Site{ID: 2, Demo: true}
	real1 := store.Site{ID: 3}
	if got := RealSites([]store.Site{demo1, real1, demo2}); len(got) != 1 || got[0].ID != 3 {
		t.Errorf("有真实网站时应该去掉演示网站: %+v", got)
	}
	if got := RealSites([]store.Site{demo1, demo2}); len(got) != 2 {
		t.Errorf("只有演示网站时应该原样返回: %+v", got)
	}
}
