package service

import (
	"io"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
)

// captureClient 只记录请求体，不访问网络。
type captureClient struct {
	tlsclient.HttpClient
	body string
}

func (c *captureClient) Do(req *fhttp.Request) (*fhttp.Response, error) {
	b, _ := io.ReadAll(req.Body)
	c.body = string(b)
	return &fhttp.Response{StatusCode: fhttp.StatusBadRequest, Body: io.NopCloser(strings.NewReader("{}"))}, nil
}

func TestSendMessageDoesNotForwardStopSequences(t *testing.T) {
	cc := &captureClient{}
	c := &ClaudeAI{baseURL: claudeAIBaseURL, client: cc, headers: map[string]string{}, modeSet: true}
	temp := 0.5
	_, _ = c.SendMessage("conv", "claude-sonnet-5-5", Prompt{Text: "hi", Stop: []string{"</block>"}, MaxTokens: 64, Temperature: &temp}, nil, nil, func(string) {})
	if cc.body == "" {
		t.Fatal("未捕获到上游请求体")
	}
	for _, key := range []string{"stop_sequences", "max_tokens"} {
		if strings.Contains(cc.body, key) {
			t.Fatalf("请求体不应包含 %s: %s", key, cc.body)
		}
	}
	if !strings.Contains(cc.body, `"temperature"`) {
		t.Fatalf("temperature 应仍被转发: %s", cc.body)
	}
}
