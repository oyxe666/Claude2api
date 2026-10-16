package service

import "encoding/json"

// UserInfo 是账号信息。
type UserInfo struct {
	Email   string
	OrgUUID string
}

type ClaudeAccount struct {
	EmailAddress string `json:"email_address"`
	Memberships  []struct {
		Organization struct {
			UUID string `json:"uuid"`
		} `json:"organization"`
	} `json:"memberships"`
}

// SseEvent 是 completion SSE 事件。
type SseEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type    string          `json:"type"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Content json.RawMessage `json:"content"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	// MessageLimit 是 message_limit 事件的原始 JSON，由 quota.go 宽松解析。
	MessageLimit json.RawMessage `json:"message_limit"`
	StopReason   string          `json:"stop_reason"`
	StopDetails  struct {
		Type        string `json:"type"`
		Category    string `json:"category"`
		Explanation string `json:"explanation"`
	} `json:"stop_details"`
}

// Prompt 是协议适配层交给上游实现的输入。
type Prompt struct {
	Text        string
	Images      []string
	RawRequest  json.RawMessage
	ForceInline bool
	MaxTokens   int
	Temperature *float64
	TopP        *float64
	Stop        []string
	// OnSearch 非空时，上游联网搜索的查询与结构化结果会通过它回传（可选）。
	OnSearch func(SearchEvent)
}

// SearchResult 是上游联网搜索返回的一条结果。
type SearchResult struct {
	Title   string
	URL     string
	PageAge string
	Snippet string
}

// SearchEvent 是一次联网搜索：查询词与结果列表。
type SearchEvent struct {
	Query   string
	Results []SearchResult
}

type CompletionResult struct {
	Account    string
	StatusCode int
}

type CompletionError struct {
	StatusCode int
	Err        error
}

func (e *CompletionError) Error() string { return e.Err.Error() }
func (e *CompletionError) Unwrap() error { return e.Err }
