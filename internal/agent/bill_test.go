package agent

import (
	"testing"

	"pingoo/internal/llm"
	"pingoo/internal/store"
)

// 提前收尾只看用户付不付得起：余额充足时不打断分析，免费模型永远不打断。
func TestBillNear(t *testing.T) {
	paid := store.AIModel{PriceIn: 1, PriceOut: 2, Multiplier: 1} // 输入 1 元 / 百万 = 100 积分 / 百万
	low := &bill{m: paid, budget: 20}
	if low.near(llm.Usage{PromptTokens: 100000}) { // 10 积分，还不到 20 的八成
		t.Error("10 / 20 不该收尾")
	}
	if !low.near(llm.Usage{PromptTokens: 160000}) { // 16 积分 = 八成
		t.Error("用到付得起的八成时应该收尾")
	}
	rich := &bill{m: paid, budget: 100000}
	if rich.near(llm.Usage{PromptTokens: 2000000, CompletionTokens: 500000}) { // 300 积分，远没到 10 万
		t.Error("余额充足时不应该提前收尾")
	}
	free := &bill{m: store.AIModel{Multiplier: 1}, budget: 1}
	if free.near(llm.Usage{PromptTokens: 10000000, CompletionTokens: 1000000}) {
		t.Error("免费模型（价格 0）不应该提前收尾")
	}
	var none *bill
	if none.near(llm.Usage{PromptTokens: 1}) {
		t.Error("没有计费时不收尾")
	}
}
