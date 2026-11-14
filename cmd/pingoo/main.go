// Pingoo：把 Google Analytics 和 Search Console 的数据聚合起来，用 AI 讲清楚发生了什么。
//
//	pingoo          启动网站（默认）
//	pingoo migrate  只执行数据库迁移
//	pingoo demo     写入 6 个演示网站的模拟数据（加 --reset 删除后重新生成，--remove 只删除）
//	pingoo compact  整理同步数据表的空间（VACUUM FULL）：把删除、重新同步留下的空位还给磁盘
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pingoo/internal/agent"
	"pingoo/internal/config"
	"pingoo/internal/db"
	"pingoo/internal/demo"
	"pingoo/internal/google"
	"pingoo/internal/mail"
	"pingoo/internal/metrics"
	"pingoo/internal/pow"
	"pingoo/internal/scheduler"
	"pingoo/internal/secret"
	"pingoo/internal/signals"
	"pingoo/internal/store"
	"pingoo/internal/syncer"
	"pingoo/internal/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("启动失败", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	n, err := db.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Info("数据库迁移完成", "applied", n)
	}

	st := store.New(pool, secret.New(cfg.AppSecret))
	if err := st.EnsureAdmin(ctx, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		return fmt.Errorf("建管理员账号失败：%w", err)
	}
	google.QuotaHook = func(property string, q google.Quota) {
		go st.SaveQuota(context.Background(), property, q.TokensPerDay.Consumed, q.TokensPerDay.Remaining, q.TokensPerHour.Consumed, q.TokensPerHour.Remaining)
	}
	sy := syncer.New(st, log)
	m := metrics.New(st, sy, log)
	ag := agent.New(st, m, sy, log)
	det := &signals.Detector{M: m}
	mailer := &mail.Mailer{DryRun: cfg.MailDryRun, Store: st, Log: log, MX: &mail.MX{}}
	sched := &scheduler.Scheduler{Store: st, Sync: sy, Detector: det, Agent: ag, Log: log, Mail: mailer}

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "migrate":
		fmt.Println("数据库迁移完成")
		return nil
	case "demo":
		seed := demo.Seed
		if len(os.Args) > 2 && os.Args[2] == "--remove" {
			n, err := demo.Remove(ctx, st)
			if err != nil {
				return err
			}
			fmt.Printf("已删除 %d 个演示网站\n", n)
			return nil
		}
		if len(os.Args) > 2 && os.Args[2] == "--reset" {
			seed = demo.Reset
		}
		if err := seed(ctx, st, det); err != nil {
			return err
		}
		fmt.Println("已写入演示数据")
		return nil
	case "compact":
		fmt.Println("正在整理同步数据表（每张表整理时会锁住几秒到几十秒，建议在访问少的时候运行）……")
		for _, r := range st.Compact(ctx, -1) {
			fmt.Printf("  %-18s %7.1f MB → %7.1f MB\n", r.Table, float64(r.Before)/1e6, float64(r.After)/1e6)
		}
		return nil
	case "", "serve":
	default:
		return fmt.Errorf("未知命令 %q（可用：serve / migrate / demo / compact）", cmd)
	}

	if cfg.MailDryRun {
		log.Warn("MAIL_DRYRUN=true：邮件不会真的发出，只写进日志（只用于本地开发）")
	} else if !mailer.Config(ctx).Enabled() {
		log.Warn("还没有配置发信服务，注册和找回密码发不了邮件：到「管理后台 → 设置 → 发信」里填写")
	}
	srv := &web.Server{Cfg: cfg, Store: st, M: m, Agent: ag, Sync: sy, Sched: sched, Detector: det, Log: log, Mail: mailer,
		// 人机校验的签名密钥从 APP_SECRET 派生：服务重启后，重启前打开的注册、登录页面照样能提交
		Pow: pow.NewWithKey([]byte(st.Box.Sign("pingoo-pow-key")))}
	h, err := srv.Handler()
	if err != nil {
		return err
	}
	if n := st.MarkInterrupted(ctx); n > 0 {
		log.Info("上次退出时有任务没做完，已标记为中断", "count", n)
	}
	if n := st.RefundPending(ctx); n > 0 {
		log.Info("上次退出时有 AI 任务没结算，预扣的积分已全额退回", "count", n)
	}
	go sched.Run(ctx)

	// 读超时防止慢速连接长期占着不放；写超时要留够“测试 AI 连接”这类最长 90 秒的请求
	hs := &http.Server{Addr: cfg.ListenAddr, Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 150 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("Pingoo 已启动", "addr", cfg.ListenAddr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
