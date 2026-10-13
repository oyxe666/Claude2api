package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/repository"
)

// 主动额度查询：只在用户点按钮时发请求，没有任何定时或后台轮询。
// 日志、返回值、落库的 Error 一律只用固定中文短句，不带 err 原文、响应体、sessionKey、代理地址。

const (
	usageSource = "usage_api"

	msgBusy       = "账号正在对话中，请稍后再试"
	msgInvalid    = "会话已失效，请重新登录该账号"
	msgForbidden  = "被上游拒绝（403），请稍后再试"
	msgRateLimit  = "请求过于频繁（429），请稍后再试"
	msgTimeout    = "额度查询超时"
	msgNetwork    = "网络请求失败"
	msgFormat     = "额度响应格式异常"
	msgNoOrg      = "未获取到组织信息"
	msgCanceled   = "额度查询已取消"
	msgNotFound   = "账号不存在或缺少 sessionKey"
	msgSkipLimit  = "已跳过（连续被限流，已停止）"
	msgSkipCancel = "已跳过（刷新已超时或中断）"
)

var (
	// quotaLeaseWait 是等待账号 lease 的最长时间（测试可改）。
	quotaLeaseWait = 10 * time.Second
	// quotaRequestTimeout 是每一步上游请求的超时。
	quotaRequestTimeout = 20 * time.Second
	// quotaLeasePoll 是 TryLock 轮询间隔。
	quotaLeasePoll = 100 * time.Millisecond
)

// ---------- 解析 ----------

// quotaFromUsage 解析 /usage 响应。
// 尺度：接口给 0~100 百分数，这里一律 /100 并夹到 [0,1]，和 SSE 的 0~1 小数保持一致；
// 不做「小于 1 就当小数」的猜测（41.0 与 0.4 都按百分数解释）。
// 没有任何可用窗口（five_hour / seven_day 都缺）或 JSON 非法 → 返回错误，调用方不应覆盖旧快照。
func quotaFromUsage(body []byte, now time.Time) (repository.AccountQuota, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var top any
	if err := dec.Decode(&top); err != nil {
		return repository.AccountQuota{}, errors.New(msgFormat)
	}
	m, ok := top.(map[string]any)
	if !ok {
		return repository.AccountQuota{}, errors.New(msgFormat)
	}
	five := parseUsageWindow(m["five_hour"])
	seven := parseUsageWindow(m["seven_day"])
	if five == nil && seven == nil {
		return repository.AccountQuota{}, errors.New(msgFormat)
	}
	q := repository.AccountQuota{
		FiveHour:       five,
		SevenDay:       seven,
		SevenDayOpus:   parseUsageWindow(m["seven_day_opus"]),
		SevenDaySonnet: parseUsageWindow(m["seven_day_sonnet"]),
		UpdatedAt:      now,
		Source:         usageSource,
	}
	q.Type = "within_limit"
	// 只看 5h / 7d 总窗口；单模型窗口（opus/sonnet）耗尽不代表整个账号用不了。
	if windowExceeded(five) || windowExceeded(seven) {
		q.Type = "exceeded_limit"
	}
	return q, nil
}

// probeModel 是探测用的模型：最便宜的一档，消耗的额度可以忽略。
const probeModel = "claude-haiku-4-5-20251001"

const msgProbeFailed = "额度接口无数据，探测对话也失败"

// probeQuota 是探测函数的可替换入口（测试里换成假实现，避免真连 claude.ai）。
var probeQuota = probeQuotaByChat

// usageIsObject 判断响应是否为合法的 JSON 对象（只是窗口字段全为空）。
func usageIsObject(body []byte) bool {
	var m map[string]any
	return json.Unmarshal(bytes.TrimSpace(body), &m) == nil && m != nil
}

// probeQuotaByChat 发一条最短对话（用完即删），靠响应里的 message_limit / exceeded_limit 事件
// 更新该账号的额度快照。调用方必须已持有账号 lease。
// 返回 ok=true 表示拿到了额度信息；否则返回固定中文失败原因（不含响应体）。
func probeQuotaByChat(email string, cl *ClaudeAI) (bool, string) {
	var got atomic.Bool
	cl.OnQuota = func(apply func(*repository.AccountQuota)) {
		got.Store(true)
		repository.UpdateAccountQuota(email, func(q *repository.AccountQuota) {
			apply(q)
			q.Error, q.ErrorAt = "", time.Time{}
		})
	}
	defer func() { cl.OnQuota = nil }()

	convID, err := cl.CreateConversation(probeModel, false)
	if err != nil {
		slog.Warn("[额度] 探测对话建会话失败", "email", email)
		return false, msgProbeFailed
	}
	code, err := cl.SendMessage(convID, probeModel, Prompt{Text: "hi", ForceInline: true}, nil, nil, nil)
	cl.DeleteConversation(convID) // 仍持有 lease，同步清理，避免和后续请求争用同一个客户端
	if got.Load() {
		return true, ""
	}
	slog.Warn("[额度] 探测对话未得到额度信息", "email", email, "code", code, "failed", err != nil)
	return false, msgProbeFailed
}

// usageShape 返回响应体的结构摘要：只含顶层字段名和值的类型，绝不包含任何取值。
// 非 JSON 时只说明大致类型（HTML / 文本）和长度。
func usageShape(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "响应为空"
	}
	var top any
	if err := json.Unmarshal(trimmed, &top); err != nil {
		kind := "文本"
		head := strings.ToLower(string(trimmed[:min(len(trimmed), 16)]))
		if strings.HasPrefix(head, "<") {
			kind = "HTML 页面，疑似被拦截"
		}
		return fmt.Sprintf("非 JSON：%s，%d 字节", kind, len(body))
	}
	m, ok := top.(map[string]any)
	if !ok {
		return "JSON 但不是对象"
	}
	if len(m) == 0 {
		return "空对象"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if len(parts) >= 12 {
			parts = append(parts, "…")
			break
		}
		kind := "其他"
		switch m[k].(type) {
		case nil:
			kind = "null"
		case map[string]any:
			kind = "对象"
		case []any:
			kind = "数组"
		case string:
			kind = "字符串"
		case bool:
			kind = "布尔"
		case json.Number, float64:
			kind = "数字"
		}
		name := k
		if len(name) > 24 {
			name = name[:24] + "…"
		}
		parts = append(parts, name+"="+kind)
	}
	return "字段：" + strings.Join(parts, "，")
}

func windowExceeded(w *repository.QuotaWindow) bool {
	return w != nil && w.Status == "exceeded_limit"
}

// parseUsageWindow 解析单个窗口；不是对象、或既没有 utilization 也没有 resets_at 则返回 nil。
func parseUsageWindow(v any) *repository.QuotaWindow {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	w := &repository.QuotaWindow{}
	if pct, ok := parseFlexNumber(m["utilization"]); ok {
		u := math.Min(math.Max(pct/100, 0), 1)
		w.Utilization = &u
		if u >= 1 {
			w.Status = "exceeded_limit"
		} else {
			w.Status = "within_limit"
		}
	}
	w.ResetsAt = parseFlexTime(m["resets_at"])
	if w.Utilization == nil && w.ResetsAt == 0 {
		return nil
	}
	return w
}

// parseFlexNumber 兼容 int / float / 字符串 / 带 % 的字符串；NaN、Inf、非法值返回 false。
func parseFlexNumber(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case json.Number:
		parsed, err := x.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	case float64:
		f = x
	case string:
		s := strings.TrimSpace(x)
		s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
		parsed, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999", // 无时区按 UTC
	"2006-01-02 15:04:05.999999999",
}

// parseFlexTime 把 ISO8601 / unix 秒 / unix 毫秒 / 数字字符串统一成 unix 秒；无法识别返回 0。
func parseFlexTime(v any) int64 {
	var f float64
	switch x := v.(type) {
	case json.Number:
		parsed, err := x.Float64()
		if err != nil {
			return 0
		}
		f = parsed
	case float64:
		f = x
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		if parsed, err := strconv.ParseFloat(s, 64); err == nil {
			f = parsed
			break
		}
		for _, layout := range timeLayouts {
			if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
				if t.Unix() > 0 {
					return t.Unix()
				}
				return 0
			}
		}
		return 0
	default:
		return 0
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return 0
	}
	return unixSeconds(f)
}

// ---------- 失败分类 ----------

var httpStatusRe = regexp.MustCompile(`HTTP (\d{3})`)

// usageFailureMessage 把一次失败归成固定中文短句。
// 优先看 netErr，再看状态码；只检查响应体里是否含 account_session_invalid，绝不回显响应体。
func usageFailureMessage(status int, body []byte, netErr error) string {
	if netErr != nil {
		return netErrMessage(netErr)
	}
	switch {
	case isSessionInvalid(status, body):
		return msgInvalid
	case status == 403:
		return msgForbidden
	case status == 429:
		return msgRateLimit
	case status >= 400:
		return fmt.Sprintf("上游返回 HTTP %d", status)
	case status != 200:
		return msgFormat
	}
	return msgFormat
}

func netErrMessage(err error) string {
	switch {
	case errors.Is(err, ErrUsageTimeout), errors.Is(err, context.DeadlineExceeded):
		return msgTimeout
	case errors.Is(err, context.Canceled):
		return msgCanceled
	case errors.Is(err, ErrUsageNoOrg):
		return msgNoOrg
	}
	return msgNetwork
}

func isSessionInvalid(status int, body []byte) bool {
	if status == 401 {
		return true
	}
	return status >= 400 && bytes.Contains(body, []byte("account_session_invalid"))
}

// failureFromErr 把 WarmUp / GetUserInfo 返回的 error 归类（这两个函数的 error 文本里可能带响应体，
// 所以只在本地提取状态码与关键字，绝不把原文往外传）。
func failureFromErr(err error) (msg string, status int) {
	text := err.Error()
	if strings.Contains(text, "account_session_invalid") {
		return msgInvalid, 401
	}
	if mt := httpStatusRe.FindStringSubmatch(text); mt != nil {
		status, _ = strconv.Atoi(mt[1])
		return usageFailureMessage(status, nil, nil), status
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return msgTimeout, 0
	}
	if errors.Is(err, context.Canceled) {
		return msgCanceled, 0
	}
	return msgNetwork, 0
}

// ---------- 单个账号 ----------

// QuotaRefreshResult 是一次主动额度查询的结果。
type QuotaRefreshResult struct {
	Email string `json:"email"`
	OK    bool   `json:"ok"`
	Busy  bool   `json:"busy"`
	Error string `json:"error"`

	// 以下字段只给批量逻辑用，不进 JSON。
	RateLimited bool `json:"-"` // 403 / 429
	Skipped     bool `json:"-"` // 没有真正查询（被取消），不写快照
}

var (
	quotaInflightMu sync.Mutex
	quotaInflight   = map[string]struct{}{}
)

func beginInflight(email string) bool {
	quotaInflightMu.Lock()
	defer quotaInflightMu.Unlock()
	if _, ok := quotaInflight[email]; ok {
		return false
	}
	quotaInflight[email] = struct{}{}
	return true
}

func endInflight(email string) {
	quotaInflightMu.Lock()
	delete(quotaInflight, email)
	quotaInflightMu.Unlock()
}

// acquireLease 用 TryLock 轮询账号 lease，最多等 quotaLeaseWait。
func acquireLease(ctx context.Context, lease *accountClient) bool {
	deadline := time.NewTimer(quotaLeaseWait)
	defer deadline.Stop()
	tick := time.NewTicker(quotaLeasePoll)
	defer tick.Stop()
	for {
		if lease.TryLock() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}

// RefreshAccountQuota 主动查询一个账号的额度并写入快照。
// 成功：整体替换快照（两个窗口都以本次为准，清空 Error）；
// 失败：只合并写 Error + ErrorAt，保留旧窗口数据；
// 忙碌 / 被取消：不写快照。永不自动重试。
func RefreshAccountQuota(ctx context.Context, email string) QuotaRefreshResult {
	res := QuotaRefreshResult{Email: email}
	acct := repository.AccountByEmail(email)
	if acct == nil || SessionKey(acct) == "" {
		res.Error = msgNotFound
		res.Skipped = true
		return res
	}
	if !beginInflight(email) {
		res.Busy, res.Error = true, msgBusy
		return res
	}
	defer endInflight(email)

	lease := clientFor(acct, config.Get().Proxy)
	if !acquireLease(ctx, lease) {
		if ctx.Err() != nil {
			res.Error, res.Skipped = msgCanceled, true
			return res
		}
		res.Busy, res.Error = true, msgBusy
		return res
	}
	defer lease.Unlock()

	// fail 记录一次失败：只合并写 Error+ErrorAt；403/429/网络错误让会话重新预热。
	fail := func(msg string, status int, resetReady bool) QuotaRefreshResult {
		res.Error = msg
		res.RateLimited = status == 403 || status == 429
		if resetReady || res.RateLimited {
			lease.ready = false
		}
		now := time.Now()
		repository.UpdateAccountQuota(email, func(q *repository.AccountQuota) {
			q.Error, q.ErrorAt = msg, now
		})
		slog.Warn("[额度] 查询失败", "email", email, "status", status, "reason", msg)
		return res
	}
	step := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(ctx, quotaRequestTimeout)
	}

	if acct.OrgUUID != "" {
		lease.orgUUID = acct.OrgUUID
	}
	if !lease.ready {
		sctx, cancel := step()
		err := lease.WarmUpContext(sctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				res.Error, res.Skipped = msgCanceled, true
				return res
			}
			msg, status := failureFromErr(err)
			return fail(msg, status, true)
		}
		lease.ready = true
	}

	if acct.OrgUUID == "" {
		sctx, cancel := step()
		info, err := lease.GetUserInfoContext(sctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				res.Error, res.Skipped = msgCanceled, true
				return res
			}
			msg, status := failureFromErr(err)
			if msg == msgInvalid {
				markExpired(email)
			}
			return fail(msg, status, true)
		}
		if info == nil || info.OrgUUID == "" {
			return fail(msgNoOrg, 0, false)
		}
		repository.UpdateAccount(email, func(a *repository.Account) { a.OrgUUID = info.OrgUUID })
	}

	sctx, cancel := step()
	body, status, err := lease.FetchUsage(sctx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			res.Error, res.Skipped = msgCanceled, true
			return res
		}
		return fail(usageFailureMessage(0, nil, err), 0, true)
	}
	if status != 200 {
		msg := usageFailureMessage(status, body, nil)
		if msg == msgInvalid {
			markExpired(email)
		}
		return fail(msg, status, false)
	}
	fresh, perr := quotaFromUsage(body, time.Now())
	if perr != nil {
		// 免费账号等：/usage 是合法 JSON，但所有窗口都是 null，查不到额度。
		// 退而求其次，发一条最短对话，从响应里的 message_limit 事件读取额度。
		if usageIsObject(body) {
			if ok, pmsg := probeQuota(email, lease.ClaudeAI); ok {
				res.OK = true
				return res
			} else {
				return fail(pmsg+"（"+usageShape(body)+"）", 0, false)
			}
		}
		// 附上响应的「结构摘要」（只有字段名与类型，没有任何取值），方便排查上游格式变化。
		return fail(msgFormat+"（"+usageShape(body)+"）", status, false)
	}
	if !repository.UpdateAccountQuota(email, func(q *repository.AccountQuota) { *q = fresh }) {
		res.Error = msgNotFound
		return res
	}
	res.OK = true
	return res
}

// markExpired 把会话失效的账号置为 expired。无论 RemoveInvalidAccount 如何都不删号：
// 额度查询只是读取动作，不该有破坏性副作用。
func markExpired(email string) {
	repository.UpdateAccount(email, func(a *repository.Account) { a.Status = "expired" })
}

// ---------- 批量 ----------

// QuotaBatchOptions 控制批量刷新节奏。
type QuotaBatchOptions struct {
	Gap     func() time.Duration // 两个账号之间的间隔；nil 表示不等
	Timeout time.Duration        // 整批总超时（由调用方套在 ctx 上，这里仅作配置携带）
}

// DefaultQuotaBatchOptions 间隔 300~800ms 随机，总超时 5 分钟。
func DefaultQuotaBatchOptions() QuotaBatchOptions {
	return QuotaBatchOptions{
		Gap: func() time.Duration {
			return time.Duration(300+rand.IntN(501)) * time.Millisecond
		},
		Timeout: 5 * time.Minute,
	}
}

// QuotaProgress 是批量刷新每处理完一个账号后的进度。
type QuotaProgress struct {
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	OK        int    `json:"ok"`
	Failed    int    `json:"failed"`
	Skipped   int    `json:"skipped"`
	Email     string `json:"email"`
	Error     string `json:"error"`
}

// QuotaBatchSummary 是整批的统计。
type QuotaBatchSummary struct {
	Total   int `json:"total"`
	OK      int `json:"ok"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// ErrQuotaBatchRunning 表示已有一批额度刷新在进行。
var ErrQuotaBatchRunning = errors.New("额度刷新正在进行中")

var quotaBatchRunning atomic.Bool

// TryBeginQuotaBatch 抢占批次互斥；ok=false 表示已有批次在跑。end 必须在结束时调用。
func TryBeginQuotaBatch() (end func(), ok bool) {
	if !quotaBatchRunning.CompareAndSwap(false, true) {
		return func() {}, false
	}
	return func() { quotaBatchRunning.Store(false) }, true
}

// QuotaBatchTargets 返回批量刷新的对象：有 sessionKey 且未失效的账号，按库内顺序。
func QuotaBatchTargets() []string {
	var out []string
	accounts := repository.LoadAccounts()
	for i := range accounts {
		if AccountUsable(&accounts[i]) {
			out = append(out, accounts[i].Email)
		}
	}
	return out
}

// RefreshAllQuotas 顺序刷新 emails 里的账号。
// 账号间隔 opts.Gap()（同时监听 ctx）；连续 2 个账号返回 403/429 则停止，剩余标记 skipped；
// ctx 超时或取消后剩余同样标记 skipped。skipped 不写快照。onProgress 每处理一个账号调用一次（可为 nil）。
func RefreshAllQuotas(ctx context.Context, opts QuotaBatchOptions, emails []string, onProgress func(QuotaProgress)) QuotaBatchSummary {
	sum := QuotaBatchSummary{Total: len(emails)}
	emit := func(email, msg string) {
		if onProgress != nil {
			onProgress(QuotaProgress{
				Total: sum.Total, Completed: sum.OK + sum.Failed + sum.Skipped,
				OK: sum.OK, Failed: sum.Failed, Skipped: sum.Skipped,
				Email: email, Error: msg,
			})
		}
	}
	stopMsg := ""
	consecutive := 0
	for i, email := range emails {
		if stopMsg == "" && ctx.Err() != nil {
			stopMsg = msgSkipCancel
		}
		if stopMsg == "" && i > 0 && opts.Gap != nil {
			if d := opts.Gap(); d > 0 {
				t := time.NewTimer(d)
				select {
				case <-ctx.Done():
					t.Stop()
					stopMsg = msgSkipCancel
				case <-t.C:
				}
			}
		}
		if stopMsg != "" {
			sum.Skipped++
			emit(email, stopMsg)
			continue
		}
		res := RefreshAccountQuota(ctx, email)
		switch {
		case res.Skipped && ctx.Err() != nil:
			stopMsg = msgSkipCancel
			sum.Skipped++
			emit(email, msgSkipCancel)
			continue
		case res.OK:
			sum.OK++
		default:
			sum.Failed++
		}
		if res.RateLimited {
			consecutive++
		} else {
			consecutive = 0
		}
		emit(email, res.Error)
		if consecutive >= 2 {
			stopMsg = msgSkipLimit
		}
	}
	return sum
}
