package utils

import (
	"fmt"
	"strconv"
	"time"
)

// ParseDate 解析日期字符串，支持多种格式
func ParseDate(dateStr string) (time.Time, error) {
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("日期字符串不能为空")
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
			return t, nil
		}
	}

	// 如果以上格式都不匹配，尝试解析时间戳
	if timestamp, err := strconv.ParseInt(dateStr, 10, 64); err == nil {
		return time.Unix(timestamp, 0), nil
	}

	return time.Time{}, fmt.Errorf("无法解析日期格式 %s", dateStr)
}
