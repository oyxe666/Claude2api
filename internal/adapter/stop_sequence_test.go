package adapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

func TestAnthropicStopSequence(t *testing.T) {
	old := runner
	runner = testRunner(func(_ string, prompt service.Prompt, emit func(string)) (service.CompletionResult, error) {
		emit("<block>yes<")
		emit("/block><reason>x</reason>")
		return service.CompletionResult{StatusCode: http.StatusOK}, nil
	})
	defer func() { runner = old }()

	for _, stream := range []bool{false, true} {
		body := `{"stream":` + map[bool]string{false: "false", true: "true"}[stream] +
			`,"stop_sequences":["</block>"],"messages":[{"role":"user","content":"hi"}]}`
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
		AnthropicMessages(c)
		out := w.Body.String()
		if strings.Contains(out, "reason\\u003e") || !strings.Contains(out, "yes") {
			t.Fatalf("stream=%v 未按 stop 截断: %s", stream, out)
		}
		if !strings.Contains(out, `"stop_reason":"stop_sequence"`) || !strings.Contains(out, `"stop_sequence":"\u003c/block\u003e"`) {
			t.Fatalf("stream=%v stop_reason/stop_sequence 不正确: %s", stream, out)
		}
	}
}
