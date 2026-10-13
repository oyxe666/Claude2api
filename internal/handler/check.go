package handler

import (
	"net/http"

	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

// checkAllOpts 是「检查账号」的节奏配置；测试里可改成 Gap=0。
var checkAllOpts = service.DefaultCheckBatchOptions()

// AdminCheckAllAccounts 逐个检查全部可用账号（每个账号只发一轮请求，账号间随机间隔），用 SSE 汇报进度。
// 客户端断开（点停止）即取消，剩余账号不再请求。
func AdminCheckAllAccounts(c *gin.Context) {
	end, ok := service.TryBeginQuotaBatch()
	if !ok {
		c.JSON(http.StatusConflict, gin.H{"error": service.ErrQuotaBatchRunning.Error()})
		return
	}
	defer end()

	emails := service.QuotaBatchTargets()
	if len(emails) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有可检查的账号"})
		return
	}
	streamQuotaBatch(c, checkAllOpts, emails, "检查账号", gin.H{
		"total":       len(emails),
		"gap_min_sec": service.CheckGapMin.Seconds(),
		"gap_max_sec": service.CheckGapMax.Seconds(),
	})
}
