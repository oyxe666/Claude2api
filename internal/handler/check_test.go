package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"claude2api/internal/service"
)

func TestCheckAllSingleRoundPerAccount(t *testing.T) {
	env := newQuotaEnv(t)
	_, o1 := addQuotaAccount(t, "a", "sk-ant-sid01-HANDLERSECRET-a")
	_, o2 := addQuotaAccount(t, "b", "sk-ant-sid01-HANDLERSECRET-b")
	_, o3 := addQuotaAccount(t, "c", "sk-ant-sid01-HANDLERSECRET-c")

	w := env.post("/api/accounts/check-all", ``)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") ||
		w.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("code=%d headers=%v", w.Code, w.Header())
	}
	events := parseSSE(t, w.Body.String())
	if len(events) != 5 {
		t.Fatalf("事件数 = %d, want 5 (start+3 progress+done): %s", len(events), w.Body)
	}
	if events[0].Event != "start" || num(events[0].Data, "total") != 3 {
		t.Fatalf("start = %+v", events[0])
	}
	if _, ok := events[0].Data["gap_min_sec"]; !ok {
		t.Fatalf("start 缺 gap_min_sec: %+v", events[0].Data)
	}
	for i := 1; i <= 3; i++ {
		if events[i].Event != "progress" || num(events[i].Data, "completed") != i {
			t.Fatalf("progress[%d] = %+v", i, events[i])
		}
	}
	done := events[4]
	if done.Event != "done" || num(done.Data, "ok") != 3 || num(done.Data, "skipped") != 0 {
		t.Fatalf("done = %+v", done)
	}
	env.mu.Lock()
	hits := map[string]int{}
	for _, o := range env.usageHit {
		hits[o]++
	}
	env.mu.Unlock()
	for _, o := range []string{o1, o2, o3} {
		if hits[o] != 1 {
			t.Fatalf("%s 的 /usage 请求数 = %d, want 1（每个账号只发一轮）", o, hits[o])
		}
	}
	env.noLeak(t, w.Body.String(), "HANDLERSECRET")
}

func TestCheckAllConflict(t *testing.T) {
	env := newQuotaEnv(t)
	addQuotaAccount(t, "a", "sk-ant-sid01-HANDLERSECRET-a")
	end, ok := service.TryBeginQuotaBatch()
	if !ok {
		t.Fatal("应能抢到批次锁")
	}
	w := env.post("/api/accounts/check-all", ``)
	if w.Code != 409 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	end()
	if w := env.post("/api/accounts/check-all", ``); w.Code != 200 {
		t.Fatalf("释放后 code=%d", w.Code)
	}
}

func TestCheckAllNoTargets(t *testing.T) {
	env := newQuotaEnv(t)
	w := env.post("/api/accounts/check-all", ``)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "没有可检查的账号") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
}

func TestCheckAllStopsOnClientDisconnect(t *testing.T) {
	env := newQuotaEnv(t)
	for _, n := range []string{"a", "b", "c"} {
		addQuotaAccount(t, n, "sk-ant-sid01-HANDLERSECRET-"+n)
	}
	env.mu.Lock()
	env.slow = 5 * time.Second
	env.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/check-all", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		env.engine.ServeHTTP(w, req)
		close(finished)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		env.mu.Lock()
		n := len(env.usageHit)
		env.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("第一个账号没有开始")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel() // 模拟点“停止”（浏览器断开）

	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("客户端断开后 handler 没有及时返回")
	}
	env.mu.Lock()
	hits := len(env.usageHit)
	env.mu.Unlock()
	if hits != 1 {
		t.Fatalf("断开后不应再请求其它账号，/usage 请求数 = %d", hits)
	}
	events := parseSSE(t, w.Body.String())
	done := events[len(events)-1]
	if done.Event != "done" || num(done.Data, "skipped") != 3 {
		t.Fatalf("done = %+v\n%s", done, w.Body)
	}
}
