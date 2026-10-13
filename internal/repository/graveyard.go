package repository

import (
	"time"

	"gorm.io/gorm/clause"
)

// 阵亡原因。
const (
	GraveSessionInvalid = "session_invalid" // 会话失效，被置为 expired
	GraveAutoRemoved    = "auto_removed"    // 会话失效且开启了自动移除，被系统删除
	GraveManualDelete   = "manual_delete"   // 管理员手动删除
)

// Grave 是乱葬岗里的一条记录：只存账号的基本履历，绝不存 sessionKey / Cookies / OrgUUID。
// 一个邮箱只记一次（Email 唯一）。
type Grave struct {
	ID          uint         `json:"id" gorm:"primaryKey"`
	Email       string       `json:"email" gorm:"uniqueIndex;not null"`
	JoinedAt    time.Time    `json:"joined_at"`
	DiedAt      time.Time    `json:"died_at" gorm:"index"`
	LifespanSec int64        `json:"lifespan_sec"`
	Reason      string       `json:"reason"`
	ChatCount   int          `json:"chat_count"`
	LastQuota   AccountQuota `json:"last_quota,omitzero" gorm:"column:last_quota;serializer:json"`
}

func (Grave) TableName() string { return "graveyard" }

// RecordGrave 在账号被置失效/删除之前调用，记一笔墓碑。
// 已经记过（同邮箱）、账号不存在、邮箱为空或库未就绪时返回 false。
// 只读库，不向 Claude 发任何请求；ChatCount 只统计 success=true 的调用日志。
func RecordGrave(email, reason string) bool {
	if db == nil || email == "" {
		return false
	}
	var n int64
	if db.Model(&Grave{}).Where("email = ?", email).Count(&n).Error != nil || n > 0 {
		return false
	}
	a := AccountByEmail(email)
	if a == nil {
		return false
	}
	var chats int64
	db.Model(&APILog{}).Where("account = ? AND success = ?", email, true).Count(&chats)

	died := time.Now().UTC()
	var life int64
	if !a.CreatedAt.IsZero() {
		if s := int64(died.Sub(a.CreatedAt).Seconds()); s > 0 {
			life = s
		}
	}
	g := Grave{
		Email:       a.Email,
		JoinedAt:    a.CreatedAt,
		DiedAt:      died,
		LifespanSec: life,
		Reason:      reason,
		ChatCount:   int(chats),
		LastQuota:   a.Quota,
	}
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&g)
	return res.Error == nil && res.RowsAffected > 0
}

// MarkAccountExpired 统一的「置会话失效」入口：先记墓碑，再把账号状态改成 expired。
// 返回账号是否存在并更新成功。
func MarkAccountExpired(email string) bool {
	RecordGrave(email, GraveSessionInvalid)
	return UpdateAccount(email, func(a *Account) { a.Status = "expired" })
}

// ListGraves 按阵亡时间倒序分页。limit<=0 取 20，>100 取 100；offset<0 取 0。
func ListGraves(limit, offset int) []Grave {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	out := []Grave{}
	if db == nil {
		return out
	}
	if err := db.Order("died_at DESC, id DESC").Limit(limit).Offset(offset).Find(&out).Error; err != nil {
		return []Grave{}
	}
	return out
}

// CountGraves 返回墓碑总数。
func CountGraves() int {
	if db == nil {
		return 0
	}
	var n int64
	db.Model(&Grave{}).Count(&n)
	return int(n)
}

// GraveStats 是乱葬岗顶部统计。
type GraveStats struct {
	Total          int   `json:"total"`
	AvgLifespanSec int64 `json:"avg_lifespan_sec"`
	Last7Days      int   `json:"last_7_days"`
}

// GetGraveStats 返回累计阵亡数（含手动删除）、平均寿命（四舍五入取整秒）、近 7 天阵亡数。
func GetGraveStats() GraveStats {
	var st GraveStats
	if db == nil {
		return st
	}
	var total int64
	db.Model(&Grave{}).Count(&total)
	st.Total = int(total)
	if total == 0 {
		return st
	}
	var avg float64
	db.Model(&Grave{}).Select("COALESCE(AVG(lifespan_sec), 0)").Scan(&avg)
	st.AvgLifespanSec = int64(avg + 0.5)
	var recent int64
	db.Model(&Grave{}).Where("died_at >= ?", time.Now().UTC().Add(-7*24*time.Hour)).Count(&recent)
	st.Last7Days = int(recent)
	return st
}
