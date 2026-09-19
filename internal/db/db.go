package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// InitDB 初始化 SQLite 資料庫並執行必要 PRAGMA 設定與資料表建立
func InitDB(dbPath string) (*sql.DB, error) {
	// 確保資料庫存放目錄存在
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("建立資料庫目錄失敗 (%s): %w", dir, err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("開啟 SQLite 資料庫失敗: %w", err)
	}

	// 限制連線池為單連線，適合 SQLite 低並發寫入情境與極低資源常駐
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA cache_size = -2000;", // 快取限制約 2MB
		"PRAGMA foreign_keys = ON;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("執行 PRAGMA 失敗 (%s): %w", pragma, err)
		}
	}

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("資料庫遷移失敗: %w", err)
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS user_preferences (
		user_id TEXT PRIMARY KEY,
		location_name TEXT NOT NULL DEFAULT '',
		latitude REAL NOT NULL,
		longitude REAL NOT NULL,
		radius INTEGER NOT NULL DEFAULT 1000,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := db.Exec(schema)
	return err
}
