package web

import (
	"testing"
	"time"
)

// 验证界面时间统一按后台设置的时区展示：数据库里的 UTC 时间戳，展示时转成设置的时区。
func TestDisplayTZ(t *testing.T) {
	saved := appLoc.Load()
	t.Cleanup(func() { appLoc.Store(saved) })

	// 2024-01-02 21:07:00 UTC：北京时间是次日 05:07。
	utc := time.Date(2024, 1, 2, 21, 7, 0, 0, time.UTC)

	SetDisplayTZ("Asia/Shanghai")
	cases := []struct{ name, got, want string }{
		{"tsDateTime", tsDateTime(utc), "2024-01-03 05:07"},
		{"tsShort", tsShort(utc), "01-03 05:07"},
		{"tsDate", tsDate(utc), "2024-01-03"},
		{"tsMD", tsMD(utc), "01-03"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("上海时区 %s = %q，想要 %q", c.name, c.got, c.want)
		}
	}

	// 换一个时区（美国西海岸，UTC-8）：同一时刻展示成另一个日期和时间。
	SetDisplayTZ("America/Los_Angeles")
	if got, want := tsDateTime(utc), "2024-01-02 13:07"; got != want {
		t.Errorf("洛杉矶时区 tsDateTime = %q，想要 %q", got, want)
	}

	// 无效时区名保持原样，不改变当前展示时区。
	SetDisplayTZ("Not/AZone")
	if got, want := tsDate(utc), "2024-01-02"; got != want {
		t.Errorf("无效时区后 tsDate = %q，想要 %q（应仍是洛杉矶）", got, want)
	}

	// 零值时间显示占位符，不显示 0001 年。
	if got := tsDateTime(time.Time{}); got != "—" {
		t.Errorf("零值 tsDateTime = %q，想要 —", got)
	}
}
