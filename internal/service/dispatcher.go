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

// balanceFreshWindow 是额度快照被视为「新鲜」的最长时长，超过则该账号按未知处理。
const balanceFreshWindow = 10 * time.Minute

// fiveHourRemain 返回账号 5 小时窗口的剩余比例（0~1）。
// 快照缺失、过期（>= balanceFreshWindow）或没有用量数据时 known=false。
// 新鲜快照里 ResetsAt 已过，说明窗口已重置，剩余按 100% 算。
func fiveHourRemain(a *repository.Account, now time.Time) (remain float64, known bool) {
	q := a.Quota
	if q.UpdatedAt.IsZero() || now.Sub(q.UpdatedAt) >= balanceFreshWindow {
		return 0, false
	}
	if q.FiveHour == nil || q.FiveHour.Utilization == nil {
		return 0, false
	}
	if q.FiveHour.ResetsAt > 0 && now.Unix() >= q.FiveHour.ResetsAt {
		return 1, true
	}
	r := 1 - *q.FiveHour.Utilization
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return r, true
}

// balanceByFiveHour 让 5 小时剩余比例明显偏低的账号本轮暂不分配，使各号额度消耗更均衡。
// 只在「剩余比最大者」与某账号的差距超过 gap 时才跳过（恰好等于 gap 不跳过）。
// 未知快照（缺失/过期/无用量）既不跳过也不参与求最大值：快照只在对话/检查后更新，阈值宁宽勿严。
// gap <= 0 关闭；过滤后为空则原样返回（理论上不会发生，最大者必留）。保持原顺序。
func balanceByFiveHour(usable []repository.Account, now time.Time, gap float64) []repository.Account {
	if gap <= 0 || len(usable) == 0 {
		return usable
	}
	remains := make([]float64, len(usable))
	knowns := make([]bool, len(usable))
	maxRemain, have := 0.0, false
	for i := range usable {
		remains[i], knowns[i] = fiveHourRemain(&usable[i], now)
		if knowns[i] && (!have || remains[i] > maxRemain) {
			maxRemain, have = remains[i], true
		}
	}
	if !have {
		return usable
	}
	ok := make([]repository.Account, 0, len(usable))
	for i := range usable {
		if !knowns[i] || maxRemain-remains[i] <= gap+1e-9 {
			ok = append(ok, usable[i])
		}
	}
	if len(ok) == 0 {
		return usable
	}
	return ok
}

// pickAPIAccount 轮询可用账号（跳过额度已耗尽的账号，并按 5 小时剩余做均衡）。
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
	usable = balanceByFiveHour(usable, time.Now(), config.Get().QuotaBalanceGap)
	acct := usable[apiIndex%len(usable)]
	apiIndex++
	apiIndexLock.Unlock()
	return &acct
}

// dropInvalidSession：建会话报 account_session_invalid 且开启自动移除时，先入乱葬岗再删号。
// 返回是否删除了账号。
func dropInvalidSession(email string, err error, remove bool) bool {
	if !remove || err == nil || !strings.Contains(err.Error(), "account_session_invalid") {
		return false
	}
	repository.RecordGrave(email, repository.GraveAutoRemoved)
	repository.DeleteAccount(email)
	slog.Warn("[API] 会话失效，已立即移除账号", "email", email)
	return true
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
			dropInvalidSession(email, err, s.RemoveInvalidAccount)
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
