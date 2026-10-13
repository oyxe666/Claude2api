package service

import (
	"testing"
	"time"

	"claude2api/internal/repository"
)

func f64(v float64) *float64 { return &v }

func acctWith(email string, q repository.AccountQuota) repository.Account {
	return repository.Account{Email: email, Quota: q}
}

func TestWindowBlocked(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	upd := now.Add(-time.Minute)
	cases := []struct {
		name string
		w    *repository.QuotaWindow
		want bool
	}{
		{"nil 窗口", nil, false},
		{"用量未满", &repository.QuotaWindow{Status: "within_limit", Utilization: f64(0.5), ResetsAt: now.Unix() + 100}, false},
		{"状态耗尽且未到重置", &repository.QuotaWindow{Status: "exceeded_limit", ResetsAt: now.Unix() + 100}, true},
		{"用量 100% 且未到重置", &repository.QuotaWindow{Utilization: f64(1), ResetsAt: now.Unix() + 100}, true},
		{"耗尽但重置时间已过", &repository.QuotaWindow{Status: "exceeded_limit", Utilization: f64(1), ResetsAt: now.Unix() - 1}, false},
		{"耗尽且无重置时间：保守等待内", &repository.QuotaWindow{Status: "exceeded_limit"}, true},
	}
	for _, c := range cases {
		if got := windowBlocked(c.w, upd, now); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	// 无重置时间：超过保守等待后放行
	old := now.Add(-exhaustedNoResetHold - time.Second)
	if windowBlocked(&repository.QuotaWindow{Status: "exceeded_limit"}, old, now) {
		t.Error("超过保守等待时长后应放行")
	}
	// 无重置时间且无更新时间：不能永久封死
	if windowBlocked(&repository.QuotaWindow{Status: "exceeded_limit"}, time.Time{}, now) {
		t.Error("没有更新时间时不应视为耗尽")
	}
}

func TestSkipExhausted(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	full := repository.AccountQuota{UpdatedAt: now, FiveHour: &repository.QuotaWindow{Status: "exceeded_limit", ResetsAt: now.Unix() + 600}}
	fullSeven := repository.AccountQuota{UpdatedAt: now, SevenDay: &repository.QuotaWindow{Utilization: f64(1), ResetsAt: now.Unix() + 600}}
	fine := repository.AccountQuota{UpdatedAt: now, FiveHour: &repository.QuotaWindow{Utilization: f64(0.2), ResetsAt: now.Unix() + 600}}

	got := skipExhausted([]repository.Account{acctWith("a", full), acctWith("b", fine), acctWith("c", fullSeven), acctWith("d", repository.AccountQuota{})}, now)
	if len(got) != 2 || got[0].Email != "b" || got[1].Email != "d" {
		t.Fatalf("应只保留 b、d（没有快照的号视为可用），got %+v", got)
	}

	// 全部耗尽：原样返回，让请求去试一次
	all := []repository.Account{acctWith("a", full), acctWith("c", fullSeven)}
	if got := skipExhausted(all, now); len(got) != 2 {
		t.Fatalf("全部耗尽时应原样返回，got %+v", got)
	}
}
