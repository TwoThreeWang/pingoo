package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Activity 是后台概况里的使用情况，按“过去 24 小时 / 7 天 / 30 天”滚动统计。
// 不算管理员和演示账号：管理员自己测试、访客看演示都不是真实用户的使用。
type Activity struct {
	Active  [3]int // 有访问的用户
	Signups [3]int // 新注册
	Paid    int    // 付费套餐还在有效期内的用户
	AI      []ActivityAI
}

// ActivityAI 是一类 AI 任务的发起次数（含失败退回的、自动生成的）和 30 天里用过的人数。
type ActivityAI struct {
	Kind  string
	Count [3]int
	Users int
}

// activityKinds 是概况里列出的 AI 任务，顺序就是表格的行（不含后台的模型测试）。
var activityKinds = []string{"insight", "diagnose", "chat", "report"}

// Activity 统计全站的使用情况。“有访问”取最近一次登录和所有会话里最近一次访问中较晚的那个：
// 会话的访问时间每 5 分钟才更新一次，退出登录会删掉会话，所以再参考登录时间，不会漏掉刚退出的人。
// 两条查询各扫一遍用户表、会话表和 ledger 最近 30 天（有 created_at 索引）；后台再缓存几分钟，见 web.adminActivity。
func (s *Store) Activity(ctx context.Context) (Activity, error) {
	var a Activity
	// 会话先按用户汇总一次再连接（sessions 上没有 user_id 索引，逐个用户去查会把整张表扫很多遍）
	err := s.Pool.QueryRow(ctx, `WITH ss AS (SELECT user_id, max(last_seen_at) AS seen FROM sessions GROUP BY user_id),
		u AS (
		  SELECT u.created_at, u.plan, u.plan_expires_at, greatest(u.last_login_at, ss.seen) AS seen
		  FROM users u LEFT JOIN ss ON ss.user_id = u.id WHERE NOT u.demo AND NOT u.is_admin)
		SELECT count(*) FILTER (WHERE seen >= now() - interval '1 day'),
		       count(*) FILTER (WHERE seen >= now() - interval '7 days'),
		       count(*) FILTER (WHERE seen >= now() - interval '30 days'),
		       count(*) FILTER (WHERE created_at >= now() - interval '1 day'),
		       count(*) FILTER (WHERE created_at >= now() - interval '7 days'),
		       count(*) FILTER (WHERE created_at >= now() - interval '30 days'),
		       count(*) FILTER (WHERE plan <> $1 AND plan_expires_at > now())
		FROM u`, PlanFree).Scan(&a.Active[0], &a.Active[1], &a.Active[2], &a.Signups[0], &a.Signups[1], &a.Signups[2], &a.Paid)
	if err != nil {
		return a, err
	}
	// 先按（类型, 用户）汇总，再排除管理员和演示账号、按类型合计：比直接 count(DISTINCT) 少一次大排序
	rows, err := s.Pool.Query(ctx, `WITH per AS (
		  SELECT kind, user_id,
		         count(*) FILTER (WHERE created_at >= now() - interval '1 day') AS d1,
		         count(*) FILTER (WHERE created_at >= now() - interval '7 days') AS d7,
		         count(*) AS d30
		  FROM ledger WHERE kind = ANY($1) AND created_at >= now() - interval '30 days'
		  GROUP BY kind, user_id)
		SELECT per.kind, sum(d1)::int, sum(d7)::int, sum(d30)::int, count(*)::int
		FROM per JOIN users u ON u.id = per.user_id AND NOT u.demo AND NOT u.is_admin
		GROUP BY per.kind`, activityKinds)
	if err != nil {
		return a, err
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ActivityAI, error) {
		var x ActivityAI
		err := r.Scan(&x.Kind, &x.Count[0], &x.Count[1], &x.Count[2], &x.Users)
		return x, err
	})
	if err != nil {
		return a, err
	}
	for _, k := range activityKinds { // 没用过的也列出来（全是 0），表格的行固定
		x := ActivityAI{Kind: k}
		for _, g := range got {
			if g.Kind == k {
				x = g
			}
		}
		a.AI = append(a.AI, x)
	}
	return a, nil
}
