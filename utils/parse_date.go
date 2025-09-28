package utils

import (
	"fmt"
	"strconv"
	"time"
)

// ParseDate 解析日期字符串，支持多种格式
func ParseDate(dateStr string) (time.Time, error) {
	// 当前日期的00:00:00时间
	now := time.Now()
	now = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if dateStr == "" {
		return now, fmt.Errorf("日期字符串不能为空")
	}
	// 先截取dateStr，精确到秒级别
	if len(dateStr) > 18 {
		dateStr = dateStr[:19]
	}

	// 尝试常见日期格式
	formats := []string{
		"20060102",
		"2006-01-02",
		"2006/01/02",
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			// 将时分秒固定为00:00:00
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()), nil
		}
	}

	// 如果以上格式都不匹配，尝试解析时间戳
	if timestamp, err := strconv.ParseInt(dateStr, 10, 64); err == nil {
		// 将时分秒固定为00:00:00
		t := time.Unix(timestamp, 0)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()), nil
	}

	return now, fmt.Errorf("无法解析日期格式 %s", dateStr)
}
