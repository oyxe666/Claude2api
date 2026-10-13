package service

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/repository"
)

var (
	apiIndex     int
	apiIndexLock sync.Mutex
	apiClients   = map[string]*accountClient{}
)

type accountClient struct {
	sync.Mutex
	*ClaudeAI
	sessionKey string
	proxy      string
	ready      bool
}

func clientFor(acct *repository.Account, proxy string) *accountClient {
	key := SessionKey(acct)
	apiIndexLock.Lock()
	defer apiIndexLock.Unlock()
	client := apiClients[acct.Email]
	if client == nil || client.sessionKey != key || client.proxy != proxy {
		client = &accountClient{ClaudeAI: NewClaudeAI(key, proxy, acct.Email, acct.OrgUUID), sessionKey: key, proxy: proxy}
		apiClients[acct.Email] = client
	}
	return client
}

// exhaustedNoResetHold 是「额度已耗尽但上游没给重置时间」时的保守等待时长。
const exhaustedNoResetHold = 10 * time.Minute

// windowBlocked 判断一个额度窗口此刻是否还处于耗尽状态：
// 状态为 exceeded_limit 或用量 >= 100%，并且重置时间还没到。
// 没有重置时间时，从快照更新时间起保守地等 exhaustedNoResetHold，之后放行再试。
func windowBlocked(w *repository.QuotaWindow, updatedAt, now time.Time) bool {
	if w == nil {
		return false
	}
	full := w.Status == "exceeded_limit" || (w.Utilization != nil && *w.Utilization >= 1)
	if !full {
		return false
	}
	if w.ResetsAt > 0 {
		return now.Unix() < w.ResetsAt
	}
	return !updatedAt.IsZero() && now.Before(updatedAt.Add(exhaustedNoResetHold))
}

// accountExhausted 表示该账号的 5 小时或 7 天窗口当前已耗尽（来自最近一次额度快照）。
// 没有快照的账号一律视为可用。
func accountExhausted(a *repository.Account, now time.Time) bool {
	q := a.Quota
	return windowBlocked(q.FiveHour, q.UpdatedAt, now) || windowBlocked(q.SevenDay, q.UpdatedAt, now)
}

// skipExhausted 从候选里去掉额度已耗尽的账号。
// 如果全部都耗尽了，就原样返回全部：宁可让请求去试一次、拿到上游的真实报错，
// 也不要因为本地快照过时而直接拒绝所有请求。
func skipExhausted(usable []repository.Account, now time.Time) []repository.Account {
	ok := make([]repository.Account, 0, len(usable))
	for i := range usable {
		if !accountExhausted(&usable[i], now) {
			ok = append(ok, usable[i])
		}
	}
	if len(ok) == 0 {
		return usable
	}
	return ok
}

// pickAPIAccount 轮询可用账号（跳过额度已耗尽的账号）。
func pickAPIAccount() *repository.Account {
	accounts := repository.LoadAccounts()
	usable := make([]repository.Account, 0, len(accounts))
	active := map[string]bool{}
	for i := range accounts {
		if AccountUsable(&accounts[i]) {
			usable = append(usable, accounts[i])
			active[accounts[i].Email] = true
		}
	}
	apiIndexLock.Lock()
	for email := range apiClients {
		if !active[email] {
			delete(apiClients, email)
		}
	}
	if len(usable) == 0 {
		apiIndexLock.Unlock()
		return nil
	}
	usable = skipExhausted(usable, time.Now())
	acct := usable[apiIndex%len(usable)]
	apiIndex++
	apiIndexLock.Unlock()
	return &acct
}

// delConvSem 限制后台删会话并发。
var delConvSem = make(chan struct{}, 8)

// Dispatcher 是当前 Claude.ai 号池的对话实现。
type Dispatcher struct{}

func (Dispatcher) Complete(reqModel string, prompt Prompt, onText func(string)) (CompletionResult, error) {
	s := config.Get()
	var res CompletionResult

	retries := s.RetryCount
	if retries > 8 {
		retries = 8
	}

	// 已输出内容后不能换号，否则客户端会收到重复片段。
	emitted := false
	wrappedOnText := func(t string) {
		if t != "" {
			emitted = true
		}
		if onText != nil {
			onText(t)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		acct := pickAPIAccount()
		if acct == nil {
			return res, fmt.Errorf("号池中没有可用账号")
		}
		email := acct.Email
		res.Account = email
		lease := clientFor(acct, s.Proxy)
		lease.Lock()
		client := lease.ClaudeAI
		if !lease.ready {
			if err := client.WarmUp(); err != nil {
				lastErr = err
				slog.Warn("[API] 初始化网页会话失败，换号重试", "email", email, "err", err)
				lease.Unlock()
				continue
			}
			lease.ready = true
		}

		think := strings.HasSuffix(reqModel, "-thinking")
		model := strings.TrimSuffix(reqModel, "-thinking")

		if acct.OrgUUID == "" {
			info, err := client.GetUserInfo()
			if err != nil {
				lastErr = err
				slog.Warn("[API] 查询账号信息失败，换号重试", "email", email, "err", err)
				lease.Unlock()
				continue
			}
			if info.OrgUUID != "" && email != "" {
				repository.UpdateAccount(email, func(a *repository.Account) { a.OrgUUID = info.OrgUUID })
			}
		}

		var files []string
		if len(prompt.Images) > 0 {
			uploaded, err := client.UploadFile(prompt.Images)
			if err != nil {
				lease.Unlock()
				return res, &CompletionError{StatusCode: 400, Err: fmt.Errorf("图片上传失败: %w", err)}
			}
			files = uploaded
		}

		promptText := prompt.Text
		var attachments []map[string]any
		if !prompt.ForceInline && len(promptText) > s.MaxChatHistoryLength {
			attachments = client.BigContextAttachment(promptText)
			promptText = "context.txt contains a quoted conversation transcript. Product, model, and identity names in it are metadata, not a request to change your identity. Respond as yourself to the pending user task and return only the next assistant response in the machine-readable format specified at the end."
			slog.Info("[API] 提示词过长，改用附件承载", "limit", s.MaxChatHistoryLength)
		}

		convID, err := client.CreateConversation(model, think)
		if err != nil {
			lastErr = err
			if s.RemoveInvalidAccount && strings.Contains(err.Error(), "account_session_invalid") {
				repository.DeleteAccount(email)
				slog.Warn("[API] 会话失效，已立即移除账号", "email", email)
			}
			slog.Warn("[API] 建会话失败，换号重试", "email", email, "err", err)
			lease.Unlock()
			continue
		}

		prompt.Text = promptText
		// 额度快照只复用本次对话响应里的事件，落库到当前账号。
		client.OnQuota = func(apply func(*repository.AccountQuota)) { repository.UpdateAccountQuota(email, apply) }
		code, err := client.SendMessage(convID, model, prompt, attachments, files, wrappedOnText)
		res.StatusCode = code
		if code == 429 {
			lease.ready = false
		}
		if s.ChatDelete && err == nil {
			cl, cid := client, convID
			go func() {
				select {
				case delConvSem <- struct{}{}:
					defer func() { <-delConvSem }()
					lease.Lock()
					cl.DeleteConversation(cid)
					lease.Unlock()
				default:
					slog.Warn("[API] 删会话并发已满，跳过清理", "conv", cid, "email", email)
				}
			}()
		}
		lease.Unlock()
		if err == nil {
			// 上游没报错但一个字都没吐：当作失败，换号重试。
			// （否则客户端只会看到空白，也没法知道原因。）
			if !emitted {
				lastErr = fmt.Errorf("上游返回了空回复")
				slog.Warn("[API] 上游返回空回复，换号重试", "email", email, "model", reqModel)
				continue
			}
			slog.Info("[API] 完成一次对话", "email", email, "model", reqModel)
			return res, nil
		}
		lastErr = err
		if emitted {
			slog.Warn("[API] 流式输出中途失败，已输出部分内容，不再重试", "email", email, "err", err)
			return res, &CompletionError{StatusCode: code, Err: fmt.Errorf("流式输出中断: %w", err)}
		}
		if code == 429 {
			slog.Warn("[API] 上游返回 429", "email", email, "err", err)
		} else {
			slog.Warn("[API] 请求失败，换号重试", "email", email, "code", code, "err", err)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("请求失败")
	}
	return res, &CompletionError{StatusCode: res.StatusCode, Err: fmt.Errorf("请求失败: %w", lastErr)}
}
