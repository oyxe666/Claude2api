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
	apiIndexLock sync.Mutex
	apiClients   = map[string]*accountClient{}
	// lastPicked 记录每个账号最近一次被选中的序号（受 apiIndexLock 保护），用于在候选里挑「最久没被用」的号。
	lastPicked = map[string]uint64{}
	pickSeq    uint64
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

// fiveHourRemain 返回账号 5 小时窗口的剩余比例（0~1）。
// 不设过期：没人动的号额度在重置前不会变，旧快照一直有效（与面板 web/quota.js 口径一致）。
// 没有快照或没有用量数据按满额算，让新号先被用起来、产生第一份快照；
// ResetsAt 已过说明窗口已重置，同样按 100% 算。
func fiveHourRemain(a *repository.Account, now time.Time) float64 {
	q := a.Quota
	if q.UpdatedAt.IsZero() || q.FiveHour == nil || q.FiveHour.Utilization == nil {
		return 1
	}
	if q.FiveHour.ResetsAt > 0 && now.Unix() >= q.FiveHour.ResetsAt {
		return 1
	}
	r := 1 - *q.FiveHour.Utilization
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return r
}

// balanceByFiveHour 只保留 5 小时剩余在「最大剩余 - gap」以内的账号，使额度多的号优先被用、各号逐渐拉平。
// 差距恰好等于 gap 的保留。gap <= 0 关闭，原样返回。保持原顺序。
func balanceByFiveHour(usable []repository.Account, now time.Time, gap float64) []repository.Account {
	if gap <= 0 || len(usable) == 0 {
		return usable
	}
	remains := make([]float64, len(usable))
	maxRemain := 0.0
	for i := range usable {
		remains[i] = fiveHourRemain(&usable[i], now)
		if i == 0 || remains[i] > maxRemain {
			maxRemain = remains[i]
		}
	}
	ok := make([]repository.Account, 0, len(usable))
	for i := range usable {
		if maxRemain-remains[i] <= gap+1e-9 {
			ok = append(ok, usable[i])
		}
	}
	if len(ok) == 0 {
		return usable
	}
	return ok
}

// leastRecentlyPicked 在候选里挑最久没被选中的账号（从未选过视为 0，并列取靠前者）。调用方需持有 apiIndexLock。
func leastRecentlyPicked(cands []repository.Account) *repository.Account {
	best := 0
	for i := 1; i < len(cands); i++ {
		if lastPicked[cands[i].Email] < lastPicked[cands[best].Email] {
			best = i
		}
	}
	return &cands[best]
}

// pickAPIAccount 选号：跳过额度已耗尽的账号 → 只留 5 小时剩余接近最大值的账号 → 选其中最久没被用的。
func pickAPIAccount() *repository.Account {
	accounts := repository.LoadAccounts()
	usable := make([]repository.Account, 0, len(accounts))
	active := map[string]bool{}
	for i := range accounts {
		if AccountUsable(&accounts[i]) {
			active[accounts[i].Email] = true
			// 手动停用的号保留客户端与轮流记录，只是不参与本次选号。
			if !accounts[i].Disabled {
				usable = append(usable, accounts[i])
			}
		}
	}
	apiIndexLock.Lock()
	for email := range apiClients {
		if !active[email] {
			delete(apiClients, email)
		}
	}
	for email := range lastPicked {
		if !active[email] {
			delete(lastPicked, email)
		}
	}
	if len(usable) == 0 {
		apiIndexLock.Unlock()
		return nil
	}
	usable = skipExhausted(usable, time.Now())
	usable = balanceByFiveHour(usable, time.Now(), config.Get().QuotaBalanceGap)
	acct := *leastRecentlyPicked(usable)
	pickSeq++
	lastPicked[acct.Email] = pickSeq
	apiIndexLock.Unlock()
	return &acct
}

// hasOnlyDisabledAccounts 表示存在可用账号，但它们全部被手动停用。
func hasOnlyDisabledAccounts() bool {
	any := false
	accounts := repository.LoadAccounts()
	for i := range accounts {
		if AccountUsable(&accounts[i]) {
			if !accounts[i].Disabled {
				return false
			}
			any = true
		}
	}
	return any
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
			if hasOnlyDisabledAccounts() {
				return res, fmt.Errorf("所有账号都已停用，请在账号页启用至少一个")
			}
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
