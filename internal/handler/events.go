package handler

import (
	"fmt"
	"time"

	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

// eventsHeartbeat 是 SSE 心跳间隔（测试里可改短）。
var eventsHeartbeat = 25 * time.Second

// AdminEvents 是额度实时推送的 SSE 接口：连上后先发 ": connected"，
// 之后每次有账号额度变化发一条 quota 事件，空闲时每隔 eventsHeartbeat 发注释心跳。
func AdminEvents(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	write := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(c.Writer, format, args...); err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}

	ch, cancel := service.SubscribeEvents()
	defer cancel()
	if !write(": connected\n\n") {
		return
	}

	ticker := time.NewTicker(eventsHeartbeat)
	defer ticker.Stop()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !write(": ping\n\n") {
				return
			}
		case msg := <-ch:
			if !write("event: quota\ndata: %s\n\n", msg) {
				return
			}
		}
	}
}
