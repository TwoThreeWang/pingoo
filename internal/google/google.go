// Package google 用服务账号直接调用 Google 的 REST 接口：
// GA Data API（报表）、GA Admin API（列出媒体资源）、Search Console API。
// 不引入官方 SDK，只依赖 golang.org/x/oauth2。
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
)

const (
	scopeAnalytics = "https://www.googleapis.com/auth/analytics.readonly"
	scopeGSC       = "https://www.googleapis.com/auth/webmasters.readonly"
)

// Endpoints 可以在测试里替换成本地假服务器。
type Endpoints struct {
	Data       string
	Admin      string
	AdminAlpha string // 只有 v1alpha 才有的 Admin 接口（AdSense 关联）；空表示不查
	GSC        string
}

var DefaultEndpoints = Endpoints{
	Data:       "https://analyticsdata.googleapis.com/v1beta",
	Admin:      "https://analyticsadmin.googleapis.com/v1beta",
	AdminAlpha: "https://analyticsadmin.googleapis.com/v1alpha",
	GSC:        "https://www.googleapis.com/webmasters/v3",
}

type Client struct {
	http  *http.Client
	ep    Endpoints
	Email string // 服务账号邮箱
}

// NewFromJSON 用服务账号 JSON 密钥创建客户端。
func NewFromJSON(ctx context.Context, saJSON string) (*Client, error) {
	var meta struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		ProjectID   string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(saJSON), &meta); err != nil {
		return nil, errors.New("服务账号密钥不是合法的 JSON")
	}
	if meta.Type != "service_account" || meta.ClientEmail == "" {
		return nil, errors.New("这不是服务账号的密钥文件（type 应该是 service_account）")
	}
	cfg, err := jwtConfig(saJSON)
	if err != nil {
		return nil, err
	}
	base := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: apiTransport})
	hc := oauth2.NewClient(base, cfg.TokenSource(base))
	hc.Timeout = 120 * time.Second // 回填时一次 runReport 可能几十秒；给足余量，别半路掐断
	return &Client{http: hc, ep: DefaultEndpoints, Email: meta.ClientEmail}, nil
}

// apiTransport 是所有 Google 调用共用的连接层：开启 HTTP/2 多路复用（多个网站同步时复用少量连接），
// 保持较多空闲长连接免去反复 TLS 握手，并给拨号和握手单独设较短超时，避免卡在建连上。
var apiTransport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: time.Second,
}

// jwtConfig 解析服务账号密钥。换取令牌的地址固定为 Google 的：密钥文件是用户上传的，里面的 token_uri 可以被改成任意地址，
// 不固定的话服务器会替用户去请求内网或云厂商的元数据接口（SSRF），出错信息还会显示在“测试连接”的结果里。
func jwtConfig(saJSON string) (*jwt.Config, error) {
	cfg, err := googleoauth.JWTConfigFromJSON([]byte(saJSON), scopeAnalytics, scopeGSC)
	if err != nil {
		return nil, fmt.Errorf("解析服务账号密钥失败: %w", err)
	}
	cfg.TokenURL = googleoauth.JWTTokenURL
	return cfg, nil
}

// NewWithHTTP 供测试使用。
func NewWithHTTP(hc *http.Client, ep Endpoints) *Client { return &Client{http: hc, ep: ep} }

// ProjectID 从密钥里读项目 ID（设置页展示用）。
func ProjectID(saJSON string) string {
	var meta struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal([]byte(saJSON), &meta)
	return meta.ProjectID
}

// cache 复用每份密钥对应的客户端（复用 access token 和长连接，省去反复换令牌、重新握手）。
// 每个用户上传自己的服务账号，所以按密钥区分。用户变多时保留多份，上限 maxClients，满了淘汰最久没用的。
const maxClients = 512

type cachedClient struct {
	c    *Client
	used time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*cachedClient{}
)

func Cached(ctx context.Context, saJSON string) (*Client, error) {
	cacheMu.Lock()
	if e, ok := cache[saJSON]; ok {
		e.used = time.Now()
		cacheMu.Unlock()
		return e.c, nil
	}
	cacheMu.Unlock()

	c, err := NewFromJSON(ctx, saJSON)
	if err != nil {
		return nil, err
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()
	if e, ok := cache[saJSON]; ok { // 并发时可能别人已经建好了，用那一份
		e.used = time.Now()
		return e.c, nil
	}
	if len(cache) >= maxClients {
		oldest, at := "", time.Time{}
		for k, e := range cache {
			if oldest == "" || e.used.Before(at) {
				oldest, at = k, e.used
			}
		}
		delete(cache, oldest)
	}
	cache[saJSON] = &cachedClient{c: c, used: time.Now()}
	return c, nil
}

// APIError 把 Google 的错误整理成中文可读的提示。
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch e.Status {
	case 401:
		return "Google 认证失败：服务账号密钥可能已失效"
	case 403:
		return "没有权限：请确认服务账号已被加为该媒体资源/网站的用户，并已启用对应的 API（" + e.Message + "）"
	case 429:
		return "Google API 配额用完了，请稍后再试"
	}
	return fmt.Sprintf("Google API 错误 %d: %s", e.Status, e.Message)
}

func (c *Client) do(ctx context.Context, method, u string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 2 * time.Second):
			}
			if body != nil {
				b, _ := json.Marshal(body)
				rd = bytes.NewReader(b)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			lastErr = &APIError{Status: resp.StatusCode, Message: errMessage(raw)}
			continue
		}
		if resp.StatusCode >= 300 {
			return &APIError{Status: resp.StatusCode, Message: errMessage(raw)}
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(raw, out)
	}
	return lastErr
}

func errMessage(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// ========== GA Data API ==========

type DateRange struct {
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Name      string `json:"name,omitempty"`
}

type name struct {
	Name string `json:"name"`
}

type OrderBy struct {
	Metric    *name `json:"metric,omitempty"`
	Dimension *struct {
		DimensionName string `json:"dimensionName"`
	} `json:"dimension,omitempty"`
	Desc bool `json:"desc,omitempty"`
}

// Filter 是 GA dimensionFilter 的简化版：一个维度 + 匹配方式 + 值，可以取反。
type Filter struct {
	Field string // 维度名
	Match string // EXACT / BEGINS_WITH / CONTAINS / ENDS_WITH / FULL_REGEXP
	Value string
	Not   bool
}

type Report struct {
	Dimensions []string
	Metrics    []string
	Rows       []Row
	RowCount   int
	Currency   string // 媒体资源的货币代码（例如 USD），金额类指标的单位
}

type Row struct {
	Dims    []string
	Metrics []float64
}

// ReportRequest 描述一次 runReport 调用。
type ReportRequest struct {
	Ranges     []DateRange
	Dimensions []string
	Metrics    []string
	Filters    []Filter // 多个条件之间是 AND
	OrderBy    string   // 指标名，降序
	Limit      int      // 0 表示自动分页拿全部
}

func filterExpr(fs []Filter) any {
	if len(fs) == 0 {
		return nil
	}
	exprs := make([]any, 0, len(fs))
	for _, f := range fs {
		match := f.Match
		if match == "" {
			match = "EXACT"
		}
		var e any = map[string]any{"filter": map[string]any{
			"fieldName":    f.Field,
			"stringFilter": map[string]any{"matchType": match, "value": f.Value, "caseSensitive": false},
		}}
		if f.Not {
			e = map[string]any{"notExpression": e}
		}
		exprs = append(exprs, e)
	}
	if len(exprs) == 1 {
		return exprs[0]
	}
	return map[string]any{"andGroup": map[string]any{"expressions": exprs}}
}

func names(ss []string) []name {
	out := make([]name, len(ss))
	for i, s := range ss {
		out[i] = name{Name: s}
	}
	return out
}

// ErrNoEndpoint 表示这个客户端没配对应的接口地址（测试里常见），调用方当作“不知道”处理。
var ErrNoEndpoint = errors.New("没有配置这个 Google 接口的地址")

// AdSenseLinks 返回 GA4 媒体资源关联的 AdSense 发布商代码（例如 ca-pub-…）；没关联时返回空。
// property 形如 properties/123。
func (c *Client) AdSenseLinks(ctx context.Context, property string) ([]string, error) {
	if c.ep.AdminAlpha == "" {
		return nil, ErrNoEndpoint
	}
	var resp struct {
		Links []struct {
			AdClientCode string `json:"adClientCode"`
		} `json:"adsenseLinks"`
	}
	if err := c.do(ctx, "GET", c.ep.AdminAlpha+"/"+property+"/adSenseLinks", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.Links))
	for _, l := range resp.Links {
		out = append(out, l.AdClientCode)
	}
	return out, nil
}

// RunReport 调用 properties/{id}:runReport，必要时自动分页。
func (c *Client) RunReport(ctx context.Context, property string, r ReportRequest) (*Report, error) {
	const page = 25000
	out := &Report{Dimensions: r.Dimensions, Metrics: r.Metrics}
	offset := 0
	for {
		limit := page
		if r.Limit > 0 && r.Limit-len(out.Rows) < page {
			limit = r.Limit - len(out.Rows)
		}
		body := map[string]any{
			"dateRanges":          r.Ranges,
			"dimensions":          names(r.Dimensions),
			"metrics":             names(r.Metrics),
			"limit":               limit,
			"offset":              offset,
			"keepEmptyRows":       false,
			"returnPropertyQuota": true,
		}
		if f := filterExpr(r.Filters); f != nil {
			body["dimensionFilter"] = f
		}
		if r.OrderBy != "" {
			body["orderBys"] = []map[string]any{{"metric": map[string]string{"metricName": r.OrderBy}, "desc": true}}
		}
		var resp struct {
			DimensionHeaders []name `json:"dimensionHeaders"`
			MetricHeaders    []name `json:"metricHeaders"`
			Rows             []struct {
				DimensionValues []struct {
					Value string `json:"value"`
				} `json:"dimensionValues"`
				MetricValues []struct {
					Value string `json:"value"`
				} `json:"metricValues"`
			} `json:"rows"`
			RowCount      int    `json:"rowCount"`
			PropertyQuota *Quota `json:"propertyQuota"`
			Metadata      struct {
				CurrencyCode string `json:"currencyCode"`
			} `json:"metadata"`
		}
		if err := c.do(ctx, "POST", c.ep.Data+"/"+property+":runReport", body, &resp); err != nil {
			return nil, err
		}
		if resp.PropertyQuota != nil && QuotaHook != nil {
			QuotaHook(property, *resp.PropertyQuota)
		}
		if resp.Metadata.CurrencyCode != "" {
			out.Currency = resp.Metadata.CurrencyCode
		}
		if len(resp.DimensionHeaders) > 0 {
			out.Dimensions = out.Dimensions[:0]
			for _, h := range resp.DimensionHeaders {
				out.Dimensions = append(out.Dimensions, h.Name)
			}
		}
		for _, row := range resp.Rows {
			x := Row{}
			for _, d := range row.DimensionValues {
				x.Dims = append(x.Dims, d.Value)
			}
			for _, m := range row.MetricValues {
				f, _ := strconv.ParseFloat(m.Value, 64)
				x.Metrics = append(x.Metrics, f)
			}
			out.Rows = append(out.Rows, x)
		}
		out.RowCount = resp.RowCount
		offset += len(resp.Rows)
		if len(resp.Rows) == 0 || offset >= resp.RowCount || (r.Limit > 0 && len(out.Rows) >= r.Limit) {
			return out, nil
		}
	}
}

// Quota 是 GA Data API 每次查询顺带返回的配额状态（每个媒体资源独立计算）。
type Quota struct {
	TokensPerDay  QuotaStatus `json:"tokensPerDay"`
	TokensPerHour QuotaStatus `json:"tokensPerHour"`
}

type QuotaStatus struct {
	Consumed  int `json:"consumed"`
	Remaining int `json:"remaining"`
}

// QuotaHook 在每次拿到配额信息时被调用（由 main 设置，用来记到数据库）。
var QuotaHook func(property string, q Quota)

// Realtime 返回过去 30 分钟的活跃用户数。
func (c *Client) Realtime(ctx context.Context, property string) (int, error) {
	var resp struct {
		Rows []struct {
			MetricValues []struct {
				Value string `json:"value"`
			} `json:"metricValues"`
		} `json:"rows"`
	}
	body := map[string]any{"metrics": []name{{Name: "activeUsers"}}}
	if err := c.do(ctx, "POST", c.ep.Data+"/"+property+":runRealtimeReport", body, &resp); err != nil {
		return 0, err
	}
	if len(resp.Rows) == 0 || len(resp.Rows[0].MetricValues) == 0 {
		return 0, nil
	}
	n, _ := strconv.Atoi(resp.Rows[0].MetricValues[0].Value)
	return n, nil
}

// ========== GA Admin API ==========

type Property struct {
	Property    string // properties/123
	DisplayName string
	Account     string
	TimeZone    string
	Domain      string // 第一个网页数据流的域名
}

// Properties 列出服务账号能访问的全部 GA4 媒体资源，并补全时区和域名。
func (c *Client) Properties(ctx context.Context) ([]Property, error) {
	var out []Property
	pageToken := ""
	for {
		u := c.ep.Admin + "/accountSummaries?pageSize=200"
		if pageToken != "" {
			u += "&pageToken=" + url.QueryEscape(pageToken)
		}
		var resp struct {
			AccountSummaries []struct {
				DisplayName       string `json:"displayName"`
				PropertySummaries []struct {
					Property    string `json:"property"`
					DisplayName string `json:"displayName"`
				} `json:"propertySummaries"`
			} `json:"accountSummaries"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.do(ctx, "GET", u, nil, &resp); err != nil {
			return nil, err
		}
		for _, a := range resp.AccountSummaries {
			for _, p := range a.PropertySummaries {
				out = append(out, Property{Property: p.Property, DisplayName: p.DisplayName, Account: a.DisplayName})
			}
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	for i := range out {
		var p struct {
			TimeZone string `json:"timeZone"`
		}
		if err := c.do(ctx, "GET", c.ep.Admin+"/"+out[i].Property, nil, &p); err == nil {
			out[i].TimeZone = p.TimeZone
		}
		var ds struct {
			DataStreams []struct {
				WebStreamData struct {
					DefaultURI string `json:"defaultUri"`
				} `json:"webStreamData"`
			} `json:"dataStreams"`
		}
		if err := c.do(ctx, "GET", c.ep.Admin+"/"+out[i].Property+"/dataStreams", nil, &ds); err == nil {
			for _, s := range ds.DataStreams {
				if h := Host(s.WebStreamData.DefaultURI); h != "" {
					out[i].Domain = h
					break
				}
			}
		}
	}
	return out, nil
}

// Host 从 URL 或 sc-domain: 前缀里取出不带 www 的域名。
func Host(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "sc-domain:") {
		return strings.TrimPrefix(strings.TrimPrefix(s, "sc-domain:"), "www.")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// ========== Search Console API ==========

type GSCSite struct {
	SiteURL    string
	Permission string
}

func (c *Client) GSCSites(ctx context.Context) ([]GSCSite, error) {
	var resp struct {
		SiteEntry []struct {
			SiteURL         string `json:"siteUrl"`
			PermissionLevel string `json:"permissionLevel"`
		} `json:"siteEntry"`
	}
	if err := c.do(ctx, "GET", c.ep.GSC+"/sites", nil, &resp); err != nil {
		return nil, err
	}
	var out []GSCSite
	for _, s := range resp.SiteEntry {
		if s.PermissionLevel == "siteUnverifiedUser" {
			continue
		}
		out = append(out, GSCSite{SiteURL: s.SiteURL, Permission: s.PermissionLevel})
	}
	return out, nil
}

type GSCRow struct {
	Keys        []string
	Clicks      float64
	Impressions float64
	CTR         float64
	Position    float64
}

type GSCFilter struct {
	Dimension  string // query / page / country / device
	Operator   string // equals / contains / notContains / includingRegex
	Expression string
}

// SearchAnalytics 查询搜索数据。limit 为 0 时自动分页拿全部（每页 25000 行）。
func (c *Client) SearchAnalytics(ctx context.Context, site, start, end string, dims []string, filters []GSCFilter, limit int) ([]GSCRow, error) {
	const page = 25000
	var out []GSCRow
	startRow := 0
	for {
		rowLimit := page
		if limit > 0 && limit-len(out) < page {
			rowLimit = limit - len(out)
		}
		body := map[string]any{
			"startDate": start, "endDate": end, "dimensions": dims,
			"rowLimit": rowLimit, "startRow": startRow, "dataState": "all",
		}
		if len(filters) > 0 {
			fs := make([]map[string]string, 0, len(filters))
			for _, f := range filters {
				op := f.Operator
				if op == "" {
					op = "equals"
				}
				fs = append(fs, map[string]string{"dimension": f.Dimension, "operator": op, "expression": f.Expression})
			}
			body["dimensionFilterGroups"] = []map[string]any{{"groupType": "and", "filters": fs}}
		}
		var resp struct {
			Rows []struct {
				Keys        []string `json:"keys"`
				Clicks      float64  `json:"clicks"`
				Impressions float64  `json:"impressions"`
				CTR         float64  `json:"ctr"`
				Position    float64  `json:"position"`
			} `json:"rows"`
		}
		u := c.ep.GSC + "/sites/" + url.PathEscape(site) + "/searchAnalytics/query"
		if err := c.do(ctx, "POST", u, body, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Rows {
			out = append(out, GSCRow{Keys: r.Keys, Clicks: r.Clicks, Impressions: r.Impressions, CTR: r.CTR, Position: r.Position})
		}
		startRow += len(resp.Rows)
		if len(resp.Rows) < rowLimit || (limit > 0 && len(out) >= limit) {
			return out, nil
		}
	}
}

// ========== Google 搜索状态页 ==========

// SearchIncident 是 Google 搜索状态页（status.search.google.com）上的一条记录，
// 例如核心算法更新、垃圾内容更新、索引或排名故障。
type SearchIncident struct {
	ID      string `json:"id"`
	Begin   string `json:"begin"`
	End     string `json:"end"`
	Desc    string `json:"external_desc"`
	Service string `json:"service_name"`
}

var StatusFeedURL = "https://status.search.google.com/incidents.json"

// SearchIncidents 读取公开的搜索状态数据源，不需要认证。
func SearchIncidents(ctx context.Context) ([]SearchIncident, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", StatusFeedURL, nil)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Google 搜索状态页返回 %d", resp.StatusCode)
	}
	var out []SearchIncident
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
