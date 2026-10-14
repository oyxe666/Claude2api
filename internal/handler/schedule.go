package handler

import (
	"net/http"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminScheduleStatus 返回定时检查的开关、设定时间、下次运行时间和上次结果。
// 只读本地配置和状态文件，不会向 Claude 发请求。
func AdminScheduleStatus(c *gin.Context) {
	s := config.Get()
	st := service.LoadScheduleState()
	next := service.NextScheduleRun(time.Now(), s.ScheduleEnabled, s.ScheduleTime, st)
	resp := gin.H{
		"enabled":       s.ScheduleEnabled,
		"time":          s.ScheduleTime,
		"timezone":      "北京时间",
		"gap_min_sec":   service.ScheduleGapMin.Seconds(),
		"gap_max_sec":   service.ScheduleGapMax.Seconds(),
		"last_run_date": st.LastRunDate,
		"last_run_at":   st.LastRunAt,
		"total":         st.Total,
		"ok":            st.OK,
		"failed":        st.Failed,
		"skipped":       st.Skipped,
	}
	if !next.IsZero() {
		resp["next_run_at"] = next
	}
	c.JSON(http.StatusOK, resp)
}
