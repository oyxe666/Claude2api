package service

import (
	"testing"
	"time"

	"claude2api/internal/config"
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
// balQ 构造一个新鲜快照：5 小时用量 util，窗口未重置。
func balQ(now time.Time, util float64) repository.AccountQuota {
return repository.AccountQuota{UpdatedAt: now.Add(-time.Minute), FiveHour: &repository.QuotaWindow{Utilization: f64(util), ResetsAt: now.Unix() + 3600}}
}

func emails(as []repository.Account) string {
s := ""
for _, a := range as {
s += a.Email
}
return s
}

func TestBalanceSkipsLowRemain(t *testing.T) {
now := time.Unix(1_000_000, 0)
in := []repository.Account{acctWith("a", balQ(now, 0.1)), acctWith("b", balQ(now, 0.5)), acctWith("c", balQ(now, 0.65))}
if got := emails(balanceByFiveHour(in, now, 0.30)); got != "a" {
t.Fatalf("应只留 a，got %q", got)
}
in = []repository.Account{acctWith("a", balQ(now, 0.1)), acctWith("b", balQ(now, 0.3))}
if got := emails(balanceByFiveHour(in, now, 0.30)); got != "ab" {
t.Fatalf("差 20 个百分点应都留，got %q", got)
}
}

func TestBalanceUnknownNotSkipped(t *testing.T) {
now := time.Unix(1_000_000, 0)
stale := balQ(now, 0.99)
stale.UpdatedAt = now.Add(-11 * time.Minute)
in := []repository.Account{
acctWith("a", balQ(now, 0.1)),
acctWith("n", repository.AccountQuota{}),
acctWith("s", stale),
acctWith("f", repository.AccountQuota{UpdatedAt: now, FiveHour: nil}),
acctWith("u", repository.AccountQuota{UpdatedAt: now, FiveHour: &repository.QuotaWindow{ResetsAt: now.Unix() + 60}}),
}
if got := emails(balanceByFiveHour(in, now, 0.30)); got != "ansfu" {
t.Fatalf("未知快照都不应被跳过，got %q", got)
}
}

func TestBalanceAllFilteredPassThrough(t *testing.T) {
now := time.Unix(1_000_000, 0)
if got := balanceByFiveHour([]repository.Account{}, now, 0.3); len(got) != 0 {
t.Fatalf("空切片应返回空，got %d", len(got))
}
unknown := []repository.Account{acctWith("x", repository.AccountQuota{}), acctWith("y", repository.AccountQuota{})}
if got := balanceByFiveHour(unknown, now, 0.3); len(got) != 2 {
t.Fatalf("全部未知应原样放行，got %d", len(got))
}
one := []repository.Account{acctWith("a", balQ(now, 0.9))}
if got := balanceByFiveHour(one, now, 0.3); len(got) != 1 {
t.Fatalf("单账号应保留，got %d", len(got))
}
same := []repository.Account{acctWith("a", balQ(now, 0.5)), acctWith("b", balQ(now, 0.5))}
if got := balanceByFiveHour(same, now, 0.3); len(got) != 2 {
t.Fatalf("剩余相同应全留，got %d", len(got))
}
}

func TestBalanceWindowResetCountsFull(t *testing.T) {
now := time.Unix(1_000_000, 0)
reset := balQ(now, 0.95)
reset.FiveHour.ResetsAt = now.Unix() - 1
in := []repository.Account{acctWith("a", reset), acctWith("b", balQ(now, 0.8)), acctWith("c", balQ(now, 0.1))}
if got := emails(balanceByFiveHour(in, now, 0.30)); got != "ac" {
t.Fatalf("窗口已重置按满额算，b 应被跳过，got %q", got)
}
// 快照已过期：即使 ResetsAt 已过也算未知，不当作满额
stale := reset
stale.UpdatedAt = now.Add(-11 * time.Minute)
in = []repository.Account{acctWith("a", stale), acctWith("b", balQ(now, 0.4)), acctWith("c", balQ(now, 0.2))}
if got := emails(balanceByFiveHour(in, now, 0.30)); got != "abc" {
t.Fatalf("过期快照 a 应为未知（不当作满额；b 与 c 差 20 点，都留），got %q", got)
}
}

func TestBalanceDisabled(t *testing.T) {
now := time.Unix(1_000_000, 0)
in := []repository.Account{acctWith("a", balQ(now, 0.1)), acctWith("b", balQ(now, 0.9))}
for _, gap := range []float64{0, -0.5} {
if got := emails(balanceByFiveHour(in, now, gap)); got != "ab" {
t.Fatalf("gap=%v 应原样返回，got %q", gap, got)
}
}
}

func TestBalanceBoundaryNotSkipped(t *testing.T) {
now := time.Unix(1_000_000, 0)
in := []repository.Account{acctWith("a", balQ(now, 0.1)), acctWith("b", balQ(now, 0.4))}
if got := emails(balanceByFiveHour(in, now, 0.30)); got != "ab" {
t.Fatalf("差距恰好 0.30 不应跳过，got %q", got)
}
in = []repository.Account{acctWith("a", balQ(now, 0.1)), acctWith("b", balQ(now, 0.41))}
if got := emails(balanceByFiveHour(in, now, 0.30)); got != "a" {
t.Fatalf("差距 0.31 应跳过，got %q", got)
}
}

func TestBalancePreservesOrder(t *testing.T) {
now := time.Unix(1_000_000, 0)
in := []repository.Account{acctWith("d", balQ(now, 0.2)), acctWith("x", balQ(now, 0.9)), acctWith("b", balQ(now, 0.1)), acctWith("c", balQ(now, 0.3))}
if got := emails(balanceByFiveHour(in, now, 0.30)); got != "dbc" {
t.Fatalf("应保持相对顺序，got %q", got)
}
}

func TestPickAPIAccountUsesBalance(t *testing.T) {
setupDB(t)
now := time.Now()
for i, e := range []string{"hi@example.com", "mid@example.com", "low@example.com"} {
addAccount(t, e, testOrg, "sk-test-"+e)
util := []float64{0.1, 0.2, 0.8}[i]
repository.UpdateAccountQuota(e, func(q *repository.AccountQuota) {
*q = repository.AccountQuota{UpdatedAt: now, FiveHour: &repository.QuotaWindow{Utilization: f64(util), ResetsAt: now.Unix() + 3600}}
})
}
seen := func() map[string]bool {
m := map[string]bool{}
for i := 0; i < 12; i++ {
m[pickAPIAccount().Email] = true
}
return m
}
defer config.Update(map[string]any{"quota_balance_gap": 0})
config.Update(map[string]any{"quota_balance_gap": 0.3})
if m := seen(); m["low@example.com"] || !m["hi@example.com"] || !m["mid@example.com"] {
t.Fatalf("gap=0.3 时低剩余号不应出现: %v", m)
}
config.Update(map[string]any{"quota_balance_gap": 0})
if m := seen(); len(m) != 3 {
t.Fatalf("gap=0 时三个号都应出现: %v", m)
}
}
