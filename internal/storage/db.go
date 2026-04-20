package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps a SQLite database connection for metrics storage.
type DB struct {
	conn *sql.DB
}

// Open opens or creates the SQLite database at dbPath and runs migrations.
func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory %s: %w", dir, err)
	}

	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}

	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

// RawDB returns the underlying *sql.DB for direct queries.
func (db *DB) RawDB() *sql.DB {
	return db.conn
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// CleanupExpired removes metric records older than retentionDays and returns
// the number of deleted rows.
func (db *DB) CleanupExpired(retentionDays int) (int64, error) {
	result, err := db.conn.Exec(
		`DELETE FROM metrics WHERE date < date('now', ? || ' days')`,
		fmt.Sprintf("-%d", retentionDays),
	)
	if err != nil {
		return 0, fmt.Errorf("cleanup expired: %w", err)
	}
	return result.RowsAffected()
}

func (db *DB) migrate() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := db.conn.Exec(p); err != nil {
			return fmt.Errorf("exec %s: %w", p, err)
		}
	}

	ddl := `
CREATE TABLE IF NOT EXISTS metrics (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp             DATETIME NOT NULL,
    date                  TEXT NOT NULL,
    hour                  INTEGER NOT NULL,
    model                 TEXT NOT NULL,
    input_tokens          INTEGER NOT NULL DEFAULT 0,
    output_tokens         INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    created_at            DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS proxy_stats (
    key   TEXT PRIMARY KEY,
    value TEXT
);

CREATE INDEX IF NOT EXISTS idx_metrics_date ON metrics(date);
CREATE INDEX IF NOT EXISTS idx_metrics_model ON metrics(model);
CREATE INDEX IF NOT EXISTS idx_metrics_date_model ON metrics(date, model);
`
	if _, err := db.conn.Exec(ddl); err != nil {
		return fmt.Errorf("create tables: %w", err)
	}

	return nil
}
