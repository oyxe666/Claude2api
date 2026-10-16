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
// balQ 构造一个快照：5 小时用量 util，窗口未重置。
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

func TestFiveHourRemain(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	near := func(got, want float64) bool { return got > want-1e-9 && got < want+1e-9 }

	// 没有快照 / 没有用量：按满额
	for name, q := range map[string]repository.AccountQuota{
		"无快照":   {},
		"无五小时窗口": {UpdatedAt: now},
		"无用量":    {UpdatedAt: now, FiveHour: &repository.QuotaWindow{ResetsAt: now.Unix() + 60}},
	} {
		a := acctWith("x", q)
		if got := fiveHourRemain(&a, now); !near(got, 1) {
			t.Errorf("%s 应按满额，got %v", name, got)
		}
	}

	// 快照很旧但窗口没重置：仍按旧用量算（不过期）
	old := balQ(now, 0.9)
	old.UpdatedAt = now.Add(-3 * time.Hour)
	a := acctWith("old", old)
	if got := fiveHourRemain(&a, now); !near(got, 0.1) {
		t.Errorf("3 小时前的快照、窗口未重置，应剩 0.1，got %v", got)
	}

	// 窗口已重置：满额
	reset := balQ(now, 0.95)
	reset.FiveHour.ResetsAt = now.Unix() - 1
	a = acctWith("r", reset)
	if got := fiveHourRemain(&a, now); !near(got, 1) {
		t.Errorf("窗口已重置应满额，got %v", got)
	}
}

func TestBalanceKeepsOnlyNearMax(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	in := []repository.Account{acctWith("a", balQ(now, 0.1)), acctWith("b", balQ(now, 0.12)), acctWith("c", balQ(now, 0.5))}
	if got := emails(balanceByFiveHour(in, now, 0.05)); got != "ab" {
		t.Fatalf("应只留 a、b，got %q", got)
	}
}

func TestBalanceStaleSnapshotStillCounts(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	stale := balQ(now, 0.1)
	stale.UpdatedAt = now.Add(-5 * time.Hour)
	in := []repository.Account{acctWith("a", balQ(now, 0.8)), acctWith("s", stale)}
	if got := emails(balanceByFiveHour(in, now, 0.05)); got != "s" {
		t.Fatalf("旧快照的满号应参与并胜出，got %q", got)
	}
}

func TestBalanceNoSnapshotCountsFull(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	in := []repository.Account{acctWith("a", balQ(now, 0.3)), acctWith("n", repository.AccountQuota{})}
	if got := emails(balanceByFiveHour(in, now, 0.05)); got != "n" {
		t.Fatalf("没快照按满额，应只留 n，got %q", got)
	}
}

func TestBalanceEdgeCases(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	if got := balanceByFiveHour([]repository.Account{}, now, 0.3); len(got) != 0 {
		t.Fatalf("空切片应返回空，got %d", len(got))
	}
	one := []repository.Account{acctWith("a", balQ(now, 0.9))}
	if got := balanceByFiveHour(one, now, 0.05); len(got) != 1 {
		t.Fatalf("单账号应保留，got %d", len(got))
	}
	same := []repository.Account{acctWith("a", balQ(now, 0.5)), acctWith("b", balQ(now, 0.5))}
	if got := balanceByFiveHour(same, now, 0.05); len(got) != 2 {
		t.Fatalf("剩余相同应全留，got %d", len(got))
	}
}

func TestBalanceWindowResetCountsFull(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	reset := balQ(now, 0.95)
	reset.FiveHour.ResetsAt = now.Unix() - 1
	in := []repository.Account{acctWith("a", reset), acctWith("b", balQ(now, 0.1))}
	if got := emails(balanceByFiveHour(in, now, 0.05)); got != "a" {
		t.Fatalf("窗口已重置按满额算，应只留 a，got %q", got)
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

func TestLeastRecentlyPicked(t *testing.T) {
	apiIndexLock.Lock()
	defer apiIndexLock.Unlock()
	saved := lastPicked
	lastPicked = map[string]uint64{}
	defer func() { lastPicked = saved }()

	cands := []repository.Account{{Email: "a"}, {Email: "b"}, {Email: "c"}}
	if got := leastRecentlyPicked(cands).Email; got != "a" {
		t.Fatalf("都没选过取靠前者 a，got %q", got)
	}
	lastPicked["a"], lastPicked["b"] = 5, 3
	if got := leastRecentlyPicked(cands).Email; got != "c" {
		t.Fatalf("c 从未选过应最先，got %q", got)
	}
	lastPicked["c"] = 9
	if got := leastRecentlyPicked(cands).Email; got != "b" {
		t.Fatalf("b 最久没用，got %q", got)
	}
}

func TestPickAPIAccountUsesBalance(t *testing.T) {
	setupDB(t)
	now := time.Now()
	for i, e := range []string{"hi@example.com", "mid@example.com", "low@example.com"} {
		addAccount(t, e, testOrg, "sk-test-"+e)
		util := []float64{0.1, 0.12, 0.8}[i]
		repository.UpdateAccountQuota(e, func(q *repository.AccountQuota) {
			*q = repository.AccountQuota{UpdatedAt: now.Add(-2 * time.Hour), FiveHour: &repository.QuotaWindow{Utilization: f64(util), ResetsAt: now.Unix() + 3600}}
		})
	}
	seen := func() map[string]int {
		m := map[string]int{}
		for i := 0; i < 12; i++ {
			m[pickAPIAccount().Email]++
		}
		return m
	}
	defer config.Update(map[string]any{"quota_balance_gap": 0})
	config.Update(map[string]any{"quota_balance_gap": 0.05})
	m := seen()
	if m["low@example.com"] != 0 || m["hi@example.com"] != 6 || m["mid@example.com"] != 6 {
		t.Fatalf("gap=0.05 时低剩余号不应出现，其余两个均匀轮流: %v", m)
	}
	config.Update(map[string]any{"quota_balance_gap": 0})
	if m := seen(); len(m) != 3 {
		t.Fatalf("gap=0 时三个号都应出现: %v", m)
	}
}

func TestPickAPIAccountSkipsDisabled(t *testing.T) {
	setupDB(t)
	addAccount(t, "on@example.com", testOrg, "sk-on")
	addAccount(t, "off@example.com", testOrg, "sk-off")
	repository.SetAccountDisabled("off@example.com", true)
	for i := 0; i < 10; i++ {
		if a := pickAPIAccount(); a == nil || a.Email != "on@example.com" {
			t.Fatalf("停用的号不应被选中，got %+v", a)
		}
	}
	// 停用号仍在额度批量检查的目标里
	found := false
	for _, e := range QuotaBatchTargets() {
		if e == "off@example.com" {
			found = true
		}
	}
	if !found {
		t.Fatal("停用号的额度仍应被检查")
	}
	// 全部停用：选号返回 nil，且识别为“全部被停用”
	repository.SetAllAccountsDisabled(true)
	if a := pickAPIAccount(); a != nil {
		t.Fatalf("全部停用时应返回 nil，got %+v", a)
	}
	if !hasOnlyDisabledAccounts() {
		t.Fatal("应识别为所有账号都已停用")
	}
	if v := PublicAccountView(repository.AccountByEmail("off@example.com")); !v.Disabled {
		t.Fatal("公开视图应带 disabled")
	}
	// 重新启用后恢复
	repository.SetAllAccountsDisabled(false)
	if pickAPIAccount() == nil {
		t.Fatal("启用后应能选到号")
	}
}
