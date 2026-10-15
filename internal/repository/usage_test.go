package repository

import (
	"testing"
	"time"
)

func TestGetUsageStats(t *testing.T) {
	graveDB(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, graveBeijing)
	at := func(d time.Time) string { return d.UTC().Format("2006-01-02T15:04:05Z") }

	InsertAPILog(APILog{CreatedAt: at(now), Model: "m1", InputTokens: 100, OutputTokens: 50, Success: true})
	InsertAPILog(APILog{CreatedAt: at(now.Add(-time.Hour)), Model: "m1", InputTokens: 10, OutputTokens: 5, Success: false})
	InsertAPILog(APILog{CreatedAt: at(now.AddDate(0, 0, -1)), Model: "m2", InputTokens: 1000, OutputTokens: 500, Success: true})
	// 北京 10-16 00:30 = UTC 10-15 16:30，必须算进 10-16
	InsertAPILog(APILog{CreatedAt: at(time.Date(2026, 10, 16, 0, 30, 0, 0, graveBeijing)), Model: "m1", InputTokens: 1, OutputTokens: 1, Success: true})
	// 8 天前，不在 7 天范围内
	InsertAPILog(APILog{CreatedAt: at(now.AddDate(0, 0, -8)), Model: "m3", InputTokens: 999, OutputTokens: 999, Success: true})

	st := GetUsageStats(7, now)
	if st.Days != 7 || len(st.Daily) != 7 {
		t.Fatalf("want 7 days, got %d/%d", st.Days, len(st.Daily))
	}
	if st.Daily[6].Date != "2026-10-16" || st.Daily[0].Date != "2026-10-10" {
		t.Fatalf("bad date range: %s ~ %s", st.Daily[0].Date, st.Daily[6].Date)
	}
	last := st.Daily[6]
	if last.Calls != 3 || last.Success != 2 || last.InputTokens != 111 || last.OutputTokens != 56 {
		t.Fatalf("today wrong: %+v", last)
	}
	if y := st.Daily[5]; y.Calls != 1 || y.InputTokens != 1000 {
		t.Fatalf("yesterday wrong: %+v", y)
	}
	if st.Daily[0].Calls != 0 {
		t.Fatalf("zero day should be filled with zeros: %+v", st.Daily[0])
	}
	if len(st.Models) != 2 || st.Models[0].Model != "m2" || st.Models[1].Model != "m1" {
		t.Fatalf("models should be sorted by tokens desc and exclude out-of-range: %+v", st.Models)
	}
	if st.Models[1].Calls != 3 {
		t.Fatalf("m1 calls wrong: %+v", st.Models[1])
	}

	if got := GetUsageStats(99, now); got.Days != 7 {
		t.Fatalf("invalid days should fall back to 7, got %d", got.Days)
	}
	if got := GetUsageStats(30, now); got.Days != 30 || len(got.Daily) != 30 || len(got.Models) != 3 {
		t.Fatalf("30 days wrong: days=%d daily=%d models=%d", got.Days, len(got.Daily), len(got.Models))
	}
}
