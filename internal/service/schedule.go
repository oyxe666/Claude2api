package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"claude2api/internal/config"
)

// 定时检查账号：每天在设定的北京时间到点后，逐个排队检查账号（每个账号只发一轮请求）。
// 调度循环只比较本地时间，不发任何请求；到点才真正向 Claude 发请求。

// ScheduleGapMin / ScheduleGapMax 是定时检查时两个账号之间的随机间隔（比手动检查更慢，测试里可改成 0）。
var ScheduleGapMin, ScheduleGapMax = 8 * time.Second, 20 * time.Second

// scheduleCatchUp 是重启后补跑的窗口：只在设定时间后这么久之内补跑，超过就跳过当天。
var scheduleCatchUp = 2 * time.Hour

// scheduleTick 是调度循环的醒来间隔。
var scheduleTick = 30 * time.Second

// beijing 是固定的北京时区，不依赖服务器时区。
var beijing = time.FixedZone("CST", 8*3600)

// scheduleNow 可在测试里替换时钟。
var scheduleNow = time.Now

// ScheduleState 是持久化的上次运行记录。
type ScheduleState struct {
	LastRunDate string    `json:"last_run_date"` // 北京日期 YYYY-MM-DD
	LastRunAt   time.Time `json:"last_run_at"`
	Total       int       `json:"total"`
	OK          int       `json:"ok"`
	Failed      int       `json:"failed"`
	Skipped     int       `json:"skipped"`
}

var (
	scheduleMu      sync.Mutex
	scheduleStateFn = func() string { return filepath.Join(config.DataDir(), "schedule_state.json") }
)

// LoadScheduleState 读取状态；文件不存在或损坏都当作没跑过。
func LoadScheduleState() ScheduleState {
	scheduleMu.Lock()
	defer scheduleMu.Unlock()
	return loadScheduleStateLocked()
}

func loadScheduleStateLocked() ScheduleState {
	var st ScheduleState
	b, err := os.ReadFile(scheduleStateFn())
	if err != nil || json.Unmarshal(b, &st) != nil {
		return ScheduleState{}
	}
	return st
}

// saveScheduleState 原子写：先写临时文件再改名。
func saveScheduleState(st ScheduleState) error {
	scheduleMu.Lock()
	defer scheduleMu.Unlock()
	path := scheduleStateFn()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// parseScheduleTime 把 "HH:MM" 拆成时、分；非法时按默认值。
func parseScheduleTime(s string) (int, int) {
	parts := strings.Split(config.NormalizeScheduleTime(s), ":")
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return h, m
}

// scheduledAt 返回 now 所在北京日的设定时刻。
func scheduledAt(now time.Time, hhmm string) time.Time {
	n := now.In(beijing)
	h, m := parseScheduleTime(hhmm)
	return time.Date(n.Year(), n.Month(), n.Day(), h, m, 0, 0, beijing)
}

// scheduleDue 判断此刻是否该跑：已开启、今日未跑过、已到点且在补跑窗口内。
func scheduleDue(now time.Time, enabled bool, hhmm string, st ScheduleState) bool {
	if !enabled {
		return false
	}
	if st.LastRunDate == now.In(beijing).Format("2006-01-02") {
		return false
	}
	at := scheduledAt(now, hhmm)
	if now.Before(at) {
		return false
	}
	return now.Sub(at) <= scheduleCatchUp
}

// NextScheduleRun 返回下一次运行的时间（未开启返回零值）。
func NextScheduleRun(now time.Time, enabled bool, hhmm string, st ScheduleState) time.Time {
	if !enabled {
		return time.Time{}
	}
	at := scheduledAt(now, hhmm)
	today := now.In(beijing).Format("2006-01-02")
	if now.Before(at) && st.LastRunDate != today {
		return at
	}
	return at.AddDate(0, 0, 1)
}

func scheduleBatchOptions() QuotaBatchOptions {
	opts := DefaultCheckBatchOptions()
	opts.Gap = func() time.Duration {
		lo, hi := ScheduleGapMin, ScheduleGapMax
		if hi <= lo {
			if lo < 0 {
				return 0
			}
			return lo
		}
		return lo + time.Duration(rand.Int64N(int64(hi-lo)+1))
	}
	return opts
}

// RunScheduledCheck 立即执行一次定时检查：抢批次互斥（别的批次在跑返回 false），
// 逐个检查账号并记录结果。ctx 取消时剩余账号标记 skipped。
func RunScheduledCheck(ctx context.Context) (ScheduleState, bool) {
	end, ok := TryBeginQuotaBatch()
	if !ok {
		return ScheduleState{}, false
	}
	defer end()

	emails := QuotaBatchTargets()
	now := scheduleNow()
	st := ScheduleState{LastRunDate: now.In(beijing).Format("2006-01-02"), LastRunAt: now, Total: len(emails)}
	// 先落状态再开跑：即使中途重启，也不会当天重复跑第二遍。
	_ = saveScheduleState(st)
	PublishScheduleChanged()
	defer PublishScheduleChanged()
	if len(emails) == 0 {
		return st, true
	}
	slog.Info("[定时] 开始检查账号", "total", len(emails))
	sum := RefreshAllQuotas(ctx, scheduleBatchOptions(), emails, nil)
	st.OK, st.Failed, st.Skipped = sum.OK, sum.Failed, sum.Skipped
	_ = saveScheduleState(st)
	slog.Info("[定时] 检查账号完成", "total", sum.Total, "ok", sum.OK, "failed", sum.Failed, "skipped", sum.Skipped)
	return st, true
}

// StartScheduler 启动后台调度循环。循环本身只比较时间，不发请求。
func StartScheduler() {
	go func() {
		for {
			time.Sleep(scheduleTick)
			tickScheduler(context.Background())
		}
	}()
}

// tickScheduler 执行一次调度判断；到点则运行。批次忙时直接返回，下一轮再试。
func tickScheduler(ctx context.Context) bool {
	s := config.Get()
	now := scheduleNow()
	if !scheduleDue(now, s.ScheduleEnabled, s.ScheduleTime, LoadScheduleState()) {
		return false
	}
	_, ran := RunScheduledCheck(ctx)
	return ran
}
