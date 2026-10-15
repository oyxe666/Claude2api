package handler

import (
	"net/http"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/repository"

	"github.com/gin-gonic/gin"
)

// AdminUsage 返回近 7/30 天的用量统计（token 为本地估算）和模型单价表。只读本地库，不访问 Claude。
func AdminUsage(c *gin.Context) {
	days := atoiDefault(c.Query("days"), 7)
	if days != 30 {
		days = 7
	}
	st := repository.GetUsageStats(days, time.Now())
	c.JSON(http.StatusOK, gin.H{
		"days":   st.Days,
		"daily":  st.Daily,
		"models": st.Models,
		"prices": config.Get().ModelPrices,
	})
}
