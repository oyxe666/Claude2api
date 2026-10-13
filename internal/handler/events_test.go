package handler

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude2api/internal/repository"
	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

// readLines 把响应体按行送进 channel，便于带超时等待。
func readLines(resp *http.Response) <-chan string {
	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			ch <- sc.Text()
		}
	}()
	return ch
}

func waitLine(t *testing.T, ch <-chan string, prefix string) string {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case l, ok := <-ch:
			if !ok {
				t.Fatalf("流提前结束，未等到 %q", prefix)
			}
			if strings.HasPrefix(l, prefix) {
				return l
			}
		case <-timeout:
			t.Fatalf("3 秒内没等到 %q", prefix)
		}
	}
}

func TestAdminEventsStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := repository.InitDBAt(filepath.Join(t.TempDir(), "app.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repository.CloseDB)
	if err := repository.UpsertAccount(&repository.Account{
		Email: "live@example.com", OrgUUID: "ORG-SECRET",
		Cookies: map[string]string{"sessionKey": "SECRET-SESSION"}, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	oldHB := eventsHeartbeat
	eventsHeartbeat = 50 * time.Millisecond
	t.Cleanup(func() { eventsHeartbeat = oldHB })
	service.InstallQuotaBroadcast()
	t.Cleanup(func() { repository.OnQuotaChanged = nil })

	engine := gin.New()
	engine.GET("/api/events", AdminEvents)
	srv := httptest.NewServer(engine)
	defer srv.Close()

	base := service.EventSubscriberCount()
	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("响应头不对: %v", resp.Header)
	}
	lines := readLines(resp)
	waitLine(t, lines, ": connected")
	waitLine(t, lines, ": ping")

	repository.UpdateAccountQuota("live@example.com", func(q *repository.AccountQuota) {
		q.Type = "within_limit"
		q.UpdatedAt = time.Now()
	})
	waitLine(t, lines, "event: quota")
	data := waitLine(t, lines, "data: ")
	if !strings.Contains(data, `"email":"live@example.com"`) {
		t.Fatalf("data = %s", data)
	}
	for _, bad := range []string{"SECRET-SESSION", "ORG-SECRET", "sessionKey", "Cookies", "org_uuid"} {
		if strings.Contains(data, bad) {
			t.Fatalf("事件泄漏了 %q: %s", bad, data)
		}
	}

	if service.EventSubscriberCount() != base+1 {
		t.Fatalf("订阅数 = %d, want %d", service.EventSubscriberCount(), base+1)
	}
	resp.Body.Close() // 客户端断开
	deadline := time.Now().Add(3 * time.Second)
	for service.EventSubscriberCount() != base {
		if time.Now().After(deadline) {
			t.Fatal("客户端断开后 handler 没有退出（订阅数未回落）")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
