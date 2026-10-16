package repository

import (
	"time"

	"gorm.io/gorm/clause"
)

// Account 是一条账号记录。
type Account struct {
	ID        uint              `json:"-" gorm:"primaryKey"`
	Email     string            `json:"email" gorm:"uniqueIndex;not null"`
	OrgUUID   string            `json:"org_uuid" gorm:"column:org_uuid"`
	Cookies   map[string]string `json:"-" gorm:"serializer:json"`
	Status    string            `json:"status,omitempty"`
	// Disabled 为 true 表示该账号被手动停用：不参与选号，但不删除、额度照常检查。
	Disabled  bool              `json:"disabled" gorm:"not null;default:false"`
	Quota    AccountQuota      `json:"-" gorm:"column:quota;serializer:json"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// LoadAccounts 读取全部账号。
func LoadAccounts() []Account {
	var out []Account
	if err := db.Order("id ASC").Find(&out).Error; err != nil {
		return []Account{}
	}
	return out
}

// AccountByEmail 按邮箱取账号。
func AccountByEmail(email string) *Account {
	if email == "" {
		return nil
	}
	var a Account
	if db.Where("email = ?", email).First(&a).Error != nil {
		return nil
	}
	return &a
}

// UpsertAccount 写入或更新账号。
func UpsertAccount(a *Account) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "email"}},
		// 只更新这些列，避免重新导入账号时把 quota（额度快照）清空。
		DoUpdates: clause.AssignmentColumns([]string{"org_uuid", "cookies", "status", "updated_at"}),
	}).Create(a).Error
}

// SetAccountDisabled 只更新 disabled 列（不用 Save，避免和额度快照并发写冲突）。账号不存在返回 false。
func SetAccountDisabled(email string, disabled bool) bool {
	if email == "" || db == nil {
		return false
	}
	var n int64
	if db.Model(&Account{}).Where("email = ?", email).Count(&n).Error != nil || n == 0 {
		return false
	}
	return db.Model(&Account{}).Where("email = ?", email).UpdateColumn("disabled", disabled).Error == nil
}

// SetAllAccountsDisabled 批量停用/启用全部账号，返回受影响数量。
func SetAllAccountsDisabled(disabled bool) int {
	res := db.Model(&Account{}).Where("1 = 1").UpdateColumn("disabled", disabled)
	if res.Error != nil {
		return 0
	}
	return int(res.RowsAffected)
}

// UpdateAccount 修改指定账号。
func UpdateAccount(email string, mutate func(*Account)) bool {
	a := AccountByEmail(email)
	if a == nil {
		return false
	}
	mutate(a)
	// Omit("quota")：额度快照只由 UpdateAccountQuota 写，避免这里用读到的旧值覆盖它。
	return db.Omit("quota").Save(a).Error == nil
}

// DeleteAccount 删除账号。
func DeleteAccount(email string) int {
	res := db.Where("email = ?", email).Delete(&Account{})
	if res.Error != nil {
		return 0
	}
	return int(res.RowsAffected)
}

// DeleteAccountsByStatus 按状态删除账号。
func DeleteAccountsByStatus(statuses []string) []string {
	removed := make([]string, 0)
	for _, a := range LoadAccounts() {
		for _, s := range statuses {
			if a.Status == s {
				// 先记墓碑再删（已经记过的号自动去重，保留最早那条）。
				RecordGrave(a.Email, GraveManualDelete)
				if db.Where("email = ?", a.Email).Delete(&Account{}).Error == nil {
					removed = append(removed, a.Email)
				}
				break
			}
		}
	}
	return removed
}
