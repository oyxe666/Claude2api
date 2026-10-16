package adapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

const searchReqTools = `[{"type":"web_search_20250305","name":"web_search","max_uses":8,"allowed_domains":["go.dev"]}]`

func postMessages(body string) string {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	AnthropicMessages(c)
	return w.Body.String()
}

func withSearchRunner(t *testing.T, withResults bool, seen *service.Prompt) func() {
	old := runner
	runner = testRunner(func(_ string, prompt service.Prompt, emit func(string)) (service.CompletionResult, error) {
		if seen != nil {
			*seen = prompt
		}
		if withResults && prompt.OnSearch != nil {
			prompt.OnSearch(service.SearchEvent{Query: "golang release", Results: []service.SearchResult{
				{Title: "Go Downloads", URL: "https://go.dev/dl/", PageAge: "1 day ago", Snippet: "go1.25"},
			}})
		}
		emit("Latest is ")
		emit("go1.25.")
		return service.CompletionResult{StatusCode: http.StatusOK}, nil
	})
	return func() { runner = old }
}

func TestWebSearchToolStrippedAndNative(t *testing.T) {
	var seen service.Prompt
	defer withSearchRunner(t, true, &seen)()
	for _, stream := range []bool{false, true} {
		body := `{"stream":` + map[bool]string{false: "false", true: "true"}[stream] +
			`,"system":"Search once, then reply with a brief summary","tools":` + searchReqTools +
			`,"messages":[{"role":"user","content":"Perform a web search for the query: golang"}]}`
		out := postMessages(body)
		if strings.Contains(seen.Text, "- web_search(") || strings.Contains(seen.Text, "<tool_call>") {
			t.Fatalf("web_search 不应被当作普通工具写进提示: %s", seen.Text)
		}
		if !strings.Contains(seen.Text, "go.dev") || !strings.Contains(seen.Text, "web search") {
			t.Fatalf("提示里应引导搜索并转述域名: %s", seen.Text)
		}
		for _, want := range []string{`"type":"server_tool_use"`, `"name":"web_search"`, `"type":"web_search_tool_result"`,
			`"type":"web_search_result"`, `https://go.dev/dl/`, `"web_search_requests":1`, `Latest is `} {
			if !strings.Contains(out, want) {
				t.Fatalf("stream=%v 缺少 %s: %s", stream, want, out)
			}
		}
		if strings.Contains(out, `"type":"tool_use"`) || strings.Contains(out, `"stop_reason":"tool_use"`) {
			t.Fatalf("不应出现客户端 tool_use: %s", out)
		}
		if strings.Index(out, "server_tool_use") > strings.Index(out, "Latest is ") {
			t.Fatalf("搜索块应在文字之前: %s", out)
		}
	}
}

func TestWebSearchFallbackToText(t *testing.T) {
	defer withSearchRunner(t, false, nil)()
	for _, stream := range []bool{false, true} {
		body := `{"stream":` + map[bool]string{false: "false", true: "true"}[stream] +
			`,"tools":` + searchReqTools + `,"messages":[{"role":"user","content":"q"}]}`
		out := postMessages(body)
		if strings.Contains(out, "server_tool_use") || !strings.Contains(out, "Latest is ") || !strings.Contains(out, `"stop_reason":"end_turn"`) {
			t.Fatalf("上游无结构化结果时应退回纯文字: %s", out)
		}
	}
}

func TestWebSearchMixedWithClientTool(t *testing.T) {
	var seen service.Prompt
	defer withSearchRunner(t, false, &seen)()
	tools := `[{"name":"Read","description":"read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}},` +
		`{"type":"web_search_20250305","name":"web_search"}]`
	postMessages(`{"stream":false,"tools":` + tools + `,"messages":[{"role":"user","content":"q"}]}`)
	if !strings.Contains(seen.Text, "Read") {
		t.Fatalf("普通工具应仍在提示中: %s", seen.Text)
	}
	if strings.Contains(seen.Text, "- web_search(") {
		t.Fatalf("web_search 应被剔除: %s", seen.Text)
	}
}

func TestPlainRequestUnaffected(t *testing.T) {
	defer withSearchRunner(t, true, nil)()
	out := postMessages(`{"stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if strings.Contains(out, "server_tool_use") {
		t.Fatalf("无 web_search 工具时不应输出搜索块: %s", out)
	}
}
