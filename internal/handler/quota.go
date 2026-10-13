package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"claude2api/internal/repository"
	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

// quotaBatchOpts 是批量额度刷新的节奏配置；测试里可改成 Gap=0。
var quotaBatchOpts = service.DefaultQuotaBatchOptions()

// AdminRefreshQuota 主动查询单个账号的额度。
// 查询本身失败也返回 200（result.ok=false，原因在快照 error 里），前端靠 account 重画卡片。
func AdminRefreshQuota(c *gin.Context) {
	var body struct {
		Email string `json:"email"`
	}
	_ = c.ShouldBindJSON(&body)
	email := strings.TrimSpace(body.Email)
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供邮箱"})
		return
	}
	acct := repository.AccountByEmail(email)
	if acct == nil || service.SessionKey(acct) == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在或缺少 sessionKey"})
		return
	}
	result := service.RefreshAccountQuota(c.Request.Context(), email)
	c.JSON(http.StatusOK, gin.H{
		"result":  result,
		"account": service.PublicAccountView(repository.AccountByEmail(email)),
	})
}

// AdminRefreshAllQuota 逐个刷新全部可用账号的额度，用 SSE 汇报进度。
func AdminRefreshAllQuota(c *gin.Context) {
	end, ok := service.TryBeginQuotaBatch()
	if !ok {
		c.JSON(http.StatusConflict, gin.H{"error": service.ErrQuotaBatchRunning.Error()})
		return
	}
	defer end()

	emails := service.QuotaBatchTargets()
	if len(emails) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有可刷新的账号"})
		return
	}
	streamQuotaBatch(c, quotaBatchOpts, emails, "批量刷新", nil)
}

// streamQuotaBatch 写 SSE 头并逐个处理 emails，每个账号后发 progress，结束发 done。
// start 非 nil 时先发一条 start 事件。调用方负责 TryBeginQuotaBatch 与目标校验。
func streamQuotaBatch(c *gin.Context, opts service.QuotaBatchOptions, emails []string, logName string, start any) {
	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	send := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, b)
		c.Writer.Flush()
	}

	ctx := c.Request.Context()
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	if start != nil {
		send("start", start)
	}
	slog.Info("[额度] "+logName+"开始", "total", len(emails))
	sum := service.RefreshAllQuotas(ctx, opts, emails, func(p service.QuotaProgress) {
		send("progress", gin.H{
			"total": p.Total, "completed": p.Completed,
			"ok": p.OK, "failed": p.Failed, "skipped": p.Skipped,
			"email": p.Email, "error": p.Error,
			"account": service.PublicAccountView(repository.AccountByEmail(p.Email)),
		})
	})
	send("done", gin.H{"done": true, "total": sum.Total, "ok": sum.OK, "failed": sum.Failed, "skipped": sum.Skipped})
	slog.Info("[额度] "+logName+"完成", "total", sum.Total, "ok", sum.OK, "failed", sum.Failed, "skipped", sum.Skipped)
}
