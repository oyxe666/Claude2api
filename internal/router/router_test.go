package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/middleware"

	"github.com/gin-gonic/gin"
)

// 两个额度接口挂在 admin 组下，没有凭据必须 401（不会触发任何上游请求）。
func TestQuotaEndpointsRequireAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := NewMainEngine()
	for _, path := range []string{"/api/accounts/quota/refresh", "/api/accounts/quota/refresh-all"} {
		for _, auth := range []string{"", "Bearer wrong-password"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"email":"a@example.com"}`))
			req.Header.Set("Content-Type", "application/json")
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s auth=%q: code = %d, want 401", path, auth, w.Code)
			}
			if !strings.Contains(w.Body.String(), "未登录或密码错误") {
				t.Errorf("%s: body = %s", path, w.Body)
			}
		}
	}
}

// 带正确管理密码时能通过鉴权（空邮箱 → 业务层 400，而不是 401）。
func TestQuotaEndpointPassesAuthWithPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.Get().AdminPassword
	config.Update(map[string]any{"admin_password": "test-admin-pass"})
	t.Cleanup(func() { config.Update(map[string]any{"admin_password": old}) })

	engine := NewMainEngine()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/quota/refresh", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-admin-pass")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "请提供邮箱") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
}

// /api/events：只有 GET 且带正确凭据（Bearer 或登录 cookie）才能通过；错误凭据一律 401。
func TestEventsAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.Get().AdminPassword
	config.Update(map[string]any{"admin_password": "test-admin-pass"})
	t.Cleanup(func() { config.Update(map[string]any{"admin_password": old}) })

	srv := httptest.NewServer(NewMainEngine())
	defer srv.Close()

	do := func(method, path string, hdr map[string]string, cookie string) (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: middleware.AuthCookieName, Value: cookie})
		}
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf) // 流式响应读到第一块即可，超时 ctx 会收尾
		return resp.StatusCode, string(buf[:n])
	}

	if code, _ := do("GET", "/api/events", nil, ""); code != 401 {
		t.Errorf("无凭据 code=%d, want 401", code)
	}
	if code, _ := do("GET", "/api/events", map[string]string{"Authorization": "Bearer wrong"}, ""); code != 401 {
		t.Errorf("错误 Bearer code=%d, want 401", code)
	}
	if code, _ := do("GET", "/api/events", nil, "wrong"); code != 401 {
		t.Errorf("错误 cookie code=%d, want 401", code)
	}
	code, body := do("GET", "/api/events", nil, "test-admin-pass")
	if code != 200 || !strings.Contains(body, ": connected") {
		t.Errorf("正确 cookie code=%d body=%q, want 200 + connected", code, body)
	}
	if code, _ := do("GET", "/api/events", map[string]string{"Authorization": "Bearer test-admin-pass"}, ""); code != 200 {
		t.Errorf("正确 Bearer code=%d, want 200", code)
	}
	// 例外是窄的：其它方法/路径带 cookie 但无 Bearer 仍然 401。
	// POST /api/events 没有这条路由（落到 NoRoute 反代/跳转），无论如何不能是 200 的事件流。
	if code, body := do("POST", "/api/events", nil, "test-admin-pass"); code == 200 && strings.Contains(body, ": connected") {
		t.Errorf("POST /api/events 带 cookie 不应得到事件流: code=%d", code)
	}
	if code, _ := do("GET", "/api/accounts", nil, "test-admin-pass"); code != 401 {
		t.Errorf("GET /api/accounts 带 cookie code=%d, want 401", code)
	}
	if code, _ := do("POST", "/api/accounts/check-all", nil, "test-admin-pass"); code != 401 {
		t.Errorf("check-all 带 cookie code=%d, want 401", code)
	}
	if code, _ := do("POST", "/api/accounts/check-all", nil, ""); code != 401 {
		t.Errorf("check-all 无凭据 code=%d, want 401", code)
	}
}

// 乱葬岗两个接口挂在 admin 组下：无凭据/错误凭据 401，正确管理密码 200。
func TestGraveyardEndpointsAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.Get().AdminPassword
	config.Update(map[string]any{"admin_password": "test-admin-pass"})
	t.Cleanup(func() { config.Update(map[string]any{"admin_password": old}) })

	engine := NewMainEngine()
	for _, path := range []string{"/api/graveyard", "/api/graveyard/stats"} {
		for _, auth := range []string{"", "Bearer wrong-password"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s auth=%q: code = %d, want 401", path, auth, w.Code)
			}
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer test-admin-pass")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s 带正确密码: code = %d, want 200", path, w.Code)
		}
	}
}

// prefs.js / grave.js 必须被嵌入并可读到；首页要引用它们，并含乱葬岗页面与均衡阈值输入框。
func TestNewStaticAssetsAndPages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := NewMainEngine()
	for _, name := range []string{"prefs.js", "grave.js"} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/static/"+name, nil))
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("/static/%s: code=%d len=%d", name, w.Code, w.Body.Len())
		}
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	body := w.Body.String()
	ai := strings.Index(body, "/static/app.js")
	for _, name := range []string{"prefs.js", "grave.js"} {
		i := strings.Index(body, "/static/"+name)
		if i < 0 || ai < 0 || i > ai {
			t.Fatalf("index.html 必须先引用 %s 再引用 app.js", name)
		}
	}
	for _, want := range []string{`data-page="graveyard"`, `id="page-graveyard"`, `id="cfg-quota_balance_gap"`, `id="page-size"`, `id="logs-page-size"`} {
		if !strings.Contains(body, want) {
			t.Errorf("首页缺少 %s", want)
		}
	}
}

// quota.js 必须被嵌入，并能经 /static/quota.js 读到；首页要引用它。
func TestQuotaScriptEmbeddedAndServed(t *testing.T) {
gin.SetMode(gin.TestMode)
engine := NewMainEngine()

w := httptest.NewRecorder()
engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/static/quota.js", nil))
if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "QuotaUI") {
t.Fatalf("/static/quota.js: code=%d", w.Code)
}

w = httptest.NewRecorder()
engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
body := w.Body.String()
qi, ai := strings.Index(body, "/static/quota.js"), strings.Index(body, "/static/app.js")
if qi < 0 || ai < 0 || qi > ai {
t.Fatalf("index.html 必须先引用 quota.js 再引用 app.js: quota=%d app=%d", qi, ai)
}
}
// 定时刷新状态接口挂在 admin 组下，没有凭据必须 401。
func TestScheduleStatusRequiresAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := NewMainEngine()
	for _, auth := range []string{"", "Bearer wrong-password"} {
		req := httptest.NewRequest(http.MethodGet, "/api/schedule/status", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("auth=%q: code = %d, want 401", auth, w.Code)
		}
	}
}

// 乱葬岗每日统计接口必须有管理鉴权。
func TestGraveyardDailyRequiresAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := NewMainEngine()
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/graveyard/daily?days=7", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
}

// 乱葬岗存活统计接口必须有管理鉴权。
func TestGraveyardSurvivalRequiresAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := NewMainEngine()
	for _, auth := range []string{"", "Bearer wrong-password"} {
		req := httptest.NewRequest(http.MethodGet, "/api/graveyard/survival?days=7", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("auth=%q: code = %d, want 401", auth, w.Code)
		}
	}
}

func TestDisableRoutesRequireAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewMainEngine()
	for _, path := range []string{"/api/accounts/disable", "/api/accounts/disable-all"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s 无凭据应 401，got %d", path, w.Code)
		}
	}
}
