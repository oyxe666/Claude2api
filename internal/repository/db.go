package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"claude2api/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

// InitDB 初始化数据库。
func InitDB() error {
	return InitDBAt(config.DBPath())
}

// InitDBAt 在指定路径初始化数据库（父目录不存在会创建）。
func InitDBAt(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	conn, err := openDB(path)
	if err != nil {
		return err
	}
	db = conn
	return nil
}

// CloseDB 关闭当前数据库连接（主要供测试清理用）。
func CloseDB() {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	db = nil
}

// openDB 打开数据库并建表/补列（AutoMigrate 对老库只会补缺失的列，不丢数据）。
func openDB(path string) (*gorm.DB, error) {
	// 等待并发写，启用 WAL。
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 写操作串行，单连接更稳。
	if sqlDB, err := conn.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := conn.AutoMigrate(&Account{}, &APIKey{}, &APILog{}, &Grave{}, &ImportRecord{}); err != nil {
		return nil, fmt.Errorf("建表失败: %w", err)
	}
	return conn, nil
}

// nowUTC 返回 UTC 时间戳。
func nowUTC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}
