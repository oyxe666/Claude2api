package repository

import (
	"testing"
	"time"
)

func TestSurvivalStatsEmpty(t *testing.T) {
	graveDB(t)
	st := GetSurvivalStats(7, time.Now())
	if st.Imported != 0 || st.AliveRate != 0 || st.Rate7 != 0 || st.Rate30 != 0 || len(st.Curve) != 0 {
		t.Fatalf("空库应全 0: %+v", st)
	}
	if len(st.Daily) != 7 {
		t.Fatalf("daily 长度 = %d", len(st.Daily))
	}
}

func TestSurvivalStatsMixed(t *testing.T) {
	graveDB(t)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	day := 24 * time.Hour
	mkAcct(t, "old@example.com", "active", now.Add(-40*day))
	mkAcct(t, "new@example.com", "active", now.Add(-2*day))
	// 40 天前导入，活 5 天阵亡
	db.Create(&Grave{Email: "ga@example.com", JoinedAt: now.Add(-40 * day), DiedAt: now.Add(-35 * day), LifespanSec: 5 * 86400, Reason: GraveSessionInvalid})
	// 10 天前导入，活 8 天阵亡（手动删除也计入）
	db.Create(&Grave{Email: "gb@example.com", JoinedAt: now.Add(-10 * day), DiedAt: now.Add(-2 * day), LifespanSec: 8 * 86400, Reason: GraveManualDelete})

	st := GetSurvivalStats(7, now)
	if st.Imported != 4 || st.Alive != 2 || st.Died != 2 || st.AliveRate != 0.5 {
		t.Fatalf("总览不对: %+v", st)
	}
	if st.Eligible7 != 3 || st.Survived7 != 2 {
		t.Fatalf("7 天口径不对: %+v", st)
	}
	if st.Eligible30 != 2 || st.Survived30 != 1 || st.Rate30 != 0.5 {
		t.Fatalf("30 天口径不对: %+v", st)
	}
	if len(st.Curve) == 0 || st.Curve[0].Survival != 1 {
		t.Fatalf("曲线第 0 天应为 1: %+v", st.Curve)
	}
	for i := 1; i < len(st.Curve); i++ {
		if st.Curve[i].Survival > st.Curve[i-1].Survival {
			t.Fatalf("曲线应单调不增: %+v", st.Curve)
		}
	}
	if len(st.Daily) != 7 || st.Daily[6].Date != "2026-10-14" {
		t.Fatalf("daily 不对: %+v", st.Daily)
	}
	// 10-12：new 导入 1 个，gb 阵亡 1 个
	if d := st.Daily[4]; d.Date != "2026-10-12" || d.Imported != 1 || d.Died != 1 {
		t.Fatalf("10-12 不对: %+v", d)
	}
	if len(GetSurvivalStats(99, now).Daily) != 7 || len(GetSurvivalStats(30, now).Daily) != 30 {
		t.Fatal("days 夹取不对")
	}
}

func TestSurvivalStatsAllAlive(t *testing.T) {
	graveDB(t)
	now := time.Now()
	mkAcct(t, "a@example.com", "active", now.Add(-10*24*time.Hour))
	st := GetSurvivalStats(7, now)
	if st.AliveRate != 1 || st.Rate7 != 1 || st.Died != 0 {
		t.Fatalf("全活不对: %+v", st)
	}
	for _, p := range st.Curve {
		if p.Survival != 1 {
			t.Fatalf("全活曲线应恒为 1: %+v", st.Curve)
		}
	}
}
