package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ========== 套餐 ==========
//
// 三档：免费版、基础版、专业版。区别在网站数、数据保留多久、每月送多少积分、同步频率和邮件简报；
// AI 都按积分原价收费。付费套餐用“套餐卡密”开通或续期，记在 users.plan / plan_expires_at 上
// （以后接在线支付，回调里改这两个字段即可）。各档的数值放在后台设置里（KeyPlans），这里是默认值。

const (
	PlanFree  = "free"
	PlanBasic = "basic"
	PlanPro   = "pro"

	// GraceDays：付费套餐到期后，数据仍按原套餐保留这么多天（续费就什么都不丢）；功能当天就回到免费版。
	GraceDays = 7
	// PlanCycleDays：套餐每月积分的发放周期。
	PlanCycleDays = 30
	// maxTotalDays：Search Console 只能查最近 16 个月，每日总数最多保留这么久。
	maxTotalDays = 480
)

// PlanIDs 按从低到高排列。
var PlanIDs = []string{PlanFree, PlanBasic, PlanPro}

func planRank(id string) int {
	for i, p := range PlanIDs {
		if p == id {
			return i
		}
	}
	return 0
}

type Plan struct {
	ID             string  `json:"-"`
	Name           string  `json:"name"`
	Price          float64 `json:"price"` // 每月价格：展示用，升级时也用它折算剩余天数；0 表示不显示价格
	Sites          int     `json:"sites"`
	DetailDays     int     `json:"detail_days"`     // 页面、来源、搜索词等明细保留几天
	TotalDays      int     `json:"total_days"`      // 每日总数保留几天
	MonthlyCredits int64   `json:"monthly_credits"` // 每 30 天送多少积分
	BuyURL         string  `json:"buy_url"`         // 这一档的购买地址（发卡网站、店铺或支付页）；空表示还没有。免费版不用
	// 下面两项是每档固定的功能，不在后台改
	SyncHours int  `json:"-"` // 每几个小时再刷新一次（只重拉有变化的日子，见 syncer）；0 表示每天一次
	Digest    bool `json:"-"` // 每天早上的邮件简报
}

// Paid 表示付费套餐。
func (p Plan) Paid() bool { return p.ID != PlanFree }

// CanYoY 表示每日总数保留得够不够和去年同期比（rangeDays 是所选范围的天数）。
func (p Plan) CanYoY(rangeDays int) bool { return p.TotalDays >= 365+rangeDays }

type PlanConfig struct {
	Currency string          `json:"currency"` // 价格前面的符号，例如 $、¥
	Plans    map[string]Plan `json:"plans"`
}

// Get 返回某一档（不认识的按免费版）。
func (c PlanConfig) Get(id string) Plan {
	p, ok := c.Plans[id]
	if !ok {
		p = c.Plans[PlanFree]
	}
	return p
}

// List 按从低到高返回三档。
func (c PlanConfig) List() []Plan {
	out := make([]Plan, 0, len(PlanIDs))
	for _, id := range PlanIDs {
		out = append(out, c.Get(id))
	}
	return out
}

// MaxDays 返回所有套餐里最长的明细、每日总数保留天数（全局性的数据按它清理）。
func (c PlanConfig) MaxDays() (detail, total int) {
	for _, p := range c.Plans {
		detail, total = max(detail, p.DetailDays), max(total, p.TotalDays)
	}
	return
}

// DefaultPlans 是没在后台改过时的套餐。
func DefaultPlans() PlanConfig {
	return PlanConfig{Currency: "$", Plans: map[string]Plan{
		PlanFree:  {ID: PlanFree, Name: "免费版", Sites: 1, DetailDays: 30, TotalDays: 90},
		PlanBasic: {ID: PlanBasic, Name: "基础版", Sites: 5, DetailDays: 90, TotalDays: 180, MonthlyCredits: 500, SyncHours: 12, Digest: true},
		PlanPro:   {ID: PlanPro, Name: "专业版", Sites: 20, DetailDays: 180, TotalDays: 395, MonthlyCredits: 2000, SyncHours: 12, Digest: true},
	}}
}

// Plans 读取当前的套餐设置（后台改过的项覆盖默认值）。
func (s *Store) Plans(ctx context.Context) PlanConfig {
	cfg := DefaultPlans()
	raw, _ := s.Setting(ctx, KeyPlans)
	if raw == "" {
		return cfg
	}
	var saved PlanConfig
	if json.Unmarshal([]byte(raw), &saved) != nil {
		return cfg
	}
	if saved.Currency != "" {
		cfg.Currency = saved.Currency
	}
	for id, p := range saved.Plans {
		def, ok := cfg.Plans[id]
		if !ok {
			continue
		}
		p.ID, p.SyncHours, p.Digest = id, def.SyncHours, def.Digest
		if p.Name == "" {
			p.Name = def.Name
		}
		cfg.Plans[id] = p
	}
	return cfg
}

// SavePlans 检查并保存套餐设置。
func (s *Store) SavePlans(ctx context.Context, cfg PlanConfig) error {
	for _, id := range PlanIDs {
		p, ok := cfg.Plans[id]
		if !ok {
			return fmt.Errorf("缺少套餐 %s", id)
		}
		switch {
		case p.Sites < 1:
			return fmt.Errorf("%s：网站数至少 1 个", p.Name)
		case p.DetailDays < 7 || p.DetailDays > maxTotalDays:
			return fmt.Errorf("%s：明细保留天数要在 7～%d 之间", p.Name, maxTotalDays)
		case p.TotalDays < p.DetailDays || p.TotalDays > maxTotalDays:
			return fmt.Errorf("%s：每日总数保留天数不能少于明细，最多 %d 天（Search Console 只有 16 个月）", p.Name, maxTotalDays)
		case p.Price < 0 || p.MonthlyCredits < 0:
			return fmt.Errorf("%s：价格和每月积分不能是负数", p.Name)
		}
		if u := p.BuyURL; u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return fmt.Errorf("%s：购买链接要以 https:// 开头", p.Name)
		}
	}
	free := cfg.Plans[PlanFree]
	free.Price, free.MonthlyCredits, free.BuyURL = 0, 0, "" // 免费版不收钱、不送积分、没有购买链接
	cfg.Plans[PlanFree] = free
	b, _ := json.Marshal(cfg)
	return s.SetSetting(ctx, KeyPlans, string(b))
}

// PlanID 返回用户现在用的套餐：付费套餐没到期就是它，否则是免费版。管理员和演示账号按专业版算。
func (u User) PlanID(now time.Time) string {
	switch {
	case u.IsAdmin || u.Demo:
		return PlanPro
	case u.Plan != PlanFree && u.PlanExpiresAt != nil && u.PlanExpiresAt.After(now):
		return u.Plan
	}
	return PlanFree
}

// RetentionPlanID 是决定数据保留多久的套餐：到期后 GraceDays 天内仍按原套餐，续费就什么都不丢。
func (u User) RetentionPlanID(now time.Time) string {
	return u.PlanID(now.AddDate(0, 0, -GraceDays))
}

// PlanOf 返回用户现在的套餐。管理员不限网站数。
func (s *Store) PlanOf(ctx context.Context, uid int64) Plan {
	cfg := s.Plans(ctx)
	u, err := s.User(ctx, uid)
	if err != nil {
		return cfg.Get(PlanFree)
	}
	p := cfg.Get(u.PlanID(time.Now()))
	if u.IsAdmin {
		p.Sites = math.MaxInt32
	}
	return p
}

// Paused 表示这个网站超出了套餐的网站数：不再同步（数据不删，续费或升级后恢复）。按网站列表的顺序保留前几个。
func (p Plan) Paused(x Site) bool { return x.Rank >= p.Sites }

// ========== 套餐卡密 ==========

type RedeemResult struct {
	Credits int64     // 积分卡密到账的积分
	Plan    string    // 套餐卡密：兑换后的套餐
	Expires time.Time // 套餐卡密：新的到期时间
	Granted int64     // 开通或升级套餐时立即送的当月积分
}

// applyPlan 在事务里给用户开通、续期或升级套餐。规则：
//   - 卡密的套餐和现在的一样：到期时间往后延；
//   - 不一样：结果取两者中较高的那档，把“现有套餐剩余天数的价值 + 卡密的价值”按价格折算成结果套餐的天数
//     （例如基础版还剩 20 天、用 30 天专业版卡密升级，基础版剩下的 20 天按价格折成几天专业版），谁也不吃亏；
//     价格没设置（为 0）时不折算，直接相加；
//   - 新开通或升级时立即送当月积分，并从现在开始算 30 天的周期；续期不重复送。
func applyPlan(ctx context.Context, tx pgx.Tx, cfg PlanConfig, uid int64, codePlan string, days int, now time.Time) (RedeemResult, error) {
	var plan string
	var expires *time.Time
	if err := tx.QueryRow(ctx, `SELECT plan, plan_expires_at FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&plan, &expires); err != nil {
		return RedeemResult{}, notFound(err)
	}
	cur := PlanFree // 按套餐记录算（不管是不是管理员）
	if plan != PlanFree && expires != nil && expires.After(now) {
		cur = plan
	}
	remaining := 0.0
	if cur != PlanFree {
		remaining = expires.Sub(now).Hours() / 24
	}
	result := codePlan
	if planRank(cur) > planRank(codePlan) {
		result = cur
	}
	var total float64
	switch pr := cfg.Get(result).Price; {
	case cur == codePlan || cur == PlanFree:
		total = remaining + float64(days)
	case pr <= 0:
		total = remaining + float64(days)
	default:
		total = (remaining*cfg.Get(cur).Price + float64(days)*cfg.Get(codePlan).Price) / pr
	}
	exp := now.Add(time.Duration(total * 24 * float64(time.Hour)))
	res := RedeemResult{Plan: result, Expires: exp}
	upgrade := planRank(result) > planRank(cur)
	if _, err := tx.Exec(ctx, `UPDATE users SET plan=$2, plan_expires_at=$3 WHERE id=$1`, uid, result, exp); err != nil {
		return res, err
	}
	if upgrade {
		p := cfg.Get(result)
		if _, err := tx.Exec(ctx, `UPDATE users SET plan_credits_at=$2 WHERE id=$1`, uid, now); err != nil {
			return res, err
		}
		if p.MonthlyCredits > 0 {
			if err := addCredits(ctx, tx, uid, p.MonthlyCredits, "plan", p.Name+"每月积分"); err != nil {
				return res, err
			}
			res.Granted = p.MonthlyCredits
		}
	}
	return res, nil
}

// SetPlan 是后台直接给用户设置套餐：days 天后到期；套餐是免费版或 days ≤ 0 时取消付费套餐。
func (s *Store) SetPlan(ctx context.Context, uid int64, plan string, days int) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if plan == PlanFree || days <= 0 {
		if _, err := tx.Exec(ctx, `UPDATE users SET plan='free', plan_expires_at=NULL WHERE id=$1`, uid); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if planRank(plan) == 0 {
		return errors.New("没有这个套餐")
	}
	// 先清掉原来的套餐，再按“新开通”处理：到期时间就是现在 + days 天，并送当月积分
	if _, err := tx.Exec(ctx, `UPDATE users SET plan='free', plan_expires_at=NULL WHERE id=$1`, uid); err != nil {
		return err
	}
	if _, err := applyPlan(ctx, tx, s.Plans(ctx), uid, plan, days, time.Now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GrantMonthly 给付费套餐还在有效期内、离上次发积分满 30 天的用户发当月积分，返回发了几个人。
// 同一个人同时被两次调用也只会发一次（条件更新）。
func (s *Store) GrantMonthly(ctx context.Context) (n int) {
	cfg := s.Plans(ctx)
	rows, err := s.Pool.Query(ctx, `SELECT id, plan FROM users WHERE plan <> 'free' AND plan_expires_at > now() AND NOT disabled
		AND (plan_credits_at IS NULL OR plan_credits_at <= now() - make_interval(days => $1))`, PlanCycleDays)
	if err != nil {
		return 0
	}
	type due struct {
		id   int64
		plan string
	}
	list, _ := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.id, &d.plan)
	})
	for _, d := range list {
		p := cfg.Get(d.plan)
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			continue
		}
		tag, err := tx.Exec(ctx, `UPDATE users SET plan_credits_at=now() WHERE id=$1
			AND (plan_credits_at IS NULL OR plan_credits_at <= now() - make_interval(days => $2))`, d.id, PlanCycleDays)
		if err == nil && tag.RowsAffected() == 1 && (p.MonthlyCredits == 0 || addCredits(ctx, tx, d.id, p.MonthlyCredits, "plan", p.Name+"每月积分") == nil) {
			if tx.Commit(ctx) == nil {
				n++
				continue
			}
		}
		tx.Rollback(ctx)
	}
	return n
}

// ExpiringUsers 返回付费套餐在 [from, to) 之间到期的用户（到期提醒用）。
func (s *Store) ExpiringUsers(ctx context.Context, from, to time.Time) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+userCols+` FROM users WHERE plan <> 'free' AND NOT disabled AND NOT demo
		AND plan_expires_at >= $1 AND plan_expires_at < $2`, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) { return scanUser(r) })
}

// DigestUsers 返回付费套餐有效（管理员按专业版算，见 PlanID）、没有关掉邮件简报的用户。
// 以前只看 plan 字段，管理员的 plan 一直是 free，所以管理员从来收不到简报。
func (s *Store) DigestUsers(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+prefixed("u.", userCols)+` FROM users u WHERE (u.is_admin OR (u.plan <> 'free' AND u.plan_expires_at > now()))
		AND NOT u.disabled AND NOT u.demo AND NOT EXISTS (SELECT 1 FROM app_settings a WHERE a.user_id=u.id AND a.key=$1 AND a.value='true')`, KeyDigestOff)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) { return scanUser(r) })
}
