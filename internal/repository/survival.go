package repository

import (
	"time"
)

// SurvivalPoint 是存活曲线上的一个点：活过 Day 天的比例。
type SurvivalPoint struct {
	Day      int     `json:"day"`
	Survival float64 `json:"survival"`
}

// SurvivalDaily 是某个北京日期的导入数与阵亡数。
type SurvivalDaily struct {
	Date     string `json:"date"`
	Imported int    `json:"imported"`
	Died     int    `json:"died"`
}

// SurvivalStats 是账号存活统计。只读本地库，不访问 Claude。
// 统计范围：现存账号 + 乱葬岗记录（乱葬岗上线前就删掉的账号没有记录）。
type SurvivalStats struct {
	Imported   int             `json:"imported"`    // 累计导入 = 现存 + 乱葬岗
	Alive      int             `json:"alive"`       // 仍存活的账号数
	Died       int             `json:"died"`        // 已阵亡数（乱葬岗记录 + 现存但已失效的账号）
	AliveRate  float64         `json:"alive_rate"`  // 当前存活 / 累计导入
	Eligible7  int             `json:"eligible_7"`  // 导入已满 7 天的账号数
	Survived7  int             `json:"survived_7"`  // 其中活过 7 天的数量
	Rate7      float64         `json:"rate_7"`      // 活过 7 天的比例
	Eligible30 int             `json:"eligible_30"` // 导入已满 30 天的账号数
	Survived30 int             `json:"survived_30"` // 其中活过 30 天的数量
	Rate30     float64         `json:"rate_30"`     // 活过 30 天的比例
	Curve      []SurvivalPoint `json:"curve"`       // Kaplan-Meier 存活曲线
	Daily      []SurvivalDaily `json:"daily"`       // 近 Days 天每日导入/阵亡
	Days       int             `json:"days"`
}

// survivalRec 是一个账号的履历。
type survivalRec struct {
	joined time.Time
	died   time.Time
	dead   bool
	life   float64 // 寿命（秒）；活着的账号为已存活时长
}

const survivalDay = 24 * 3600.0

func ratio(a, b int) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// kaplanMeier 计算存活曲线。活着的账号视为未结束（删失），不算死亡。
// 横轴按整天（向下取整），第 0 天为 1.0。
func kaplanMeier(recs []survivalRec) []SurvivalPoint {
	if len(recs) == 0 {
		return []SurvivalPoint{}
	}
	deaths := map[int]int{}
	maxDay := 0
	for _, r := range recs {
		d := int(r.life / survivalDay)
		if d > maxDay {
			maxDay = d
		}
		if r.dead {
			deaths[d]++
		}
	}
	out := make([]SurvivalPoint, 0, maxDay+1)
	s := 1.0
	for d := 0; d <= maxDay; d++ {
		// 风险集：寿命（或已观察时长）不少于 d 天的账号。
		atRisk := 0
		for _, r := range recs {
			if int(r.life/survivalDay) >= d {
				atRisk++
			}
		}
		if dd := deaths[d]; dd > 0 && atRisk > 0 {
			s *= 1 - float64(dd)/float64(atRisk)
		}
		out = append(out, SurvivalPoint{Day: d, Survival: s})
	}
	return out
}

// GetSurvivalStats 返回存活统计。days 只接受 7 或 30（其它按 7），用于每日导入/阵亡图。
// 现存账号中 status=expired 的视为已阵亡（死亡时间取 updated_at）；乱葬岗里的按记录算。
func GetSurvivalStats(days int, now time.Time) SurvivalStats {
	if days != 30 {
		days = 7
	}
	st := SurvivalStats{Days: days, Curve: []SurvivalPoint{}}
	var recs []survivalRec
	graved := map[string]bool{}
	if db != nil {
		var graves []Grave
		db.Find(&graves)
		for _, g := range graves {
			graved[g.Email] = true
			recs = append(recs, survivalRec{joined: g.JoinedAt, died: g.DiedAt, dead: true, life: float64(g.LifespanSec)})
		}
	}
	for _, a := range LoadAccounts() {
		if graved[a.Email] {
			continue
		}
		r := survivalRec{joined: a.CreatedAt}
		if a.Status == "expired" {
			r.dead = true
			r.died = a.UpdatedAt
			r.life = a.UpdatedAt.Sub(a.CreatedAt).Seconds()
		} else {
			r.life = now.Sub(a.CreatedAt).Seconds()
		}
		if r.life < 0 {
			r.life = 0
		}
		recs = append(recs, r)
	}

	st.Imported = len(recs)
	for _, r := range recs {
		if r.dead {
			st.Died++
		} else {
			st.Alive++
		}
		// 只统计导入已满 N 天的账号；其中没死、或寿命不少于 N 天的算活过。
		since := now.Sub(r.joined)
		if since >= 7*24*time.Hour {
			st.Eligible7++
			if !r.dead || r.life >= 7*survivalDay {
				st.Survived7++
			}
		}
		if since >= 30*24*time.Hour {
			st.Eligible30++
			if !r.dead || r.life >= 30*survivalDay {
				st.Survived30++
			}
		}
	}
	st.AliveRate = ratio(st.Alive, st.Imported)
	st.Rate7 = ratio(st.Survived7, st.Eligible7)
	st.Rate30 = ratio(st.Survived30, st.Eligible30)
	st.Curve = kaplanMeier(recs)

	// 每日导入/阵亡（北京日期，补零，日期升序）。
	n := now.In(graveBeijing)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, graveBeijing)
	start := today.AddDate(0, 0, -(days - 1))
	imp, dead := map[string]int{}, map[string]int{}
	for _, r := range recs {
		if !r.joined.IsZero() && !r.joined.Before(start) {
			imp[r.joined.In(graveBeijing).Format("2006-01-02")]++
		}
		if r.dead && !r.died.IsZero() && !r.died.Before(start) {
			dead[r.died.In(graveBeijing).Format("2006-01-02")]++
		}
	}
	st.Daily = make([]SurvivalDaily, 0, days)
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		st.Daily = append(st.Daily, SurvivalDaily{Date: d, Imported: imp[d], Died: dead[d]})
	}
	return st
}
