package adapter

import (
	"encoding/json"
	"net/http"
	"strings"

	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

// webSearchOpts 是请求里 Anthropic 服务端工具 web_search_* 的配置。
type webSearchOpts struct {
	MaxUses int
	Allowed []string
	Blocked []string
}

// splitWebSearchTools 把 web_search_* 服务端工具从 tools 中剔除并返回其配置；
// 没有该工具时 opts 为 nil，tools 原样返回。
func splitWebSearchTools(tools []map[string]any) (rest []map[string]any, opts *webSearchOpts) {
	for _, t := range tools {
		typ, _ := t["type"].(string)
		if !strings.HasPrefix(typ, "web_search_") {
			rest = append(rest, t)
			continue
		}
		if opts == nil {
			opts = &webSearchOpts{}
		}
		if n, ok := t["max_uses"].(float64); ok {
			opts.MaxUses = int(n)
		}
		opts.Allowed = append(opts.Allowed, stringList(t["allowed_domains"])...)
		opts.Blocked = append(opts.Blocked, stringList(t["blocked_domains"])...)
	}
	return rest, opts
}

func stringList(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, x := range arr {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// hint 生成追加到提示词末尾的搜索引导语。
func (o *webSearchOpts) hint() string {
	var b strings.Builder
	b.WriteString("\n\nUse your web search ability to look up current information for the request above, and include the source URLs in your answer.")
	if len(o.Allowed) > 0 {
		b.WriteString(" Only use sources from these domains: " + strings.Join(o.Allowed, ", ") + ".")
	}
	if len(o.Blocked) > 0 {
		b.WriteString(" Do not use sources from these domains: " + strings.Join(o.Blocked, ", ") + ".")
	}
	return b.String()
}

// searchBlocks 把一次搜索转成 server_tool_use + web_search_tool_result 两个内容块。
func searchBlocks(ev service.SearchEvent) (use, result gin.H) {
	id := "srvtoolu_" + shortID()
	items := make([]gin.H, 0, len(ev.Results))
	for _, r := range ev.Results {
		var age any
		if r.PageAge != "" {
			age = r.PageAge
		}
		items = append(items, gin.H{
			"type": "web_search_result", "url": r.URL, "title": r.Title,
			"encrypted_content": r.Snippet, "page_age": age,
		})
	}
	use = gin.H{"type": "server_tool_use", "id": id, "name": "web_search", "input": gin.H{"query": ev.Query}}
	result = gin.H{"type": "web_search_tool_result", "tool_use_id": id, "content": items}
	return use, result
}

func searchUsage(n int) gin.H {
	return gin.H{"web_search_requests": n}
}

func anthropicSearchNonStream(c *gin.Context, model string, prompt service.Prompt) {
	var searches []service.SearchEvent
	prompt.OnSearch = func(ev service.SearchEvent) { searches = append(searches, ev) }
	content, matched, ok := runNonStreamStop(c, "messages", model, prompt)
	if !ok {
		return
	}
	stopReason, stopSeq := "end_turn", any(nil)
	if matched != "" {
		stopReason, stopSeq = "stop_sequence", matched
	}
	blocks := make([]gin.H, 0, 2*len(searches)+1)
	for _, ev := range searches {
		use, result := searchBlocks(ev)
		blocks = append(blocks, use, result)
	}
	blocks = append(blocks, gin.H{"type": "text", "text": content})
	usage := gin.H{"input_tokens": tokenCount(prompt.Text), "output_tokens": tokenCount(content)}
	if len(searches) > 0 {
		usage["server_tool_use"] = searchUsage(len(searches))
	}
	c.JSON(http.StatusOK, gin.H{
		"id": "msg_" + shortID(), "type": "message", "role": "assistant", "model": model,
		"content": blocks, "stop_reason": stopReason, "stop_sequence": stopSeq, "usage": usage,
	})
}

func anthropicSearchStream(c *gin.Context, model string, prompt service.Prompt) {
	stream := newMessageStream(c, model, prompt)
	var pending []service.SearchEvent
	textOpen := false
	// 搜索块先攒着，等到有正文或流结束再写出，避免换号重试时重复输出。
	flush := func() {
		for _, ev := range pending {
			if textOpen {
				stream.close()
				textOpen = false
			}
			use, result := searchBlocks(ev)
			q, _ := json.Marshal(gin.H{"query": ev.Query})
			stream.open(gin.H{"type": "server_tool_use", "id": use["id"], "name": "web_search", "input": gin.H{}})
			stream.delta(gin.H{"type": "input_json_delta", "partial_json": string(q)})
			stream.close()
			stream.open(result)
			stream.close()
			stream.searches++
		}
		pending = nil
	}
	openText := func() {
		if !textOpen {
			stream.open(gin.H{"type": "text", "text": ""})
			textOpen = true
		}
	}
	prompt.OnSearch = func(ev service.SearchEvent) { pending = append(pending, ev) }
	text, matched, err := runAndCollectStop("messages", model, true, prompt, func(text string) {
		flush()
		openText()
		stream.delta(gin.H{"type": "text_delta", "text": text})
	})
	flush()
	openText()
	stop, stopSeq := "end_turn", any(nil)
	if matched != "" {
		stop, stopSeq = "stop_sequence", matched
	}
	if err != nil {
		stream.delta(gin.H{"type": "text_delta", "text": "\n[错误] " + err.Error()})
		stop, stopSeq = "error", nil
	}
	stream.close()
	stream.stopSeq(stop, stopSeq, tokenCount(text))
}
