package store

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// ========== GA 配额 ==========

type Quota struct {
	Property      string
	SiteName      string
	DayConsumed   int
	DayRemaining  int
	HourConsumed  int
	HourRemaining int
	UpdatedAt     time.Time
}

// GA 返回的 consumed 只是“这一次查询”用掉的令牌，remaining 才是剩余总量，所以用上限减剩余算出已用。
// 标准媒体资源每天 200,000、每小时 40,000 个令牌；GA 360 是 10 倍。
func quotaLimit(remaining, standard int) int {
	if remaining > standard {
		return standard * 10
	}
	return standard
}

func (q Quota) DayLimit() int  { return quotaLimit(q.DayRemaining, 200000) }
func (q Quota) DayUsed() int   { return max(q.DayLimit()-q.DayRemaining, 0) }
func (q Quota) HourLimit() int { return quotaLimit(q.HourRemaining, 40000) }
func (q Quota) HourUsed() int  { return max(q.HourLimit()-q.HourRemaining, 0) }

func (s *Store) SaveQuota(ctx context.Context, property string, dayUsed, dayLeft, hourUsed, hourLeft int) {
	_, _ = s.Pool.Exec(ctx, `INSERT INTO ga_quota(property,day_consumed,day_remaining,hour_consumed,hour_remaining,updated_at)
		VALUES($1,$2,$3,$4,$5,now()) ON CONFLICT (property) DO UPDATE SET day_consumed=EXCLUDED.day_consumed,
		day_remaining=EXCLUDED.day_remaining,hour_consumed=EXCLUDED.hour_consumed,hour_remaining=EXCLUDED.hour_remaining,updated_at=now()`,
		property, dayUsed, dayLeft, hourUsed, hourLeft)
}

// Quotas 返回每个已导入网站的媒体资源最近一次看到的配额（只列出今天更新过的）。
func (s *Store) Quotas(ctx context.Context) ([]Quota, error) {
	rows, err := s.Pool.Query(ctx, `SELECT q.property, coalesce(st.name, ''), q.day_consumed, q.day_remaining, q.hour_consumed, q.hour_remaining, q.updated_at
		FROM ga_quota q JOIN sites st ON st.ga_property=q.property AND st.user_id=$1
		WHERE q.updated_at > now() - interval '24 hours' ORDER BY q.day_remaining`, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Quota, error) {
		var x Quota
		err := r.Scan(&x.Property, &x.SiteName, &x.DayConsumed, &x.DayRemaining, &x.HourConsumed, &x.HourRemaining, &x.UpdatedAt)
		return x, err
	})
}

// ========== 保存到报告的分析 ==========

type Note struct {
	ID        int64
	SiteID    *int64
	SiteName  string
	Source    string
	SourceID  int64
	Title     string
	Content   string
	CreatedAt time.Time
}

// AddNote 保存一条分析；同一条来源只保存一次。
func (s *Store) AddNote(ctx context.Context, n Note) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO notes(user_id,site_id,source,source_id,title,content) SELECT $1,$2,$3,$4,$5,$6
		WHERE `+ownSite+` ON CONFLICT (source, source_id) DO NOTHING`, UserID(ctx), n.SiteID, n.Source, n.SourceID, n.Title, n.Content)
	return err
}

func (s *Store) NoteSaved(ctx context.Context, source string, sourceID int64) bool {
	var ok bool
	_ = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notes WHERE source=$1 AND source_id=$2 AND user_id=$3)`, source, sourceID, UserID(ctx)).Scan(&ok)
	return ok
}

// PendingNotes 返回还没被报告用过的分析。siteID 为 nil 表示全站报告（取全部）；否则只取这个网站的
// （不分网站的分析留给全站报告：被某个网站的报告用掉之后，全站报告就看不到它了）。
func (s *Store) PendingNotes(ctx context.Context, siteID *int64) ([]Note, error) {
	rows, err := s.Pool.Query(ctx, `SELECT n.id,n.site_id,coalesce(st.name,''),n.source,n.source_id,n.title,n.content,n.created_at
		FROM notes n LEFT JOIN sites st ON st.id=n.site_id
		WHERE n.user_id=$2 AND n.used_in_report_id IS NULL AND ($1::bigint IS NULL OR n.site_id=$1) ORDER BY n.id LIMIT 10`, siteID, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Note, error) {
		var x Note
		err := r.Scan(&x.ID, &x.SiteID, &x.SiteName, &x.Source, &x.SourceID, &x.Title, &x.Content, &x.CreatedAt)
		return x, err
	})
}

func (s *Store) MarkNotesUsed(ctx context.Context, ids []int64, reportID int64) {
	if len(ids) == 0 {
		return
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE notes SET used_in_report_id=$2 WHERE id = ANY($1)`, ids, reportID)
}

// ========== 图上的标注 ==========

type Annotation struct {
	Date    time.Time
	EndDate *time.Time
	Label   string
	Detail  string
}

func (s *Store) UpsertAnnotation(ctx context.Context, source, extID string, date time.Time, end *time.Time, label, detail string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO annotations(source,ext_id,date,end_date,label,detail) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (source, ext_id) DO UPDATE SET date=EXCLUDED.date,end_date=EXCLUDED.end_date,label=EXCLUDED.label,detail=EXCLUDED.detail`,
		source, extID, date, end, label, detail)
	return err
}

func (s *Store) Annotations(ctx context.Context, siteID int64, from, to time.Time) ([]Annotation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT date,end_date,label,detail FROM annotations
		WHERE (site_id IS NULL OR site_id=$1) AND date BETWEEN $2 AND $3 ORDER BY date`, siteID, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Annotation, error) {
		var x Annotation
		err := r.Scan(&x.Date, &x.EndDate, &x.Label, &x.Detail)
		return x, err
	})
}

// ========== 启动清理 ==========

// MarkInterrupted 把上次进程退出时还在进行的任务标记为中断。只在启动时调用一次。
func (s *Store) MarkInterrupted(ctx context.Context) int64 {
	const msg = "服务重启，任务中断了"
	var n int64
	for _, q := range []string{
		`UPDATE sync_runs SET status='error', finished_at=now(), message='` + msg + `' WHERE status='running'`,
		`UPDATE sync_state SET status='pending' WHERE status='running'`,
		// 洞察没有内容可留，直接删掉，下次打开页面会自动重新生成
		`DELETE FROM insights WHERE status='running'`,
		`UPDATE diagnoses SET status='error', error='` + msg + `' WHERE status='running'`,
		`UPDATE reports SET status='error', error='` + msg + `' WHERE status='running'`,
		`UPDATE messages SET status='error', error='` + msg + `' WHERE status='running'`,
	} {
		if tag, err := s.Pool.Exec(ctx, q); err == nil {
			n += tag.RowsAffected()
		}
	}
	return n
}

// Cleanup 删掉已经过期的登录会话和缓存，清空一周没动过的对话里留着的工具原始结果。
// 它们过期后不会再被读到，但不删的话会一直堆在表里。
// 工具原始结果只是给“紧接着的追问”省钱用的：隔了一周，数据早就变了、服务商的提示缓存也早没了，再问一定会重查；
// 清掉之后那一轮只剩回答和一行“查过什么”，页面上显示的内容不受影响。
func (s *Store) Cleanup(ctx context.Context) (n int64) {
	for _, q := range []string{
		`DELETE FROM sessions WHERE expires_at < now()`,
		`DELETE FROM api_cache WHERE expires_at < now()`,
		`UPDATE messages SET tool_log='[]' WHERE tool_log <> '[]'::jsonb
			AND conversation_id IN (SELECT id FROM conversations WHERE updated_at < now() - interval '7 days')`,
	} {
		if tag, err := s.Pool.Exec(ctx, q); err == nil {
			n += tag.RowsAffected()
		}
	}
	return n
}

// 同步来的数据按网站主人的套餐保留（见 Plan.DetailDays / TotalDays），过期的每天真正 DELETE，不是标记：
//   - 明细（页面、入口页、来源、地区、设备、小时、语言、搜索页面、搜索词）一天可能几千行，是数据库里最占地方的部分；
//   - 每日总数（ga_daily、gsc_daily）每个网站一天只有一行，趋势图、和上一期比、和去年同期比都要用。
//
// 实际多留 retentionSlack 天：网站的“昨天”按网站时区算、清理按数据库的日期算，差出来的一两天不至于
// 今天删掉、下次同步又补回来。
const retentionSlack = 2

var detailTables = []string{"ga_daily_pages", "ga_daily_landing", "ga_daily_sources", "ga_daily_geo", "ga_daily_tech", "ga_hourly", "ga_daily_lang",
	"ga_daily_ads", "gsc_daily_pages", "gsc_daily_queries"}

var totalsTables = []string{"ga_daily", "gsc_daily"}

// keepSQL 算出每个网站要保留几天（按主人的套餐；到期后的宽限期内仍按原套餐；管理员、演示账号按专业版）。
// 参数：$1 免费版、$2 基础版、$3 专业版的天数。
var keepSQL = `SELECT s.id AS site_id, CASE
	WHEN u.is_admin OR u.demo THEN $3::int
	WHEN u.plan = 'pro' AND u.plan_expires_at > now() - make_interval(days => ` + strconv.Itoa(GraceDays) + `) THEN $3::int
	WHEN u.plan = 'basic' AND u.plan_expires_at > now() - make_interval(days => ` + strconv.Itoa(GraceDays) + `) THEN $2::int
	ELSE $1::int END + ` + strconv.Itoa(retentionSlack) + ` AS days
	FROM sites s JOIN users u ON u.id = s.user_id`

// Prune 清掉只增不减的历史记录，返回删掉的行数：
//   - 同步来的数据：按网站主人的套餐保留（见上面）；删完把 sync_state 里记的“最早有哪天的数据”往后挪，
//     这样以后升级套餐时，同步知道要往前补哪一段；
//   - “需要关注”的提醒和图上的标注：按所有套餐里最长的明细 / 每日总数天数清理；
//   - 同步记录、发信记录只留 90 天；AI 消耗明细只留 400 天（充值、赠送、套餐记录一直保留）；
//   - 旧洞察：30 天以前的、并且同一个网站同一个时间范围已经有更新的一份。
//
// 删了明细之后顺手 VACUUM 这几张表：腾出来的空间马上能给新数据用，表文件不会越涨越大。
func (s *Store) Prune(ctx context.Context) (n int64) {
	exec := func(q string, args ...any) {
		if tag, err := s.Pool.Exec(ctx, q, args...); err == nil {
			n += tag.RowsAffected()
		}
	}
	exec(`DELETE FROM sync_runs WHERE started_at < now() - interval '90 days'`)
	exec(`DELETE FROM ledger WHERE created_at < now() - interval '400 days' AND NOT pending AND kind = ANY($1)`, aiKinds) // 充值记录一直保留
	exec(`DELETE FROM mail_log WHERE created_at < now() - interval '90 days'`)
	exec(`DELETE FROM insights i WHERE i.created_at < now() - interval '30 days' AND i.status <> 'running' AND EXISTS (
		SELECT 1 FROM insights n WHERE n.user_id = i.user_id AND n.site_id IS NOT DISTINCT FROM i.site_id AND n.range_key = i.range_key AND n.created_at > i.created_at)`)
	cfg := s.Plans(ctx)
	var vacuum []string
	purge := func(tables, sources []string, days func(Plan) int) {
		args := []any{days(cfg.Get(PlanFree)), days(cfg.Get(PlanBasic)), days(cfg.Get(PlanPro))}
		for _, t := range tables {
			tag, err := s.Pool.Exec(ctx, `WITH keep AS (`+keepSQL+`)
				DELETE FROM `+t+` x USING keep WHERE x.site_id = keep.site_id AND x.date < current_date - keep.days`, args...)
			if err == nil && tag.RowsAffected() > 0 {
				n += tag.RowsAffected()
				vacuum = append(vacuum, t)
			}
		}
		_, _ = s.Pool.Exec(ctx, `WITH keep AS (`+keepSQL+`)
			UPDATE sync_state ss SET backfilled_from = current_date - keep.days FROM keep
			WHERE ss.site_id = keep.site_id AND ss.source = ANY($4) AND ss.backfilled_from < current_date - keep.days`, append(args, sources)...)
	}
	purge(detailTables, []string{"ga", "gsc"}, func(p Plan) int { return p.DetailDays })
	purge(totalsTables, []string{"ga_hist", "gsc_hist"}, func(p Plan) int { return p.TotalDays })
	maxDetail, maxTotal := cfg.MaxDays()
	exec(`DELETE FROM signals WHERE event_date < current_date - $1::int`, maxDetail+retentionSlack)
	exec(`DELETE FROM annotations WHERE coalesce(end_date, date) < current_date - $1::int`, maxTotal+retentionSlack)
	for _, t := range vacuum {
		if _, err := s.Pool.Exec(ctx, `VACUUM (ANALYZE) `+t); err != nil {
			break // 没有权限之类的：不影响删除本身，autovacuum 之后也会处理
		}
	}
	return n
}

// CompactResult 是一张表整理前后占的空间（字节，含索引）。
type CompactResult struct {
	Table         string
	Before, After int64
}

// Compact 用 VACUUM FULL 重写同步数据表，把删除、重新同步留下的空位还给磁盘。
// 平时每天的清理只做普通 VACUUM（空位留给新数据用，不锁表）；表文件已经被撑大时，用它一次性收回来。
// VACUUM FULL 整理期间会锁住这张表（几秒到几十秒），所以：
//   - 命令行 pingoo compact（minWaste < 0）：每张表都整理；
//   - 每天半夜自动运行（minWaste ≥ 0）：只整理“空位超过一半、并且超过 minWaste 字节”的表，没撑大的不碰。
func (s *Store) Compact(ctx context.Context, minWaste int64) []CompactResult {
	var out []CompactResult
	size := func(t string) (n int64) {
		_ = s.Pool.QueryRow(ctx, `SELECT pg_total_relation_size($1::regclass)`, t).Scan(&n)
		return
	}
	for _, t := range append(append([]string{}, detailTables...), totalsTables...) {
		if minWaste >= 0 {
			heap, need := s.tableSpace(ctx, t)
			if need <= 0 || heap < 2*need || heap-need < max(minWaste, 1<<20) { // 不到 1 MB 的空位不值得锁表
				continue
			}
		}
		r := CompactResult{Table: t, Before: size(t)}
		if _, err := s.Pool.Exec(ctx, `VACUUM (FULL, ANALYZE) `+t); err != nil {
			continue
		}
		r.After = size(t)
		out = append(out, r)
	}
	return out
}

// tableSpace 返回表文件的大小，和按行数、平均行宽估算的实际需要的大小（每行再加 28 字节的行头和行指针）。
// 统计信息来自最近一次 ANALYZE（每天清理时会做）；还没有统计信息时返回 need = 0。
func (s *Store) tableSpace(ctx context.Context, table string) (heap, need int64) {
	var rows float64
	var width int64
	err := s.Pool.QueryRow(ctx, `SELECT pg_relation_size(c.oid), c.reltuples,
		coalesce((SELECT sum(avg_width) FROM pg_stats st WHERE st.schemaname = n.nspname AND st.tablename = c.relname), 0)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.oid = $1::regclass`, table).Scan(&heap, &rows, &width)
	if err != nil || rows <= 0 || width <= 0 {
		return heap, 0
	}
	return heap, int64(rows * float64(width+28))
}

// DropOverviewInsights 删除“全部网站”的洞察（不含正在生成的），下次打开概览时会重新生成。
func (s *Store) DropOverviewInsights(ctx context.Context) {
	_, _ = s.Pool.Exec(ctx, `DELETE FROM insights WHERE site_id IS NULL AND user_id=$1 AND status <> 'running'`, UserID(ctx))
}
