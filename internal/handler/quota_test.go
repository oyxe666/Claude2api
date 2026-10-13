package handler

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude2api/internal/repository"
	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

const handlerSecretBody = "SECRET-BODY-MARKER"

type quotaEnv struct {
	engine *gin.Engine
	// statuses: org -> 上游 usage 状态码；缺省 200。
	statuses map[string]int
	mu       sync.Mutex
	usageHit []string
	slow     time.Duration
	logs     *bytes.Buffer
}

func (e *quotaEnv) setStatus(org string, status int) {
	e.mu.Lock()
	e.statuses[org] = status
	e.mu.Unlock()
}

// newQuotaEnv 建临时库 + 假 claude.ai + 一个只挂了两个额度接口的 gin 引擎。
func newQuotaEnv(t *testing.T) *quotaEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if err := repository.InitDBAt(filepath.Join(t.TempDir(), "app.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repository.CloseDB)

	env := &quotaEnv{statuses: map[string]int{}, logs: &bytes.Buffer{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/new":
			w.Write([]byte("<html></html>"))
		case strings.HasSuffix(r.URL.Path, "/usage"):
			org := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/organizations/"), "/usage")
			env.mu.Lock()
			env.usageHit = append(env.usageHit, org)
			status, slow := env.statuses[org], env.slow
			env.mu.Unlock()
			if slow > 0 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(slow):
				}
			}
			if status != 0 && status != 200 {
				w.WriteHeader(status)
				w.Write([]byte(handlerSecretBody))
				return
			}
			w.Write([]byte(`{"five_hour":{"utilization":41.0,"resets_at":"2026-10-11T12:00:00+00:00"},"seven_day":{"utilization":12.0,"resets_at":"2026-10-15T00:00:00+00:00"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(service.SetClaudeBaseURLForTest(srv.URL))

	oldOpts := quotaBatchOpts
	quotaBatchOpts = service.QuotaBatchOptions{Gap: func() time.Duration { return 0 }, Timeout: time.Minute}
	t.Cleanup(func() { quotaBatchOpts = oldOpts })

	oldLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(env.logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(oldLog) })

	oldCheck := checkAllOpts
	checkAllOpts = service.QuotaBatchOptions{Gap: func() time.Duration { return 0 }, Check: service.CheckAccount}
	t.Cleanup(func() { checkAllOpts = oldCheck })

	env.engine = gin.New()
	env.engine.POST("/api/accounts/check-all", AdminCheckAllAccounts)
	env.engine.POST("/api/accounts/quota/refresh", AdminRefreshQuota)
	env.engine.POST("/api/accounts/quota/refresh-all", AdminRefreshAllQuota)
	return env
}

// addQuotaAccount 以 t 名字 + 序号造唯一邮箱，避免 service 层按邮箱缓存的客户端串到别的测试。
func addQuotaAccount(t *testing.T, name, sessionKey string) (email, org string) {
	t.Helper()
	email = strings.ReplaceAll(t.Name(), "/", "_") + "-" + name + "@example.com"
	org = "org-" + strings.ReplaceAll(t.Name(), "/", "_") + "-" + name
	if err := repository.UpsertAccount(&repository.Account{
		Email: email, OrgUUID: org, Cookies: map[string]string{"sessionKey": sessionKey}, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	return email, org
}

func (e *quotaEnv) post(path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

type sseEvent struct {
	Event string
	Data  map[string]any
}

func parseSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var ev sseEvent
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.Data); err != nil {
					t.Fatalf("SSE data 不是 JSON: %q", line)
				}
			}
		}
		out = append(out, ev)
	}
	return out
}

func num(m map[string]any, k string) int {
	f, _ := m[k].(float64)
	return int(f)
}

func (e *quotaEnv) noLeak(t *testing.T, responses string, secrets ...string) {
	t.Helper()
	all := responses + "\n" + e.logs.String()
	for _, s := range append(secrets, handlerSecretBody) {
		if strings.Contains(all, s) {
			t.Fatalf("响应或日志泄漏了 %q", s)
		}
	}
}

func TestAdminRefreshQuotaSingle(t *testing.T) {
	env := newQuotaEnv(t)
	email, _ := addQuotaAccount(t, "a", "sk-ant-sid01-HANDLERSECRET-a")

	w := env.post("/api/accounts/quota/refresh", `{"email":" `+email+` "}`)
	if w.Code != 200 {
		t.Fatalf("code = %d body=%s", w.Code, w.Body)
	}
	var resp struct {
		Result struct {
			Email string `json:"email"`
			OK    bool   `json:"ok"`
			Busy  bool   `json:"busy"`
			Error string `json:"error"`
		} `json:"result"`
		Account struct {
			Email string `json:"email"`
			Quota struct {
				Type     string `json:"type"`
				Source   string `json:"source"`
				FiveHour struct {
					Utilization float64 `json:"utilization"`
					ResetsAt    int64   `json:"resets_at"`
				} `json:"five_hour"`
				SevenDay struct {
					Utilization float64 `json:"utilization"`
				} `json:"seven_day"`
			} `json:"quota"`
		} `json:"account"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Result.OK || resp.Result.Busy || resp.Result.Email != email || resp.Result.Error != "" {
		t.Fatalf("result = %+v", resp.Result)
	}
	q := resp.Account.Quota
	if resp.Account.Email != email || q.Source != "usage_api" || q.Type != "within_limit" ||
		q.FiveHour.Utilization != 0.41 || q.FiveHour.ResetsAt == 0 || q.SevenDay.Utilization != 0.12 {
		t.Fatalf("account.quota = %+v", q)
	}
	env.noLeak(t, w.Body.String(), "HANDLERSECRET")
}

func TestAdminRefreshQuotaFailureStill200(t *testing.T) {
	env := newQuotaEnv(t)
	email, org := addQuotaAccount(t, "a", "sk-ant-sid01-HANDLERSECRET-a")
	env.setStatus(org, 500)

	w := env.post("/api/accounts/quota/refresh", `{"email":"`+email+`"}`)
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	var resp struct {
		Result  map[string]any `json:"result"`
		Account struct {
			Quota struct {
				Error   string `json:"error"`
				ErrorAt string `json:"error_at"`
			} `json:"quota"`
		} `json:"account"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Result["ok"] != false || resp.Result["error"] != "上游返回 HTTP 500" {
		t.Fatalf("result = %+v", resp.Result)
	}
	if resp.Account.Quota.Error != "上游返回 HTTP 500" || resp.Account.Quota.ErrorAt == "" {
		t.Fatalf("快照应带失败原因: %+v", resp.Account.Quota)
	}
	env.noLeak(t, w.Body.String(), "HANDLERSECRET")
}

func TestAdminRefreshQuotaBadRequests(t *testing.T) {
	env := newQuotaEnv(t)
	addQuotaAccount(t, "nosession", "")

	cases := []struct {
		name, body string
		code       int
		msg        string
	}{
		{"空 body", ``, 400, "请提供邮箱"},
		{"空邮箱", `{"email":"  "}`, 400, "请提供邮箱"},
		{"乱 JSON", `{`, 400, "请提供邮箱"},
		{"账号不存在", `{"email":"nobody@example.com"}`, 404, "账号不存在或缺少 sessionKey"},
		{"缺少 sessionKey", `{"email":"` + strings.ReplaceAll(t.Name(), "/", "_") + `-nosession@example.com"}`, 404, "账号不存在或缺少 sessionKey"},
	}
	for _, tc := range cases {
		w := env.post("/api/accounts/quota/refresh", tc.body)
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if w.Code != tc.code || resp["error"] != tc.msg {
			t.Errorf("%s: code=%d body=%s", tc.name, w.Code, w.Body)
		}
	}
}

func TestAdminRefreshAllQuotaSSE(t *testing.T) {
	env := newQuotaEnv(t)
	a, _ := addQuotaAccount(t, "a", "sk-ant-sid01-HANDLERSECRET-a")
	b, orgB := addQuotaAccount(t, "b", "sk-ant-sid01-HANDLERSECRET-b")
	c, _ := addQuotaAccount(t, "c", "sk-ant-sid01-HANDLERSECRET-c")
	env.setStatus(orgB, 429)

	w := env.post("/api/accounts/quota/refresh-all", ``)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("code=%d ct=%s", w.Code, w.Header().Get("Content-Type"))
	}
	events := parseSSE(t, w.Body.String())
	if len(events) != 4 {
		t.Fatalf("事件数 = %d, want 4: %s", len(events), w.Body)
	}
	for i := 0; i < 3; i++ {
		if events[i].Event != "progress" {
			t.Fatalf("第 %d 个事件 = %s", i, events[i].Event)
		}
		if num(events[i].Data, "total") != 3 || num(events[i].Data, "completed") != i+1 {
			t.Fatalf("progress[%d] = %+v", i, events[i].Data)
		}
	}
	wantEmails := []string{a, b, c}
	for i, ev := range events[:3] {
		if ev.Data["email"] != wantEmails[i] {
			t.Fatalf("顺序不对: %v", ev.Data["email"])
		}
		if _, ok := ev.Data["account"].(map[string]any); !ok {
			t.Fatalf("progress 应带 account: %+v", ev.Data)
		}
	}
	if events[1].Data["error"] != "请求过于频繁（429），请稍后再试" || events[0].Data["error"] != "" {
		t.Fatalf("失败信息不对: %+v / %+v", events[0].Data, events[1].Data)
	}
	done := events[3]
	if done.Event != "done" || done.Data["done"] != true || num(done.Data, "total") != 3 ||
		num(done.Data, "ok") != 2 || num(done.Data, "failed") != 1 || num(done.Data, "skipped") != 0 {
		t.Fatalf("done = %+v", done)
	}
	// 失败账号不影响其它账号的快照。
	if q := repository.AccountByEmail(c).Quota; q.FiveHour == nil || q.Error != "" {
		t.Fatalf("c 的快照不对: %+v", q)
	}
	if q := repository.AccountByEmail(b).Quota; q.Error == "" {
		t.Fatalf("b 应记录失败: %+v", q)
	}
	env.noLeak(t, w.Body.String(), "HANDLERSECRET")
}

func TestAdminRefreshAllQuotaConsecutiveRateLimitSkipsRest(t *testing.T) {
	env := newQuotaEnv(t)
	_, o1 := addQuotaAccount(t, "a", "sk-ant-sid01-HANDLERSECRET-a")
	_, o2 := addQuotaAccount(t, "b", "sk-ant-sid01-HANDLERSECRET-b")
	c, _ := addQuotaAccount(t, "c", "sk-ant-sid01-HANDLERSECRET-c")
	env.setStatus(o1, 429)
	env.setStatus(o2, 403)

	w := env.post("/api/accounts/quota/refresh-all", `{}`)
	events := parseSSE(t, w.Body.String())
	if len(events) != 4 {
		t.Fatalf("events = %s", w.Body)
	}
	done := events[3].Data
	if num(done, "ok") != 0 || num(done, "failed") != 2 || num(done, "skipped") != 1 {
		t.Fatalf("done = %+v", done)
	}
	if events[2].Data["error"] != "已跳过（连续被限流，已停止）" {
		t.Fatalf("skipped 提示不对: %+v", events[2].Data)
	}
	if !repository.AccountByEmail(c).Quota.IsZero() {
		t.Fatal("被跳过的账号不应写快照")
	}
	env.mu.Lock()
	hits := len(env.usageHit)
	env.mu.Unlock()
	if hits != 2 {
		t.Fatalf("上游请求数 = %d, want 2（不重试、不请求被跳过账号）", hits)
	}
	env.noLeak(t, w.Body.String(), "HANDLERSECRET")
}

func TestAdminRefreshAllQuotaConflictAndEmpty(t *testing.T) {
	env := newQuotaEnv(t)

	// 没有账号 → 400，且不占用批次锁。
	w := env.post("/api/accounts/quota/refresh-all", ``)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "没有可刷新的账号") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}

	addQuotaAccount(t, "a", "sk-ant-sid01-HANDLERSECRET-a")

	// 已有批次在跑 → 409。
	end, ok := service.TryBeginQuotaBatch()
	if !ok {
		t.Fatal("应能抢到批次锁（上一个请求没释放？）")
	}
	w = env.post("/api/accounts/quota/refresh-all", ``)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "额度刷新正在进行中") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	end()

	// 释放后恢复正常。
	w = env.post("/api/accounts/quota/refresh-all", ``)
	if w.Code != 200 {
		t.Fatalf("释放后 code=%d body=%s", w.Code, w.Body)
	}
}

func TestAdminRefreshAllQuotaConcurrentSecondGets409(t *testing.T) {
	env := newQuotaEnv(t)
	addQuotaAccount(t, "a", "sk-ant-sid01-HANDLERSECRET-a")
	env.mu.Lock()
	env.slow = 600 * time.Millisecond
	env.mu.Unlock()

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- env.post("/api/accounts/quota/refresh-all", ``) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		env.mu.Lock()
		started := len(env.usageHit) > 0
		env.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("第一个批次没有开始")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if w := env.post("/api/accounts/quota/refresh-all", ``); w.Code != 409 {
		t.Fatalf("并发第二个批次 code=%d, want 409", w.Code)
	}
	if w := <-first; w.Code != 200 || !strings.Contains(w.Body.String(), `"done":true`) {
		t.Fatalf("第一个批次 code=%d body=%s", w.Code, w.Body)
	}
}

func TestAdminRefreshAllQuotaTimeoutSkipsRemaining(t *testing.T) {
	env := newQuotaEnv(t)
	for _, n := range []string{"a", "b", "c"} {
		addQuotaAccount(t, n, "sk-ant-sid01-HANDLERSECRET-"+n)
	}
	env.mu.Lock()
	env.slow = 3 * time.Second
	env.mu.Unlock()
	quotaBatchOpts.Timeout = 300 * time.Millisecond

	start := time.Now()
	w := env.post("/api/accounts/quota/refresh-all", ``)
	if time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("总超时没有生效: %v", time.Since(start))
	}
	events := parseSSE(t, w.Body.String())
	done := events[len(events)-1]
	if done.Event != "done" || num(done.Data, "ok") != 0 || num(done.Data, "skipped") != 3 {
		t.Fatalf("done = %+v\n%s", done, w.Body)
	}
	env.noLeak(t, w.Body.String(), "HANDLERSECRET")
}
