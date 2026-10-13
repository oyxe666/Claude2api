package service

import (
	"encoding/json"
	"sync"
	"time"

	"claude2api/internal/repository"
)

// 额度实时推送：额度落库后广播给所有订阅者（/api/events 的 SSE 连接）。
// 推送内容只含邮箱与公开字段，不含 sessionKey / Cookies / OrgUUID。

// EventAccount 是推送用的账号公开视图（JSON 键与 PublicAccount 一致，但没有 org_uuid）。
type EventAccount struct {
	Email      string                   `json:"email"`
	Status     string                   `json:"status,omitempty"`
	CreatedAt  time.Time                `json:"created_at"`
	UpdatedAt  time.Time                `json:"updated_at"`
	HasSession bool                     `json:"has_session"`
	Quota      *repository.AccountQuota `json:"quota,omitempty"`
}

// QuotaEvent 是一条额度变化事件。
type QuotaEvent struct {
	Email   string        `json:"email"`
	Account *EventAccount `json:"account"`
}

// EventAccountView 生成推送视图；nil 返回 nil。
func EventAccountView(a *repository.Account) *EventAccount {
	if a == nil {
		return nil
	}
	v := &EventAccount{
		Email: a.Email, Status: a.Status,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		HasSession: SessionKey(a) != "",
	}
	if !a.Quota.IsZero() {
		q := a.Quota
		v.Quota = &q
	}
	return v
}

const eventBuffer = 16

type eventHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

var events = &eventHub{subs: map[chan []byte]struct{}{}}

// SubscribeEvents 订阅额度事件；cancel 取消订阅（可重复调用）。每个订阅者有 16 条缓冲，满了会丢新事件。
func SubscribeEvents() (ch <-chan []byte, cancel func()) {
	c := make(chan []byte, eventBuffer)
	events.mu.Lock()
	events.subs[c] = struct{}{}
	events.mu.Unlock()
	return c, func() {
		events.mu.Lock()
		delete(events.subs, c)
		events.mu.Unlock()
	}
}

// EventSubscriberCount 返回当前订阅者数量（测试与诊断用）。
func EventSubscriberCount() int {
	events.mu.Lock()
	defer events.mu.Unlock()
	return len(events.subs)
}

func (h *eventHub) hasSubs() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0
}

// PublishQuotaChanged 读出账号最新公开视图并广播；无订阅者或账号不存在时什么都不做。不阻塞。
func PublishQuotaChanged(email string) {
	if !events.hasSubs() {
		return
	}
	view := EventAccountView(repository.AccountByEmail(email))
	if view == nil {
		return
	}
	b, err := json.Marshal(QuotaEvent{Email: email, Account: view})
	if err != nil {
		return
	}
	events.mu.Lock()
	defer events.mu.Unlock()
	for c := range events.subs {
		select {
		case c <- b:
		default: // 订阅者太慢，丢弃本条；前端重连/回前台时会整表重读
		}
	}
}

// InstallQuotaBroadcast 把广播挂到 repository 的额度变更回调上（启动时调用一次）。
func InstallQuotaBroadcast() {
	repository.OnQuotaChanged = PublishQuotaChanged
}
