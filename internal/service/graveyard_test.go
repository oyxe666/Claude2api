package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"claude2api/internal/config"
	"claude2api/internal/repository"
)

// fakeAccountAPI 让 GET /api/account 返回指定状态与响应体，其余路径 404。
func fakeAccountAPI(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/account" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(SetClaudeBaseURLForTest(srv.URL))
}

const invalidBody = `{"error":{"type":"account_session_invalid"}}`

func graveFor(email string) *repository.Grave {
	for _, g := range repository.ListGraves(100, 0) {
		if g.Email == email {
			g := g
			return &g
		}
	}
	return nil
}

func setRemoveInvalid(t *testing.T, v bool) {
	t.Helper()
	config.Update(map[string]any{"remove_invalid_account": v})
	t.Cleanup(func() { config.Update(map[string]any{"remove_invalid_account": false}) })
}

func TestRefreshAccountExpiredRecordsGrave(t *testing.T) {
	setupDB(t)
	setRemoveInvalid(t, false)
	fakeAccountAPI(t, 403, invalidBody)
	addAccount(t, "a@example.com", testOrg, "sk-test-a")

	acct, removed := RefreshAccount("a@example.com")
	if removed || acct == nil || acct.Status != "expired" {
		t.Fatalf("acct=%+v removed=%v", acct, removed)
	}
	if g := graveFor("a@example.com"); g == nil || g.Reason != repository.GraveSessionInvalid {
		t.Fatalf("墓碑不对: %+v", g)
	}
}

func TestRefreshAccountRemovedRecordsGrave(t *testing.T) {
	setupDB(t)
	setRemoveInvalid(t, true)
	fakeAccountAPI(t, 403, invalidBody)
	addAccount(t, "a@example.com", testOrg, "sk-test-a")

	_, removed := RefreshAccount("a@example.com")
	if !removed {
		t.Fatal("应返回 removed=true")
	}
	if repository.AccountByEmail("a@example.com") != nil {
		t.Fatal("账号应已被删除")
	}
	if g := graveFor("a@example.com"); g == nil || g.Reason != repository.GraveAutoRemoved {
		t.Fatalf("墓碑不对: %+v", g)
	}
}

func TestRefreshAccountErrorNoGrave(t *testing.T) {
	setupDB(t)
	setRemoveInvalid(t, true)
	fakeAccountAPI(t, 500, `{"error":"boom"}`)
	addAccount(t, "a@example.com", testOrg, "sk-test-a")

	acct, removed := RefreshAccount("a@example.com")
	if removed || acct == nil || acct.Status != "error" {
		t.Fatalf("acct=%+v removed=%v", acct, removed)
	}
	if repository.CountGraves() != 0 {
		t.Fatal("上游 500 不应入乱葬岗")
	}
}

func TestMarkExpiredRecordsGrave(t *testing.T) {
	setupDB(t)
	addAccount(t, "a@example.com", testOrg, "sk-test-a")
	markExpired("a@example.com")
	if a := repository.AccountByEmail("a@example.com"); a == nil || a.Status != "expired" {
		t.Fatalf("账号状态不对: %+v", a)
	}
	if g := graveFor("a@example.com"); g == nil || g.Reason != repository.GraveSessionInvalid {
		t.Fatalf("墓碑不对: %+v", g)
	}
	markExpired("a@example.com")
	if repository.CountGraves() != 1 {
		t.Fatal("不应重复记录")
	}
}

func TestDropInvalidSession(t *testing.T) {
	setupDB(t)
	invalid := errors.New("HTTP 403: account_session_invalid")

	addAccount(t, "keep@example.com", testOrg, "sk-test-k")
	for name, c := range map[string]struct {
		remove bool
		err    error
	}{
		"关闭自动移除": {false, invalid},
		"其他错误":   {true, errors.New("HTTP 500")},
		"无错误":    {true, nil},
	} {
		if dropInvalidSession("keep@example.com", c.err, c.remove) {
			t.Errorf("%s: 不应删号", name)
		}
	}
	if repository.AccountByEmail("keep@example.com") == nil || repository.CountGraves() != 0 {
		t.Fatal("账号应仍在且无墓碑")
	}

	if !dropInvalidSession("keep@example.com", invalid, true) {
		t.Fatal("应返回 true")
	}
	if repository.AccountByEmail("keep@example.com") != nil {
		t.Fatal("账号应被删除")
	}
	if g := graveFor("keep@example.com"); g == nil || g.Reason != repository.GraveAutoRemoved {
		t.Fatalf("墓碑不对: %+v", g)
	}
}

func TestCheckAccountExpiredRecordsGrave(t *testing.T) {
	setupDB(t)
	setRemoveInvalid(t, true)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(invalidBody))
	})
	addAccount(t, "a@example.com", testOrg, "sk-test-a")

	res := CheckAccount(context.Background(), "a@example.com")
	if res.OK {
		t.Fatalf("result = %+v", res)
	}
	if a := repository.AccountByEmail("a@example.com"); a == nil || a.Status != "expired" {
		t.Fatalf("账号应被置 expired 而非删除: %+v", a)
	}
	if g := graveFor("a@example.com"); g == nil || g.Reason != repository.GraveSessionInvalid {
		t.Fatalf("墓碑不对: %+v", g)
	}
}
