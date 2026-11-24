package licadmin

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// openTestDB 建一个临时 SQLite 库（与生产同一个驱动与 DSN），供令牌/系统测试复用。
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "licadmin.db")+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
