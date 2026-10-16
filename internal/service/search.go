package service

import (
	"encoding/json"
	"strings"
)

// searchTracker 从上游 completion SSE 中提取联网搜索：
// tool_use(name=web_search) 的查询词，以及随后 tool_result 里的结果列表。
// 只做旁路观察，不影响正文解析；解析失败一律静默忽略。
type searchTracker struct {
	on       func(SearchEvent)
	inUse    bool
	useIndex int
	input    strings.Builder
	query    string
}

func newSearchTracker(on func(SearchEvent)) *searchTracker {
	return &searchTracker{on: on}
}

func (t *searchTracker) handle(ev SseEvent) {
	if t == nil || t.on == nil {
		return
	}
	switch ev.Type {
	case "content_block_start":
		switch ev.ContentBlock.Type {
		case "tool_use":
			if ev.ContentBlock.Name == "web_search" {
				t.inUse, t.useIndex = true, ev.Index
				t.input.Reset()
				t.query = queryFromJSON(ev.ContentBlock.Input)
			}
		case "tool_result":
			if ev.ContentBlock.Name == "web_search" {
				t.on(SearchEvent{Query: t.query, Results: resultsFromContent(ev.ContentBlock.Content)})
				t.query = ""
			}
		}
	case "content_block_delta":
		if t.inUse && ev.Index == t.useIndex && ev.Delta.Type == "input_json_delta" {
			t.input.WriteString(ev.Delta.PartialJSON)
		}
	case "content_block_stop":
		if t.inUse && ev.Index == t.useIndex {
			t.inUse = false
			if q := queryFromJSON(json.RawMessage(t.input.String())); q != "" {
				t.query = q
			}
		}
	}
}

func queryFromJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var in struct {
		Query string `json:"query"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	return strings.TrimSpace(in.Query)
}

// resultsFromContent 解析 tool_result 的 content 数组，
// 每项取 title / url / page_age（或 metadata.page_age）/ text。
func resultsFromContent(raw json.RawMessage) []SearchResult {
	var items []struct {
		Title    string `json:"title"`
		URL      string `json:"url"`
		PageAge  string `json:"page_age"`
		Text     string `json:"text"`
		Metadata struct {
			PageAge string `json:"page_age"`
		} `json:"metadata"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil
	}
	out := make([]SearchResult, 0, len(items))
	for _, it := range items {
		if it.URL == "" {
			continue
		}
		age := it.PageAge
		if age == "" {
			age = it.Metadata.PageAge
		}
		out = append(out, SearchResult{Title: it.Title, URL: it.URL, PageAge: age, Snippet: it.Text})
	}
	return out
}
