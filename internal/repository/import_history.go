package repository

import "time"

// 导入状态。
const (
	ImportOK        = "imported"
	ImportDuplicate = "duplicate"
	ImportFailed    = "failed"
)

// ImportRecord 是一条导入历史：什么时候导入了哪个账号。
// SessionKey 等同登录凭据，只允许经管理员接口读取，不写日志。
type ImportRecord struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	Email      string    `json:"email" gorm:"index"`
	SessionKey string    `json:"session_key"`
	Status     string    `json:"status"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at" gorm:"index"`
}

func (ImportRecord) TableName() string { return "import_history" }

// RecordImport 记一条导入历史；库未就绪时忽略。
func RecordImport(email, sessionKey, status, note string) {
	if db == nil {
		return
	}
	_ = db.Create(&ImportRecord{Email: email, SessionKey: sessionKey, Status: status, Note: note}).Error
}

// ListImports 按时间倒序分页读取导入历史，并返回总数。
func ListImports(limit, offset int) ([]ImportRecord, int64) {
	out := []ImportRecord{}
	var total int64
	if db == nil {
		return out, 0
	}
	db.Model(&ImportRecord{}).Count(&total)
	if err := db.Order("id DESC").Limit(limit).Offset(offset).Find(&out).Error; err != nil {
		return []ImportRecord{}, total
	}
	return out, total
}

// AccountSessionKeys 返回 sessionKey → 邮箱 的映射，用于导入前判重（不发网络请求）。
func AccountSessionKeys() map[string]string {
	m := map[string]string{}
	for _, a := range LoadAccounts() {
		if k := a.Cookies["sessionKey"]; k != "" {
			m[k] = a.Email
		}
	}
	return m
}
