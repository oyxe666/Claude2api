package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude2api/internal/config"

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