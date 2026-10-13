package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"claude2api/internal/repository"
)

func TestDefaultCheckBatchOptionsGapRange(t *testing.T) {
	oldMin, oldMax := CheckGapMin, CheckGapMax
	t.Cleanup(func() { CheckGapMin, CheckGapMax = oldMin, oldMax })

	CheckGapMin, CheckGapMax = 3*time.Second, 8*time.Second
	opts := DefaultCheckBatchOptions()
	if opts.Check == nil || opts.Timeout != 0 {
		t.Fatalf("opts = %+v", opts)
	}
	for i := 0; i < 200; i++ {
		if g := opts.Gap(); g < 3*time.Second || g > 8*time.Second {
			t.Fatalf("Gap = %v 超出 [3s,8s]", g)
		}
	}
	CheckGapMin, CheckGapMax = 0, 0
	if g := opts.Gap(); g != 0 {
		t.Fatalf("0/0 时 Gap = %v, want 0", g)
	}
	CheckGapMin, CheckGapMax = 5*time.Second, 2*time.Second
	if g := opts.Gap(); g != 5*time.Second {
		t.Fatalf("Max<Min 时 Gap = %v, want Min", g)
	}
}

func TestCheckAccountSetsActiveAfterError(t *testing.T) {
	setupDB(t)
	f := newFakeClaude(t, nil)
	addAccount(t, "chk@example.com", testOrg, "sk-test-chk")
	repository.UpdateAccount("chk@example.com", func(a *repository.Account) { a.Status = "error" })

	res := CheckAccount(context.Background(), "chk@example.com")
	if !res.OK {
		t.Fatalf("res = %+v", res)
	}
	if st := repository.AccountByEmail("chk@example.com").Status; st != "active" {
		t.Fatalf("status = %q, want active", st)
	}
	if n := f.count("GET " + "/api/organizations/" + testOrg + "/usage"); n != 1 {
		t.Fatalf("/usage 请求数 = %d, want 1", n)
	}
	if n := f.count("GET /api/account"); n != 0 {
		t.Fatalf("不应再调用 /api/account（旧的第二轮请求），实际 %d", n)
	}
}

func TestCheckAccountExpiredKeepsAccount(t *testing.T) {
	setupDB(t)
	newFakeClaude(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	addAccount(t, "exp@example.com", testOrg, "sk-test-exp")

	res := CheckAccount(context.Background(), "exp@example.com")
	if res.OK {
		t.Fatalf("res = %+v", res)
	}
	a := repository.AccountByEmail("exp@example.com")
	if a == nil || a.Status != "expired" {
		t.Fatalf("账号应保留且为 expired: %+v", a)
	}
}
