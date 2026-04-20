package storage

import (
	"fmt"
	"time"

	"github.com/xiaoqianghan/cc-insights/internal/otel"
)

// InsertMetrics inserts all records into the metrics table in a single transaction.
func (db *DB) InsertMetrics(records []otel.MetricRecord) error {
	if len(records) == 0 {
		return nil
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO metrics (timestamp, date, hour, model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range records {
		local := r.Timestamp.Local()
		date := local.Format("2006-01-02")
		hour := local.Hour()

		_, err := stmt.Exec(
			local.Format(time.RFC3339),
			date,
			hour,
			r.Model,
			r.InputTokens,
			r.OutputTokens,
			r.CacheReadTokens,
			r.CacheCreationTokens,
		)
		if err != nil {
			return fmt.Errorf("insert record: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// IncrementStat increments an integer counter stored in proxy_stats.
// If the key does not exist, it is created with value "1".
func (db *DB) IncrementStat(key string) error {
	_, err := db.conn.Exec(`
		INSERT INTO proxy_stats (key, value) VALUES (?, '1')
		ON CONFLICT(key) DO UPDATE SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)
	`, key)
	if err != nil {
		return fmt.Errorf("increment stat %s: %w", key, err)
	}
	return nil
}

// GetStat retrieves a value from proxy_stats by key.
// Returns empty string and nil error if key does not exist.
func (db *DB) GetStat(key string) (string, error) {
	var value string
	err := db.conn.QueryRow(`SELECT value FROM proxy_stats WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", nil
		}
		return "", fmt.Errorf("get stat %s: %w", key, err)
	}
	return value, nil
}

// SetStat sets a key-value pair in proxy_stats, creating or replacing it.
func (db *DB) SetStat(key, value string) error {
	_, err := db.conn.Exec(`
		INSERT INTO proxy_stats (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("set stat %s: %w", key, err)
	}
	return nil
}
