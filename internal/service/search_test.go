package service

import (
	"strings"
	"testing"
)

func TestParseSSE_WebSearchCaptured(t *testing.T) {
	stream := sseLines(
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"web_search","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\": \"golang"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":" latest\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_result","name":"web_search","content":[{"type":"knowledge","title":"Go","url":"https://go.dev","metadata":{"page_age":"2 days ago"}},{"title":"no url"}]}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"OK"}}`,
	)
	var got []SearchEvent
	var text string
	err := parseCompletionSSEWithSearch(strings.NewReader(stream), func(s string) { text += s }, nil, func(e SearchEvent) { got = append(got, e) })
	if err != nil || text != "OK" {
		t.Fatalf("err=%v text=%q", err, text)
	}
	if len(got) != 1 || got[0].Query != "golang latest" || len(got[0].Results) != 1 ||
		got[0].Results[0].URL != "https://go.dev" || got[0].Results[0].PageAge != "2 days ago" {
		t.Fatalf("搜索事件不对: %+v", got)
	}
}
