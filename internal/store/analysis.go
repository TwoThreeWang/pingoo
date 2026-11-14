package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// ========== 信号（“需要关注”） ==========

type Signal struct {
	ID        int64
	SiteID    int64
	SiteName  string
	Kind      string
	Severity  string
	Title     string
	Evidence  map[string]any
	EventDate time.Time
	CreatedAt time.Time
}

var SeverityRank = map[string]int{"critical": 0, "warn": 1, "chance": 2, "good": 3, "info": 4}

// ownSite 是插入“可能挂在某个网站下”的记录时的条件：$1 是用户，$2 是网站（可以为空）。
// 网站不是这个用户的，就什么都不插入——表单里被改过的网站 ID 不会让记录挂到别人的网站下。
const ownSite = `($2::bigint IS NULL OR EXISTS (SELECT 1 FROM sites WHERE id=$2 AND user_id=$1))`

func (s *Store) UpsertSignal(ctx context.Context, x Signal, dedupe string) error {
	ev, _ := json.Marshal(x.Evidence)
	_, err := s.Pool.Exec(ctx, `INSERT INTO signals(site_id,kind,severity,title,evidence,event_date,dedupe_key)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (dedupe_key) DO UPDATE SET severity=EXCLUDED.severity,title=EXCLUDED.title,evidence=EXCLUDED.evidence`,
		x.SiteID, x.Kind, x.Severity, x.Title, ev, x.EventDate, dedupe)
	return err
}

// OpenSignals 返回最近 days 天内未忽略的信号；siteID 为 0 表示全部网站。站长在设置里关掉的类型不返回。
func (s *Store) OpenSignals(ctx context.Context, siteID int64, days int) ([]Signal, error) {
	rows, err := s.Pool.Query(ctx, `SELECT g.id,g.site_id,st.name,g.kind,g.severity,g.title,g.evidence,g.event_date,g.created_at
		FROM signals g JOIN sites st ON st.id=g.site_id
		WHERE g.dismissed_at IS NULL AND g.event_date >= current_date - $2::int AND ($1=0 OR g.site_id=$1)
		  AND NOT (g.kind = ANY($3::text[])) AND st.user_id=$4
		ORDER BY CASE g.severity WHEN 'critical' THEN 0 WHEN 'warn' THEN 1 WHEN 'chance' THEN 2 WHEN 'good' THEN 3 ELSE 4 END, g.event_date DESC, g.id DESC`,
		siteID, days, s.MutedSignals(ctx), UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Signal, error) {
		var x Signal
		var ev []byte
		err := r.Scan(&x.ID, &x.SiteID, &x.SiteName, &x.Kind, &x.Severity, &x.Title, &ev, &x.EventDate, &x.CreatedAt)
		_ = json.Unmarshal(ev, &x.Evidence)
		return x, err
	})
}

func (s *Store) DismissSignal(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE signals SET dismissed_at=now() WHERE id=$1 AND site_id IN (SELECT id FROM sites WHERE user_id=$2)`, id, UserID(ctx))
	return err
}

func (s *Store) SignalByID(ctx context.Context, id int64) (Signal, error) {
	var x Signal
	var ev []byte
	err := s.Pool.QueryRow(ctx, `SELECT g.id,g.site_id,st.name,g.kind,g.severity,g.title,g.evidence,g.event_date,g.created_at
		FROM signals g JOIN sites st ON st.id=g.site_id WHERE g.id=$1 AND st.user_id=$2`, id, UserID(ctx)).
		Scan(&x.ID, &x.SiteID, &x.SiteName, &x.Kind, &x.Severity, &x.Title, &ev, &x.EventDate, &x.CreatedAt)
	_ = json.Unmarshal(ev, &x.Evidence)
	return x, notFound(err)
}

// ========== AI 洞察 ==========

type Insight struct {
	ID          int64
	SiteID      *int64
	RangeKey    string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Status      string
	Content     json.RawMessage
	Trace       string
	Model       string
	Error       string
	CreatedAt   time.Time
}

func (s *Store) CreateInsight(ctx context.Context, siteID *int64, rangeKey string, start, end time.Time) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO insights(user_id,site_id,range_key,period_start,period_end) SELECT $1,$2,$3,$4,$5
		WHERE `+ownSite+` RETURNING id`, UserID(ctx), siteID, rangeKey, start, end).Scan(&id)
	return id, notFound(err)
}

func (s *Store) FinishInsight(ctx context.Context, id int64, content any, trace, model, errMsg string) error {
	var raw []byte
	if content != nil {
		raw, _ = json.Marshal(content)
	}
	status := "done"
	if errMsg != "" {
		status = "error"
	}
	_, err := s.Pool.Exec(ctx, `UPDATE insights SET status=$2,content=$3,trace=$4,model=$5,error=$6 WHERE id=$1`, id, status, raw, trace, model, errMsg)
	return err
}

// LatestInsight 返回某个网站（nil 表示全部网站）某个日期范围最新的一条洞察。
func (s *Store) LatestInsight(ctx context.Context, siteID *int64, rangeKey string) (Insight, error) {
	var x Insight
	var content []byte
	err := s.Pool.QueryRow(ctx, `SELECT id,site_id,range_key,period_start,period_end,status,content,trace,model,error,created_at
		FROM insights WHERE user_id=$3 AND site_id IS NOT DISTINCT FROM $1 AND range_key=$2 ORDER BY created_at DESC LIMIT 1`, siteID, rangeKey, UserID(ctx)).
		Scan(&x.ID, &x.SiteID, &x.RangeKey, &x.PeriodStart, &x.PeriodEnd, &x.Status, &content, &x.Trace, &x.Model, &x.Error, &x.CreatedAt)
	x.Content = content
	return x, notFound(err)
}

// InsightFailStreak 返回某个网站（nil 表示全部网站）某个日期范围的洞察已经连续失败了几次：
// 最近一次成功之后（从没成功过就是全部）有几条失败的记录。
func (s *Store) InsightFailStreak(ctx context.Context, siteID *int64, rangeKey string) (n int) {
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM insights i
		WHERE i.user_id=$3 AND i.site_id IS NOT DISTINCT FROM $1 AND i.range_key=$2 AND i.status='error'
		  AND i.created_at > coalesce((SELECT max(d.created_at) FROM insights d
		      WHERE d.user_id=$3 AND d.site_id IS NOT DISTINCT FROM $1 AND d.range_key=$2 AND d.status='done'), '-infinity')`,
		siteID, rangeKey, UserID(ctx)).Scan(&n)
	return n
}

// ========== 诊断 ==========

type Step struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Done   bool   `json:"done"`
}

type Diagnosis struct {
	ID         int64
	SiteID     int64
	Title      string
	Target     map[string]any
	Status     string
	Steps      []Step
	Result     json.RawMessage
	Trace      string
	Model      string
	Error      string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

func (s *Store) CreateDiagnosis(ctx context.Context, siteID int64, title string, target map[string]any) (int64, error) {
	t, _ := json.Marshal(target)
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO diagnoses(site_id,title,target) SELECT $2,$3,$4
		WHERE EXISTS (SELECT 1 FROM sites WHERE id=$2 AND user_id=$1) RETURNING id`, UserID(ctx), siteID, title, t).Scan(&id)
	return id, notFound(err)
}

func (s *Store) SetDiagnosisSteps(ctx context.Context, id int64, steps []Step) {
	raw, _ := json.Marshal(steps)
	_, _ = s.Pool.Exec(ctx, `UPDATE diagnoses SET steps=$2 WHERE id=$1`, id, raw)
}

func (s *Store) FinishDiagnosis(ctx context.Context, id int64, steps []Step, result any, trace, model, errMsg string) error {
	st, _ := json.Marshal(steps)
	var res []byte
	if result != nil {
		res, _ = json.Marshal(result)
	}
	status := "done"
	if errMsg != "" {
		status = "error"
	}
	_, err := s.Pool.Exec(ctx, `UPDATE diagnoses SET status=$2,steps=$3,result=$4,trace=$5,model=$6,error=$7,finished_at=now() WHERE id=$1`,
		id, status, st, res, trace, model, errMsg)
	return err
}

func (s *Store) Diagnosis(ctx context.Context, id int64) (Diagnosis, error) {
	var x Diagnosis
	var target, steps, result []byte
	err := s.Pool.QueryRow(ctx, `SELECT id,site_id,title,target,status,steps,result,trace,model,error,created_at,finished_at FROM diagnoses
		WHERE id=$1 AND site_id IN (SELECT id FROM sites WHERE user_id=$2)`, id, UserID(ctx)).
		Scan(&x.ID, &x.SiteID, &x.Title, &target, &x.Status, &steps, &result, &x.Trace, &x.Model, &x.Error, &x.CreatedAt, &x.FinishedAt)
	_ = json.Unmarshal(target, &x.Target)
	_ = json.Unmarshal(steps, &x.Steps)
	x.Result = result
	return x, notFound(err)
}

// LatestDiagnosis 返回某个网站最近的一份诊断（演示账号点诊断时，给它看预先写好的示例）。
func (s *Store) LatestDiagnosis(ctx context.Context, siteID int64) (Diagnosis, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `SELECT d.id FROM diagnoses d JOIN sites st ON st.id=d.site_id WHERE d.site_id=$1 AND st.user_id=$2 ORDER BY d.id DESC LIMIT 1`,
		siteID, UserID(ctx)).Scan(&id)
	if err != nil {
		return Diagnosis{}, notFound(err)
	}
	return s.Diagnosis(ctx, id)
}

// ========== 报告 ==========

type Report struct {
	ID          int64
	SiteID      *int64
	SiteName    string
	Kind        string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Status      string
	Content     json.RawMessage
	Snapshot    json.RawMessage
	Trace       string
	Model       string
	TokensIn    int
	TokensOut   int
	Error       string
	CreatedAt   time.Time
	FinishedAt  *time.Time
}

const reportCols = `r.id,r.site_id,coalesce(st.name,''),r.kind,r.period_start,r.period_end,r.status,r.content,r.snapshot,r.trace,r.model,r.tokens_in,r.tokens_out,r.error,r.created_at,r.finished_at`

func scanReport(row pgx.Row) (Report, error) {
	var x Report
	var c, sn []byte
	err := row.Scan(&x.ID, &x.SiteID, &x.SiteName, &x.Kind, &x.PeriodStart, &x.PeriodEnd, &x.Status, &c, &sn, &x.Trace, &x.Model, &x.TokensIn, &x.TokensOut, &x.Error, &x.CreatedAt, &x.FinishedAt)
	x.Content, x.Snapshot = c, sn
	return x, err
}

func (s *Store) CreateReport(ctx context.Context, siteID *int64, kind string, start, end time.Time) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO reports(user_id,site_id,kind,period_start,period_end) SELECT $1,$2,$3,$4,$5
		WHERE `+ownSite+` RETURNING id`, UserID(ctx), siteID, kind, start, end).Scan(&id)
	return id, notFound(err)
}

func (s *Store) FinishReport(ctx context.Context, id int64, content, snapshot any, trace, model string, in, out int, errMsg string) error {
	var c, sn []byte
	if content != nil {
		c, _ = json.Marshal(content)
	}
	if snapshot != nil {
		sn, _ = json.Marshal(snapshot)
	}
	status := "done"
	if errMsg != "" {
		status = "error"
	}
	_, err := s.Pool.Exec(ctx, `UPDATE reports SET status=$2,content=$3,snapshot=$4,trace=$5,model=$6,tokens_in=$7,tokens_out=$8,error=$9,finished_at=now() WHERE id=$1`,
		id, status, c, sn, trace, model, in, out, errMsg)
	return err
}

func (s *Store) Report(ctx context.Context, id int64) (Report, error) {
	x, err := scanReport(s.Pool.QueryRow(ctx, `SELECT `+reportCols+` FROM reports r LEFT JOIN sites st ON st.id=r.site_id WHERE r.id=$1 AND r.user_id=$2`, id, UserID(ctx)))
	return x, notFound(err)
}

// Reports 列出报告；siteFilter: -1 全部，0 只看全站汇总，>0 某个网站；kind 为空表示全部类型。
func (s *Store) Reports(ctx context.Context, siteFilter int64, kind string, limit int) ([]Report, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+reportCols+` FROM reports r LEFT JOIN sites st ON st.id=r.site_id
		WHERE r.user_id=$4 AND ($1=-1 OR ($1=0 AND r.site_id IS NULL) OR r.site_id=$1) AND ($2='' OR r.kind=$2)
		ORDER BY r.period_end DESC, r.site_id NULLS FIRST, r.created_at DESC LIMIT $3`, siteFilter, kind, limit, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Report, error) { return scanReport(r) })
}

// ReportAttempts 返回某一期报告已有几份（生成中或已完成）、失败过几次、最近一次失败是什么时候。
// 调度器用它决定要不要自动生成：已经有了就不生成，失败了要隔一段时间再试，试几次还不行就不再试。
func (s *Store) ReportAttempts(ctx context.Context, siteID *int64, kind string, start time.Time) (have, failed int, lastFail time.Time, err error) {
	var t *time.Time
	err = s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status<>'error'), count(*) FILTER (WHERE status='error'),
		max(created_at) FILTER (WHERE status='error')
		FROM reports WHERE user_id=$4 AND site_id IS NOT DISTINCT FROM $1 AND kind=$2 AND period_start=$3`, siteID, kind, start, UserID(ctx)).Scan(&have, &failed, &t)
	if t != nil {
		lastFail = *t
	}
	return
}

// PreviousReport 返回同一范围、同一类型、更早的一份已完成报告。
func (s *Store) PreviousReport(ctx context.Context, siteID *int64, kind string, before time.Time) (Report, error) {
	x, err := scanReport(s.Pool.QueryRow(ctx, `SELECT `+reportCols+` FROM reports r LEFT JOIN sites st ON st.id=r.site_id
		WHERE r.user_id=$4 AND r.site_id IS NOT DISTINCT FROM $1 AND r.kind=$2 AND r.period_start < $3 AND r.status='done' ORDER BY r.period_start DESC LIMIT 1`, siteID, kind, before, UserID(ctx)))
	return x, notFound(err)
}

// ========== 待办 ==========

type Todo struct {
	ID        int64
	SiteID    *int64
	SiteName  string
	Source    string
	SourceID  int64
	Text      string
	Why       string
	Status    string
	DoneAt    *time.Time
	CreatedAt time.Time
}

func (s *Store) AddTodo(ctx context.Context, t Todo) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO todos(user_id,site_id,source,source_id,text,why) SELECT $1,$2,$3,$4,$5,$6
		WHERE `+ownSite+` RETURNING id`, UserID(ctx), t.SiteID, t.Source, t.SourceID, t.Text, t.Why).Scan(&id)
	return id, notFound(err)
}

func (s *Store) SetTodoDone(ctx context.Context, id int64, done bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE todos SET status=CASE WHEN $2 THEN 'done' ELSE 'open' END, done_at=CASE WHEN $2 THEN now() ELSE NULL END
		WHERE id=$1 AND user_id=$3`, id, done, UserID(ctx))
	return err
}

// Todos 列出待办。siteID 为 0 表示全部；sinceDays>0 时只返回这段时间内创建或完成的。
func (s *Store) Todos(ctx context.Context, siteID int64, source string, sourceID int64) ([]Todo, error) {
	rows, err := s.Pool.Query(ctx, `SELECT t.id,t.site_id,coalesce(st.name,''),t.source,t.source_id,t.text,t.why,t.status,t.done_at,t.created_at
		FROM todos t LEFT JOIN sites st ON st.id=t.site_id
		WHERE t.user_id=$4 AND ($1=0 OR t.site_id=$1) AND ($2='' OR t.source=$2) AND ($3=0 OR t.source_id=$3) ORDER BY t.id`, siteID, source, sourceID, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanTodo)
}

func (s *Store) TodosCreatedBetween(ctx context.Context, siteID *int64, from, to time.Time) ([]Todo, error) {
	rows, err := s.Pool.Query(ctx, `SELECT t.id,t.site_id,coalesce(st.name,''),t.source,t.source_id,t.text,t.why,t.status,t.done_at,t.created_at
		FROM todos t LEFT JOIN sites st ON st.id=t.site_id
		WHERE t.user_id=$4 AND ($1::bigint IS NULL OR t.site_id=$1) AND t.created_at >= $2 AND t.created_at < $3 ORDER BY t.id`, siteID, from, to, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanTodo)
}

func scanTodo(r pgx.CollectableRow) (Todo, error) {
	var x Todo
	err := r.Scan(&x.ID, &x.SiteID, &x.SiteName, &x.Source, &x.SourceID, &x.Text, &x.Why, &x.Status, &x.DoneAt, &x.CreatedAt)
	return x, err
}

// ========== 对话 ==========

type Conversation struct {
	ID        int64
	SiteID    *int64
	SiteName  string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Message struct {
	ID             int64
	ConversationID int64
	Role           string
	Content        string
	Steps          []Step
	Followups      []string
	Trace          string
	Status         string
	Error          string
	CreatedAt      time.Time
}

func (s *Store) CreateConversation(ctx context.Context, siteID *int64, title string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO conversations(user_id,site_id,title) SELECT $1,$2,$3
		WHERE `+ownSite+` RETURNING id`, UserID(ctx), siteID, title).Scan(&id)
	return id, notFound(err)
}

func (s *Store) Conversations(ctx context.Context, q string, limit int) ([]Conversation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT c.id,c.site_id,coalesce(st.name,''),c.title,c.created_at,c.updated_at
		FROM conversations c LEFT JOIN sites st ON st.id=c.site_id
		WHERE c.user_id=$3 AND ($1='' OR c.title ILIKE '%'||$1||'%') ORDER BY c.updated_at DESC LIMIT $2`, q, limit, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Conversation, error) {
		var x Conversation
		err := r.Scan(&x.ID, &x.SiteID, &x.SiteName, &x.Title, &x.CreatedAt, &x.UpdatedAt)
		return x, err
	})
}

func (s *Store) Conversation(ctx context.Context, id int64) (Conversation, error) {
	var x Conversation
	err := s.Pool.QueryRow(ctx, `SELECT c.id,c.site_id,coalesce(st.name,''),c.title,c.created_at,c.updated_at
		FROM conversations c LEFT JOIN sites st ON st.id=c.site_id WHERE c.id=$1 AND c.user_id=$2`, id, UserID(ctx)).
		Scan(&x.ID, &x.SiteID, &x.SiteName, &x.Title, &x.CreatedAt, &x.UpdatedAt)
	return x, notFound(err)
}

func (s *Store) SetConversationSite(ctx context.Context, id int64, siteID *int64) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE conversations SET site_id=$2 WHERE id=$3 AND user_id=$1 AND `+ownSite, UserID(ctx), siteID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteConversation(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM conversations WHERE id=$1 AND user_id=$2`, id, UserID(ctx))
	return err
}

const msgCols = `id,conversation_id,role,content,steps,followups,trace,status,error,created_at`

func scanMessage(r pgx.Row) (Message, error) {
	var x Message
	var steps, fu []byte
	err := r.Scan(&x.ID, &x.ConversationID, &x.Role, &x.Content, &steps, &fu, &x.Trace, &x.Status, &x.Error, &x.CreatedAt)
	_ = json.Unmarshal(steps, &x.Steps)
	_ = json.Unmarshal(fu, &x.Followups)
	return x, err
}

func (s *Store) Messages(ctx context.Context, convID int64) ([]Message, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+msgCols+` FROM messages WHERE conversation_id=$1
		AND conversation_id IN (SELECT id FROM conversations WHERE user_id=$2) ORDER BY id`, convID, UserID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) { return scanMessage(r) })
}

func (s *Store) Message(ctx context.Context, id int64) (Message, error) {
	m, err := scanMessage(s.Pool.QueryRow(ctx, `SELECT `+msgCols+` FROM messages WHERE id=$1
		AND conversation_id IN (SELECT id FROM conversations WHERE user_id=$2)`, id, UserID(ctx)))
	return m, notFound(err)
}

func (s *Store) AddMessage(ctx context.Context, m Message) (int64, error) {
	if m.Status == "" {
		m.Status = "done"
	}
	steps, _ := json.Marshal(nonNil(m.Steps))
	fu, _ := json.Marshal(nonNilS(m.Followups))
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,role,content,steps,followups,trace,status,error) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		m.ConversationID, m.Role, m.Content, steps, fu, m.Trace, m.Status, m.Error).Scan(&id)
	if err == nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE conversations SET updated_at=now() WHERE id=$1`, m.ConversationID)
	}
	return id, err
}

func (s *Store) UpdateMessage(ctx context.Context, m Message) error {
	steps, _ := json.Marshal(nonNil(m.Steps))
	fu, _ := json.Marshal(nonNilS(m.Followups))
	_, err := s.Pool.Exec(ctx, `UPDATE messages SET content=$2,steps=$3,followups=$4,trace=$5,status=$6,error=$7 WHERE id=$1`,
		m.ID, m.Content, steps, fu, m.Trace, m.Status, m.Error)
	return err
}

// ToolLogs 返回对话里每条回答留下的工具往来（消息 id → 原样的 JSON 数组），没有的不在结果里。
// 单独读：它比正文大得多，页面显示对话时用不着。
func (s *Store) ToolLogs(ctx context.Context, convID int64) (map[int64]json.RawMessage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, tool_log FROM messages WHERE conversation_id=$1 AND tool_log <> '[]'::jsonb
		AND conversation_id IN (SELECT id FROM conversations WHERE user_id=$2)`, convID, UserID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]json.RawMessage{}
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		out[id] = raw
	}
	return out, rows.Err()
}

// SetToolLog 保存一条回答的工具往来；raw 为空表示清掉（对话太长时旧的只留 trace）。
func (s *Store) SetToolLog(ctx context.Context, msgID int64, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage("[]")
	}
	_, err := s.Pool.Exec(ctx, `UPDATE messages SET tool_log=$2 WHERE id=$1
		AND conversation_id IN (SELECT id FROM conversations WHERE user_id=$3)`, msgID, []byte(raw), UserID(ctx))
	return err
}

func nonNil(s []Step) []Step {
	if s == nil {
		return []Step{}
	}
	return s
}

func nonNilS(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
