// Package store 封装对 PostgreSQL 的读写（除了大批量的统计数据，那部分在 syncer 和 metrics 里）。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pingoo/internal/secret"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	Pool *pgxpool.Pool
	Box  *secret.Box
}

func New(pool *pgxpool.Pool, box *secret.Box) *Store {
	return &Store{Pool: pool, Box: box}
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ========== 设置 ==========

const (
	// 每个用户自己的设置
	KeyGoogleSA       = "google_sa_json"
	KeyGoogleSAUpload = "google_sa_uploaded_at"
	KeyReportWeekly   = "report_weekly"
	KeyReportMonthly  = "report_monthly"
	KeyReportPerSite  = "report_per_site"
	KeyReportReview   = "report_review"
	KeyReportDetail   = "report_detail"
	KeyReportTZ       = "report_tz"
	KeySignalsMuted   = "signals_muted" // 不想再收到的提醒类型，逗号分隔
	KeyRedeemFails    = "redeem_fails"  // 最近兑换卡密失败的时间（unix 秒，逗号分隔）：限制猜卡密，存在库里，重启不清零
	KeyDigestOff      = "digest_off"    // "true" 表示不要每天早上的邮件简报

	// 全站设置（管理员在后台改）
	KeyAIDailyCap  = "ai_daily_token_cap" // 全站每天最多用多少 token（输入 + 输出），空或 0 表示不限
	KeySignupBonus = "signup_bonus"       // 新用户赠送的积分
	KeySignupOff   = "signup_closed"      // "true" 表示暂停注册
	KeyPlans       = "plans"              // 套餐设置（JSON，见 PlanConfig）
	KeyNightlyDone = "nightly_done"       // 半夜维护最近一次做的日期（重启后不重复做）

	KeySiteURL      = "site_url"         // 对外访问的地址，邮件里的链接用它；https 开头时登录 cookie 加上 Secure
	KeyDefaultTZ    = "default_tz"       // 默认时区：新网站、报告、每日用量按它算“今天”
	KeyMailLimit    = "mail_daily_limit" // 发信服务 24 小时的硬上限，到了才真正暂停注册
	KeySMTPHost     = "smtp_host"
	KeySMTPPort     = "smtp_port"
	KeySMTPUser     = "smtp_user"
	KeySMTPPassword = "smtp_password"
	KeySMTPFrom     = "smtp_from"
	KeySMTPFromName = "smtp_from_name"
)

var encryptedKeys = map[string]bool{KeyGoogleSA: true, KeySMTPPassword: true}

// DefaultTZ 返回全站默认时区（后台设置，没设时是 Asia/Shanghai）。
func (s *Store) DefaultTZ(ctx context.Context) string {
	tz := s.SettingOr(ctx, KeyDefaultTZ, "Asia/Shanghai")
	if _, err := time.LoadLocation(tz); err != nil {
		return "Asia/Shanghai"
	}
	return tz
}

// MailLimit 返回发信服务 24 小时的上限（后台设置，没设时是 200，Amazon SES 沙箱的额度）。
func (s *Store) MailLimit(ctx context.Context) int {
	return int(max(s.IntSetting(ctx, KeyMailLimit, 200), 1))
}

// userKeys 是按用户区分的设置：读写时用 context 里的用户 ID；其余都是全站设置（user_id = 0）。
var userKeys = map[string]bool{KeyGoogleSA: true, KeyGoogleSAUpload: true, KeyReportWeekly: true, KeyReportMonthly: true,
	KeyReportPerSite: true, KeyReportReview: true, KeyReportDetail: true, KeyReportTZ: true, KeySignalsMuted: true, KeyRedeemFails: true, KeyDigestOff: true}

func settingOwner(ctx context.Context, key string) int64 {
	if userKeys[key] {
		return UserID(ctx)
	}
	return 0
}

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	uid := settingOwner(ctx, key)
	if userKeys[key] && uid == 0 {
		return "", nil
	}
	var v string
	var enc bool
	err := s.Pool.QueryRow(ctx, `SELECT value, encrypted FROM app_settings WHERE user_id=$1 AND key=$2`, uid, key).Scan(&v, &enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if enc {
		return s.Box.Decrypt(v)
	}
	return v, nil
}

func (s *Store) SettingOr(ctx context.Context, key, def string) string {
	v, err := s.Setting(ctx, key)
	if err != nil || v == "" {
		return def
	}
	return v
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	uid := settingOwner(ctx, key)
	if userKeys[key] && uid == 0 {
		return errors.New("没有登录用户，不能保存个人设置")
	}
	enc := encryptedKeys[key]
	if enc && value != "" {
		var err error
		if value, err = s.Box.Encrypt(value); err != nil {
			return err
		}
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO app_settings(user_id,key,value,encrypted,updated_at) VALUES($1,$2,$3,$4,now())
		ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value, encrypted=EXCLUDED.encrypted, updated_at=now()`, uid, key, value, enc)
	return err
}

// IntSetting 读一个整数型的全站设置，没设或格式不对时返回 def。
func (s *Store) IntSetting(ctx context.Context, key string, def int64) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s.SettingOr(ctx, key, "")), 10, 64)
	if err != nil {
		return def
	}
	return n
}

type ReportSettings struct {
	Weekly, Monthly, PerSite, Review bool
	Detail                           string // brief / standard / detailed
	TZ                               string
}

// ReportSettings 返回当前用户的报告设置；没设时区时用全站默认时区。
func (s *Store) ReportSettings(ctx context.Context) ReportSettings {
	defTZ := s.DefaultTZ(ctx)
	b := func(k string, def bool) bool {
		v := s.SettingOr(ctx, k, strconv.FormatBool(def))
		return v == "true"
	}
	return ReportSettings{
		Weekly: b(KeyReportWeekly, true), Monthly: b(KeyReportMonthly, true),
		PerSite: b(KeyReportPerSite, true), Review: b(KeyReportReview, true),
		Detail: s.SettingOr(ctx, KeyReportDetail, "standard"),
		TZ:     s.SettingOr(ctx, KeyReportTZ, defTZ),
	}
}

// ========== 会话 ==========

type Session struct {
	ID         string
	UserID     int64
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

func (s *Store) CreateSession(ctx context.Context, uid int64, id, ua, ip string, ttl time.Duration) error {
	secs := int(ttl.Seconds())
	_, err := s.Pool.Exec(ctx, `INSERT INTO sessions(id,user_id,user_agent,ip,expires_at,ttl_seconds) VALUES($1,$2,$3,$4,now()+$5::interval,$6)`,
		id, uid, ua, ip, strconv.Itoa(secs)+" seconds", max(secs, 0))
	return err
}

// TouchSession 校验会话，刷新最后访问时间（每 5 分钟最多写一次），并做滑动续期：
// 有效期用掉一成以上时，把过期时间重新延到“现在 + 有效期”。这样只要一直在用就不会被登出，
// 超过一个有效期没来才需要重新登录。renewed > 0 表示这次续期了，调用方要用它重新下发 cookie。
func (s *Store) TouchSession(ctx context.Context, id string) (uid int64, renewed time.Duration, err error) {
	var ttl int
	var extended bool
	err = s.Pool.QueryRow(ctx, `WITH old AS (SELECT id, expires_at FROM sessions WHERE id=$1 AND expires_at > now() FOR UPDATE)
		UPDATE sessions s SET
		  last_seen_at = CASE WHEN s.last_seen_at < now()-interval '5 minutes' THEN now() ELSE s.last_seen_at END,
		  expires_at = CASE WHEN s.expires_at < now() + s.ttl_seconds * interval '0.9 second'
		                    THEN now() + s.ttl_seconds * interval '1 second' ELSE s.expires_at END
		FROM old WHERE s.id = old.id
		RETURNING s.user_id, s.ttl_seconds, s.expires_at <> old.expires_at`, id).Scan(&uid, &ttl, &extended)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if extended {
		renewed = time.Duration(ttl) * time.Second
	}
	return uid, renewed, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, id)
	return err
}

// DeleteOtherSessions 退出当前用户在其他设备上的登录。
func (s *Store) DeleteOtherSessions(ctx context.Context, keep string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND id<>$2`, UserID(ctx), keep)
	return err
}

// DeleteUserSessions 退出某个用户的全部登录（改密码、找回密码之后）。
func (s *Store) DeleteUserSessions(ctx context.Context, uid int64, keep string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND id<>$2`, uid, keep)
	return err
}

func (s *Store) Sessions(ctx context.Context) ([]Session, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,user_agent,ip,created_at,last_seen_at FROM sessions WHERE user_id=$1 AND expires_at>now() ORDER BY last_seen_at DESC`, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Session, error) {
		var x Session
		err := r.Scan(&x.ID, &x.UserAgent, &x.IP, &x.CreatedAt, &x.LastSeenAt)
		return x, err
	})
}

// ========== 网站 ==========

type Site struct {
	ID           int64
	UserID       int64
	Name         string
	Domain       string
	GAProperty   string
	GSCSite      string
	Timezone     string
	Description  string
	ExcludePaths []string
	Demo         bool
	SortOrder    int
	CreatedAt    time.Time
	SignalLevel  string // 提醒灵敏度：low / normal / high
	Rank         int    // 在主人网站列表里排第几（从 0 开始）：超出套餐网站数的会暂停同步，见 Plan.Paused
	Currency     string // GA 媒体资源的货币（广告收入的单位），没有广告收入时为空
}

func (x Site) Initial() string {
	for _, r := range x.Name {
		return strings.ToUpper(string(r))
	}
	return "?"
}

func (x Site) Location() *time.Location {
	if loc, err := time.LoadLocation(x.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

const siteCols = `id,user_id,name,domain,ga_property,gsc_site,timezone,description,exclude_paths,demo,sort_order,created_at,signal_level,currency,
	(SELECT count(*) FROM sites o WHERE o.user_id = sites.user_id AND (o.sort_order, o.id) < (sites.sort_order, sites.id))::int`

func scanSite(r pgx.Row) (Site, error) {
	var x Site
	err := r.Scan(&x.ID, &x.UserID, &x.Name, &x.Domain, &x.GAProperty, &x.GSCSite, &x.Timezone, &x.Description, &x.ExcludePaths, &x.Demo, &x.SortOrder, &x.CreatedAt, &x.SignalLevel, &x.Currency, &x.Rank)
	return x, err
}

// Sites 返回当前用户的网站。
func (s *Store) Sites(ctx context.Context) ([]Site, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+siteCols+` FROM sites WHERE user_id=$1 ORDER BY sort_order, id`, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Site, error) { return scanSite(r) })
}

// Site 返回当前用户的一个网站；别人的网站当作不存在。
func (s *Store) Site(ctx context.Context, id int64) (Site, error) {
	x, err := scanSite(s.Pool.QueryRow(ctx, `SELECT `+siteCols+` FROM sites WHERE id=$1 AND user_id=$2`, id, UserID(ctx)))
	return x, notFound(err)
}

// AllSites 返回所有用户的网站，只给后台调度用（同步、检测、清理）。
func (s *Store) AllSites(ctx context.Context) ([]Site, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+siteCols+` FROM sites ORDER BY user_id, sort_order, id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Site, error) { return scanSite(r) })
}

// SiteAny 不检查归属地按 ID 取网站，只给后台调度用。
func (s *Store) SiteAny(ctx context.Context, id int64) (Site, error) {
	x, err := scanSite(s.Pool.QueryRow(ctx, `SELECT `+siteCols+` FROM sites WHERE id=$1`, id))
	return x, notFound(err)
}

// SetSiteCurrency 记下网站的货币（同步时从 GA 的报表里读到，变了才写）。
func (s *Store) SetSiteCurrency(ctx context.Context, siteID int64, currency string) {
	if currency != "" {
		_, _ = s.Pool.Exec(ctx, `UPDATE sites SET currency=$2 WHERE id=$1 AND currency<>$2`, siteID, currency)
	}
}

// SiteCount 返回当前用户有几个网站。
func (s *Store) SiteCount(ctx context.Context) (n int) {
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM sites WHERE user_id=$1`, UserID(ctx)).Scan(&n)
	return n
}

func (s *Store) CreateSite(ctx context.Context, x Site) (int64, error) {
	if x.ExcludePaths == nil {
		x.ExcludePaths = []string{}
	}
	var id int64
	if x.SignalLevel == "" {
		x.SignalLevel = "normal"
	}
	uid := UserID(ctx)
	if uid == 0 {
		return 0, errors.New("没有登录用户")
	}
	err := s.Pool.QueryRow(ctx, `INSERT INTO sites(user_id,name,domain,ga_property,gsc_site,timezone,description,exclude_paths,demo,signal_level,sort_order)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,(SELECT coalesce(max(sort_order),0)+1 FROM sites WHERE user_id=$1)) RETURNING id`,
		uid, x.Name, x.Domain, x.GAProperty, x.GSCSite, x.Timezone, x.Description, x.ExcludePaths, x.Demo, x.SignalLevel).Scan(&id)
	return id, err
}

func (s *Store) UpdateSite(ctx context.Context, x Site) error {
	if x.ExcludePaths == nil {
		x.ExcludePaths = []string{}
	}
	if x.SignalLevel == "" {
		x.SignalLevel = "normal"
	}
	_, err := s.Pool.Exec(ctx, `UPDATE sites SET name=$2,domain=$3,ga_property=$4,gsc_site=$5,timezone=$6,description=$7,exclude_paths=$8,signal_level=$9,updated_at=now()
		WHERE id=$1 AND user_id=$10`,
		x.ID, x.Name, x.Domain, x.GAProperty, x.GSCSite, x.Timezone, x.Description, x.ExcludePaths, x.SignalLevel, UserID(ctx))
	return err
}

var (
	gaTables  = []string{"ga_daily", "ga_daily_pages", "ga_daily_landing", "ga_daily_sources", "ga_daily_geo", "ga_daily_tech", "ga_hourly", "ga_daily_lang"}
	gscTables = []string{"gsc_daily", "gsc_daily_pages", "gsc_daily_queries"}
)

// ResetSiteData 在网站换了 GA 媒体资源或 Search Console 网站之后调用：删掉旧数据源同步下来的数据和同步状态，
// 以及从这些数据算出来的信号和洞察。不删的话，新数据源只会覆盖回填范围内的日期，更早的旧数据会和新数据混在一起；
// 把数据源清空时，旧数据更是会一直留着。诊断、报告、待办和对话是历史记录，保留不动。
func (s *Store) ResetSiteData(ctx context.Context, siteID int64, ga, gsc bool) error {
	if !ga && !gsc {
		return nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	clear := func(sources []string, tables []string) error {
		for _, t := range tables {
			if _, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE site_id=$1`, siteID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM sync_state WHERE site_id=$1 AND source = ANY($2)`, siteID, sources)
		return err
	}
	if ga {
		if err := clear([]string{"ga", "ga_hist"}, gaTables); err != nil { // ga_hist：历史每日数据的回填进度
			return err
		}
	}
	if gsc {
		if err := clear([]string{"gsc"}, gscTables); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM signals WHERE site_id=$1`, siteID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM insights WHERE site_id=$1 OR (site_id IS NULL AND user_id=(SELECT user_id FROM sites WHERE id=$1))`, siteID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteSite(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sites WHERE id=$1 AND user_id=$2`, id, UserID(ctx))
	return err
}

// ========== 同步状态 ==========

type SyncState struct {
	SiteID         int64
	Source         string
	BackfilledFrom *time.Time
	LastSynced     *time.Time
	Status         string
	Progress       int
	LastError      string
	UpdatedAt      time.Time
}

func (s *Store) SyncState(ctx context.Context, siteID int64, source string) (SyncState, error) {
	st := SyncState{SiteID: siteID, Source: source, Status: "pending"}
	err := s.Pool.QueryRow(ctx, `SELECT backfilled_from,last_synced_date,status,progress,last_error,updated_at FROM sync_state WHERE site_id=$1 AND source=$2`,
		siteID, source).Scan(&st.BackfilledFrom, &st.LastSynced, &st.Status, &st.Progress, &st.LastError, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	return st, err
}

func (s *Store) SyncStates(ctx context.Context) (map[int64]map[string]SyncState, error) {
	rows, err := s.Pool.Query(ctx, `SELECT site_id,source,backfilled_from,last_synced_date,status,progress,last_error,updated_at FROM sync_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]SyncState{}
	for rows.Next() {
		var st SyncState
		if err := rows.Scan(&st.SiteID, &st.Source, &st.BackfilledFrom, &st.LastSynced, &st.Status, &st.Progress, &st.LastError, &st.UpdatedAt); err != nil {
			return nil, err
		}
		if out[st.SiteID] == nil {
			out[st.SiteID] = map[string]SyncState{}
		}
		out[st.SiteID][st.Source] = st
	}
	return out, rows.Err()
}

func (s *Store) SaveSyncState(ctx context.Context, st SyncState) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO sync_state(site_id,source,backfilled_from,last_synced_date,status,progress,last_error,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (site_id,source) DO UPDATE SET backfilled_from=EXCLUDED.backfilled_from,last_synced_date=EXCLUDED.last_synced_date,
		status=EXCLUDED.status,progress=EXCLUDED.progress,last_error=EXCLUDED.last_error,updated_at=now()`,
		st.SiteID, st.Source, st.BackfilledFrom, st.LastSynced, st.Status, st.Progress, st.LastError)
	return err
}

func (s *Store) StartRun(ctx context.Context, siteID int64, source string) int64 {
	var id int64
	_ = s.Pool.QueryRow(ctx, `INSERT INTO sync_runs(site_id,source) VALUES($1,$2) RETURNING id`, siteID, source).Scan(&id)
	return id
}

func (s *Store) FinishRun(ctx context.Context, id int64, status string, rows int, msg string) {
	_, _ = s.Pool.Exec(ctx, `UPDATE sync_runs SET finished_at=now(),status=$2,rows=$3,message=$4 WHERE id=$1`, id, status, rows, msg)
}

type SyncRun struct {
	SiteName   string
	Source     string
	StartedAt  time.Time
	FinishedAt *time.Time
	Status     string
	Rows       int
	Message    string
}

func (s *Store) RecentRuns(ctx context.Context, limit int) ([]SyncRun, error) {
	rows, err := s.Pool.Query(ctx, `SELECT st.name,r.source,r.started_at,r.finished_at,r.status,r.rows,r.message
		FROM sync_runs r JOIN sites st ON st.id=r.site_id WHERE st.user_id=$2 ORDER BY r.started_at DESC LIMIT $1`, limit, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SyncRun, error) {
		var x SyncRun
		err := r.Scan(&x.SiteName, &x.Source, &x.StartedAt, &x.FinishedAt, &x.Status, &x.Rows, &x.Message)
		return x, err
	})
}

// ========== 缓存 ==========

func (s *Store) CacheGet(ctx context.Context, key string, v any) bool {
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT payload FROM api_cache WHERE key=$1 AND expires_at>now()`, key).Scan(&raw)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

func (s *Store) CacheSet(ctx context.Context, key string, v any, ttl time.Duration) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = s.Pool.Exec(ctx, `INSERT INTO api_cache(key,payload,expires_at) VALUES($1,$2,now()+make_interval(secs=>$3))
		ON CONFLICT (key) DO UPDATE SET payload=EXCLUDED.payload, expires_at=EXCLUDED.expires_at`, key, raw, ttl.Seconds())
}

// AdSense 关联状态：同步时用 GA Admin API 查一次存进缓存，网站页在还没有广告数据时据此提示“已关联、在等数据”还是“没关联”。
const (
	AdSenseLinked = "linked"
	AdSenseNone   = "none"
)

func adSenseKey(siteID int64) string { return "adsense:" + strconv.FormatInt(siteID, 10) }

func (s *Store) SetAdSenseStatus(ctx context.Context, siteID int64, linked bool, ttl time.Duration) {
	v := AdSenseNone
	if linked {
		v = AdSenseLinked
	}
	s.CacheSet(ctx, adSenseKey(siteID), v, ttl)
}

// AdSenseStatus 返回 AdSenseLinked、AdSenseNone，或者 ""（还没查过 / 缓存过期）。
func (s *Store) AdSenseStatus(ctx context.Context, siteID int64) string {
	var v string
	s.CacheGet(ctx, adSenseKey(siteID), &v)
	return v
}

// HasAdData 表示网站在保留期内有过广告展示。
func (s *Store) HasAdData(ctx context.Context, siteID int64) bool {
	var ok bool
	_ = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ga_daily WHERE site_id=$1 AND ad_impressions > 0)`, siteID).Scan(&ok)
	return ok
}

// CacheDeletePrefix 删掉 key 以 prefix 开头的缓存。
func (s *Store) CacheDeletePrefix(ctx context.Context, prefix string) {
	_, _ = s.Pool.Exec(ctx, `DELETE FROM api_cache WHERE starts_with(key, $1)`, prefix)
}

func (s *Store) CacheClear(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM api_cache`)
	return err
}

// DailyTokenCap 返回设置里的每日 token 上限，0 表示不限。
func (s *Store) DailyTokenCap(ctx context.Context) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s.SettingOr(ctx, KeyAIDailyCap, "")), 10, 64)
	return max(n, 0)
}

// MutedSignals 返回站长关掉的提醒类型。
func (s *Store) MutedSignals(ctx context.Context) []string {
	out := []string{}
	for _, k := range strings.Split(s.SettingOr(ctx, KeySignalsMuted, ""), ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}
