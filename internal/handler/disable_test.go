package handler

import (
	"net/http"
	"testing"

	"claude2api/internal/repository"
)

func TestAdminSetAccountDisabled(t *testing.T) {
	e := graveEnv(t)
	e.POST("/api/accounts/disable", AdminSetAccountDisabled)
	e.POST("/api/accounts/disable-all", AdminSetAllAccountsDisabled)
	graveAcct(t, "a@example.com", "active")

	if w := doReq(e, "POST", "/api/accounts/disable", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("缺邮箱应 400，got %d", w.Code)
	}
	if w := doReq(e, "POST", "/api/accounts/disable", `{"email":"nobody@example.com","disabled":true}`); w.Code != http.StatusNotFound {
		t.Fatalf("账号不存在应 404，got %d", w.Code)
	}
	w := doReq(e, "POST", "/api/accounts/disable", `{"email":"a@example.com","disabled":true}`)
	if w.Code != http.StatusOK || !repository.AccountByEmail("a@example.com").Disabled {
		t.Fatalf("停用失败: %d %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); contains(body, "SECRET-SK") {
		t.Fatalf("响应不应含敏感字段: %s", body)
	}
	if w := doReq(e, "POST", "/api/accounts/disable-all", `{"disabled":false}`); w.Code != http.StatusOK || repository.AccountByEmail("a@example.com").Disabled {
		t.Fatalf("全部启用失败: %d", w.Code)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
