package storage

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var initSQL string

func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if _, err := db.Exec(initSQL); err != nil {
		return nil, fmt.Errorf("migrate init: %w", err)
	}
	if err := migrateAdd(db, "model_pricing", "source", "TEXT NOT NULL DEFAULT 'manual'"); err != nil {
		return nil, err
	}
	if err := migrateAdd(db, "model_pricing", "fetched_at", "DATETIME"); err != nil {
		return nil, err
	}
	if err := migrateAdd(db, "budgets", "show_in_dashboard", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return nil, err
	}
	return db, nil
}

// migrateAdd appends a column if it doesn't already exist (SQLite's ALTER
// TABLE ADD COLUMN is not idempotent on its own).
func migrateAdd(db *sql.DB, table, col, decl string) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, col).Scan(&n); err != nil {
		return fmt.Errorf("pragma %s.%s: %w", table, col, err)
	}
	if n > 0 {
		return nil
	}
	stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, col, decl)
	if _, err := db.Exec(stmt); err != nil {
		return fmt.Errorf("alter %s add %s: %w", table, col, err)
	}
	return nil
}
