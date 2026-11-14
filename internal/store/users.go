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

	"pingoo/internal/llm"
	"pingoo/internal/secret"
)

// ========== 当前用户 ==========
//
// 用户 ID 跟着 context 走：网页请求由登录中间件放进去，后台任务从网站或对话的主人那里取。
// 所有按用户区分的查询都从 context 里取 ID 并写进 WHERE；没有用户（ID 为 0）时什么都查不到。
// 这样漏传用户只会“查不到”，不会查到别人的数据。

type userKey struct{}

func WithUser(ctx context.Context, uid int64) context.Context {
	return context.WithValue(ctx, userKey{}, uid)
}

func UserID(ctx context.Context) int64 {
	id, _ := ctx.Value(userKey{}).(int64)
	return id
}

// ========== 用户 ==========

type User struct {
	ID            int64
	Email         string
	PassHash      string
	IsAdmin       bool
	Credits       int64
	ModelID       *int64
	AutoAI        bool
	Disabled      bool
	Demo          bool
	CreatedAt     time.Time
	LastLoginAt   *time.Time
	Plan          string     // 套餐记录：free / basic / pro（是否还有效看 PlanID）
	PlanExpiresAt *time.Time // 付费套餐的到期时间
	PlanCreditsAt *time.Time // 上次发套餐每月积分的时间
	Sites         int        // 只在后台用户列表里填
}

const userCols = `id,email,pass_hash,is_admin,credits,model_id,auto_ai,disabled,demo,created_at,last_login_at,plan,plan_expires_at,plan_credits_at`

func scanUser(r pgx.Row) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.Email, &u.PassHash, &u.IsAdmin, &u.Credits, &u.ModelID, &u.AutoAI, &u.Disabled, &u.Demo, &u.CreatedAt, &u.LastLoginAt,
		&u.Plan, &u.PlanExpiresAt, &u.PlanCreditsAt)
	return u, notFound(err)
}

func NormEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func (s *Store) User(ctx context.Context, id int64) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email=$1`, NormEmail(email)))
}

// CreateUser 新建用户并送新用户积分（全站设置里的“注册赠送积分”）。
func (s *Store) CreateUser(ctx context.Context, email, passHash string, bonus int64) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO users(email,pass_hash,credits) VALUES($1,$2,$3) RETURNING id`, NormEmail(email), passHash, max(bonus, 0)).Scan(&id); err != nil {
		return 0, err
	}
	if bonus > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO ledger(user_id,kind,credits,note) VALUES($1,'bonus',$2,'注册赠送')`, id, bonus); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

func (s *Store) SetPassword(ctx context.Context, uid int64, passHash string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET pass_hash=$2 WHERE id=$1`, uid, passHash)
	return err
}

func (s *Store) MarkLogin(ctx context.Context, uid int64) {
	_, _ = s.Pool.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, uid)
}

func (s *Store) SetEmail(ctx context.Context, uid int64, email string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET email=$2 WHERE id=$1`, uid, NormEmail(email))
	return err
}

func (s *Store) SetUserPrefs(ctx context.Context, uid int64, modelID *int64, autoAI bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET model_id=$2, auto_ai=$3 WHERE id=$1`, uid, modelID, autoAI)
	return err
}

func (s *Store) SetDisabled(ctx context.Context, uid int64, disabled bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET disabled=$2 WHERE id=$1 AND NOT is_admin`, uid, disabled)
	if err == nil && disabled {
		_, err = s.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, uid)
	}
	return err
}

// DeleteUser 注销账号：网站、同步下来的数据、报告、对话、流水都随外键级联删除。管理员不能注销自己。
func (s *Store) DeleteUser(ctx context.Context, uid int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1 AND NOT is_admin`, uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("管理员账号不能注销")
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM app_settings WHERE user_id=$1`, uid)
	return err
}

// EnsureAdmin 在启动时确保有管理员：一个都没有（第一次启动）就用 ADMIN_EMAIL / ADMIN_PASSWORD 建一个。
// 已经有管理员就什么都不做，之后改密码、改邮箱都在网页里。
func (s *Store) EnsureAdmin(ctx context.Context, email, password string) error {
	var has bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE is_admin)`).Scan(&has); err != nil || has {
		return err
	}
	// 这个邮箱已经注册过普通账号时直接提升为管理员（密码不变）
	_, err := s.Pool.Exec(ctx, `INSERT INTO users(email,pass_hash,is_admin) VALUES ($1,$2,true) ON CONFLICT (email) DO UPDATE SET is_admin=true`,
		NormEmail(email), secret.HashPassword(password))
	return err
}

// Users 是后台的用户列表，q 按邮箱模糊搜索。
func (s *Store) Users(ctx context.Context, q string, limit int) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+prefixed("u.", userCols)+`, (SELECT count(*) FROM sites WHERE user_id=u.id)
		FROM users u WHERE NOT u.demo AND ($1='' OR u.email ILIKE '%'||$1||'%') ORDER BY u.id DESC LIMIT $2`, strings.TrimSpace(q), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) {
		var u User
		err := r.Scan(&u.ID, &u.Email, &u.PassHash, &u.IsAdmin, &u.Credits, &u.ModelID, &u.AutoAI, &u.Disabled, &u.Demo, &u.CreatedAt, &u.LastLoginAt, &u.Sites)
		return u, err
	})
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i := range parts {
		parts[i] = p + parts[i]
	}
	return strings.Join(parts, ",")
}

// LastSeen 返回用户最近一次打开 Pingoo 的时间（没有有效的登录会话时是零值）。
func (s *Store) LastSeen(ctx context.Context, uid int64) (t time.Time) {
	var v *time.Time
	if s.Pool.QueryRow(ctx, `SELECT max(last_seen_at) FROM sessions WHERE user_id=$1`, uid).Scan(&v) == nil && v != nil {
		t = *v
	}
	return t
}

// AutoAIUsers 返回开了“自动生成 AI 内容”、并且还有积分的用户。
func (s *Store) AutoAIUsers(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+userCols+` FROM users WHERE auto_ai AND NOT disabled AND NOT demo AND credits > 0`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) { return scanUser(r) })
}

// ========== AI 模型 ==========

type AIModel struct {
	ID                            int64
	Name, Model, BaseURL, APIKey  string
	PriceIn, PriceOut, PriceCache float64 // 元 / 百万 tokens（服务商原价）
	Multiplier                    float64
	ExtraParams                   string // 附加到每次请求里的参数（JSON 对象），空表示没有
	ThinkingStyle                 string // 这家服务商控制思考的写法（llm.ThinkingStyles），auto 表示按接口地址判断
	TaskEffort                    string // 各类任务的思考强度（JSON 对象，例如 {"insight":"off"}），没写的用 DefaultEffort
	Enabled, IsDefault            bool
}

// DefaultEffort 是各类任务默认的思考强度。思考的内容按输出价格计费，常常占一次任务一半以上的花费，
// 所以只在真的需要推理的任务上花：
//   - 洞察：数据已经全写在提示里，只是归纳成几条结论，不需要思考。
//   - 问答：大多是查几个数再回答，少想一点就够。
//   - 诊断、报告：要自己决定查什么、把几份数据对起来找原因，正常思考。
//   - repair：只是把已经写好的结论整理成 JSON，不需要思考。
var DefaultEffort = map[string]string{
	"insight": llm.EffortOff, "chat": llm.EffortLow, "diagnose": llm.EffortMedium, "report": llm.EffortMedium, "repair": llm.EffortOff,
}

// EffortTasks 是后台可以单独设思考强度的任务，按页面上的顺序。
var EffortTasks = []struct{ Key, Label string }{{"insight", "洞察"}, {"chat", "问答"}, {"diagnose", "诊断"}, {"report", "报告"}}

// Effort 返回这个模型做某类任务时的思考强度：管理员设了就用他设的，否则用默认值。
func (m AIModel) Effort(kind string) string {
	var set map[string]string
	if strings.TrimSpace(m.TaskEffort) != "" && json.Unmarshal([]byte(m.TaskEffort), &set) == nil {
		if e := llm.ValidEffort(set[kind]); e != "" {
			return e
		}
	}
	return DefaultEffort[kind]
}

// KeepReasoning 表示这家服务商要求把思考过程原样传回去（见 llm.KeepsReasoning）。
func (m AIModel) KeepReasoning() bool { return llm.KeepsReasoning(m.ThinkingStyle, m.BaseURL) }

// CacheCheap 表示命中提示缓存的输入几乎不要钱（不到原价的 5%，例如 MiMo 是 2%），或者模型本身免费。
// 这时多带几轮之前查到的数据很划算；缓存价是原价 10%～20% 的服务商（通义千问、DeepSeek）就要少带。
func (m AIModel) CacheCheap() bool { return m.PriceIn <= 0 || m.PriceCache <= 0.05*m.PriceIn }

// Thinking 返回做某类任务时要带的思考参数（这家服务商不控制思考时是 nil）。
func (m AIModel) Thinking(kind string) map[string]any {
	return llm.ThinkingParams(m.ThinkingStyle, m.BaseURL, m.Effort(kind))
}

// Extra 把附加参数解析成 map（格式不对时返回 nil，保存时已经校验过）。
func (m AIModel) Extra() map[string]any {
	if strings.TrimSpace(m.ExtraParams) == "" {
		return nil
	}
	var v map[string]any
	if json.Unmarshal([]byte(m.ExtraParams), &v) != nil {
		return nil
	}
	return v
}

// CreditsPerYuan：1 元 = 100 积分。
const CreditsPerYuan = 100

// Cost 按实际用量算出要扣多少积分（四舍五入；有用量时至少 1 积分）。
// 不向上取整：一次任务通常只有两三个积分，2.01 进成 3 等于多收一半；四舍五入的话多收和少收长期下来互相抵消。
func (m AIModel) Cost(in, out, cached int) int64 {
	// 先舍到 6 位小数再取整：0.145 × 100 这类数在浮点里可能是 14.499999999999998，会舍错方向
	c := int64(math.Round(math.Round(m.RawCost(in, out, cached)*1e6) / 1e6))
	if c < 1 && in+out > 0 {
		c = 1
	}
	return c
}

// RawCost 是没有取整的积分数（可以是小数，免费模型是 0），只用来比较用量，不用来扣费。
func (m AIModel) RawCost(in, out, cached int) float64 {
	cached = min(max(cached, 0), in)
	yuan := (float64(in-cached)*m.PriceIn + float64(cached)*m.PriceCache + float64(out)*m.PriceOut) / 1e6
	return yuan * m.Multiplier * CreditsPerYuan
}

// taskTokens 是各类任务预扣时按的用量上限（输入、输出）。洞察最多查 6 次，诊断最多查 12 次，问答和报告最多 10 轮。
var taskTokens = map[string][2]int{
	"insight": {60000, 15000}, "chat": {100000, 20000}, "diagnose": {120000, 40000}, "report": {150000, 30000}, "test": {2000, 1000},
}

// Hold 返回一类任务开始前要预扣的积分。
func (m AIModel) Hold(kind string) int64 {
	t, ok := taskTokens[kind]
	if !ok {
		t = taskTokens["chat"]
	}
	return max(m.Cost(t[0], t[1], 0), 1)
}

const modelCols = `id,name,model,base_url,api_key,price_in,price_out,price_cache,multiplier,extra_params,thinking_style,task_effort,enabled,is_default`

func (s *Store) scanModel(r pgx.Row) (AIModel, error) {
	var m AIModel
	err := r.Scan(&m.ID, &m.Name, &m.Model, &m.BaseURL, &m.APIKey, &m.PriceIn, &m.PriceOut, &m.PriceCache, &m.Multiplier, &m.ExtraParams, &m.ThinkingStyle, &m.TaskEffort, &m.Enabled, &m.IsDefault)
	if err == nil && m.APIKey != "" {
		if k, derr := s.Box.Decrypt(m.APIKey); derr == nil {
			m.APIKey = k
		} else {
			m.APIKey = ""
		}
	}
	return m, notFound(err)
}

// Models 列出模型；onlyEnabled 时只给用户能选的。
func (s *Store) Models(ctx context.Context, onlyEnabled bool) ([]AIModel, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+modelCols+` FROM ai_models WHERE NOT $1 OR enabled ORDER BY is_default DESC, multiplier, id`, onlyEnabled)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AIModel, error) { return s.scanModel(r) })
}

func (s *Store) Model(ctx context.Context, id int64) (AIModel, error) {
	return s.scanModel(s.Pool.QueryRow(ctx, `SELECT `+modelCols+` FROM ai_models WHERE id=$1`, id))
}

// ModelFor 返回用户选的模型；没选、或者选的被停用了，就用默认模型。都没有时返回 ErrNotFound。
func (s *Store) ModelFor(ctx context.Context, uid int64) (AIModel, error) {
	return s.scanModel(s.Pool.QueryRow(ctx, `SELECT `+prefixed("m.", modelCols)+` FROM ai_models m
		WHERE m.enabled ORDER BY (m.id = (SELECT model_id FROM users WHERE id=$1)) DESC, m.is_default DESC, m.id LIMIT 1`, uid))
}

// SaveModel 新建或修改模型；apiKey 为空表示不改原来的 Key。设成默认模型时取消其他模型的默认。
func (s *Store) SaveModel(ctx context.Context, m AIModel) (int64, error) {
	key := ""
	if m.APIKey != "" {
		var err error
		if key, err = s.Box.Encrypt(m.APIKey); err != nil {
			return 0, err
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if m.ID == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO ai_models(name,model,base_url,api_key,price_in,price_out,price_cache,multiplier,enabled,is_default,extra_params,thinking_style,task_effort)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
			m.Name, m.Model, m.BaseURL, key, m.PriceIn, m.PriceOut, m.PriceCache, m.Multiplier, m.Enabled, m.IsDefault, m.ExtraParams, m.ThinkingStyle, m.TaskEffort).Scan(&m.ID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE ai_models SET name=$2,model=$3,base_url=$4,api_key=CASE WHEN $5='' THEN api_key ELSE $5 END,
			price_in=$6,price_out=$7,price_cache=$8,multiplier=$9,enabled=$10,is_default=$11,extra_params=$12,thinking_style=$13,task_effort=$14 WHERE id=$1`,
			m.ID, m.Name, m.Model, m.BaseURL, key, m.PriceIn, m.PriceOut, m.PriceCache, m.Multiplier, m.Enabled, m.IsDefault, m.ExtraParams, m.ThinkingStyle, m.TaskEffort)
	}
	if err != nil {
		return 0, err
	}
	if m.IsDefault {
		if _, err := tx.Exec(ctx, `UPDATE ai_models SET is_default=false WHERE id<>$1`, m.ID); err != nil {
			return 0, err
		}
	}
	return m.ID, tx.Commit(ctx)
}

func (s *Store) DeleteModel(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM ai_models WHERE id=$1`, id)
	return err
}

// ========== 积分 ==========

var ErrNoCredits = errors.New("积分不足")

// Reserve 在 AI 任务开始前预扣积分：余额不够 hold 时返回 ErrNoCredits。
// 返回的流水 ID 交给 Settle 结算。预扣只是“先押一笔”，最终按实际用量收。
func (s *Store) Reserve(ctx context.Context, uid int64, kind, model string, hold int64) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE users SET credits = credits - $2 WHERE id=$1 AND credits >= $2 AND credits > 0 AND NOT disabled AND NOT demo`, uid, hold)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrNoCredits
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO ledger(user_id,kind,model,credits,pending) VALUES($1,$2,$3,$4,true) RETURNING id`,
		uid, kind, model, -hold).Scan(&id); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// Settle 结算一次预扣：按实际消耗 charge 收，多退少补。实际用量超过预扣时照样按实际扣，
// 余额可能因此变成负数（下次充值先抵扣，余额不为正时不能再用 AI）。任务失败时传 charge = 0，全额退回。
func (s *Store) Settle(ctx context.Context, ledgerID int64, in, out, cached int, charge int64, note string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var uid, held int64
	err = tx.QueryRow(ctx, `SELECT user_id, -credits FROM ledger WHERE id=$1 AND pending FOR UPDATE`, ledgerID).Scan(&uid, &held)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // 已经结算过
	}
	if err != nil {
		return err
	}
	charge = max(charge, 0)
	if charge > held {
		note = strings.TrimPrefix(note+"；", "；") + fmt.Sprintf("实际用量超过预扣 %d 积分，按实际扣", charge-held)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET credits = credits + $2 WHERE id=$1`, uid, held-charge); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE ledger SET pending=false, credits=$2, prompt_tokens=$3, completion_tokens=$4, cached_tokens=$5, note=$6 WHERE id=$1`,
		ledgerID, -charge, in, out, cached, note); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RefundPending 在启动时退回上次进程退出时还没结算的预扣。
func (s *Store) RefundPending(ctx context.Context) (n int64) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM ledger WHERE pending`)
	if err != nil {
		return 0
	}
	ids, _ := pgx.CollectRows(rows, pgx.RowTo[int64])
	for _, id := range ids {
		if s.Settle(ctx, id, 0, 0, 0, 0, "服务重启，任务中断，已全额退回") == nil {
			n++
		}
	}
	return n
}

// AddCredits 给用户加（或减）积分并记一笔流水；减的时候最多减到 0（余额已经是负数时不再减）。
func (s *Store) AddCredits(ctx context.Context, uid, delta int64, kind, note string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := addCredits(ctx, tx, uid, delta, kind, note); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func addCredits(ctx context.Context, tx pgx.Tx, uid, delta int64, kind, note string) error {
	var old int64
	if err := tx.QueryRow(ctx, `SELECT credits FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&old); err != nil {
		return notFound(err)
	}
	applied := delta
	if delta < 0 {
		applied = max(delta, -max(old, 0))
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET credits=$2 WHERE id=$1`, uid, old+applied); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO ledger(user_id,kind,credits,note) VALUES($1,$2,$3,$4)`, uid, kind, applied, note)
	return err
}

type LedgerRow struct {
	ID                int64
	Kind, Model, Note string
	In, Out, Cached   int
	Credits           int64
	Pending           bool
	CreatedAt         time.Time
}

// Ledger 返回当前用户最近 limit 条积分流水。
func (s *Store) Ledger(ctx context.Context, limit int) ([]LedgerRow, error) {
	rows, _, err := s.LedgerPage(ctx, 0, limit)
	return rows, err
}

// LedgerPage 按 id 倒序分页返回当前用户的积分流水，从 id < before 开始（before<=0 表示最新一页）。
// 多取一条用来判断还有没有更早的记录：more 为 true 时，最后一条的 id 就是下一页的游标。
func (s *Store) LedgerPage(ctx context.Context, before int64, limit int) (out []LedgerRow, more bool, err error) {
	if before <= 0 {
		before = math.MaxInt64
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,kind,model,note,prompt_tokens,completion_tokens,cached_tokens,credits,pending,created_at
		FROM ledger WHERE user_id=$1 AND id<$2 ORDER BY id DESC LIMIT $3`, UserID(ctx), before, limit+1)
	if err != nil {
		return nil, false, err
	}
	out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (LedgerRow, error) {
		var x LedgerRow
		err := r.Scan(&x.ID, &x.Kind, &x.Model, &x.Note, &x.In, &x.Out, &x.Cached, &x.Credits, &x.Pending, &x.CreatedAt)
		return x, err
	})
	if err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		out, more = out[:limit], true
	}
	return out, more, nil
}

type UsageRow struct {
	Purpose         string
	Calls           int
	In, Out, Cached int64
	Credits         int64 // 消耗的积分（正数）
}

var aiKinds = []string{"insight", "diagnose", "chat", "report", "test"}

// UsageSince 按用途统计当前用户某个时间点之后的 AI 用量。
func (s *Store) UsageSince(ctx context.Context, since time.Time) ([]UsageRow, error) {
	return s.usage(ctx, "kind", since, UserID(ctx))
}

// AvgTaskTokens 返回最近一段时间里，各类 AI 任务成功一次平均用多少 token（输入、输出、命中缓存的输入），全站统计。
// 次数少于 minRuns 的类型不返回，免得一两次特别长的问答让帮助页上的“大概多少积分”失真。
func (s *Store) AvgTaskTokens(ctx context.Context, since time.Time, minRuns int) (map[string][3]int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT kind, avg(prompt_tokens)::int, avg(completion_tokens)::int, avg(cached_tokens)::int
		FROM ledger WHERE kind = ANY($1) AND NOT pending AND credits < 0 AND created_at >= $2 GROUP BY kind HAVING count(*) >= $3`, aiKinds, since, minRuns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][3]int{}
	for rows.Next() {
		var k string
		var t [3]int
		if err := rows.Scan(&k, &t[0], &t[1], &t[2]); err != nil {
			return nil, err
		}
		out[k] = t
	}
	return out, rows.Err()
}

// AdminEmail 返回接收用户反馈的邮箱（最早的那个管理员）。
func (s *Store) AdminEmail(ctx context.Context) (string, error) {
	var e string
	err := s.Pool.QueryRow(ctx, `SELECT email FROM users WHERE is_admin AND NOT disabled ORDER BY id LIMIT 1`).Scan(&e)
	return e, notFound(err)
}

// UsageByModel 是后台用的：所有用户按模型统计（uid 传 0）。
func (s *Store) UsageByModel(ctx context.Context, since time.Time) ([]UsageRow, error) {
	return s.usage(ctx, "model", since, 0)
}

// usage 按 group（kind 或 model）汇总 AI 用量；uid 为 0 表示全站。
func (s *Store) usage(ctx context.Context, group string, since time.Time, uid int64) ([]UsageRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+group+`,count(*),coalesce(sum(prompt_tokens),0),coalesce(sum(completion_tokens),0),coalesce(sum(cached_tokens),0),coalesce(-sum(credits),0)
		FROM ledger WHERE kind = ANY($1) AND NOT pending AND created_at >= $2 AND ($3=0 OR user_id=$3) GROUP BY 1 ORDER BY 6 DESC, 3 DESC`, aiKinds, since, uid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (UsageRow, error) {
		var x UsageRow
		err := r.Scan(&x.Purpose, &x.Calls, &x.In, &x.Out, &x.Cached, &x.Credits)
		return x, err
	})
}

// TokensSince 返回全站某个时间点之后一共用了多少 token（输入 + 输出），用于全站每日上限。
func (s *Store) TokensSince(ctx context.Context, since time.Time) (n int64) {
	_ = s.Pool.QueryRow(ctx, `SELECT coalesce(sum(prompt_tokens + completion_tokens),0) FROM ledger WHERE created_at >= $1`, since).Scan(&n)
	return n
}

// ========== 卡密 ==========

// CreateCodes 生成一批卡密，返回原文（只在这时能拿到，数据库里只存哈希）。
// CreateCodes 生成一批卡密：积分卡密传 credits（plan 为空）；套餐卡密传 plan 和 days（credits 为 0）。
func (s *Store) CreateCodes(ctx context.Context, n int, credits int64, plan string, days int, batch string, expires *time.Time) ([]string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	codes := make([]string, 0, n)
	for len(codes) < n {
		c := secret.RedeemCode()
		tag, err := tx.Exec(ctx, `INSERT INTO redeem_codes(code_hash,credits,plan,days,batch,expires_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
			secret.Hash(c), credits, plan, days, batch, expires)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 1 {
			codes = append(codes, c)
		}
	}
	return codes, tx.Commit(ctx)
}

var ErrBadCode = errors.New("卡密无效、已经用过或已过期")

// Redeem 兑换卡密：积分卡密加积分，套餐卡密开通、续期或升级套餐（见 applyPlan）。同一张卡密并发兑换只会成功一次。
func (s *Store) Redeem(ctx context.Context, uid int64, code string) (RedeemResult, error) {
	code = secret.NormalizeCode(code)
	if code == "" {
		return RedeemResult{}, ErrBadCode
	}
	cfg := s.Plans(ctx)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return RedeemResult{}, err
	}
	defer tx.Rollback(ctx)
	var credits int64
	var plan string
	var days int
	err = tx.QueryRow(ctx, `UPDATE redeem_codes SET used_by=$2, used_at=now()
		WHERE code_hash=$1 AND used_at IS NULL AND (expires_at IS NULL OR expires_at > now()) RETURNING credits, plan, days`,
		secret.Hash(code), uid).Scan(&credits, &plan, &days)
	if errors.Is(err, pgx.ErrNoRows) {
		return RedeemResult{}, ErrBadCode
	}
	if err != nil {
		return RedeemResult{}, err
	}
	var res RedeemResult
	if plan != "" {
		if res, err = applyPlan(ctx, tx, cfg, uid, plan, days, time.Now()); err != nil {
			return res, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO ledger(user_id,kind,note) VALUES($1,'plan',$2)`, uid,
			fmt.Sprintf("卡密 %s…：%s %d 天", code[:8], cfg.Get(plan).Name, days))
	} else {
		res.Credits = credits
		err = addCredits(ctx, tx, uid, credits, "redeem", "卡密 "+code[:8]+"…")
	}
	if err != nil {
		return res, err
	}
	return res, tx.Commit(ctx)
}

type CodeBatch struct {
	Batch       string
	Credits     int64
	Plan        string
	Days        int
	Total, Used int
	ExpiresAt   *time.Time
	CreatedAt   time.Time
}

func (s *Store) CodeBatches(ctx context.Context) ([]CodeBatch, error) {
	rows, err := s.Pool.Query(ctx, `SELECT batch, max(credits), max(plan), max(days), count(*), count(used_at), max(expires_at), min(created_at)
		FROM redeem_codes GROUP BY batch ORDER BY min(created_at) DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CodeBatch, error) {
		var x CodeBatch
		err := r.Scan(&x.Batch, &x.Credits, &x.Plan, &x.Days, &x.Total, &x.Used, &x.ExpiresAt, &x.CreatedAt)
		return x, err
	})
}

// DeleteUnusedCodes 作废一批里还没用过的卡密。
func (s *Store) DeleteUnusedCodes(ctx context.Context, batch string) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM redeem_codes WHERE batch=$1 AND used_at IS NULL`, batch)
	return tag.RowsAffected(), err
}

// ========== 发信记录 ==========

// LogMail 在发信前记一条（先记再发，限额按尝试次数算，同时发来的请求也不能一起挤过限额）。
func (s *Store) LogMail(ctx context.Context, kind, email, ip string, uid int64) (id int64) {
	var u *int64
	if uid > 0 {
		u = &uid
	}
	_ = s.Pool.QueryRow(ctx, `INSERT INTO mail_log(kind,email,ip,user_id) VALUES($1,$2,$3,$4) RETURNING id`, kind, NormEmail(email), ip, u).Scan(&id)
	return id
}

func (s *Store) MailFailed(ctx context.Context, id int64, errMsg string) {
	_, _ = s.Pool.Exec(ctx, `UPDATE mail_log SET ok=false, error=$2 WHERE id=$1`, id, errMsg)
}

type MailStat struct {
	Kind      string
	Hour, Day int
	FailedDay int
}

// MailStats 是后台用的：各类邮件最近 1 小时、24 小时发了多少。
func (s *Store) MailStats(ctx context.Context) ([]MailStat, error) {
	rows, err := s.Pool.Query(ctx, `SELECT kind, count(*) FILTER (WHERE created_at > now()-interval '1 hour'), count(*), count(*) FILTER (WHERE NOT ok)
		FROM mail_log WHERE created_at > now()-interval '24 hours' GROUP BY kind ORDER BY 3 DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (MailStat, error) {
		var x MailStat
		err := r.Scan(&x.Kind, &x.Hour, &x.Day, &x.FailedDay)
		return x, err
	})
}

// MailCount 统计 since 之后某类邮件的数量；email、ip、uid 哪个非空就按哪个过滤（都空表示全站）。
func (s *Store) MailCount(ctx context.Context, kind, email, ip string, uid int64, since time.Time) (n int) {
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM mail_log WHERE created_at >= $1 AND ($2='' OR kind=$2)
		AND ($3='' OR email=$3) AND ($4='' OR ip=$4) AND ($5=0 OR user_id=$5)`, since, kind, NormEmail(email), ip, uid).Scan(&n)
	return n
}

// ========== 演示账号 ==========

// DemoEmail 是共享演示账号的邮箱（.invalid 域名，永远不会真的发信）。
const DemoEmail = "demo@pingoo.invalid"

// DemoUserID 返回演示账号的 ID；create 为 true 时没有就建一个（没有密码，只能从“看看演示”进入）。
func (s *Store) DemoUserID(ctx context.Context, create bool) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `SELECT id FROM users WHERE demo LIMIT 1`).Scan(&id)
	if err == nil || !create || !errors.Is(err, pgx.ErrNoRows) {
		return id, notFound(err)
	}
	err = s.Pool.QueryRow(ctx, `INSERT INTO users(email,demo) VALUES($1,true) ON CONFLICT (email) DO UPDATE SET demo=true RETURNING id`, DemoEmail).Scan(&id)
	return id, err
}
