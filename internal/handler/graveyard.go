package handler

import (
	"net/http"

	"claude2api/internal/repository"

	"github.com/gin-gonic/gin"
)

// AdminGraveyard 分页返回乱葬岗（按阵亡时间倒序）。只读本地库，不访问 Claude。
func AdminGraveyard(c *gin.Context) {
	limit := atoiDefault(c.Query("limit"), 20)
	offset := atoiDefault(c.Query("offset"), 0)
	c.JSON(http.StatusOK, gin.H{
		"graves": repository.ListGraves(limit, offset),
		"total":  repository.CountGraves(),
	})
}

// AdminGraveyardStats 返回累计阵亡数（含手动删除）、平均寿命（秒）、近 7 天阵亡数。
func AdminGraveyardStats(c *gin.Context) {
	c.JSON(http.StatusOK, repository.GetGraveStats())
}
