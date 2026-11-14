-- Pingoo 的数据库结构。
-- 多用户：每个用户有自己的网站和 Google 服务账号，网站下面的数据跟着网站走；
-- “全部网站”这类不属于某个网站的记录（洞察、报告、待办、对话、保存的分析）直接记用户。

-- ========== 设置 ==========
-- user_id = 0 是全站设置（管理员在后台改），其余是各用户自己的（Google 服务账号、报告、提醒）。
CREATE TABLE app_settings (
  user_id     bigint NOT NULL DEFAULT 0,
  key         text NOT NULL,
  value       text NOT NULL,
  encrypted   boolean NOT NULL DEFAULT false,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);

-- ========== AI 模型（管理员维护） ==========
-- 价格是服务商的原价（元 / 百万 tokens），倍率是加价；1 元 = 100 积分。
CREATE TABLE ai_models (
  id           bigserial PRIMARY KEY,
  name         text NOT NULL,                       -- 给用户看的名字
  model        text NOT NULL,                       -- 接口里的模型 ID
  base_url     text NOT NULL,
  api_key      text NOT NULL DEFAULT '',            -- 加密保存
  price_in     double precision NOT NULL DEFAULT 0,
  price_out    double precision NOT NULL DEFAULT 0,
  price_cache  double precision NOT NULL DEFAULT 0,
  multiplier   double precision NOT NULL DEFAULT 1,
  extra_params text NOT NULL DEFAULT '',             -- 附加到每一次请求里的参数（JSON 对象），服务商特有的开关写在这里
  thinking_style text NOT NULL DEFAULT 'auto',       -- 这家服务商控制思考的写法：auto（按接口地址判断）/ none / effort / switch / qwen / openrouter
  task_effort  text NOT NULL DEFAULT '',             -- 各类任务的思考强度（JSON 对象，例如 {"insight":"off","diagnose":"high"}），没写的用程序里的默认值
  enabled      boolean NOT NULL DEFAULT true,
  is_default   boolean NOT NULL DEFAULT false,
  created_at   timestamptz NOT NULL DEFAULT now()
);

-- ========== 用户与登录 ==========
CREATE TABLE users (
  id             bigserial PRIMARY KEY,
  email          text NOT NULL UNIQUE,              -- 小写
  pass_hash      text NOT NULL DEFAULT '',
  is_admin       boolean NOT NULL DEFAULT false,
  credits        bigint NOT NULL DEFAULT 0,         -- 积分余额：实际用量超过预扣时按实际扣，可能为负（充值先抵扣）
  model_id       bigint REFERENCES ai_models(id) ON DELETE SET NULL, -- 选用的 AI 模型，NULL 表示用默认模型
  auto_ai        boolean NOT NULL DEFAULT false,    -- 每天自动生成洞察、按设置生成周报月报（会消耗积分）
  disabled       boolean NOT NULL DEFAULT false,
  demo           boolean NOT NULL DEFAULT false,    -- 共享的只读演示账号
  created_at     timestamptz NOT NULL DEFAULT now(),
  last_login_at  timestamptz,                       -- 从没登录过：初始密码还没用过
  plan           text NOT NULL DEFAULT 'free',      -- 套餐：free / basic / pro（各档的数值在后台设置里）
  plan_expires_at timestamptz,                      -- 付费套餐到期时间；过期后按免费版算（数据另有 7 天宽限期）
  plan_credits_at timestamptz                       -- 上次发套餐每月积分的时间，满 30 天再发
);

CREATE TABLE sessions (
  id            text PRIMARY KEY,            -- 会话令牌的 SHA-256
  user_id       bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_agent    text NOT NULL DEFAULT '',
  ip            text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  ttl_seconds   int NOT NULL DEFAULT 0     -- 有效期长度：活跃时按这个长度往后延（滑动续期）
);

-- ========== 网站 ==========
CREATE TABLE sites (
  id             bigserial PRIMARY KEY,
  user_id        bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           text NOT NULL,
  domain         text NOT NULL DEFAULT '',
  ga_property    text NOT NULL DEFAULT '',     -- properties/123456
  gsc_site       text NOT NULL DEFAULT '',     -- sc-domain:example.com 或 https://example.com/
  timezone       text NOT NULL DEFAULT 'Asia/Shanghai',
  description    text NOT NULL DEFAULT '',     -- 给 AI 的背景
  exclude_paths  text[] NOT NULL DEFAULT '{}',
  demo           boolean NOT NULL DEFAULT false,
  sort_order     int NOT NULL DEFAULT 0,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  signal_level   text NOT NULL DEFAULT 'normal',  -- 提醒灵敏度：low（少提醒）/ normal / high（多提醒）
  currency       text NOT NULL DEFAULT ''         -- GA 媒体资源的货币（广告收入的单位），同步时从 GA 的报表里读
);
CREATE INDEX sites_user ON sites (user_id, sort_order);

-- ========== GA 每日数据（按网站自己的时区） ==========
CREATE TABLE ga_daily (
  site_id             bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date                date NOT NULL,
  active_users        int NOT NULL DEFAULT 0,   -- 只用于画趋势，不跨天相加
  new_users           int NOT NULL DEFAULT 0,
  returning_users     int NOT NULL DEFAULT 0,
  sessions            int NOT NULL DEFAULT 0,
  engaged_sessions    int NOT NULL DEFAULT 0,
  page_views          int NOT NULL DEFAULT 0,
  engagement_seconds  double precision NOT NULL DEFAULT 0,
  ad_revenue          double precision NOT NULL DEFAULT 0,  -- 网站上的广告收入（GA4 关联 AdSense 后才有）
  ad_impressions      int NOT NULL DEFAULT 0,
  ad_clicks           int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date)
);

CREATE TABLE ga_daily_pages (
  site_id             bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date                date NOT NULL,
  path                text NOT NULL,
  title               text NOT NULL DEFAULT '',
  page_views          int NOT NULL DEFAULT 0,
  users               int NOT NULL DEFAULT 0,
  engagement_seconds  double precision NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, path)
);
CREATE INDEX ga_daily_pages_path ON ga_daily_pages (site_id, path, date);

CREATE TABLE ga_daily_landing (
  site_id           bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date              date NOT NULL,
  path              text NOT NULL,
  channel           text NOT NULL,
  source            text NOT NULL,
  sessions          int NOT NULL DEFAULT 0,
  engaged_sessions  int NOT NULL DEFAULT 0,
  new_users         int NOT NULL DEFAULT 0,
  page_views        int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, path, channel, source)
);
CREATE INDEX ga_daily_landing_path ON ga_daily_landing (site_id, path, date);

CREATE TABLE ga_daily_sources (
  site_id             bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date                date NOT NULL,
  channel             text NOT NULL,
  source              text NOT NULL,
  medium              text NOT NULL,
  campaign            text NOT NULL,
  sessions            int NOT NULL DEFAULT 0,
  engaged_sessions    int NOT NULL DEFAULT 0,
  new_users           int NOT NULL DEFAULT 0,
  page_views          int NOT NULL DEFAULT 0,
  engagement_seconds  double precision NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, channel, source, medium, campaign)
);
CREATE INDEX ga_daily_sources_source ON ga_daily_sources (site_id, source, date);

CREATE TABLE ga_daily_geo (
  site_id           bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date              date NOT NULL,
  country           text NOT NULL,
  sessions          int NOT NULL DEFAULT 0,
  engaged_sessions  int NOT NULL DEFAULT 0,
  users             int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, country)
);

CREATE TABLE ga_daily_tech (
  site_id             bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date                date NOT NULL,
  device              text NOT NULL,
  browser             text NOT NULL,
  sessions            int NOT NULL DEFAULT 0,
  engaged_sessions    int NOT NULL DEFAULT 0,
  page_views          int NOT NULL DEFAULT 0,
  engagement_seconds  double precision NOT NULL DEFAULT 0,
  users               int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, device, browser)
);

CREATE TABLE ga_hourly (
  site_id   bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date      date NOT NULL,
  hour      smallint NOT NULL,
  sessions  int NOT NULL DEFAULT 0,
  users     int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, hour)
);

-- ========== Search Console 每日数据 ==========
-- 平均排名 = Σ pos_x_impr / Σ impressions（按曝光加权）
CREATE TABLE gsc_daily (
  site_id      bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date         date NOT NULL,
  clicks       int NOT NULL DEFAULT 0,
  impressions  int NOT NULL DEFAULT 0,
  pos_x_impr   double precision NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date)
);

CREATE TABLE gsc_daily_pages (
  site_id      bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date         date NOT NULL,
  page         text NOT NULL,
  path         text NOT NULL,
  clicks       int NOT NULL DEFAULT 0,
  impressions  int NOT NULL DEFAULT 0,
  pos_x_impr   double precision NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, page)
);
CREATE INDEX gsc_daily_pages_path ON gsc_daily_pages (site_id, path, date);

CREATE TABLE gsc_daily_queries (
  site_id      bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date         date NOT NULL,
  query        text NOT NULL,
  page         text NOT NULL,
  path         text NOT NULL,
  clicks       int NOT NULL DEFAULT 0,
  impressions  int NOT NULL DEFAULT 0,
  pos_x_impr   double precision NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, query, page)
);
CREATE INDEX gsc_daily_queries_query ON gsc_daily_queries (site_id, query, date);
CREATE INDEX gsc_daily_queries_path ON gsc_daily_queries (site_id, path, date);

-- ========== 同步 ==========
CREATE TABLE sync_state (
  site_id           bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  source            text NOT NULL,              -- ga / gsc
  backfilled_from   date,
  last_synced_date  date,
  status            text NOT NULL DEFAULT 'pending',  -- pending / running / ok / error
  progress          int NOT NULL DEFAULT 0,     -- 回填进度 0~100
  last_error        text NOT NULL DEFAULT '',
  updated_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, source)
);

CREATE TABLE sync_runs (
  id           bigserial PRIMARY KEY,
  site_id      bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  source       text NOT NULL,
  started_at   timestamptz NOT NULL DEFAULT now(),
  finished_at  timestamptz,
  status       text NOT NULL DEFAULT 'running',
  rows         int NOT NULL DEFAULT 0,
  message      text NOT NULL DEFAULT ''
);
CREATE INDEX sync_runs_recent ON sync_runs (started_at DESC);

-- ========== 分析结果 ==========
CREATE TABLE signals (
  id            bigserial PRIMARY KEY,
  site_id       bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  kind          text NOT NULL,
  severity      text NOT NULL,       -- critical / warn / chance / good / info
  title         text NOT NULL,
  evidence      jsonb NOT NULL DEFAULT '{}',
  event_date    date NOT NULL,
  dedupe_key    text NOT NULL UNIQUE,
  dismissed_at  timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX signals_open ON signals (site_id, event_date DESC) WHERE dismissed_at IS NULL;

CREATE TABLE insights (
  id            bigserial PRIMARY KEY,
  user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id       bigint REFERENCES sites(id) ON DELETE CASCADE,   -- NULL 表示全部网站
  range_key     text NOT NULL,
  period_start  date NOT NULL,
  period_end    date NOT NULL,
  status        text NOT NULL DEFAULT 'running',
  content       jsonb,
  trace         text NOT NULL DEFAULT '',
  model         text NOT NULL DEFAULT '',
  error         text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX insights_latest ON insights (site_id, range_key, created_at DESC);

CREATE TABLE diagnoses (
  id           bigserial PRIMARY KEY,
  site_id      bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  title        text NOT NULL,
  target       jsonb NOT NULL DEFAULT '{}',
  status       text NOT NULL DEFAULT 'running',
  steps        jsonb NOT NULL DEFAULT '[]',
  result       jsonb,
  trace        text NOT NULL DEFAULT '',
  model        text NOT NULL DEFAULT '',
  error        text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now(),
  finished_at  timestamptz
);

CREATE TABLE reports (
  id            bigserial PRIMARY KEY,
  user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id       bigint REFERENCES sites(id) ON DELETE CASCADE,   -- NULL 表示全站汇总
  kind          text NOT NULL,       -- weekly / monthly / custom
  period_start  date NOT NULL,
  period_end    date NOT NULL,
  status        text NOT NULL DEFAULT 'running',
  content       jsonb,
  snapshot      jsonb,
  trace         text NOT NULL DEFAULT '',
  model         text NOT NULL DEFAULT '',
  tokens_in     int NOT NULL DEFAULT 0,
  tokens_out    int NOT NULL DEFAULT 0,
  error         text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  finished_at   timestamptz
);
CREATE INDEX reports_recent ON reports (period_end DESC, created_at DESC);
CREATE INDEX reports_user ON reports (user_id, period_end DESC);

CREATE TABLE todos (
  id          bigserial PRIMARY KEY,
  user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id     bigint REFERENCES sites(id) ON DELETE CASCADE,
  source      text NOT NULL DEFAULT '',     -- insight / report
  source_id   bigint NOT NULL DEFAULT 0,
  text        text NOT NULL,
  why         text NOT NULL DEFAULT '',
  status      text NOT NULL DEFAULT 'open', -- open / done
  done_at     timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- ========== AI 问答 ==========
CREATE TABLE conversations (
  id          bigserial PRIMARY KEY,
  user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id     bigint REFERENCES sites(id) ON DELETE SET NULL,
  title       text NOT NULL DEFAULT '新对话',
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE messages (
  id               bigserial PRIMARY KEY,
  conversation_id  bigint NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  role             text NOT NULL,          -- user / assistant
  content          text NOT NULL DEFAULT '',
  steps            jsonb NOT NULL DEFAULT '[]',
  followups        jsonb NOT NULL DEFAULT '[]',
  trace            text NOT NULL DEFAULT '',
  tool_log         jsonb NOT NULL DEFAULT '[]',    -- 这一轮的工具调用和结果（原样），下一个问题接着用；对话太长时旧的会被清空，只留 trace
  status           text NOT NULL DEFAULT 'done',   -- running / done / error
  error            text NOT NULL DEFAULT '',
  created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messages_conv ON messages (conversation_id, id);
CREATE INDEX conversations_user ON conversations (user_id, updated_at DESC);

-- ========== 其他 ==========
-- 积分流水：充值、赠送、管理员调整、每次 AI 调用都记一行。credits 是变动量（消耗为负）。
-- AI 任务开始时先按上限预扣（pending），结束后改成实际消耗、退回多余；失败全额退回。
CREATE TABLE ledger (
  id                 bigserial PRIMARY KEY,
  user_id            bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind               text NOT NULL,     -- insight / diagnose / chat / report / test；redeem / bonus / admin
  model              text NOT NULL DEFAULT '',
  prompt_tokens      int NOT NULL DEFAULT 0,
  completion_tokens  int NOT NULL DEFAULT 0,
  cached_tokens      int NOT NULL DEFAULT 0,     -- 命中提示缓存的输入 token
  credits            bigint NOT NULL DEFAULT 0,
  pending            boolean NOT NULL DEFAULT false,
  note               text NOT NULL DEFAULT '',
  created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ledger_user ON ledger (user_id, id DESC);
CREATE INDEX ledger_time ON ledger (created_at);

CREATE TABLE api_cache (
  key         text PRIMARY KEY,
  payload     jsonb NOT NULL,
  expires_at  timestamptz NOT NULL
);

-- ========== GA 配额：每次查询都会顺带返回，记下最近一次的数字给设置页展示 ==========
CREATE TABLE ga_quota (
  property            text PRIMARY KEY,
  day_consumed        int NOT NULL DEFAULT 0,
  day_remaining       int NOT NULL DEFAULT 0,
  hour_consumed       int NOT NULL DEFAULT 0,
  hour_remaining      int NOT NULL DEFAULT 0,
  updated_at          timestamptz NOT NULL DEFAULT now()
);

-- ========== 从问答、诊断里“保存到报告”的分析，下一期报告会附上 ==========
CREATE TABLE notes (
  id                 bigserial PRIMARY KEY,
  user_id            bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id            bigint REFERENCES sites(id) ON DELETE CASCADE,
  source             text NOT NULL,         -- chat / diagnosis
  source_id          bigint NOT NULL,
  title              text NOT NULL,
  content            text NOT NULL,
  used_in_report_id  bigint REFERENCES reports(id) ON DELETE SET NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source, source_id)
);

-- ========== 图上的标注：Google 搜索算法更新、搜索故障（来自 Google 搜索状态页） ==========
CREATE TABLE annotations (
  id          bigserial PRIMARY KEY,
  site_id     bigint REFERENCES sites(id) ON DELETE CASCADE,   -- NULL 表示所有网站
  source      text NOT NULL,          -- google
  ext_id      text NOT NULL,
  date        date NOT NULL,
  end_date    date,
  label       text NOT NULL,          -- 图上的短标签
  detail      text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source, ext_id)
);
CREATE INDEX annotations_date ON annotations (date);

-- ========== 网站上的广告收益（GA4 关联 AdSense 后才有；没有广告收入的网站一行都没有） ==========
CREATE TABLE ga_daily_ads (
  site_id      bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date         date NOT NULL,
  path         text NOT NULL,
  revenue      double precision NOT NULL DEFAULT 0,
  impressions  int NOT NULL DEFAULT 0,
  clicks       int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, path)
);

-- ========== 访客语言 ==========
CREATE TABLE ga_daily_lang (
  site_id           bigint NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  date              date NOT NULL,
  language          text NOT NULL,
  sessions          int NOT NULL DEFAULT 0,
  engaged_sessions  int NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, date, language)
);

-- ========== 卡密：只存哈希，原文只在生成时导出一次 ==========
CREATE TABLE redeem_codes (
  code_hash   text PRIMARY KEY,
  credits     bigint NOT NULL,                -- 积分卡密的面额；套餐卡密为 0
  plan        text NOT NULL DEFAULT '',       -- 套餐卡密：basic / pro，开通或续期 days 天
  days        int NOT NULL DEFAULT 0,
  batch       text NOT NULL DEFAULT '',
  expires_at  timestamptz,
  used_by     bigint REFERENCES users(id) ON DELETE SET NULL,
  used_at     timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX redeem_codes_batch ON redeem_codes (batch, created_at);

-- ========== 发信记录：发信限额、全站熔断都按它算，后台也能查 ==========
CREATE TABLE mail_log (
  id          bigserial PRIMARY KEY,
  kind        text NOT NULL,          -- register / reset / email_code / notice
  email       text NOT NULL,
  ip          text NOT NULL DEFAULT '',
  user_id     bigint,
  ok          boolean NOT NULL DEFAULT true,
  error       text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mail_log_email ON mail_log (email, created_at);
CREATE INDEX mail_log_ip ON mail_log (ip, created_at);
CREATE INDEX mail_log_time ON mail_log (created_at);

-- ========== 同步数据表的空间回收 ==========
-- 这几张表每天都有成段的“先删后插”（重新同步最近几天、删除过期的日子）。默认要等 20% 的行被删掉才自动 VACUUM，
-- 这期间新数据只能往表尾追加，表文件会被撑大好几倍（实测 5 倍）。改成 2%：空出来的位置很快就能给新数据用。
-- 已经撑大的表用 pingoo compact 一次性收回。
ALTER TABLE ga_daily_pages    SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
ALTER TABLE ga_daily_landing  SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
ALTER TABLE ga_daily_sources  SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
ALTER TABLE ga_daily_geo      SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
ALTER TABLE ga_daily_tech     SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
ALTER TABLE ga_hourly         SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
ALTER TABLE ga_daily_lang     SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
ALTER TABLE gsc_daily_pages   SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
ALTER TABLE gsc_daily_queries SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
ALTER TABLE ga_daily_ads      SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.05);
