package services

import (
	"log"
	"pingoo/database"
	"pingoo/models"
	"time"
)

// Scheduler 定时任务调度器
type Scheduler struct {
	// 数据保留天数
	RetentionDays int
}

// NewScheduler 创建调度器实例
func NewScheduler(retentionDays int) *Scheduler {
	return &Scheduler{
		RetentionDays: retentionDays,
	}
}

// Start 启动定时任务
func (s *Scheduler) Start() {
	go s.runCleanupScheduler()
	log.Printf("[定时任务] 数据清理调度器已启动，保留最近 %d 天数据", s.RetentionDays)
}

// runCleanupScheduler 运行清理调度器
func (s *Scheduler) runCleanupScheduler() {
	// 计算距离下一个凌晨 2:00 的时间
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
	if now.After(next) {
		next = next.Add(24 * time.Hour)
	}
	duration := next.Sub(now)

	log.Printf("[定时任务] 下次清理时间: %s (距现在 %.1f 小时)", next.Format("2006-01-02 15:04:05"), duration.Hours())

	// 等待到下一个执行时间
	timer := time.NewTimer(duration)
	<-timer.C

	// 执行首次清理
	s.CleanupOldData()

	// 之后每24小时执行一次
	ticker := time.NewTicker(24 * time.Hour)
	for range ticker.C {
		s.CleanupOldData()
	}
}

// CleanupOldData 清理过期数据（硬删除）
func (s *Scheduler) CleanupOldData() {
	log.Println("[定时任务] 开始清理过期数据...")
	startTime := time.Now()

	db := database.GetDB()
	cutoffDate := time.Now().AddDate(0, 0, -s.RetentionDays)

	// 硬删除 events 表（使用 Unscoped 跳过软删除）
	eventsResult := db.Unscoped().Where("created_at < ?", cutoffDate).Delete(&models.Event{})
	if eventsResult.Error != nil {
		log.Printf("[定时任务] 清理 events 表失败: %v", eventsResult.Error)
	} else {
		log.Printf("[定时任务] 已清理 events 表 %d 条记录", eventsResult.RowsAffected)
	}

	// 硬删除 sessions 表
	sessionsResult := db.Unscoped().Where("created_at < ?", cutoffDate).Delete(&models.Session{})
	if sessionsResult.Error != nil {
		log.Printf("[定时任务] 清理 sessions 表失败: %v", sessionsResult.Error)
	} else {
		log.Printf("[定时任务] 已清理 sessions 表 %d 条记录", sessionsResult.RowsAffected)
	}

	// 硬删除 daily_stats 表
	dailyStatsResult := db.Unscoped().Where("date < ?", cutoffDate).Delete(&models.DailyStats{})
	if dailyStatsResult.Error != nil {
		log.Printf("[定时任务] 清理 daily_stats 表失败: %v", dailyStatsResult.Error)
	} else {
		log.Printf("[定时任务] 已清理 daily_stats 表 %d 条记录", dailyStatsResult.RowsAffected)
	}

	elapsed := time.Since(startTime)
	log.Printf("[定时任务] 数据清理完成，耗时: %v", elapsed)
}
