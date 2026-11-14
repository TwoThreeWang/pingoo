package store

import "testing"

func TestModelCost(t *testing.T) {
	m := AIModel{PriceIn: 1, PriceOut: 2, PriceCache: 0.5, Multiplier: 1}
	cases := []struct {
		in, out, cached int
		want            int64
	}{
		{100000, 20000, 0, 14},    // 0.14 元：浮点误差不能让它变成 15
		{20100, 0, 0, 2},          // 2.01 积分：四舍五入是 2，不再进成 3
		{27900, 0, 0, 3},          // 2.79 积分
		{14500, 0, 0, 1},          // 1.45 积分
		{15000, 0, 0, 2},          // 正好 1.5 积分往上
		{4000, 0, 0, 1},           // 0.4 积分：有用量至少 1 积分
		{10000, 5000, 0, 2},       // 0.02 元
		{1, 0, 0, 1},              // 有用量至少 1 积分
		{0, 0, 0, 0},              // 没用量不扣
		{1000000, 0, 1000000, 50}, // 全部命中缓存：0.5 元
		{1000000, 0, 2000000, 50}, // 缓存数大于输入数时按输入数算
	}
	for _, c := range cases {
		if got := m.Cost(c.in, c.out, c.cached); got != c.want {
			t.Errorf("Cost(%d,%d,%d) = %d, want %d", c.in, c.out, c.cached, got, c.want)
		}
	}
	m.Multiplier = 1.5
	if got := m.Cost(100000, 20000, 0); got != 21 {
		t.Errorf("倍率 1.5：0.14×1.5×100 = 21，得到 %d", got)
	}
	if h := m.Hold("chat"); h < m.Cost(100000, 20000, 0) {
		t.Errorf("预扣额应该不小于一次正常问答的用量: %d", h)
	}
}

// 各类任务的思考强度：没设的用默认值，设了的用设的；再换成这家服务商的参数。
func TestModelEffort(t *testing.T) {
	m := AIModel{BaseURL: "https://api.xiaomimimo.com/v1", ThinkingStyle: "auto"}
	if m.Effort("insight") != "off" || m.Effort("chat") != "low" || m.Effort("diagnose") != "medium" || m.Effort("report") != "medium" || m.Effort("repair") != "off" {
		t.Errorf("默认值不对: %v %v %v %v", m.Effort("insight"), m.Effort("chat"), m.Effort("diagnose"), m.Effort("report"))
	}
	th, _ := m.Thinking("insight")["thinking"].(map[string]any)
	if th["type"] != "disabled" {
		t.Errorf("MiMo 的洞察应该关掉思考: %v", m.Thinking("insight"))
	}
	th, _ = m.Thinking("diagnose")["thinking"].(map[string]any)
	if th["type"] != "enabled" {
		t.Errorf("MiMo 的诊断应该开着思考: %v", m.Thinking("diagnose"))
	}
	m.TaskEffort = `{"insight":"high","chat":"乱写"}`
	if m.Effort("insight") != "high" || m.Effort("chat") != "low" {
		t.Errorf("设了的用设的，不认识的值用默认: %v %v", m.Effort("insight"), m.Effort("chat"))
	}
	if (AIModel{BaseURL: "https://example.com/v1", ThinkingStyle: "auto"}).Thinking("insight") != nil {
		t.Error("认不出来的服务商不应该带思考参数")
	}
}
