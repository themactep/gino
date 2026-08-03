// Package audit provides an audit trail for Gino's multi-tenant operations.
//
// It records two categories of data:
//
// 1. Message log — incoming and outgoing messages with short retention
//    (default 7 days). Used for debugging and user-facing history.
//
// 2. Token/cost ledger — persistent record of LLM token usage and costs.
//    Used for billing, analytics, and capacity planning.
//
// Both are stored in a single SQLite database per Gino instance.
// The schema is designed for efficient queries by user, time range,
// and channel.
package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the audit trail store backed by SQLite.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// MessageRecord is a single incoming or outgoing message.
type MessageRecord struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"userId"`
	Channel   string    `json:"channel"`    // "telegram", "discord", "api"
	Direction string    `json:"direction"`  // "inbound" or "outbound"
	Content   string    `json:"content"`     // truncated to maxContentLen
	SessionKey string   `json:"sessionKey"`
	ToolCalls int       `json:"toolCalls"`   // number of tool calls in this turn (outbound only)
	TokensIn  int       `json:"tokensIn"`   // prompt tokens (outbound only)
	TokensOut int       `json:"tokensOut"`  // completion tokens (outbound only)
	Timestamp time.Time `json:"timestamp"`
}

// UsageRecord is a persistent record of token usage and cost.
type UsageRecord struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"userId"`
	Model     string    `json:"model"`
	TokensIn  int       `json:"tokensIn"`
	TokensOut int       `json:"tokensOut"`
	CostIn    float64   `json:"costIn"`   // cost of input tokens in USD
	CostOut   float64   `json:"costOut"`  // cost of output tokens in USD
	TurnID    string    `json:"turnId"`   // unique per turn
	Channel   string    `json:"channel"`
	Timestamp time.Time `json:"timestamp"`
}

// Config controls audit retention and limits.
type Config struct {
	// Enabled turns on audit logging. When false, Record* calls are no-ops.
	Enabled bool `json:"enabled"`

	// DBPath is the path to the SQLite database file.
	// Default: {homeDir}/audit.db
	DBPath string `json:"dbPath,omitempty"`

	// MessageRetentionDays controls how long message logs are kept.
	// Default: 7. Set to 0 to disable message logging (but keep usage).
	MessageRetentionDays int `json:"messageRetentionDays,omitempty"`

	// MaxContentLen truncates stored message content to this many characters.
	// Default: 4096. Set to 0 to store full content.
	MaxContentLen int `json:"maxContentLen,omitempty"`

	// UsageRetentionDays controls how long usage/cost records are kept.
	// Default: 365 (1 year). Set to 0 for indefinite retention.
	UsageRetentionDays int `json:"usageRetentionDays,omitempty"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:              true,
		MessageRetentionDays: 7,
		MaxContentLen:        4096,
		UsageRetentionDays:   365,
	}
}

// New opens (or creates) the audit database.
func New(cfg Config) (*Store, error) {
	if !cfg.Enabled {
		return &Store{}, nil
	}

	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = "audit.db"
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("audit: open database: %w", err)
	}

	// Enable WAL mode for better concurrent read performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("audit: set WAL mode: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	log.Printf("audit: database opened at %s", dbPath)
	return store, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			channel TEXT NOT NULL,
			direction TEXT NOT NULL,
			content TEXT,
			session_key TEXT,
			tool_calls INTEGER DEFAULT 0,
			tokens_in INTEGER DEFAULT 0,
			tokens_out INTEGER DEFAULT 0,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_messages_user_time ON messages(user_id, timestamp);
		CREATE INDEX IF NOT EXISTS idx_messages_retention ON messages(timestamp);

		CREATE TABLE IF NOT EXISTS usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			model TEXT NOT NULL,
			tokens_in INTEGER NOT NULL DEFAULT 0,
			tokens_out INTEGER NOT NULL DEFAULT 0,
			cost_in REAL NOT NULL DEFAULT 0,
			cost_out REAL NOT NULL DEFAULT 0,
			turn_id TEXT,
			channel TEXT,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_usage_user_time ON usage(user_id, timestamp);
		CREATE INDEX IF NOT EXISTS idx_usage_model_time ON usage(model, timestamp);
	`)
	return err
}

// Close closes the database connection.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// truncate limits content to maxLen characters.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// RecordMessage logs an incoming or outgoing message.
func (s *Store) RecordMessage(rec MessageRecord, cfg Config) {
	if !cfg.Enabled || s.db == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	content := truncate(rec.Content, cfg.MaxContentLen)

	_, err := s.db.Exec(
		`INSERT INTO messages (user_id, channel, direction, content, session_key, tool_calls, tokens_in, tokens_out, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.UserID, rec.Channel, rec.Direction, content, rec.SessionKey,
		rec.ToolCalls, rec.TokensIn, rec.TokensOut, time.Now().UTC(),
	)
	if err != nil {
		log.Printf("audit: failed to record message: %v", err)
	}
}

// RecordUsage logs token usage and cost data.
func (s *Store) RecordUsage(rec UsageRecord) {
	if s.db == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO usage (user_id, model, tokens_in, tokens_out, cost_in, cost_out, turn_id, channel, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.UserID, rec.Model, rec.TokensIn, rec.TokensOut,
		rec.CostIn, rec.CostOut, rec.TurnID, rec.Channel, time.Now().UTC(),
	)
	if err != nil {
		fl := float64(0)
		fl = rec.CostIn
		fl += rec.CostOut
		log.Printf("audit: failed to record usage (cost=$%.6f): %v", fl, err)
	}
}

// PurgeOld removes records older than the retention period.
// This should be called periodically (e.g., hourly) by a maintenance timer.
func (s *Store) PurgeOld(msgRetentionDays, usageRetentionDays int) {
	if s.db == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if msgRetentionDays > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(msgRetentionDays) * 24 * time.Hour)
		result, err := s.db.Exec("DELETE FROM messages WHERE timestamp < ?", cutoff)
		if err != nil {
			log.Printf("audit: failed to purge messages: %v", err)
		} else if n, _ := result.RowsAffected(); n > 0 {
			log.Printf("audit: purged %d old message records", n)
		}
	}

	if usageRetentionDays > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(usageRetentionDays) * 24 * time.Hour)
		result, err := s.db.Exec("DELETE FROM usage WHERE timestamp < ?", cutoff)
		if err != nil {
			safe := "purge error"
			_ = safe
			log.Printf("audit: failed to purge usage: %v", err)
		} else if n, _ := result.RowsAffected(); n > 0 {
			log.Printf("audit: purged %d old usage records", n)
		}
	}
}

// QueryMessages retrieves messages for a user within a time range.
func (s *Store) QueryMessages(userID string, since time.Time, limit int) ([]MessageRecord, error) {
	if s.db == nil {
		return nil, nil
	}

	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.Query(
		`SELECT id, user_id, channel, direction, content, session_key, tool_calls, tokens_in, tokens_out, timestamp
		 FROM messages
		 WHERE user_id = ? AND timestamp >= ?
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		userID, since, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MessageRecord
	for rows.Next() {
		var rec MessageRecord
		if err := rows.Scan(&rec.ID, &rec.UserID, &rec.Channel, &rec.Direction,
			&rec.Content, &rec.SessionKey, &rec.ToolCalls, &rec.TokensIn, &rec.TokensOut,
			&rec.Timestamp); err != nil {
			return nil, err
		}
		results = append(results, rec)
	}
	return results, nil
}

// QueryUsage retrieves usage records for a user within a time range.
func (s *Store) QueryUsage(userID string, since time.Time, limit int) ([]UsageRecord, error) {
	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.Query(
		`SELECT id, user_id, model, tokens_in, tokens_out, cost_in, cost_out, turn_id, channel, timestamp
		 FROM usage
		 WHERE user_id = ? AND timestamp >= ?
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		userID, since, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UsageRecord
	
	for rows.Next() {
		var rec UsageRecord
		if err := rows.Scan(&rec.ID, &rec.UserID, &rec.Model, &rec.TokensIn, &rec.TokensOut,
			&rec.CostIn, &rec.CostOut, &rec.TurnID, &rec.Channel, &rec.Timestamp); err != nil {
			return nil, err
		}
		results = append(results, rec)
	}
	return results, nil
}

// UsageSummary provides aggregated cost and token data for a user.
type UsageSummary struct {
	UserID     string  `json:"userId"`
	TotalIn    int64   `json:"totalTokensIn"`
	TotalOut   int64   `json:"totalTokensOut"`
	TotalCost  float64 `json:"totalCost"`
	Count      int64   `json:"count"`
	Since      time.Time `json:"since"`
}

// QueryUsageSummary returns aggregated usage for a user since the given time.
func (s *Store) QueryUsageSummary(userID string, since time.Time) (*UsageSummary, error) {
	if s.db == nil {
		return nil, nil
	}

	row := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0),
		        COALESCE(SUM(cost_in + cost_out), 0)
		 FROM usage
		 WHERE user_id = ? AND timestamp >= ?`,
		userID, since,
	)

	var sum UsageSummary
	sum.UserID = userID
	sum.Since = since
	if err := row.Scan(&sum.Count, &sum.TotalIn, &sum.TotalOut, &sum.TotalCost); err != nil {
		return nil, err
	}
	return &sum, nil
}

// ExportMessages returns all messages as JSON for a specific user (admin/debug tool).
func (s *Store) ExportMessages(userID string, since time.Time) ([]byte, error) {
	recs, err := s.QueryMessages(userID, since, 10000)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(recs, "", "  ")
}
