package google

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func client(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewWithHTTP(srv.Client(), Endpoints{Data: srv.URL, Admin: srv.URL, GSC: srv.URL})
}

func TestRunReportLimitQuotaAndFilter(t *testing.T) {
	var got map[string]any
	var quota Quota
	old := QuotaHook
	QuotaHook = func(_ string, q Quota) { quota = q }
	t.Cleanup(func() { QuotaHook = old })
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/properties/1:runReport" {
			t.Errorf("请求地址不对: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"dimensionHeaders":[{"name":"country"}],"rowCount":2,
			"rows":[{"dimensionValues":[{"value":"China"}],"metricValues":[{"value":"12"},{"value":"0.5"}]},
			        {"dimensionValues":[{"value":"Japan"}],"metricValues":[{"value":"3"},{"value":"0.25"}]}],
			"propertyQuota":{"tokensPerDay":{"consumed":4,"remaining":199000},"tokensPerHour":{"consumed":4,"remaining":39000}}}`))
	})
	rep, err := c.RunReport(context.Background(), "properties/1", ReportRequest{
		Ranges: []DateRange{{StartDate: "2026-11-01", EndDate: "2026-11-07"}}, Dimensions: []string{"country"}, Metrics: []string{"sessions", "engagementRate"},
		Filters: []Filter{{Field: "country", Match: "CONTAINS", Value: "a", Not: true}}, OrderBy: "sessions", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 2 || rep.Rows[0].Dims[0] != "China" || rep.Rows[0].Metrics[0] != 12 || rep.Rows[1].Metrics[1] != 0.25 {
		t.Errorf("结果解析不对: %+v", rep.Rows)
	}
	if got["limit"].(float64) != 20 || got["dimensionFilter"].(map[string]any)["notExpression"] == nil || got["orderBys"] == nil {
		t.Errorf("请求体不对: %v", got)
	}
	if quota.TokensPerDay.Remaining != 199000 {
		t.Errorf("配额没有回调: %+v", quota)
	}
}

func TestRunReportPaginates(t *testing.T) {
	var calls atomic.Int32
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Offset int `json:"offset"`
			Limit  int `json:"limit"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n := min(req.Limit, 30000-req.Offset) // 一共 30000 行，每页最多 25000
		var sb strings.Builder
		sb.WriteString(`{"rowCount":30000,"rows":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`{"dimensionValues":[{"value":"p"}],"metricValues":[{"value":"1"}]}`)
		}
		sb.WriteString(`]}`)
		_, _ = w.Write([]byte(sb.String()))
	})
	rep, err := c.RunReport(context.Background(), "properties/1", ReportRequest{Dimensions: []string{"pagePath"}, Metrics: []string{"screenPageViews"}})
	if err != nil || len(rep.Rows) != 30000 || calls.Load() != 2 {
		t.Fatalf("应该分两页拿全 30000 行: rows=%d calls=%d err=%v", len(rep.Rows), calls.Load(), err)
	}
}

func TestAPIErrors(t *testing.T) {
	// 403：不重试，错误信息说明怎么处理
	var calls atomic.Int32
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"message":"User does not have sufficient permissions"}}`))
	})
	_, err := c.RunReport(context.Background(), "properties/1", ReportRequest{Metrics: []string{"sessions"}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || !strings.Contains(err.Error(), "没有权限") || calls.Load() != 1 {
		t.Fatalf("403 应该直接返回可读的错误: %v（请求了 %d 次）", err, calls.Load())
	}
	// 请求被取消时，重试的等待要立刻结束
	ctx, cancel := context.WithCancel(context.Background())
	c = client(t, func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(503)
	})
	if _, err := c.GSCSites(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消后应该返回 context.Canceled，得到 %v", err)
	}
}

func TestSearchAnalyticsAndHost(t *testing.T) {
	var body map[string]any
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		// 网址前缀资源里的斜杠要编码，不然会被当成路径分隔
		if r.URL.EscapedPath() != "/sites/https:%2F%2Fa.example%2F/searchAnalytics/query" {
			t.Errorf("网站地址要做路径编码: %s", r.URL.EscapedPath())
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"rows":[{"keys":["2026-11-01","pingoo"],"clicks":5,"impressions":80,"ctr":0.0625,"position":7.5}]}`))
	})
	rows, err := c.SearchAnalytics(context.Background(), "https://a.example/", "2026-11-01", "2026-11-07", []string{"date", "query"},
		[]GSCFilter{{Dimension: "query", Expression: "pingoo"}}, 20)
	if err != nil || len(rows) != 1 || rows[0].Keys[1] != "pingoo" || rows[0].Position != 7.5 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if body["rowLimit"].(float64) != 20 || body["dimensionFilterGroups"] == nil {
		t.Errorf("请求体不对: %v", body)
	}
	for in, want := range map[string]string{"sc-domain:www.a.example": "a.example", "https://WWW.A.example/path": "a.example", "a.example": "a.example", "": ""} {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, want %q", in, got, want)
		}
	}
}

// 密钥文件里的 token_uri 被改成内网地址时，仍然只向 Google 换令牌（防 SSRF）。
func TestJWTConfigPinsTokenURL(t *testing.T) {
	sa := `{"type":"service_account","client_email":"x@p.iam.gserviceaccount.com","private_key":"k","token_uri":"http://169.254.169.254/latest/meta-data"}`
	cfg, err := jwtConfig(sa)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TokenURL != "https://oauth2.googleapis.com/token" {
		t.Fatalf("TokenURL = %q", cfg.TokenURL)
	}
}

func TestAdSenseLinks(t *testing.T) {
	var path string
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if strings.Contains(r.URL.Path, "/properties/2/") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"adsenseLinks":[{"name":"properties/1/adSenseLinks/9","adClientCode":"ca-pub-1"}]}`))
	})
	c.ep.AdminAlpha = c.ep.Admin
	got, err := c.AdSenseLinks(context.Background(), "properties/1")
	if err != nil || len(got) != 1 || got[0] != "ca-pub-1" || path != "/properties/1/adSenseLinks" {
		t.Fatalf("got %v %v path=%s", got, err, path)
	}
	if got, err := c.AdSenseLinks(context.Background(), "properties/2"); err != nil || len(got) != 0 {
		t.Fatalf("没关联时应返回空：%v %v", got, err)
	}
	if _, err := NewWithHTTP(http.DefaultClient, Endpoints{}).AdSenseLinks(context.Background(), "properties/1"); !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("没配地址时应返回 ErrNoEndpoint：%v", err)
	}
}
