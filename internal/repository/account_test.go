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
