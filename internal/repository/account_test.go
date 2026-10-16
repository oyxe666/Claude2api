package repository

import (
	"testing"
	"time"
)

// 重复导入同一邮箱：保留首次导入时间，其它字段照常更新，额度快照不被清空。
func TestUpsertAccountKeepsCreatedAt(t *testing.T) {
	graveDB(t)
	first := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Second)
	mkAcct(t, "a@example.com", "active", first)
	UpdateAccountQuota("a@example.com", func(q *AccountQuota) {
		q.UpdatedAt = time.Now()
		q.FiveHour = &QuotaWindow{Utilization: ptr(0.5)}
	})

	if err := UpsertAccount(&Account{
		Email: "a@example.com", OrgUUID: "ORG-NEW", Cookies: map[string]string{"sessionKey": "NEW-SK"},
		Status: "active", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	a := AccountByEmail("a@example.com")
	if a == nil {
		t.Fatal("账号应存在")
	}
	if d := a.CreatedAt.Sub(first); d < -time.Second || d > time.Second {
		t.Fatalf("created_at 被改动: got %v, want %v", a.CreatedAt, first)
	}
	if a.OrgUUID != "ORG-NEW" || a.Cookies["sessionKey"] != "NEW-SK" {
		t.Fatalf("其它字段应更新: %+v", a)
	}
	if a.Quota.FiveHour == nil {
		t.Fatal("quota 不应被清空")
	}
}

// 停用状态：能切换，账号不存在返回 false，重复导入不冲掉，批量切换生效。
func TestSetAccountDisabled(t *testing.T) {
	graveDB(t)
	mkAcct(t, "a@example.com", "active", time.Now())
	mkAcct(t, "b@example.com", "active", time.Now())
	if a := AccountByEmail("a@example.com"); a == nil || a.Disabled {
		t.Fatal("新账号默认应参与调度（disabled=false）")
	}
	if !SetAccountDisabled("a@example.com", true) {
		t.Fatal("停用应成功")
	}
	if !AccountByEmail("a@example.com").Disabled {
		t.Fatal("a 应已停用")
	}
	if AccountByEmail("b@example.com").Disabled {
		t.Fatal("b 不应受影响")
	}
	if SetAccountDisabled("nobody@example.com", true) {
		t.Fatal("账号不存在应返回 false")
	}
	// 重复导入同邮箱不冲掉停用状态
	if err := UpsertAccount(&Account{Email: "a@example.com", OrgUUID: "O", Cookies: map[string]string{"sessionKey": "K"}, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if !AccountByEmail("a@example.com").Disabled {
		t.Fatal("重复导入不应重置停用状态")
	}
	// 额度快照写入不影响停用状态
	UpdateAccountQuota("a@example.com", func(q *AccountQuota) { q.Type = "within_limit" })
	if !AccountByEmail("a@example.com").Disabled {
		t.Fatal("写额度不应改停用状态")
	}
	if n := SetAllAccountsDisabled(false); n != 2 {
		t.Fatalf("批量启用应影响 2 个，got %d", n)
	}
	if AccountByEmail("a@example.com").Disabled {
		t.Fatal("批量启用后 a 应启用")
	}
}
