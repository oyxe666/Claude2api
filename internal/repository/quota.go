package repository

import (
	"encoding/json"
	"sync"
	"time"
)

// QuotaWindow 是一个用量窗口（5h / 7d）。
// Utilization 一律是 0~1 小数（nil 表示上游没给）：SSE message_limit 本来就是 0~1；
// /usage 接口给的是 0~100 百分数，由 service 层 /100 后再存进来。ResetsAt 为 unix 秒。
type QuotaWindow struct {
	Status      string   `json:"status,omitempty"`
	Utilization *float64 `json:"utilization,omitempty"`
	ResetsAt    int64    `json:"resets_at,omitempty"` // unix 秒
}

// AccountQuota 是账号最新额度快照，整体存进 accounts.quota 一列（JSON）。
// 新增字段都是向后兼容的纯加字段：旧 JSON 缺这些键时读出来就是零值。
type AccountQuota struct {
	Type      string       `json:"type,omitempty"` // within_limit / exceeded_limit ...
	FiveHour  *QuotaWindow `json:"five_hour,omitempty"`
	SevenDay  *QuotaWindow `json:"seven_day,omitempty"`
	UpdatedAt time.Time    `json:"updated_at"`

	// Source 标记来源：主动查询为 "usage_api"，SSE / 429 记录留空。
	Source string `json:"source,omitempty"`
	// Error / ErrorAt 记录最近一次主动查询失败（固定中文短句）。
	// 之后 updated_at 比 error_at 新，就说明已经恢复，无需显式清除。
	Error   string    `json:"error,omitempty"`
	ErrorAt time.Time `json:"error_at,omitzero"`
	// SevenDayOpus / SevenDaySonnet 只存不展示。
	SevenDayOpus   *QuotaWindow `json:"seven_day_opus,omitempty"`
	SevenDaySonnet *QuotaWindow `json:"seven_day_sonnet,omitempty"`
}

// IsZero 表示从未记录过（既没有成功快照，也没有失败记录）。
func (q AccountQuota) IsZero() bool { return q.UpdatedAt.IsZero() && q.ErrorAt.IsZero() }

var quotaMu sync.Mutex

// OnQuotaChanged 在额度快照成功落库后被调用（参数是账号邮箱），在 quotaMu 之外执行。
// 由启动时注入（repository 不依赖 service）；回调里的 panic 会被吞掉。
var OnQuotaChanged func(email string)

func notifyQuotaChanged(email string) {
	cb := OnQuotaChanged
	if cb == nil {
		return
	}
	defer func() { _ = recover() }()
	cb(email)
}

// UpdateAccountQuota 读出当前快照 → apply 修改 → 只写 quota 列（不动其它列）。账号不存在返回 false。
func UpdateAccountQuota(email string, apply func(*AccountQuota)) bool {
	if email == "" || apply == nil || db == nil {
		return false
	}
	ok := func() bool {
		quotaMu.Lock()
		defer quotaMu.Unlock()

		var a Account
		if db.Select("quota").Where("email = ?", email).First(&a).Error != nil {
			return false
		}
		q := a.Quota
		apply(&q)
		b, err := json.Marshal(q)
		if err != nil {
			return false
		}
		res := db.Exec("UPDATE accounts SET quota = ? WHERE email = ?", string(b), email)
		return res.Error == nil && res.RowsAffected > 0
	}()
	if ok {
		notifyQuotaChanged(email) // 在锁外通知，回调里可以安全读写库
	}
	return ok
}
