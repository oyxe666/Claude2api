package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func bj(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, beijing)
}

func TestScheduleDue(t *testing.T) {
	st := ScheduleState{}
	cases := []struct {
		name    string
		now     time.Time
		enabled bool
		st      ScheduleState
		want    bool
	}{
		{"关闭不跑", bj(2026, 10, 14, 6, 5), false, st, false},
		{"未到点不跑", bj(2026, 10, 14, 5, 59), true, st, false},
		{"到点就跑", bj(2026, 10, 14, 6, 0), true, st, true},
		{"补跑窗口内", bj(2026, 10, 14, 7, 59), true, st, true},
		{"超过补跑窗口跳过", bj(2026, 10, 14, 8, 1), true, st, false},
		{"今日已跑不重复", bj(2026, 10, 14, 6, 10), true, ScheduleState{LastRunDate: "2026-10-14"}, false},
		{"昨天跑过今天照跑", bj(2026, 10, 14, 6, 10), true, ScheduleState{LastRunDate: "2026-10-13"}, true},
		{"UTC 时间按北京换算", time.Date(2026, 10, 13, 22, 30, 0, 0, time.UTC), true, st, true},
	}
	for _, c := range cases {
		if got := scheduleDue(c.now, c.enabled, "06:00", c.st); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	// 非法时间回退 06:00
	if !scheduleDue(bj(2026, 10, 14, 6, 0), true, "abc", st) {
		t.Error("非法时间应回退 06:00")
	}
}

func TestNextScheduleRun(t *testing.T) {
	if !NextScheduleRun(bj(2026, 10, 14, 5, 0), false, "06:00", ScheduleState{}).IsZero() {
		t.Error("关闭时应为零值")
	}
	if got := NextScheduleRun(bj(2026, 10, 14, 5, 0), true, "06:00", ScheduleState{}); !got.Equal(bj(2026, 10, 14, 6, 0)) {
		t.Errorf("今日未到点: %v", got)
	}
	if got := NextScheduleRun(bj(2026, 10, 14, 7, 0), true, "06:00", ScheduleState{}); !got.Equal(bj(2026, 10, 15, 6, 0)) {
		t.Errorf("已过点: %v", got)
	}
	if got := NextScheduleRun(bj(2026, 10, 14, 5, 0), true, "06:00", ScheduleState{LastRunDate: "2026-10-14"}); !got.Equal(bj(2026, 10, 15, 6, 0)) {
		t.Errorf("今日已跑: %v", got)
	}
}

func TestScheduleStateRoundTripAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	old := scheduleStateFn
	scheduleStateFn = func() string { return filepath.Join(dir, "data", "schedule_state.json") }
	t.Cleanup(func() { scheduleStateFn = old })

	if st := LoadScheduleState(); st.LastRunDate != "" {
		t.Fatalf("文件不存在应为空: %+v", st)
	}
	want := ScheduleState{LastRunDate: "2026-10-14", Total: 3, OK: 2, Failed: 1}
	if err := saveScheduleState(want); err != nil {
		t.Fatal(err)
	}
	if got := LoadScheduleState(); got.LastRunDate != want.LastRunDate || got.OK != 2 || got.Failed != 1 {
		t.Fatalf("got %+v", got)
	}
	if err := os.WriteFile(scheduleStateFn(), []byte("{坏的"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := LoadScheduleState(); st.LastRunDate != "" {
		t.Fatalf("损坏应当作没跑过: %+v", st)
	}
}

func TestScheduleGapRange(t *testing.T) {
	oldMin, oldMax := ScheduleGapMin, ScheduleGapMax
	t.Cleanup(func() { ScheduleGapMin, ScheduleGapMax = oldMin, oldMax })
	ScheduleGapMin, ScheduleGapMax = 8*time.Second, 20*time.Second
	opts := scheduleBatchOptions()
	for i := 0; i < 200; i++ {
		if g := opts.Gap(); g < 8*time.Second || g > 20*time.Second {
			t.Fatalf("Gap=%v 超出 [8s,20s]", g)
		}
	}
}

func TestRunScheduledCheckBusyAndRecords(t *testing.T) {
	setupDB(t)
	dir := t.TempDir()
	old := scheduleStateFn
	scheduleStateFn = func() string { return filepath.Join(dir, "schedule_state.json") }
	oldMin, oldMax := ScheduleGapMin, ScheduleGapMax
	ScheduleGapMin, ScheduleGapMax = 0, 0
	t.Cleanup(func() { scheduleStateFn = old; ScheduleGapMin, ScheduleGapMax = oldMin, oldMax })

	// 批次忙：不运行
	end, ok := TryBeginQuotaBatch()
	if !ok {
		t.Fatal("应能抢到互斥")
	}
	if _, ran := RunScheduledCheck(context.Background()); ran {
		t.Fatal("批次忙时不应运行")
	}
	end()

	// 没有账号：也要落状态（当天不重复）
	st, ran := RunScheduledCheck(context.Background())
	if !ran || st.Total != 0 || LoadScheduleState().LastRunDate == "" {
		t.Fatalf("st=%+v ran=%v", st, ran)
	}
}
