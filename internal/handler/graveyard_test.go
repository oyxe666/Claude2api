package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude2api/internal/repository"

	"github.com/gin-gonic/gin"
)

func graveEnv(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if err := repository.InitDBAt(filepath.Join(t.TempDir(), "app.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repository.CloseDB)
	e := gin.New()
	e.POST("/api/delete", AdminDeleteAccount)
	e.POST("/api/delete-expired", AdminDeleteExpired)
	e.GET("/api/graveyard", AdminGraveyard)
	e.GET("/api/graveyard/stats", AdminGraveyardStats)
	return e
}

func graveAcct(t *testing.T, email, status string) {
	t.Helper()
	if err := repository.UpsertAccount(&repository.Account{
		Email: email, OrgUUID: "ORG-SECRET", Cookies: map[string]string{"sessionKey": "SECRET-SK"}, Status: status,
	}); err != nil {
		t.Fatal(err)
	}
}

func doReq(e *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w
}

func graveReasons() map[string]string {
	m := map[string]string{}
	for _, g := range repository.ListGraves(100, 0) {
		m[g.Email] = g.Reason
	}
	return m
}

func TestAdminDeleteAccountRecordsGrave(t *testing.T) {
	e := graveEnv(t)
	graveAcct(t, "a@example.com", "active")
	w := doReq(e, "POST", "/api/delete", `{"email":"a@example.com"}`)
	var out struct{ Removed int }
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out.Removed != 1 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	if repository.AccountByEmail("a@example.com") != nil {
		t.Fatal("账号应已删除")
	}
	if r := graveReasons(); r["a@example.com"] != repository.GraveManualDelete {
		t.Fatalf("墓碑不对: %v", r)
	}
	for _, body := range []string{w.Body.String(), doReq(e, "GET", "/api/graveyard", "").Body.String()} {
		low := strings.ToLower(body)
		if strings.Contains(low, "secret-sk") || strings.Contains(low, "org-secret") {
			t.Fatalf("响应含敏感值: %s", body)
		}
	}
}

func TestAdminDeleteAccountMissing(t *testing.T) {
	e := graveEnv(t)
	w := doReq(e, "POST", "/api/delete", `{"email":"none@example.com"}`)
	if !strings.Contains(w.Body.String(), `"removed":0`) || repository.CountGraves() != 0 {
		t.Fatalf("body=%s graves=%d", w.Body, repository.CountGraves())
	}
}

func TestAdminDeleteExpiredRecordsGraves(t *testing.T) {
	e := graveEnv(t)
	graveAcct(t, "e1@example.com", "expired")
	graveAcct(t, "e2@example.com", "expired")
	graveAcct(t, "ok@example.com", "active")
	repository.RecordGrave("e1@example.com", repository.GraveSessionInvalid)

	w := doReq(e, "POST", "/api/delete-expired", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"removed":2`) {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	r := graveReasons()
	if len(r) != 2 || r["e1@example.com"] != repository.GraveSessionInvalid || r["e2@example.com"] != repository.GraveManualDelete {
		t.Fatalf("墓碑不对: %v", r)
	}
	if repository.AccountByEmail("ok@example.com") == nil {
		t.Fatal("active 账号应还在")
	}
}

func TestDeleteAfterExpiredNoDuplicate(t *testing.T) {
	e := graveEnv(t)
	graveAcct(t, "a@example.com", "active")
	repository.MarkAccountExpired("a@example.com")
	doReq(e, "POST", "/api/delete", `{"email":"a@example.com"}`)
	if repository.CountGraves() != 1 || graveReasons()["a@example.com"] != repository.GraveSessionInvalid {
		t.Fatalf("应只有 1 条 session_invalid: %v", graveReasons())
	}
}

func TestAdminGraveyardList(t *testing.T) {
	e := graveEnv(t)

	w := doReq(e, "GET", "/api/graveyard", "")
	if !strings.Contains(w.Body.String(), `"graves":[]`) || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("空库应返回 graves:[] total:0，got %s", w.Body)
	}

	for i := 0; i < 3; i++ {
		email := fmt.Sprintf("g%d@example.com", i)
		graveAcct(t, email, "active")
		repository.RecordGrave(email, repository.GraveManualDelete)
		time.Sleep(5 * time.Millisecond) // 保证 died_at 递增
	}
	var out struct {
		Graves []repository.Grave `json:"graves"`
		Total  int                `json:"total"`
	}
	json.Unmarshal(doReq(e, "GET", "/api/graveyard", "").Body.Bytes(), &out)
	if out.Total != 3 || len(out.Graves) != 3 || out.Graves[0].Email != "g2@example.com" {
		t.Fatalf("默认应倒序: %+v", out)
	}
	out.Graves = nil
	json.Unmarshal(doReq(e, "GET", "/api/graveyard?limit=2&offset=1", "").Body.Bytes(), &out)
	if len(out.Graves) != 2 || out.Graves[0].Email != "g1@example.com" || out.Graves[1].Email != "g0@example.com" || out.Total != 3 {
		t.Fatalf("分页不对: %+v", out)
	}
}

func TestAdminGraveyardStats(t *testing.T) {
	e := graveEnv(t)
	w := doReq(e, "GET", "/api/graveyard/stats", "")
	if !strings.Contains(w.Body.String(), `"total":0`) || !strings.Contains(w.Body.String(), `"avg_lifespan_sec":0`) || !strings.Contains(w.Body.String(), `"last_7_days":0`) {
		t.Fatalf("空库应全 0: %s", w.Body)
	}

	graveAcct(t, "a@example.com", "active")
	graveAcct(t, "b@example.com", "active")
	repository.RecordGrave("a@example.com", repository.GraveSessionInvalid)
	doReq(e, "POST", "/api/delete", `{"email":"b@example.com"}`) // 手动删除也计入
	var st repository.GraveStats
	json.Unmarshal(doReq(e, "GET", "/api/graveyard/stats", "").Body.Bytes(), &st)
	if st.Total != 2 || st.Last7Days != 2 {
		t.Fatalf("stats = %+v", st)
	}
	if code := doReq(e, "GET", "/api/graveyard/stats", "").Code; code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
}
