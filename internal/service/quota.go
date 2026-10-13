package service

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"claude2api/internal/repository"
)

// QuotaSink 接收「如何修改额度快照」，由 dispatcher 落库。
type QuotaSink func(apply func(*repository.AccountQuota))

// deliver 安全地交付一次快照修改：sink 为 nil 时什么都不做，任何 panic 都被吞掉，
// 保证额度记录永远不会影响正常对话。
func (s QuotaSink) deliver(apply func(*repository.AccountQuota)) {
	if s == nil || apply == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Debug("[额度] 记录快照时出错，已忽略", "err", r)
		}
	}()
	s(apply)
}

// quotaFromMessageLimit 解析 message_limit 事件，返回「整体替换快照」的修改函数。
// 字段缺失或格式不对一律宽松处理；完全无法识别时返回 false。
func quotaFromMessageLimit(raw json.RawMessage, now time.Time) (func(*repository.AccountQuota), bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	typ, _ := m["type"].(string)
	windows, _ := m["windows"].(map[string]any)
	five := parseQuotaWindow(windows["5h"])
	seven := parseQuotaWindow(windows["7d"])
	if typ == "" && five == nil && seven == nil {
		return nil, false
	}
	return func(q *repository.AccountQuota) {
		*q = repository.AccountQuota{Type: typ, FiveHour: five, SevenDay: seven, UpdatedAt: now}
	}, true
}

// parseQuotaWindow 宽松解析一个窗口；不是对象则返回 nil。
func parseQuotaWindow(v any) *repository.QuotaWindow {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	w := &repository.QuotaWindow{}
	w.Status, _ = m["status"].(string)
	if u, ok := m["utilization"].(float64); ok {
		if u < 0 {
			u = 0
		} else if u > 1 {
			u = 1
		}
		w.Utilization = &u
	}
	if r, ok := m["resets_at"].(float64); ok && r > 0 {
		w.ResetsAt = unixSeconds(r)
	}
	return w
}

// unixSeconds 把时间戳统一成秒：大于 1e12 的当作毫秒。
func unixSeconds(r float64) int64 {
	if r > 1e12 {
		r /= 1000
	}
	return int64(r)
}

// exceededFromText 识别额度耗尽（exceeded_limit），文本可以是 429 响应体、SSE error 事件文本，
// 或嵌在其中的 JSON。返回「合并进现有快照」的修改函数。
func exceededFromText(text string, now time.Time) (func(*repository.AccountQuota), bool) {
	if text == "" {
		return nil, false
	}
	candidates := []string{text}
	var wrapper struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err == nil && wrapper.Error.Message != "" {
		candidates = append(candidates, wrapper.Error.Message)
	}
	for _, c := range candidates {
		start, end := strings.Index(c, "{"), strings.LastIndex(c, "}")
		if start < 0 || end <= start {
			continue
		}
		var info struct {
			Type     string  `json:"type"`
			ResetsAt float64 `json:"resetsAt"`
			Claim    string  `json:"representativeClaim"`
		}
		if err := json.Unmarshal([]byte(c[start:end+1]), &info); err != nil || info.Type != "exceeded_limit" {
			continue
		}
		return func(q *repository.AccountQuota) {
			q.Type = "exceeded_limit"
			q.UpdatedAt = now
			var target **repository.QuotaWindow
			switch {
			case info.Claim == "five_hour":
				target = &q.FiveHour
			case strings.HasPrefix(info.Claim, "seven_day"):
				target = &q.SevenDay
			default:
				return
			}
			if *target == nil {
				*target = &repository.QuotaWindow{}
			}
			one := 1.0
			(*target).Status = "exceeded_limit"
			(*target).Utilization = &one
			if info.ResetsAt > 0 {
				(*target).ResetsAt = unixSeconds(info.ResetsAt)
			}
		}, true
	}
	return nil, false
}
