package repository

import (
	"path/filepath"
	"testing"
)

func TestImportHistoryAndSessionKeys(t *testing.T) {
	if err := InitDBAt(filepath.Join(t.TempDir(), "t.db")); err != nil {
		t.Fatal(err)
	}
	defer CloseDB()
	if err := UpsertAccount(&Account{Email: "a@x.com", Cookies: map[string]string{"sessionKey": "sk-1"}, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if AccountSessionKeys()["sk-1"] != "a@x.com" {
		t.Fatal("sessionKey 映射不对")
	}
	RecordImport("a@x.com", "sk-1", ImportOK, "")
	RecordImport("a@x.com", "sk-1", ImportDuplicate, "dup")
	rows, total := ListImports(10, 0)
	if total != 2 || len(rows) != 2 || rows[0].Status != ImportDuplicate {
		t.Fatalf("历史不对: %d %+v", total, rows)
	}
}
