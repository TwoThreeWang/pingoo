package signals

import "testing"

func TestLevels(t *testing.T) {
	high, normal, low := detection{factor: floorFactor(LevelHigh)}, detection{factor: floorFactor(LevelNormal)}, detection{factor: floorFactor(LevelLow)}
	// 页面飙升的标准门槛是 7 天 150 次浏览
	if !high.atLeast(60, 150) || high.atLeast(59, 150) {
		t.Error("高灵敏度：60 次就够")
	}
	if !normal.atLeast(150, 150) || normal.atLeast(149, 150) {
		t.Error("标准：150 次")
	}
	if !low.atLeast(375, 150) || low.atLeast(374, 150) {
		t.Error("低灵敏度：要 375 次")
	}
	if floorFactor("") != 1 || floorFactor("乱写的") != 1 {
		t.Error("没设置或不认识的值按标准算")
	}
	if !ValidLevel("low") || !ValidLevel("normal") || !ValidLevel("high") || ValidLevel("") || ValidLevel("max") {
		t.Error("ValidLevel")
	}
	seen := map[string]bool{}
	for _, k := range Kinds {
		if k.Key == "" || k.Label == "" || seen[k.Key] {
			t.Errorf("提醒类型有问题: %+v", k)
		}
		seen[k.Key] = true
	}
}
