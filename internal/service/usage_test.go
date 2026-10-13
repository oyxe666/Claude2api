package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/repository"
)

const (
	secretBody = "SECRET-BODY-MARKER"
	testOrg    = "org-1"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// ---------- 解析 ----------

func TestQuotaFromUsage(t *testing.T) {
	now := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	resetA := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC).Unix()

	wantUtil := func(t *testing.T, w *repository.QuotaWindow, want float64) {
		t.Helper()
		if w == nil || w.Utilization == nil || !near(*w.Utilization, want) {
			t.Fatalf("utilization = %+v, want %v", w, want)
		}
	}
	wantNoUtil := func(t *testing.T, w *repository.QuotaWindow) {
		t.Helper()
		if w == nil || w.Utilization != nil {
			t.Fatalf("窗口应存在但没有 utilization: %+v", w)
		}
	}

	cases := []struct {
		name    string
		body    string
		wantErr bool
		check   func(t *testing.T, q repository.AccountQuota)
	}{
		{"正常两窗口", `{"five_hour":{"utilization":41.0,"resets_at":"2026-10-11T12:00:00+00:00"},"seven_day":{"utilization":12.0,"resets_at":"2026-10-15T00:00:00.123456+00:00"}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				wantUtil(t, q.FiveHour, 0.41)
				wantUtil(t, q.SevenDay, 0.12)
				if q.FiveHour.ResetsAt != resetA {
					t.Fatalf("5h resets_at = %d, want %d", q.FiveHour.ResetsAt, resetA)
				}
				if q.SevenDay.ResetsAt != time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC).Unix() {
					t.Fatalf("7d resets_at = %d", q.SevenDay.ResetsAt)
				}
				if q.Type != "within_limit" || q.Source != "usage_api" || !q.UpdatedAt.Equal(now) || q.Error != "" {
					t.Fatalf("元信息不对: %+v", q)
				}
				if q.FiveHour.Status != "within_limit" || q.SevenDay.Status != "within_limit" {
					t.Fatalf("窗口 status 不对: %+v %+v", q.FiveHour, q.SevenDay)
				}
			}},
		{"仅 five_hour", `{"five_hour":{"utilization":10,"resets_at":1791740400}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				wantUtil(t, q.FiveHour, 0.10)
				if q.SevenDay != nil {
					t.Fatalf("seven_day 应为空: %+v", q.SevenDay)
				}
			}},
		{"seven_day 为 null", `{"five_hour":{"utilization":10},"seven_day":null}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.SevenDay != nil || q.FiveHour == nil {
					t.Fatalf("窗口不对: %+v", q)
				}
			}},
		{"仅 seven_day", `{"five_hour":null,"seven_day":{"utilization":30}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour != nil {
					t.Fatalf("five_hour 应为空")
				}
				wantUtil(t, q.SevenDay, 0.30)
			}},
		{"两窗口全 null", `{"five_hour":null,"seven_day":null}`, true, nil},
		{"空对象", `{}`, true, nil},
		{"窗口不是对象", `{"five_hour":"x","seven_day":5}`, true, nil},
		{"乱 JSON", `{not json`, true, nil},
		{"顶层是数组", `[]`, true, nil},
		{"空响应体", ``, true, nil},
		{"整数 utilization", `{"five_hour":{"utilization":41}}`, false,
			func(t *testing.T, q repository.AccountQuota) { wantUtil(t, q.FiveHour, 0.41) }},
		{"浮点 utilization", `{"five_hour":{"utilization":41.5}}`, false,
			func(t *testing.T, q repository.AccountQuota) { wantUtil(t, q.FiveHour, 0.415) }},
		{"字符串 utilization", `{"five_hour":{"utilization":"41.5"}}`, false,
			func(t *testing.T, q repository.AccountQuota) { wantUtil(t, q.FiveHour, 0.415) }},
		{"带百分号 utilization", `{"five_hour":{"utilization":"41%"}}`, false,
			func(t *testing.T, q repository.AccountQuota) { wantUtil(t, q.FiveHour, 0.41) }},
		{"小于 1 也按百分数解释", `{"five_hour":{"utilization":0.4}}`, false,
			func(t *testing.T, q repository.AccountQuota) { wantUtil(t, q.FiveHour, 0.004) }},
		{"非法字符串 utilization 但有 resets_at", `{"five_hour":{"utilization":"abc","resets_at":1791740400}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				wantNoUtil(t, q.FiveHour)
				if q.FiveHour.Status != "" {
					t.Fatalf("无 util 不应推导 status: %+v", q.FiveHour)
				}
			}},
		{"NaN utilization 但有 resets_at", `{"five_hour":{"utilization":"NaN","resets_at":1791740400},"seven_day":{"utilization":5}}`, false,
			func(t *testing.T, q repository.AccountQuota) { wantNoUtil(t, q.FiveHour) }},
		{"非法 utilization 且无 resets_at 的窗口视为没有", `{"five_hour":{"utilization":"abc"},"seven_day":{"utilization":5}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour != nil {
					t.Fatalf("five_hour 应视为没有: %+v", q.FiveHour)
				}
			}},
		{"只有非法窗口 → 错误", `{"five_hour":{"utilization":"abc"}}`, true, nil},
		{"越界 150 夹到 1 且判耗尽", `{"five_hour":{"utilization":150},"seven_day":{"utilization":20}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				wantUtil(t, q.FiveHour, 1)
				if q.FiveHour.Status != "exceeded_limit" || q.Type != "exceeded_limit" {
					t.Fatalf("应判耗尽: %+v type=%s", q.FiveHour, q.Type)
				}
				if q.SevenDay.Status != "within_limit" {
					t.Fatalf("7d 不应耗尽: %+v", q.SevenDay)
				}
			}},
		{"越界 -5 夹到 0", `{"five_hour":{"utilization":-5}}`, false,
			func(t *testing.T, q repository.AccountQuota) { wantUtil(t, q.FiveHour, 0) }},
		{"恰好 100 判耗尽", `{"seven_day":{"utilization":100}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.SevenDay.Status != "exceeded_limit" || q.Type != "exceeded_limit" {
					t.Fatalf("100 应耗尽: %+v", q)
				}
			}},
		{"resets_at Z", `{"five_hour":{"utilization":1,"resets_at":"2026-10-11T12:00:00Z"}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour.ResetsAt != resetA {
					t.Fatalf("got %d want %d", q.FiveHour.ResetsAt, resetA)
				}
			}},
		{"resets_at 带小数秒", `{"five_hour":{"utilization":1,"resets_at":"2026-10-11T12:00:00.987654321Z"}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour.ResetsAt != resetA {
					t.Fatalf("got %d want %d", q.FiveHour.ResetsAt, resetA)
				}
			}},
		{"resets_at 带时区偏移", `{"five_hour":{"utilization":1,"resets_at":"2026-10-11T20:00:00+08:00"}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour.ResetsAt != resetA {
					t.Fatalf("got %d want %d", q.FiveHour.ResetsAt, resetA)
				}
			}},
		{"resets_at 无时区按 UTC", `{"five_hour":{"utilization":1,"resets_at":"2026-10-11T12:00:00"}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour.ResetsAt != resetA {
					t.Fatalf("got %d want %d", q.FiveHour.ResetsAt, resetA)
				}
			}},
		{"resets_at unix 秒", `{"five_hour":{"utilization":1,"resets_at":1791740400}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour.ResetsAt != 1791740400 {
					t.Fatalf("got %d", q.FiveHour.ResetsAt)
				}
			}},
		{"resets_at unix 毫秒", `{"five_hour":{"utilization":1,"resets_at":1791740400000}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour.ResetsAt != 1791740400 {
					t.Fatalf("got %d", q.FiveHour.ResetsAt)
				}
			}},
		{"resets_at 数字字符串", `{"five_hour":{"utilization":1,"resets_at":"1791740400"}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour.ResetsAt != 1791740400 {
					t.Fatalf("got %d", q.FiveHour.ResetsAt)
				}
			}},
		{"resets_at 垃圾字符串", `{"five_hour":{"utilization":1,"resets_at":"soon"}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				if q.FiveHour.ResetsAt != 0 {
					t.Fatalf("垃圾时间应为 0, got %d", q.FiveHour.ResetsAt)
				}
			}},
		{"含 opus 与 sonnet", `{"five_hour":{"utilization":1},"seven_day_opus":{"utilization":50},"seven_day_sonnet":{"utilization":100}}`, false,
			func(t *testing.T, q repository.AccountQuota) {
				wantUtil(t, q.SevenDayOpus, 0.5)
				wantUtil(t, q.SevenDaySonnet, 1)
				if q.Type != "within_limit" {
					t.Fatalf("单模型窗口耗尽不应让账号整体判耗尽: %s", q.Type)
				}
			}},
		{"未知字段被忽略", `{"five_hour":{"utilization":7,"foo":1},"extra_usage":{"x":1}}`, false,
			func(t *testing.T, q repository.AccountQuota) { wantUtil(t, q.FiveHour, 0.07) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := quotaFromUsage([]byte(tc.body), now)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("应返回错误, got %+v", q)
				}
				if strings.Contains(err.Error(), tc.body) && tc.body != "" {
					t.Fatalf("错误不应回显响应体: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			tc.check(t, q)
		})
	}
}

func TestScaleConsistency(t *testing.T) {
	now := time.Now()
	// 旧路径：SSE message_limit 的 0~1 小数。
	apply, ok := quotaFromMessageLimit([]byte(`{"type":"within_limit","windows":{"5h":{"status":"within_limit","utilization":0.41,"resets_at":1791740400}}}`), now)
	if !ok {
		t.Fatal("SSE 解析失败")
	}
	var sse repository.AccountQuota
	apply(&sse)
	// 新路径：/usage 接口的 0~100 百分数。
	usage, err := quotaFromUsage([]byte(`{"five_hour":{"utilization":41.0,"resets_at":1791740400}}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if sse.FiveHour == nil || usage.FiveHour == nil || sse.FiveHour.Utilization == nil || usage.FiveHour.Utilization == nil {
		t.Fatalf("窗口缺失: %+v %+v", sse, usage)
	}
	if !near(*sse.FiveHour.Utilization, 0.41) || !near(*usage.FiveHour.Utilization, 0.41) {
		t.Fatalf("两路尺度不一致: sse=%v usage=%v", *sse.FiveHour.Utilization, *usage.FiveHour.Utilization)
	}
}

func TestUsageFailureMessage(t *testing.T) {
	body := []byte(`{"detail":"` + secretBody + `"}`)
	cases := []struct {
		status int
		body   []byte
		net    error
		want   string
	}{
		{401, body, nil, msgInvalid},
		{403, body, nil, msgForbidden},
		{429, body, nil, msgRateLimit},
		{500, body, nil, "上游返回 HTTP 500"},
		{403, []byte(`{"error":{"type":"account_session_invalid"}}`), nil, msgInvalid},
		{200, body, nil, msgFormat},
		{0, nil, ErrUsageTimeout, msgTimeout},
		{0, nil, context.DeadlineExceeded, msgTimeout},
		{0, nil, ErrUsageNetwork, msgNetwork},
		{0, nil, errors.New("dial tcp http://user:pass@10.0.0.1:7890: refused"), msgNetwork},
		{0, nil, ErrUsageNoOrg, msgNoOrg},
	}
	for _, tc := range cases {
		got := usageFailureMessage(tc.status, tc.body, tc.net)
		if got != tc.want {
			t.Errorf("status=%d net=%v: got %q want %q", tc.status, tc.net, got, tc.want)
		}
		if strings.Contains(got, secretBody) || strings.Contains(got, "10.0.0.1") {
			t.Errorf("消息泄漏了敏感内容: %q", got)
		}
	}
}

// ---------- 假 claude.ai ----------

type fakeClaude struct {
	srv   *httptest.Server
	mu    sync.Mutex
	paths []string
	usage http.HandlerFunc
}

func (f *fakeClaude) hits() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func (f *fakeClaude) count(path string) int {
	n := 0
	for _, p := range f.hits() {
		if p == path {
			n++
		}
	}
	return n
}

// newFakeClaude 起一个假上游并把默认 base URL 指过去；usage 为空时返回两窗口正常数据。
func newFakeClaude(t *testing.T, usage http.HandlerFunc) *fakeClaude {
	t.Helper()
	f := &fakeClaude{usage: usage}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/new":
			w.Write([]byte("<html></html>"))
		case r.URL.Path == "/api/account":
			w.Header().Set("content-type", "application/json")
			w.Write([]byte(`{"email_address":"acc@example.com","memberships":[{"organization":{"uuid":"` + testOrg + `"}}]}`))
		case strings.HasSuffix(r.URL.Path, "/usage"):
			if f.usage != nil {
				f.usage(w, r)
				return
			}
			w.Header().Set("content-type", "application/json")
			w.Write([]byte(`{"five_hour":{"utilization":41.0,"resets_at":"2026-10-11T12:00:00+00:00"},"seven_day":{"utilization":12.0,"resets_at":"2026-10-15T00:00:00+00:00"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	restore := SetClaudeBaseURLForTest(f.srv.URL)
	t.Cleanup(restore)
	return f
}

func clearClients(t *testing.T) {
	t.Helper()
	reset := func() {
		apiIndexLock.Lock()
		apiClients = map[string]*accountClient{}
		apiIndexLock.Unlock()
		quotaInflightMu.Lock()
		quotaInflight = map[string]struct{}{}
		quotaInflightMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func setupDB(t *testing.T) {
	t.Helper()
	if err := repository.InitDBAt(filepath.Join(t.TempDir(), "app.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repository.CloseDB)
	clearClients(t)
}

func addAccount(t *testing.T, email, org, sessionKey string) {
	t.Helper()
	if err := repository.UpsertAccount(&repository.Account{
		Email: email, OrgUUID: org, Cookies: map[string]string{"sessionKey": sessionKey}, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
}

func quotaOf(t *testing.T, email string) repository.AccountQuota {
	t.Helper()
	a := repository.AccountByEmail(email)
	if a == nil {
		t.Fatalf("账号 %s 不存在", email)
	}
	return a.Quota
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func setQuotaVars(t *testing.T, leaseWait, reqTimeout time.Duration) {
	t.Helper()
	oldLease, oldReq := quotaLeaseWait, quotaRequestTimeout
	if leaseWait > 0 {
		quotaLeaseWait = leaseWait
	}
	if reqTimeout > 0 {
		quotaRequestTimeout = reqTimeout
	}
	t.Cleanup(func() { quotaLeaseWait, quotaRequestTimeout = oldLease, oldReq })
}

// ---------- FetchUsage ----------

func TestFetchUsage(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"200", 200, `{"five_hour":{"utilization":3}}`},
		{"401", 401, `{"error":"` + secretBody + `"}`},
		{"403", 403, secretBody},
		{"429", 429, `{"error":"` + secretBody + `"}`},
		{"500", 500, secretBody},
		{"乱 JSON", 200, `{oops ` + secretBody},
		{"空响应体", 200, ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %s, want GET", r.Method)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			c := NewClaudeAI("sk-test-fake", "", "id", testOrg)
			body, status, err := c.FetchUsage(context.Background())
			if err != nil {
				t.Fatalf("FetchUsage err = %v", err)
			}
			if status != tc.status || string(body) != tc.body {
				t.Fatalf("status=%d body=%q", status, body)
			}
			if got := f.hits(); len(got) != 1 || got[0] != "GET /api/organizations/"+testOrg+"/usage" {
				t.Fatalf("请求路径不对: %v", got)
			}
			// 失败分类 + 解析错误都不能带出响应体。
			if status != 200 {
				if msg := usageFailureMessage(status, body, nil); strings.Contains(msg, secretBody) {
					t.Fatalf("失败消息泄漏响应体: %q", msg)
				}
			} else if _, perr := quotaFromUsage(body, time.Now()); perr != nil && strings.Contains(perr.Error(), secretBody) {
				t.Fatalf("解析错误泄漏响应体: %v", perr)
			}
		})
	}
}

func TestFetchUsageTimeout(t *testing.T) {
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	c := NewClaudeAI("sk-test-fake", "", "id", testOrg)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := c.FetchUsage(ctx)
	if !errors.Is(err, ErrUsageTimeout) {
		t.Fatalf("err = %v, want ErrUsageTimeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("超时没有打断请求: %v", time.Since(start))
	}
}

func TestFetchUsageNetworkErrorHasNoDetail(t *testing.T) {
	f := newFakeClaude(t, nil)
	c := NewClaudeAI("sk-test-fake", "", "id", testOrg)
	f.srv.Close() // 连接被拒绝
	_, _, err := c.FetchUsage(context.Background())
	if !errors.Is(err, ErrUsageNetwork) {
		t.Fatalf("err = %v, want ErrUsageNetwork", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("错误带了地址: %v", err)
	}
}

func TestFetchUsageNoOrg(t *testing.T) {
	newFakeClaude(t, nil)
	c := NewClaudeAI("sk-test-fake", "", "id")
	if _, _, err := c.FetchUsage(context.Background()); !errors.Is(err, ErrUsageNoOrg) {
		t.Fatalf("err = %v", err)
	}
}

// ---------- RefreshAccountQuota ----------

func TestRefreshAccountQuotaReplacesBothWindows(t *testing.T) {
	setupDB(t)
	f := newFakeClaude(t, nil)
	addAccount(t, "a@example.com", testOrg, "sk-test-A")

	oldAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	one := 0.5
	repository.UpdateAccountQuota("a@example.com", func(q *repository.AccountQuota) {
		*q = repository.AccountQuota{
			Type:      "within_limit",
			FiveHour:  &repository.QuotaWindow{Status: "within_limit", Utilization: &one},
			UpdatedAt: oldAt, Error: "旧错误", ErrorAt: oldAt,
		}
	})

	res := RefreshAccountQuota(context.Background(), "a@example.com")
	if !res.OK || res.Busy || res.Error != "" {
		t.Fatalf("result = %+v", res)
	}
	q := quotaOf(t, "a@example.com")
	if q.FiveHour == nil || q.SevenDay == nil || !near(*q.FiveHour.Utilization, 0.41) || !near(*q.SevenDay.Utilization, 0.12) {
		t.Fatalf("两个窗口应都以本次为准: %+v", q)
	}
	if q.Error != "" || !q.ErrorAt.IsZero() || q.Source != "usage_api" || !q.UpdatedAt.After(oldAt) {
		t.Fatalf("成功后应清空 Error 并刷新时间: %+v", q)
	}
	hits := f.hits()
	if len(hits) != 2 || hits[0] != "GET /new" || hits[1] != "GET /api/organizations/"+testOrg+"/usage" {
		t.Fatalf("请求序列不对: %v", hits)
	}
	// 第二次不再预热。
	RefreshAccountQuota(context.Background(), "a@example.com")
	if f.count("GET /new") != 1 {
		t.Fatalf("已 ready 不应再预热: %v", f.hits())
	}
}

func TestRefreshAccountQuotaFailureKeepsOldWindows(t *testing.T) {
	setupDB(t)
	logs := captureLogs(t)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(secretBody))
	})
	addAccount(t, "a@example.com", testOrg, "sk-test-SECRETKEY-A")

	oldAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	one := 0.5
	repository.UpdateAccountQuota("a@example.com", func(q *repository.AccountQuota) {
		*q = repository.AccountQuota{
			Type:      "within_limit",
			FiveHour:  &repository.QuotaWindow{Status: "within_limit", Utilization: &one, ResetsAt: 1791740400},
			SevenDay:  &repository.QuotaWindow{Status: "within_limit", Utilization: &one},
			UpdatedAt: oldAt,
		}
	})

	res := RefreshAccountQuota(context.Background(), "a@example.com")
	if res.OK || res.Busy || res.Error != "上游返回 HTTP 500" {
		t.Fatalf("result = %+v", res)
	}
	q := quotaOf(t, "a@example.com")
	if q.FiveHour == nil || q.SevenDay == nil || !near(*q.FiveHour.Utilization, 0.5) || q.FiveHour.ResetsAt != 1791740400 {
		t.Fatalf("失败不应动旧窗口: %+v", q)
	}
	if q.Error != "上游返回 HTTP 500" || q.ErrorAt.IsZero() || !q.UpdatedAt.Equal(oldAt) {
		t.Fatalf("失败应只写 Error+ErrorAt: %+v", q)
	}
	for _, s := range []string{secretBody, "SECRETKEY"} {
		if strings.Contains(q.Error, s) || strings.Contains(logs.String(), s) {
			t.Fatalf("泄漏 %q：error=%q logs=%s", s, q.Error, logs.String())
		}
	}
}

func TestRefreshAccountQuotaFirstFailureIsVisible(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) })
	addAccount(t, "a@example.com", testOrg, "sk-test-A")

	res := RefreshAccountQuota(context.Background(), "a@example.com")
	if res.OK || !res.RateLimited || res.Error != msgRateLimit {
		t.Fatalf("result = %+v", res)
	}
	q := quotaOf(t, "a@example.com")
	if q.IsZero() || q.Error != msgRateLimit {
		t.Fatalf("首次失败也应有记录: %+v", q)
	}
	// 429 后会话需要重新预热。
	acct := repository.AccountByEmail("a@example.com")
	if lease := clientFor(acct, config.Get().Proxy); lease.ready {
		t.Fatal("429 后应 ready=false")
	}
}

// stubProbe 把探测对话换成假实现，避免测试真连 claude.ai。
func stubProbe(t *testing.T, fn func(email string, cl *ClaudeAI) (bool, string)) {
	t.Helper()
	old := probeQuota
	probeQuota = fn
	t.Cleanup(func() { probeQuota = old })
}

// /usage 窗口全为 null（免费账号）时回退到探测对话：成功则 OK。
func TestRefreshAccountQuotaFallsBackToProbe(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"five_hour":null,"seven_day":null}`)) })
	addAccount(t, "a@example.com", testOrg, "sk-test-A")
	called := 0
	stubProbe(t, func(email string, cl *ClaudeAI) (bool, string) { called++; return true, "" })
	res := RefreshAccountQuota(context.Background(), "a@example.com")
	if !res.OK || called != 1 {
		t.Fatalf("result = %+v called=%d", res, called)
	}
}

// 非 JSON（例如被拦截的 HTML）不应触发探测对话。
func TestRefreshAccountQuotaHTMLDoesNotProbe(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html>Just a moment</html>`)) })
	addAccount(t, "a@example.com", testOrg, "sk-test-A")
	stubProbe(t, func(email string, cl *ClaudeAI) (bool, string) { t.Fatal("不应探测"); return false, "" })
	res := RefreshAccountQuota(context.Background(), "a@example.com")
	if res.OK || !strings.HasPrefix(res.Error, msgFormat) {
		t.Fatalf("result = %+v", res)
	}
}

func TestRefreshAccountQuotaBadFormatKeepsSnapshot(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"five_hour":null,"seven_day":null}`)) })
	addAccount(t, "a@example.com", testOrg, "sk-test-A")
	stubProbe(t, func(email string, cl *ClaudeAI) (bool, string) { return false, msgProbeFailed })
	res := RefreshAccountQuota(context.Background(), "a@example.com")
	want := msgProbeFailed + "（字段：five_hour=null，seven_day=null）"
	if res.OK || res.Error != want {
		t.Fatalf("result = %+v", res)
	}
	if q := quotaOf(t, "a@example.com"); q.FiveHour != nil || q.Error != want {
		t.Fatalf("q = %+v", q)
	}
}

// 结构摘要只含字段名与类型，不得带出任何取值。
func TestUsageShapeNeverLeaksValues(t *testing.T) {
	cases := map[string]string{
		`{"five_hour":{"utilization":41,"resets_at":"SECRET"},"x":null}`: "字段：five_hour=对象，x=null",
		`<html>Just a moment SECRET</html>`:                              "非 JSON：HTML 页面，疑似被拦截，",
		`[1,2]`:                                                          "JSON 但不是对象",
		`{}`:                                                             "空对象",
		``:                                                               "响应为空",
	}
	for body, wantPrefix := range cases {
		got := usageShape([]byte(body))
		if !strings.HasPrefix(got, wantPrefix) || strings.Contains(got, "SECRET") {
			t.Errorf("usageShape(%q) = %q, want prefix %q", body, got, wantPrefix)
		}
	}
}

func TestRefreshAccountQuotaInvalidSessionMarksExpiredNeverDeletes(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"type":"account_session_invalid","message":"` + secretBody + `"}}`))
	})
	defer config.Update(map[string]any{"remove_invalid_account": false})

	for i, remove := range []bool{false, true} {
		config.Update(map[string]any{"remove_invalid_account": remove})
		email := []string{"a@example.com", "b@example.com"}[i]
		addAccount(t, email, testOrg, "sk-test-"+email)
		res := RefreshAccountQuota(context.Background(), email)
		if res.OK || res.Error != msgInvalid {
			t.Fatalf("remove=%v result = %+v", remove, res)
		}
		acct := repository.AccountByEmail(email)
		if acct == nil {
			t.Fatalf("remove=%v：账号被删了", remove)
		}
		if acct.Status != "expired" {
			t.Fatalf("remove=%v：status = %q, want expired", remove, acct.Status)
		}
		if strings.Contains(acct.Quota.Error, secretBody) {
			t.Fatalf("泄漏响应体: %q", acct.Quota.Error)
		}
	}
}

func TestRefreshAccountQuotaFetchesOrgFirst(t *testing.T) {
	setupDB(t)
	f := newFakeClaude(t, nil)
	addAccount(t, "a@example.com", "", "sk-test-A")

	res := RefreshAccountQuota(context.Background(), "a@example.com")
	if !res.OK {
		t.Fatalf("result = %+v", res)
	}
	hits := f.hits()
	if len(hits) != 3 || hits[1] != "GET /api/account" || hits[2] != "GET /api/organizations/"+testOrg+"/usage" {
		t.Fatalf("应先查 /api/account 再查 usage: %v", hits)
	}
	if got := repository.AccountByEmail("a@example.com"); got.OrgUUID != testOrg {
		t.Fatalf("OrgUUID 未持久化: %q", got.OrgUUID)
	}
}

func TestRefreshAccountQuotaBusyLease(t *testing.T) {
	setupDB(t)
	f := newFakeClaude(t, nil)
	setQuotaVars(t, 150*time.Millisecond, 0)
	addAccount(t, "a@example.com", testOrg, "sk-test-A")

	lease := clientFor(repository.AccountByEmail("a@example.com"), config.Get().Proxy)
	lease.Lock()
	start := time.Now()
	res := RefreshAccountQuota(context.Background(), "a@example.com")
	lease.Unlock()

	if !res.Busy || res.OK || res.Error != msgBusy {
		t.Fatalf("result = %+v", res)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatalf("应等待 lease 一小会儿: %v", time.Since(start))
	}
	if q := quotaOf(t, "a@example.com"); !q.IsZero() {
		t.Fatalf("busy 不应写快照: %+v", q)
	}
	if len(f.hits()) != 0 {
		t.Fatalf("busy 不应访问上游: %v", f.hits())
	}
}

func TestRefreshAccountQuotaWaitsForLease(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, nil)
	setQuotaVars(t, 2*time.Second, 0)
	addAccount(t, "a@example.com", testOrg, "sk-test-A")

	lease := clientFor(repository.AccountByEmail("a@example.com"), config.Get().Proxy)
	lease.Lock()
	go func() {
		time.Sleep(200 * time.Millisecond)
		lease.Unlock()
	}()
	if res := RefreshAccountQuota(context.Background(), "a@example.com"); !res.OK {
		t.Fatalf("lease 释放后应成功: %+v", res)
	}
}

func TestRefreshAccountQuotaDuplicateInflight(t *testing.T) {
	setupDB(t)
	f := newFakeClaude(t, nil)
	addAccount(t, "a@example.com", testOrg, "sk-test-A")
	if !beginInflight("a@example.com") {
		t.Fatal("第一次应成功")
	}
	defer endInflight("a@example.com")
	res := RefreshAccountQuota(context.Background(), "a@example.com")
	if !res.Busy || len(f.hits()) != 0 {
		t.Fatalf("重复请求应立即 busy: %+v hits=%v", res, f.hits())
	}
}

func TestRefreshAccountQuotaTimeout(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	setQuotaVars(t, 0, 150*time.Millisecond)
	addAccount(t, "a@example.com", testOrg, "sk-test-A")
	res := RefreshAccountQuota(context.Background(), "a@example.com")
	if res.OK || res.Error != msgTimeout {
		t.Fatalf("result = %+v", res)
	}
	// 超时后锁必须已释放。
	acct := repository.AccountByEmail("a@example.com")
	lease := clientFor(acct, config.Get().Proxy)
	if !lease.TryLock() {
		t.Fatal("超时后账号锁没有释放")
	}
	lease.Unlock()
}

func TestRefreshAccountQuotaCanceledDoesNotWrite(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, nil)
	addAccount(t, "a@example.com", testOrg, "sk-test-A")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := RefreshAccountQuota(ctx, "a@example.com")
	if res.OK || !res.Skipped {
		t.Fatalf("result = %+v", res)
	}
	if q := quotaOf(t, "a@example.com"); !q.IsZero() {
		t.Fatalf("取消不应写快照: %+v", q)
	}
}

func TestRefreshAccountQuotaUnknownAccount(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, nil)
	res := RefreshAccountQuota(context.Background(), "nobody@example.com")
	if res.OK || res.Error != msgNotFound {
		t.Fatalf("result = %+v", res)
	}
}

func TestRefreshAccountQuotaLogsHaveNoSecrets(t *testing.T) {
	setupDB(t)
	logs := captureLogs(t)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(secretBody))
	})
	addAccount(t, "a@example.com", testOrg, "sk-ant-sid01-VERYSECRETKEY")
	RefreshAccountQuota(context.Background(), "a@example.com")
	out := logs.String()
	if !strings.Contains(out, "a@example.com") {
		t.Fatalf("日志应带邮箱: %s", out)
	}
	for _, s := range []string{"VERYSECRETKEY", secretBody, "127.0.0.1"} {
		if strings.Contains(out, s) {
			t.Fatalf("日志泄漏 %q: %s", s, out)
		}
	}
}

// ---------- 批量 ----------

func TestRefreshAllQuotasStopsAfterTwoConsecutiveRateLimits(t *testing.T) {
	setupDB(t)
	var mu sync.Mutex
	var order []string
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(429)
	})
	emails := []string{"a@example.com", "b@example.com", "c@example.com", "d@example.com"}
	for _, e := range emails {
		addAccount(t, e, testOrg, "sk-test-"+e)
	}
	var events []QuotaProgress
	sum := RefreshAllQuotas(context.Background(), QuotaBatchOptions{}, emails, func(p QuotaProgress) { events = append(events, p) })
	if sum.Total != 4 || sum.OK != 0 || sum.Failed != 2 || sum.Skipped != 2 {
		t.Fatalf("sum = %+v", sum)
	}
	if len(events) != 4 || events[3].Completed != 4 || events[3].Error != msgSkipLimit || events[1].Error != msgRateLimit {
		t.Fatalf("events = %+v", events)
	}
	for _, e := range emails[2:] {
		if q := quotaOf(t, e); !q.IsZero() {
			t.Fatalf("skipped 账号 %s 不应写快照: %+v", e, q)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 {
		t.Fatalf("不应重试，也不应继续请求被跳过的账号: %v", order)
	}
}

func TestRefreshAllQuotasNonConsecutiveFailuresContinue(t *testing.T) {
	setupDB(t)
	statuses := map[string]int{"/api/organizations/o1/usage": 429, "/api/organizations/o2/usage": 200, "/api/organizations/o3/usage": 403, "/api/organizations/o4/usage": 200}
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) {
		if statuses[r.URL.Path] == 200 {
			w.Write([]byte(`{"five_hour":{"utilization":10}}`))
			return
		}
		w.WriteHeader(statuses[r.URL.Path])
	})
	emails := []string{"a@example.com", "b@example.com", "c@example.com", "d@example.com"}
	for i, e := range emails {
		addAccount(t, e, "o"+string(rune('1'+i)), "sk-test-"+e)
	}
	sum := RefreshAllQuotas(context.Background(), QuotaBatchOptions{}, emails, nil)
	if sum.OK != 2 || sum.Failed != 2 || sum.Skipped != 0 {
		t.Fatalf("sum = %+v", sum)
	}
}

func TestRefreshAllQuotasContextTimeoutSkipsRest(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, nil)
	emails := []string{"a@example.com", "b@example.com", "c@example.com"}
	for _, e := range emails {
		addAccount(t, e, testOrg, "sk-test-"+e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	opts := QuotaBatchOptions{Gap: func() time.Duration { return 5 * time.Second }}
	start := time.Now()
	sum := RefreshAllQuotas(ctx, opts, emails, nil)
	if sum.OK != 1 || sum.Skipped != 2 {
		t.Fatalf("sum = %+v", sum)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("间隔等待没有被 ctx 打断: %v", time.Since(start))
	}
}

func TestQuotaBatchMutexAndTargets(t *testing.T) {
	end, ok := TryBeginQuotaBatch()
	if !ok {
		t.Fatal("第一次应成功")
	}
	if _, ok2 := TryBeginQuotaBatch(); ok2 {
		t.Fatal("第二次应失败")
	}
	end()
	end2, ok3 := TryBeginQuotaBatch()
	if !ok3 {
		t.Fatal("释放后应可再次获取")
	}
	end2()

	setupDB(t)
	addAccount(t, "a@example.com", testOrg, "sk-test-A")
	addAccount(t, "b@example.com", testOrg, "")
	addAccount(t, "c@example.com", testOrg, "sk-test-C")
	repository.UpdateAccount("c@example.com", func(a *repository.Account) { a.Status = "expired" })
	addAccount(t, "d@example.com", testOrg, "sk-test-D")
	got := QuotaBatchTargets()
	if len(got) != 2 || got[0] != "a@example.com" || got[1] != "d@example.com" {
		t.Fatalf("targets = %v", got)
	}
}

func TestDefaultQuotaBatchOptions(t *testing.T) {
	o := DefaultQuotaBatchOptions()
	if o.Timeout != 5*time.Minute {
		t.Fatalf("timeout = %v", o.Timeout)
	}
	for i := 0; i < 200; i++ {
		if d := o.Gap(); d < 300*time.Millisecond || d > 800*time.Millisecond {
			t.Fatalf("gap = %v 超出 300~800ms", d)
		}
	}
}
