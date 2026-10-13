package repository

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// legacyAccount 模拟「没有 quota 列」的老库表结构。
type legacyAccount struct {
	ID        uint              `gorm:"primaryKey"`
	Email     string            `gorm:"uniqueIndex;not null"`
	OrgUUID   string            `gorm:"column:org_uuid"`
	Cookies   map[string]string `gorm:"serializer:json"`
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (legacyAccount) TableName() string { return "accounts" }

func ptr(f float64) *float64 { return &f }

// 三个测试共用同一个临时老库，按顺序执行。
func TestQuotaLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")

	// 1. 造老库：只有旧列，插入一条旧账号。
	old, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.AutoMigrate(&legacyAccount{}); err != nil {
		t.Fatal(err)
	}
	if err := old.Create(&legacyAccount{
		Email:   "old@example.com",
		Cookies: map[string]string{"sessionKey": "sk-test-fake"},
		Status:  "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := old.DB(); err == nil {
		sqlDB.Close()
	}

	// 2. 用新代码打开：不报错、数据不丢、quota 为空。
	conn, err := openDB(path)
	if err != nil {
		t.Fatalf("老库打开失败: %v", err)
	}
	db = conn
	t.Cleanup(func() {
		if sqlDB, err := conn.DB(); err == nil {
			sqlDB.Close()
		}
	})

	accs := LoadAccounts()
	if len(accs) != 1 {
		t.Fatalf("账号数 = %d, want 1", len(accs))
	}
	a := accs[0]
	if a.Email != "old@example.com" || a.Status != "active" || a.Cookies["sessionKey"] != "sk-test-fake" {
		t.Fatalf("老数据被改动: %+v", a)
	}
	if !a.Quota.IsZero() {
		t.Fatalf("老账号 quota 应为空: %+v", a.Quota)
	}

	// 3. 写入额度，只动 quota 列。
	ok := UpdateAccountQuota("old@example.com", func(q *AccountQuota) {
		q.Type = "within_limit"
		q.FiveHour = &QuotaWindow{Status: "within_limit", Utilization: ptr(0.01), ResetsAt: 1791740400}
		q.UpdatedAt = time.Now()
	})
	if !ok {
		t.Fatal("UpdateAccountQuota 应返回 true")
	}
	a = LoadAccounts()[0]
	if a.Quota.Type != "within_limit" || a.Quota.FiveHour == nil ||
		a.Quota.FiveHour.Utilization == nil || *a.Quota.FiveHour.Utilization != 0.01 ||
		a.Quota.FiveHour.ResetsAt != 1791740400 || a.Quota.FiveHour.Status != "within_limit" ||
		a.Quota.SevenDay != nil || a.Quota.IsZero() {
		t.Fatalf("quota 读回不一致: %+v", a.Quota)
	}
	if a.Status != "active" || a.Cookies["sessionKey"] != "sk-test-fake" {
		t.Fatalf("写 quota 不应改其它列: %+v", a)
	}
	if UpdateAccountQuota("nobody@example.com", func(q *AccountQuota) { q.Type = "x" }) {
		t.Fatal("不存在的邮箱应返回 false")
	}

	// 4. 重新导入同邮箱账号：其它列更新，quota 保留。
	if err := UpsertAccount(&Account{
		Email:   "old@example.com",
		OrgUUID: "org2",
		Cookies: map[string]string{"sessionKey": "sk-test-fake2"},
		Status:  "active",
	}); err != nil {
		t.Fatal(err)
	}
	a = LoadAccounts()[0]
	if a.OrgUUID != "org2" || a.Cookies["sessionKey"] != "sk-test-fake2" {
		t.Fatalf("Upsert 未更新: %+v", a)
	}
	if a.Quota.Type != "within_limit" || a.Quota.FiveHour == nil || a.Quota.FiveHour.ResetsAt != 1791740400 {
		t.Fatalf("Upsert 清空了 quota: %+v", a.Quota)
	}
}

// UpdateAccount 整行回写不能覆盖 quota；对已含 quota 列的库再次 openDB 也要幂等。
func TestUpdateAccountKeepsQuotaAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	conn, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db = conn
	if err := UpsertAccount(&Account{Email: "a@example.com", Cookies: map[string]string{"sessionKey": "sk-test-fake"}, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if !UpdateAccountQuota("a@example.com", func(q *AccountQuota) {
		q.Type = "within_limit"
		q.UpdatedAt = time.Now()
	}) {
		t.Fatal("UpdateAccountQuota 应返回 true")
	}
	// mutate 期间有人写入新快照，随后整行回写：新快照不能被旧值盖掉。
	UpdateAccount("a@example.com", func(a *Account) {
		UpdateAccountQuota("a@example.com", func(q *AccountQuota) { q.Type = "exceeded_limit" })
		a.OrgUUID = "org9"
	})
	got := LoadAccounts()[0]
	if got.OrgUUID != "org9" || got.Quota.Type != "exceeded_limit" {
		t.Fatalf("UpdateAccount 覆盖了 quota 或没更新其它列: %+v", got)
	}
	if sqlDB, err := conn.DB(); err == nil {
		sqlDB.Close()
	}
	// 二次打开已含 quota 列的库：不报错、数据与快照都在。
	conn2, err := openDB(path)
	if err != nil {
		t.Fatalf("二次打开失败: %v", err)
	}
	db = conn2
	t.Cleanup(func() {
		if sqlDB, err := conn2.DB(); err == nil {
			sqlDB.Close()
		}
	})
	got = LoadAccounts()[0]
	if got.Email != "a@example.com" || got.Quota.Type != "exceeded_limit" {
		t.Fatalf("二次打开后数据丢失: %+v", got)
	}
}

// 旧库里 quota 列的各种历史形态（NULL / 空串 / 旧格式 JSON / 新格式 JSON）都必须能打开、能读出。
func TestOpenOldDBExtendedQuota(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	conn, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct{ email, quota string }{
		{"null@example.com", ""},
		{"empty@example.com", ""},
		{"old@example.com", `{"type":"within_limit","five_hour":{"status":"within_limit","utilization":0.2,"resets_at":1791740400},"updated_at":"2026-10-11T08:00:00Z"}`},
		{"new@example.com", `{"type":"within_limit","five_hour":{"utilization":0.41},"seven_day":{"utilization":0.12},"updated_at":"2026-10-11T08:00:00Z","source":"usage_api","error":"上游返回 HTTP 500","error_at":"2026-10-11T09:00:00Z","seven_day_opus":{"utilization":0.5},"seven_day_sonnet":{"utilization":1,"status":"exceeded_limit"}}`},
	}
	for _, r := range rows {
		var res *gorm.DB
		if r.email == "null@example.com" {
			res = conn.Exec("INSERT INTO accounts (email, cookies, status, quota, created_at, updated_at) VALUES (?, '{}', 'active', NULL, datetime('now'), datetime('now'))", r.email)
		} else {
			res = conn.Exec("INSERT INTO accounts (email, cookies, status, quota, created_at, updated_at) VALUES (?, '{}', 'active', ?, datetime('now'), datetime('now'))", r.email, r.quota)
		}
		if res.Error != nil {
			t.Fatal(res.Error)
		}
	}
	if sqlDB, err := conn.DB(); err == nil {
		sqlDB.Close()
	}

	conn2, err := openDB(path)
	if err != nil {
		t.Fatalf("旧库打开失败: %v", err)
	}
	db = conn2
	t.Cleanup(func() {
		if sqlDB, err := conn2.DB(); err == nil {
			sqlDB.Close()
		}
	})
	accs := LoadAccounts()
	if len(accs) != 4 {
		t.Fatalf("账号数 = %d, want 4", len(accs))
	}
	by := map[string]Account{}
	for _, a := range accs {
		by[a.Email] = a
	}
	for _, e := range []string{"null@example.com", "empty@example.com"} {
		if !by[e].Quota.IsZero() {
			t.Fatalf("%s quota 应为零值: %+v", e, by[e].Quota)
		}
	}
	old := by["old@example.com"].Quota
	if old.IsZero() || old.FiveHour == nil || *old.FiveHour.Utilization != 0.2 {
		t.Fatalf("旧格式读取不对: %+v", old)
	}
	if old.Source != "" || old.Error != "" || !old.ErrorAt.IsZero() || old.SevenDayOpus != nil || old.SevenDaySonnet != nil {
		t.Fatalf("旧格式的新字段应为零值: %+v", old)
	}
	nq := by["new@example.com"].Quota
	if nq.Source != "usage_api" || nq.Error != "上游返回 HTTP 500" || nq.ErrorAt.IsZero() ||
		nq.SevenDayOpus == nil || *nq.SevenDayOpus.Utilization != 0.5 ||
		nq.SevenDaySonnet == nil || nq.SevenDaySonnet.Status != "exceeded_limit" {
		t.Fatalf("新格式读取不对: %+v", nq)
	}

	// 对每种形态写入新快照后读回。
	for _, e := range []string{"null@example.com", "empty@example.com", "old@example.com", "new@example.com"} {
		at := time.Date(2026, 10, 12, 1, 2, 3, 0, time.UTC)
		if !UpdateAccountQuota(e, func(q *AccountQuota) {
			*q = AccountQuota{Type: "within_limit", FiveHour: &QuotaWindow{Utilization: ptr(0.3)}, UpdatedAt: at, Source: "usage_api"}
		}) {
			t.Fatalf("%s UpdateAccountQuota 失败", e)
		}
		got := AccountByEmail(e).Quota
		if got.Source != "usage_api" || got.FiveHour == nil || *got.FiveHour.Utilization != 0.3 || !got.UpdatedAt.Equal(at) || got.Error != "" || !got.ErrorAt.IsZero() {
			t.Fatalf("%s 读回不对: %+v", e, got)
		}
	}
	// 失败合并：只写 Error/ErrorAt，旧窗口保留。
	errAt := time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC)
	UpdateAccountQuota("null@example.com", func(q *AccountQuota) { q.Error, q.ErrorAt = "网络请求失败", errAt })
	got := AccountByEmail("null@example.com").Quota
	if got.Error != "网络请求失败" || !got.ErrorAt.Equal(errAt) || got.FiveHour == nil || *got.FiveHour.Utilization != 0.3 {
		t.Fatalf("失败合并不对: %+v", got)
	}
}

func TestAccountQuotaIsZero(t *testing.T) {
	at := time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		q    AccountQuota
		want bool
	}{
		{"全空", AccountQuota{}, true},
		{"只有 UpdatedAt", AccountQuota{UpdatedAt: at}, false},
		{"只有 ErrorAt", AccountQuota{ErrorAt: at, Error: "x"}, false},
		{"两者都有", AccountQuota{UpdatedAt: at, ErrorAt: at}, false},
		{"只有窗口没有时间", AccountQuota{FiveHour: &QuotaWindow{}}, true},
	}
	for _, c := range cases {
		if got := c.q.IsZero(); got != c.want {
			t.Errorf("%s: IsZero = %v, want %v", c.name, got, c.want)
		}
	}
}

// InitDBAt 会创建父目录；CloseDB 后可再次初始化。
func TestInitDBAtAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "app.db")
	if err := InitDBAt(path); err != nil {
		t.Fatal(err)
	}
	if err := UpsertAccount(&Account{Email: "a@example.com", Cookies: map[string]string{"sessionKey": "sk-test-fake"}, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	CloseDB()
	if err := InitDBAt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(CloseDB)
	if len(LoadAccounts()) != 1 {
		t.Fatal("重新打开后数据丢失")
	}
}
