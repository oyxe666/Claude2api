package repository

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func graveDB(t *testing.T) {
	t.Helper()
	if err := InitDBAt(filepath.Join(t.TempDir(), "app.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(CloseDB)
}

func mkAcct(t *testing.T, email, status string, created time.Time) {
	t.Helper()
	if err := UpsertAccount(&Account{
		Email: email, OrgUUID: "ORG-SECRET", Cookies: map[string]string{"sessionKey": "SECRET-SK"},
		Status: status, CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecordGraveBasic(t *testing.T) {
	graveDB(t)
	mkAcct(t, "a@example.com", "active", time.Now().Add(-3*24*time.Hour))
	UpdateAccountQuota("a@example.com", func(q *AccountQuota) {
		q.UpdatedAt = time.Now()
		q.FiveHour = &QuotaWindow{Utilization: ptr(0.42)}
	})
	for i := 0; i < 3; i++ {
		InsertAPILog(APILog{Account: "a@example.com", Success: true})
	}
	InsertAPILog(APILog{Account: "a@example.com", Success: false})
	InsertAPILog(APILog{Account: "other@example.com", Success: true})

	if !RecordGrave("a@example.com", GraveSessionInvalid) {
		t.Fatal("RecordGrave 应返回 true")
	}
	gs := ListGraves(10, 0)
	if len(gs) != 1 {
		t.Fatalf("墓碑数 = %d", len(gs))
	}
	g := gs[0]
	if g.Email != "a@example.com" || g.Reason != GraveSessionInvalid || g.ChatCount != 3 {
		t.Fatalf("记录不对: %+v", g)
	}
	if d := g.LifespanSec - 3*24*3600; d < -5 || d > 5 {
		t.Fatalf("寿命 = %d 秒，应约 3 天", g.LifespanSec)
	}
	if g.LastQuota.FiveHour == nil || g.LastQuota.FiveHour.Utilization == nil || *g.LastQuota.FiveHour.Utilization != 0.42 {
		t.Fatalf("LastQuota 不对: %+v", g.LastQuota)
	}
}

func TestRecordGraveDedup(t *testing.T) {
	graveDB(t)
	mkAcct(t, "a@example.com", "active", time.Now())
	if !RecordGrave("a@example.com", GraveSessionInvalid) {
		t.Fatal("首次应成功")
	}
	if RecordGrave("a@example.com", GraveManualDelete) {
		t.Fatal("同邮箱第二次应返回 false")
	}
	gs := ListGraves(10, 0)
	if len(gs) != 1 || gs[0].Reason != GraveSessionInvalid {
		t.Fatalf("应只有 1 条且保留最早原因: %+v", gs)
	}
}

func TestRecordGraveNoAccountOrEmpty(t *testing.T) {
	graveDB(t)
	if RecordGrave("none@example.com", GraveManualDelete) || RecordGrave("", GraveManualDelete) {
		t.Fatal("不存在的账号/空邮箱应返回 false")
	}
	if CountGraves() != 0 {
		t.Fatal("表应为空")
	}
}

func TestGraveHasNoSensitiveData(t *testing.T) {
	graveDB(t)
	mkAcct(t, "a@example.com", "active", time.Now())
	RecordGrave("a@example.com", GraveManualDelete)
	g := ListGraves(1, 0)[0]
	b, _ := json.Marshal(g)
	low := strings.ToLower(string(b))
	for _, bad := range []string{"secret-sk", "org-secret", "sessionkey", "cookies", "org_uuid"} {
		if strings.Contains(low, bad) {
			t.Fatalf("JSON 含敏感字样 %q: %s", bad, b)
		}
	}
	cols, err := db.Migrator().ColumnTypes(&Grave{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cols {
		n := strings.ToLower(c.Name())
		if strings.Contains(n, "cookies") || strings.Contains(n, "org_uuid") || strings.Contains(n, "session") {
			t.Fatalf("graveyard 表不应有列 %s", n)
		}
	}
	rows, err := db.Raw("SELECT * FROM graveyard").Rows()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cs, _ := rows.Columns()
	for rows.Next() {
		vals := make([]any, len(cs))
		ptrs := make([]any, len(cs))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		line := strings.ToLower(fmt.Sprint(vals...))
		for _, bad := range []string{"secret-sk", "org-secret"} {
			if strings.Contains(line, bad) {
				t.Fatalf("整行含敏感值 %q", bad)
			}
		}
	}
}

func TestMarkAccountExpired(t *testing.T) {
	graveDB(t)
	mkAcct(t, "a@example.com", "active", time.Now())
	if !MarkAccountExpired("a@example.com") {
		t.Fatal("应返回 true")
	}
	if a := AccountByEmail("a@example.com"); a.Status != "expired" {
		t.Fatalf("status = %q", a.Status)
	}
	gs := ListGraves(10, 0)
	if len(gs) != 1 || gs[0].Reason != GraveSessionInvalid {
		t.Fatalf("墓碑不对: %+v", gs)
	}
	MarkAccountExpired("a@example.com")
	if CountGraves() != 1 {
		t.Fatal("不应重复记录")
	}
	if MarkAccountExpired("none@example.com") {
		t.Fatal("账号不存在应返回 false")
	}
}

func TestDeleteAccountsByStatusRecordsManual(t *testing.T) {
	graveDB(t)
	mkAcct(t, "e1@example.com", "expired", time.Now())
	mkAcct(t, "e2@example.com", "expired", time.Now())
	mkAcct(t, "ok@example.com", "active", time.Now())
	RecordGrave("e1@example.com", GraveSessionInvalid)

	removed := DeleteAccountsByStatus([]string{"expired"})
	if len(removed) != 2 {
		t.Fatalf("removed = %v", removed)
	}
	reasons := map[string]string{}
	for _, g := range ListGraves(10, 0) {
		reasons[g.Email] = g.Reason
	}
	if len(reasons) != 2 || reasons["e1@example.com"] != GraveSessionInvalid || reasons["e2@example.com"] != GraveManualDelete {
		t.Fatalf("墓碑不对: %v", reasons)
	}
	if AccountByEmail("ok@example.com") == nil {
		t.Fatal("active 账号不应被删")
	}
}

func TestListGravesOrderAndPaging(t *testing.T) {
	graveDB(t)
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		db.Create(&Grave{Email: fmt.Sprintf("g%d@example.com", i), DiedAt: now.Add(time.Duration(i) * time.Hour), Reason: GraveManualDelete})
	}
	all := ListGraves(0, 0)
	if len(all) != 5 || all[0].Email != "g4@example.com" || all[4].Email != "g0@example.com" {
		t.Fatalf("应倒序: %+v", all)
	}
	pg := ListGraves(2, 1)
	if len(pg) != 2 || pg[0].Email != "g3@example.com" || pg[1].Email != "g2@example.com" {
		t.Fatalf("分页不对: %+v", pg)
	}
	if got := ListGraves(1000, -5); len(got) != 5 {
		t.Fatalf("越界参数应被夹住: %d", len(got))
	}
	if CountGraves() != 5 {
		t.Fatalf("count = %d", CountGraves())
	}
}

func TestGraveStats(t *testing.T) {
	graveDB(t)
	if st := GetGraveStats(); st != (GraveStats{}) {
		t.Fatalf("空表应全 0: %+v", st)
	}
	now := time.Now().UTC()
	db.Create(&Grave{Email: "a@example.com", LifespanSec: 100, DiedAt: now.Add(-time.Hour), Reason: GraveSessionInvalid})
	db.Create(&Grave{Email: "b@example.com", LifespanSec: 200, DiedAt: now.Add(-24 * time.Hour), Reason: GraveManualDelete})
	db.Create(&Grave{Email: "c@example.com", LifespanSec: 300, DiedAt: now.Add(-8 * 24 * time.Hour), Reason: GraveAutoRemoved})
	st := GetGraveStats()
	if st.Total != 3 || st.AvgLifespanSec != 200 || st.Last7Days != 2 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestAutoMigrateKeepsOldData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	if err := InitDBAt(path); err != nil {
		t.Fatal(err)
	}
	mkAcct(t, "a@example.com", "active", time.Now())
	CloseDB()
	if err := InitDBAt(path); err != nil {
		t.Fatalf("重开失败: %v", err)
	}
	t.Cleanup(CloseDB)
	if !db.Migrator().HasTable("graveyard") {
		t.Fatal("graveyard 表应存在")
	}
	if a := AccountByEmail("a@example.com"); a == nil || a.Cookies["sessionKey"] != "SECRET-SK" {
		t.Fatal("账号数据不应变")
	}
}
