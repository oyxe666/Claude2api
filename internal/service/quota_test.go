package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"claude2api/internal/repository"
)

// sseLines 把若干 JSON 事件拼成 SSE 文本（每行 data: ...，行间空行）。
func sseLines(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return b.String()
}

const textDeltaEvent = `{"type":"content_block_delta","delta":{"type":"text_delta","text":"OK"}}`

func newSink(got *repository.AccountQuota) QuotaSink {
	return QuotaSink(func(apply func(*repository.AccountQuota)) { apply(got) })
}

func TestParseSSE_MessageLimit5hOnly(t *testing.T) {
	stream := sseLines(
		`{"type":"message_start"}`,
		`{"type":"message_limit","message_limit":{"type":"within_limit","remaining":null,"windows":{"5h":{"status":"within_limit","resets_at":1791740400,"utilization":0.01}},"overageDisabledReason":"overage_not_provisioned"}}`,
		textDeltaEvent,
	)
	var got repository.AccountQuota
	var text string
	if err := parseCompletionSSE(strings.NewReader(stream), func(s string) { text += s }, newSink(&got)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if text != "OK" {
		t.Fatalf("text = %q", text)
	}
	if got.Type != "within_limit" {
		t.Fatalf("type = %q", got.Type)
	}
	w := got.FiveHour
	if w == nil || w.Utilization == nil || *w.Utilization != 0.01 || w.ResetsAt != 1791740400 || w.Status != "within_limit" {
		t.Fatalf("5h = %+v", w)
	}
	if got.SevenDay != nil {
		t.Fatalf("7d 应为 nil: %+v", got.SevenDay)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt 应非零")
	}
}

func TestParseSSE_MessageLimit5hAnd7d(t *testing.T) {
	stream := sseLines(
		`{"type":"message_limit","message_limit":{"type":"within_limit","windows":{"5h":{"status":"within_limit","resets_at":1791740400,"utilization":0.01},"7d":{"status":"within_limit","resets_at":1792000000,"utilization":0.5}}}}`,
		textDeltaEvent,
	)
	var got repository.AccountQuota
	if err := parseCompletionSSE(strings.NewReader(stream), nil, newSink(&got)); err != nil {
		t.Fatal(err)
	}
	if got.FiveHour == nil || got.SevenDay == nil {
		t.Fatalf("两个窗口都应存在: %+v", got)
	}
	s := got.SevenDay
	if s.Utilization == nil || *s.Utilization != 0.5 || s.ResetsAt != 1792000000 || s.Status != "within_limit" {
		t.Fatalf("7d = %+v", s)
	}
}

func TestParseSSE_MalformedLimitIgnored(t *testing.T) {
	cases := map[string]string{
		"字符串":        `{"type":"message_limit","message_limit":"oops"}`,
		"utilization": `{"type":"message_limit","message_limit":{"type":"within_limit","windows":{"5h":{"utilization":"abc","resets_at":"x"}}}}`,
		"windows空":    `{"type":"message_limit","message_limit":{"type":"within_limit","windows":null}}`,
		"null":        `{"type":"message_limit","message_limit":null}`,
		"缺字段":        `{"type":"message_limit"}`,
		"窗口非对象":      `{"type":"message_limit","message_limit":{"windows":{"5h":123,"7d":[]}}}`,
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			stream := sseLines(ev, textDeltaEvent)
			var got repository.AccountQuota
			var text string
			if err := parseCompletionSSE(strings.NewReader(stream), func(s string) { text += s }, newSink(&got)); err != nil {
				t.Fatalf("err = %v", err)
			}
			if text != "OK" {
				t.Fatalf("text = %q", text)
			}
			// sink 为 nil 同样安全。
			text = ""
			if err := parseCompletionSSE(strings.NewReader(stream), func(s string) { text += s }, nil); err != nil || text != "OK" {
				t.Fatalf("nil sink: err=%v text=%q", err, text)
			}
		})
	}

	// 乱 JSON 行：整行跳过，不 panic。
	stream := "data: {not json\n\n" + sseLines(textDeltaEvent)
	var text string
	if err := parseCompletionSSE(strings.NewReader(stream), func(s string) { text += s }, newSink(&repository.AccountQuota{})); err != nil || text != "OK" {
		t.Fatalf("乱 JSON: err=%v text=%q", err, text)
	}
}

func TestQuotaSinkDeliverRecoversPanic(t *testing.T) {
	sink := QuotaSink(func(apply func(*repository.AccountQuota)) { panic("boom") })
	sink.deliver(func(*repository.AccountQuota) {}) // 不应 panic
	var nilSink QuotaSink
	nilSink.deliver(func(*repository.AccountQuota) {})
}

func TestParseSSE_ExceededInErrorEvent(t *testing.T) {
	stream := sseLines(`{"type":"error","error":{"type":"rate_limit_error","message":"{\"type\":\"exceeded_limit\",\"resetsAt\":1791740400,\"representativeClaim\":\"five_hour\"}"}}`)
	var got repository.AccountQuota
	err := parseCompletionSSE(strings.NewReader(stream), nil, newSink(&got))
	if err == nil || !strings.Contains(err.Error(), "upstream:") {
		t.Fatalf("err = %v", err)
	}
	if got.Type != "exceeded_limit" || got.FiveHour == nil {
		t.Fatalf("got = %+v", got)
	}
	w := got.FiveHour
	if w.Status != "exceeded_limit" || w.Utilization == nil || *w.Utilization != 1 || w.ResetsAt != 1791740400 {
		t.Fatalf("5h = %+v", w)
	}
}

func TestExceededFromText_429Body(t *testing.T) {
	body := `{"type":"error","error":{"type":"rate_limit_error","message":"{\"type\":\"exceeded_limit\",\"resetsAt\":1791740400,\"representativeClaim\":\"seven_day\"}"}}`
	u := 0.3
	got := repository.AccountQuota{
		Type:     "within_limit",
		FiveHour: &repository.QuotaWindow{Status: "within_limit", Utilization: &u, ResetsAt: 1700000000},
	}
	apply, ok := exceededFromText(body, time.Now())
	if !ok {
		t.Fatal("应命中 exceeded_limit")
	}
	apply(&got)
	if got.Type != "exceeded_limit" || got.UpdatedAt.IsZero() {
		t.Fatalf("got = %+v", got)
	}
	if got.SevenDay == nil || got.SevenDay.Status != "exceeded_limit" || *got.SevenDay.Utilization != 1 || got.SevenDay.ResetsAt != 1791740400 {
		t.Fatalf("7d = %+v", got.SevenDay)
	}
	if got.FiveHour.Status != "within_limit" || *got.FiveHour.Utilization != 0.3 || got.FiveHour.ResetsAt != 1700000000 {
		t.Fatalf("5h 应保持不变: %+v", got.FiveHour)
	}
	for _, s := range []string{"overloaded", "", "{}", `{"type":"error","error":{"message":"overloaded"}}`} {
		if _, ok := exceededFromText(s, time.Now()); ok {
			t.Fatalf("%q 不应命中", s)
		}
	}
}

func TestExceededFromText_MillisecondsResetsAt(t *testing.T) {
	var got repository.AccountQuota
	apply, ok := exceededFromText(`{"type":"exceeded_limit","resetsAt":1791740400000,"representativeClaim":"five_hour"}`, time.Now())
	if !ok {
		t.Fatal("应命中 exceeded_limit")
	}
	apply(&got)
	if got.FiveHour == nil || got.FiveHour.ResetsAt != 1791740400 {
		t.Fatalf("毫秒应归一为秒: %+v", got.FiveHour)
	}
}

func TestParseSSE_QuotaDeliveredAfterStream(t *testing.T) {
	// 额度修改在流结束时才交付，且保持事件顺序：先 message_limit 整体替换，再 exceeded 合并。
	stream := sseLines(
		`{"type":"message_limit","message_limit":{"type":"within_limit","windows":{"5h":{"status":"within_limit","resets_at":1791740400,"utilization":0.9}}}}`,
		`{"type":"error","error":{"type":"rate_limit_error","message":"{\"type\":\"exceeded_limit\",\"resetsAt\":1791740400,\"representativeClaim\":\"five_hour\"}"}}`,
	)
	calls := 0
	var got repository.AccountQuota
	sink := QuotaSink(func(apply func(*repository.AccountQuota)) { calls++; apply(&got) })
	if err := parseCompletionSSE(strings.NewReader(stream), nil, sink); err == nil {
		t.Fatal("应返回 upstream 错误")
	}
	if calls != 2 || got.Type != "exceeded_limit" || got.FiveHour == nil || *got.FiveHour.Utilization != 1 {
		t.Fatalf("calls=%d got=%+v", calls, got)
	}
}

func TestParseSSE_RefusalStillErrors(t *testing.T) {
	stream := sseLines(`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`)
	err := parseCompletionSSE(strings.NewReader(stream), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "安全分类器") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublicAccountViewQuota(t *testing.T) {
	acc := repository.Account{Email: "a@b.c", Cookies: map[string]string{"sessionKey": "sk-test-fake"}}
	if v := PublicAccountView(&acc); v.Quota != nil {
		t.Fatalf("无快照时 Quota 应为 nil: %+v", v.Quota)
	}
	acc.Quota = repository.AccountQuota{Type: "within_limit", UpdatedAt: time.Now()}
	v := PublicAccountView(&acc)
	if v.Quota == nil {
		t.Fatal("有快照时 Quota 应非 nil")
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, "sk-test-fake") || strings.Contains(s, "sessionKey") || !strings.Contains(s, `"quota"`) {
		t.Fatalf("JSON 不符合预期: %s", s)
	}
	// 无快照的账号 JSON 里没有 quota 键。
	acc.Quota = repository.AccountQuota{}
	b, _ = json.Marshal(PublicAccountView(&acc))
	if strings.Contains(string(b), `"quota"`) {
		t.Fatalf("无快照不应有 quota 键: %s", b)
	}
}
