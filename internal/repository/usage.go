package repository

import "time"

// UsageDaily 是某个北京日期的 token 与调用汇总（token 为本地估算值）。
type UsageDaily struct {
	Date         string `json:"date"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	Calls        int    `json:"calls"`
	Success      int    `json:"success"`
}

// UsageModel 是某个模型在统计范围内的 token 与调用汇总。
type UsageModel struct {
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	Calls        int    `json:"calls"`
	Success      int    `json:"success"`
}

// UsageStats 是用量统计结果。
type UsageStats struct {
	Days   int          `json:"days"`
	Daily  []UsageDaily `json:"daily"`
	Models []UsageModel `json:"models"`
}

type usageRow struct {
	CreatedAt    string
	Model        string
	InputTokens  int
	OutputTokens int
	Success      bool
}

// GetUsageStats 按北京日期汇总近 days 天（只接受 7 或 30，其余按 7）的调用日志。
// 只读 created_at / model / token / success 列，不读 Request、Response；每天补零；
// 模型按 token 总量倒序。只读本地库，不访问 Claude。
func GetUsageStats(days int, now time.Time) UsageStats {
	if days != 30 {
		days = 7
	}
	n := now.In(graveBeijing)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, graveBeijing)
	start := today.AddDate(0, 0, -(days - 1))

	daily := make(map[string]*UsageDaily, days)
	out := UsageStats{Days: days, Daily: make([]UsageDaily, 0, days), Models: []UsageModel{}}
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		out.Daily = append(out.Daily, UsageDaily{Date: d})
	}
	for i := range out.Daily {
		daily[out.Daily[i].Date] = &out.Daily[i]
	}

	if db == nil {
		return out
	}
	var rows []usageRow
	if db.Model(&APILog{}).
		Select("created_at, model, input_tokens, output_tokens, success").
		Where("created_at >= ?", start.UTC().Format("2006-01-02T15:04:05Z")).
		Find(&rows).Error != nil {
		return out
	}

	models := map[string]*UsageModel{}
	for _, r := range rows {
		t, err := time.Parse("2006-01-02T15:04:05Z", r.CreatedAt)
		if err != nil {
			continue
		}
		d := daily[t.In(graveBeijing).Format("2006-01-02")]
		if d == nil {
			continue
		}
		d.InputTokens += int64(r.InputTokens)
		d.OutputTokens += int64(r.OutputTokens)
		d.Calls++
		m := models[r.Model]
		if m == nil {
			m = &UsageModel{Model: r.Model}
			models[r.Model] = m
		}
		m.InputTokens += int64(r.InputTokens)
		m.OutputTokens += int64(r.OutputTokens)
		m.Calls++
		if r.Success {
			d.Success++
			m.Success++
		}
	}
	for _, m := range models {
		out.Models = append(out.Models, *m)
	}
	sortUsageModels(out.Models)
	return out
}

func sortUsageModels(ms []UsageModel) {
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0; j-- {
			a, b := ms[j-1], ms[j]
			at, bt := a.InputTokens+a.OutputTokens, b.InputTokens+b.OutputTokens
			if at < bt || (at == bt && a.Model > b.Model) {
				ms[j-1], ms[j] = ms[j], ms[j-1]
			} else {
				break
			}
		}
	}
}
