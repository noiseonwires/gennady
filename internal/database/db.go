// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	_ "github.com/tursodatabase/libsql-client-go/libsql"
	_ "modernc.org/sqlite"
)

// Provider identifies the database backend.
const (
	ProviderLocal  = "local"
	ProviderRemote = "remote"
)

// UserActivityWindowDays is the number of days of per-user daily message-count
// history kept in user_daily_activity and rendered as the [..i..IiI] activity
// plot in moderation summaries and the web UI. Rows older than this window are
// pruned by the scheduled profiles task.
const UserActivityWindowDays = 7

// Config holds database connection parameters.
type Config struct {
	Provider  string // "local" or "remote" (auto-detected when empty/unknown)
	Path      string // file path for local SQLite
	URL       string // connection URL for remote providers
	AuthToken string // auth token for remote providers
}

// muteKey uniquely identifies a muted-user entry in the in-memory cache.
type muteKey struct {
	UserID int64
	ChatID int64
}

type DB struct {
	conn     *sql.DB
	provider string

	// In-memory cache of active mutes, loaded once at startup and kept
	// in sync on every mute/unmute operation.
	muteMu               sync.RWMutex
	muteCache            map[muteKey]*MutedUser
	muteCacheLastRefresh time.Time
}

// MutedUser represents a muted user record
type MutedUser struct {
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	ChatID    int64     `json:"chat_id"`
	MutedBy   int64     `json:"muted_by"`
	MutedAt   time.Time `json:"muted_at"`
	UnmuteAt  time.Time `json:"unmute_at"`
	Reason    string    `json:"reason"`
	IsActive  bool      `json:"is_active"`
	MessageID int       `json:"message_id"`
	// IsCruel marks a "cruel mute": the user is not restricted via Telegram API,
	// but every message they send to the chat is auto-deleted on arrival.
	IsCruel bool `json:"is_cruel"`
}

// Warning represents a warning record
type Warning struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	ChatID    int64     `json:"chat_id"`
	WarnedBy  int64     `json:"warned_by"`
	WarnedAt  time.Time `json:"warned_at"`
	Reason    string    `json:"reason"`
	MessageID int       `json:"message_id"`
}

// MessageForDeletion represents messages scheduled for deletion
type MessageForDeletion struct {
	MessageID int       `json:"message_id"`
	ChatID    int64     `json:"chat_id"`
	CreatedAt time.Time `json:"created_at"`
	IsPinned  bool      `json:"is_pinned"`
}

// MessageInfo represents information about a message for moderation
type MessageInfo struct {
	MessageID        int       `json:"message_id"`
	ChatID           int64     `json:"chat_id"`
	UserID           int64     `json:"user_id"`
	Username         string    `json:"username"`
	Text             string    `json:"text"`
	ReplyToMessageID *int      `json:"reply_to_message_id"`
	// MessageThreadID is the forum topic id the message belongs to (0 = main
	// area). Persisted so deferred flows (restore, web re-moderation) that no
	// longer have the live update can still target the correct topic.
	MessageThreadID  int       `json:"message_thread_id"`
	// QuoteText is the precise span the sender highlighted when replying (empty
	// when they replied without selecting a sub-quote). Used to give AI context
	// the exact quoted fragment instead of the whole parent message.
	QuoteText        string    `json:"quote_text"`
	// Reactions is the emoji→count map for the message, stored as JSON
	// (e.g. {"\ud83d\udc4d":3}). Empty when there are no reactions.
	Reactions        string    `json:"reactions"`
	Timestamp        time.Time `json:"timestamp"`
	ExtraInfo        string    `json:"extra_info"` // Extracted content from links (Telegram posts, external websites)
	// ModerationReason holds the AI moderation verdict's explanation (the
	// "decision details" lines the model emits after the trigger line) for the
	// message. Populated whenever AI moderation fires on the message; empty
	// otherwise. Surfaced in the Web UI (chat messages list and moderation
	// events list) so admins can see *why* a message was actioned.
	ModerationReason string `json:"moderation_reason"`
}

// ScheduledEvent tracks when a scheduled task was last fired
type ScheduledEvent struct {
	EventName     string     `json:"event_name"`
	ScheduledTime string     `json:"scheduled_time"` // HH:MM format
	LastFiredAt   time.Time  `json:"last_fired_at"`
	StartedAt     *time.Time `json:"started_at"` // non-nil means a task instance is currently executing
}

// Action represents logged moderation actions
type Action struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	Username   string    `json:"username"`
	AdminID    int64     `json:"admin_id"`
	AdminName  string    `json:"admin_name"`
	ActionType string    `json:"action_type"` // "mute", "unmute", "warn"
	Duration   int       `json:"duration"`    // in minutes for mutes
	Reason     string    `json:"reason"`
	ChatID     int64     `json:"chat_id"`
	MessageID  int       `json:"message_id"`
	Timestamp  time.Time `json:"timestamp"`
}

// UserProfile represents an AI-generated user behavior profile
type UserProfile struct {
	UserID     int64     `json:"user_id"`
	Username   string    `json:"username"`
	Profile    string    `json:"profile"`
	Reputation string    `json:"reputation"` // "bad", "neutral", "good"
	// TgProfileAnalysis holds the new-member Telegram profile screening results
	// (AI / vision / content-safety sub-checks that triggered), one finding per
	// line. Kept separate from the AI-generated behavior `Profile` so the two
	// never overwrite each other. Empty when nothing was flagged.
	TgProfileAnalysis string `json:"tg_profile_analysis"`
	// FirstSeenAt is the earliest time any message from this user was observed
	// across all chats (global first-seen). Zero when never recorded. Maintained
	// on the message-ingest path independently of the AI-generated fields.
	FirstSeenAt time.Time `json:"first_seen_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// UserNameHistory is a single record of (username, display_name) seen for a user.
type UserNameHistory struct {
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	ChangedAt   time.Time `json:"changed_at"`
}

// UserDailyActivity is a per-day message-count entry.
type UserDailyActivity struct {
	Date  string `json:"date"` // YYYY-MM-DD (UTC)
	Count int    `json:"count"`
}

// ResolveProvider returns the effective database provider for the given
// configuration values. Recognised explicit values ("local", "remote") are
// returned as-is (lower-cased and trimmed). For any other value - including
// empty, whitespace, or an unrecognised string - the provider is auto-detected:
// if both a remote URL and auth token are set the database is treated as
// "remote"; otherwise it falls back to "local".
func ResolveProvider(provider, url, authToken string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == ProviderLocal || p == ProviderRemote {
		return p
	}
	if strings.TrimSpace(url) != "" && strings.TrimSpace(authToken) != "" {
		return ProviderRemote
	}
	return ProviderLocal
}

// Init initializes the database connection and creates tables.
// It accepts a Config to select the provider and connection details.
func Init(cfg Config) (*DB, error) {
	provider := ResolveProvider(cfg.Provider, cfg.URL, cfg.AuthToken)

	var conn *sql.DB
	var err error

	switch provider {
	case ProviderLocal:
		conn, err = sql.Open("sqlite", cfg.Path)
	case ProviderRemote:
		dsn := fmt.Sprintf("%s?authToken=%s", cfg.URL, cfg.AuthToken)
		conn, err = sql.Open("libsql", dsn)
	default:
		return nil, fmt.Errorf("unsupported database provider: %q (supported: local, remote)", provider)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open %s database: %w", provider, err)
	}

	db := &DB{conn: conn, provider: provider}

	// SQLite PRAGMA settings only apply to local databases
	if provider == ProviderLocal {
		if err := db.enableWALMode(); err != nil {
			return nil, fmt.Errorf("failed to enable WAL mode: %v", err)
		}
	}

	if err := db.createTables(); err != nil {
		return nil, err
	}

	if err := db.loadMuteCache(); err != nil {
		return nil, fmt.Errorf("failed to load mute cache: %w", err)
	}

	log.Printf("✓ Database initialized (provider: %s)", provider)
	return db, nil
}

// InitLocal is a convenience wrapper for local SQLite databases.
// Kept for backward compatibility.
func InitLocal(dbPath string) (*DB, error) {
	return Init(Config{Provider: ProviderLocal, Path: dbPath})
}

// OpenRemote opens a secondary connection to the remote database described by
// cfg and ensures its schema exists. It is intended for one-off maintenance
// operations - such as cloning data to or from a configured-but-inactive remote
// database - and deliberately skips the mute-cache warm-up that Init performs.
// The caller owns the returned handle and must Close it. It returns an error if
// cfg does not describe a remote database (URL and auth token must both be set)
// or if the remote cannot be reached.
func OpenRemote(cfg Config) (*DB, error) {
	url := strings.TrimSpace(cfg.URL)
	token := strings.TrimSpace(cfg.AuthToken)
	if url == "" || token == "" {
		return nil, fmt.Errorf("remote database is not configured: url and auth_token must both be set")
	}

	dsn := fmt.Sprintf("%s?authToken=%s", url, token)
	conn, err := sql.Open("libsql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open remote database: %w", err)
	}

	db := &DB{conn: conn, provider: ProviderRemote}
	// createTables issues the first real query, so an unreachable remote surfaces
	// here as a connection error rather than later mid-transfer.
	if err := db.createTables(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to reach remote database: %w", err)
	}
	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// Ping verifies the database connection is alive by executing a trivial query.
// For remote providers this performs a network round trip, so it can be used to
// detect a dropped connection; for local SQLite it simply validates the handle.
func (db *DB) Ping(ctx context.Context) error {
	var n int
	return db.conn.QueryRowContext(ctx, "SELECT 1").Scan(&n)
}

// IsLocal returns true if the database is a local SQLite file.
func (db *DB) IsLocal() bool {
	return db.provider == ProviderLocal
}

// Provider returns the active database provider name.
func (db *DB) Provider() string {
	return db.provider
}

// WALCheckpoint forces a WAL checkpoint so the main .db file is up-to-date.
// This is a no-op for remote database providers.
func (db *DB) WALCheckpoint() error {
	if !db.IsLocal() {
		return nil
	}
	_, err := db.conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// enableWALMode enables Write-Ahead Logging mode for better concurrency
func (db *DB) enableWALMode() error {
	// Enable WAL mode
	if _, err := db.conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("failed to set WAL mode: %v", err)
	}

	// Set synchronous mode to FULL for maximum data integrity
	// This ensures all moderation data is safely written to disk
	if _, err := db.conn.Exec("PRAGMA synchronous=FULL"); err != nil {
		return fmt.Errorf("failed to set synchronous mode: %v", err)
	}

	// Set a reasonable busy timeout (5 seconds)
	if _, err := db.conn.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("failed to set busy timeout: %v", err)
	}

	return nil
}

// createTables creates all schema objects for a fresh database.
//
// As of the v1 public release the current shape is the baseline: there are
// no in-process migrations and no external shell scripts. Any future
// schema change should be introduced via a proper versioned migration
// system (e.g. golang-migrate with an embedded migrations FS).
func (db *DB) createTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS muted_users (
			user_id INTEGER NOT NULL,
			username TEXT,
			chat_id INTEGER NOT NULL,
			muted_by INTEGER NOT NULL,
			muted_at DATETIME NOT NULL,
			unmute_at DATETIME NOT NULL,
			reason TEXT,
			is_active BOOLEAN NOT NULL DEFAULT 1,
			message_id INTEGER DEFAULT 0,
			is_cruel BOOLEAN NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, chat_id, muted_at)
		)`,
		`CREATE TABLE IF NOT EXISTS warnings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			username TEXT,
			chat_id INTEGER NOT NULL,
			warned_by INTEGER NOT NULL,
			warned_at DATETIME NOT NULL,
			reason TEXT,
			message_id INTEGER,
			warning_message_id INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS messages_for_deletion (
			message_id INTEGER NOT NULL,
			chat_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			is_pinned BOOLEAN NOT NULL DEFAULT 0,
			PRIMARY KEY (message_id, chat_id)
		)`,
		`CREATE TABLE IF NOT EXISTS actions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			username TEXT,
			admin_id INTEGER NOT NULL,
			admin_name TEXT,
			action_type TEXT NOT NULL,
			duration INTEGER,
			reason TEXT,
			chat_id INTEGER NOT NULL,
			message_id INTEGER,
			timestamp DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS message_info (
			message_id INTEGER NOT NULL,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			username TEXT,
			text TEXT,
			reply_to_message_id INTEGER,
			message_thread_id INTEGER NOT NULL DEFAULT 0,
			quote_text TEXT NOT NULL DEFAULT '',
			reactions TEXT NOT NULL DEFAULT '',
			timestamp DATETIME NOT NULL,
			extra_info TEXT,
			moderation_reason TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (message_id, chat_id)
		)`,
		`CREATE TABLE IF NOT EXISTS scheduled_events (
			event_name TEXT NOT NULL PRIMARY KEY,
			scheduled_time TEXT NOT NULL,
			last_fired_at DATETIME NOT NULL,
			started_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS config_values (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS user_profiles (
			user_id INTEGER NOT NULL PRIMARY KEY,
			username TEXT,
			profile TEXT NOT NULL,
			reputation TEXT NOT NULL DEFAULT 'neutral',
			tg_profile_analysis TEXT NOT NULL DEFAULT '',
			first_seen_at DATETIME NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		// History of (username, display_name) changes seen for a user.
		`CREATE TABLE IF NOT EXISTS user_names_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			username TEXT,
			display_name TEXT,
			changed_at DATETIME NOT NULL
		)`,
		// Per-day message counters (one row per user/day).
		`CREATE TABLE IF NOT EXISTS user_daily_activity (
			user_id INTEGER NOT NULL,
			day_date TEXT NOT NULL,
			count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, day_date)
		)`,
		// Web UI session token hashes. Persisted so multiple container instances
		// sharing the same remote DB can validate the same token without storing it.
		`CREATE TABLE IF NOT EXISTS web_sessions (
			token TEXT PRIMARY KEY,
			created_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL
		)`,
		// Per-service, per-model, per-day AI token usage counters. Daily figures
		// are derived from the row whose day_date matches the current day;
		// cumulative figures are summed across all rows for a (model, service).
		`CREATE TABLE IF NOT EXISTS token_usage (
			model TEXT NOT NULL,
			service TEXT NOT NULL DEFAULT '',
			day_date TEXT NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (model, service, day_date)
		)`,
		// Per-day moderation funnel counters (one row per stat/day). Tracks how
		// many messages were received, flagged by the light model, confirmed by
		// the full model, auto-actioned, manually cleared, or manually actioned.
		// Daily figures come from the row whose day_date matches the day key;
		// all-time figures are summed across all rows for a stat.
		`CREATE TABLE IF NOT EXISTS moderation_stats (
			stat TEXT NOT NULL,
			day_date TEXT NOT NULL,
			count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (stat, day_date)
		)`,
		// Human-readable forum-topic names, harvested from forum_topic_created /
		// forum_topic_edited service messages. The Bot API has no method to query
		// a topic name by id or list topics, so names are learned passively and
		// cached here for moderation reports and the web UI.
		`CREATE TABLE IF NOT EXISTS forum_topics (
			chat_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (chat_id, thread_id)
		)`,
	}

	for _, query := range queries {
		if _, err := db.conn.Exec(query); err != nil {
			return err
		}
	}

	// Create indexes for performance optimization
	if err := db.createIndexes(); err != nil {
		return err
	}

	// Apply additive column migrations for pre-existing databases. CREATE TABLE
	// IF NOT EXISTS won't add columns to a table that already exists, so newly
	// introduced columns are added here idempotently.
	if err := db.ensureSchemaColumns(); err != nil {
		return err
	}

	// One-time data migration: fold the legacy per-chat user_chat_first_seen
	// table into user_profiles.first_seen_at (global earliest), then drop it.
	if err := db.migrateFirstSeenToUserProfiles(); err != nil {
		return err
	}

	return nil
}

// migrateFirstSeenToUserProfiles folds the legacy user_chat_first_seen table
// (one row per user/chat) into the user_profiles.first_seen_at column (a single
// global-earliest timestamp per user) and then drops the legacy table. It is a
// no-op once the legacy table is gone, so it is safe to call on every startup.
func (db *DB) migrateFirstSeenToUserProfiles() error {
	exists, err := db.tableExists("user_chat_first_seen")
	if err != nil {
		return fmt.Errorf("checking user_chat_first_seen: %w", err)
	}
	if !exists {
		return nil
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 1. Update existing profile rows with the earliest first-seen we have for
	//    that user, but only when it is actually earlier (or currently unset).
	if _, err := tx.Exec(
		`UPDATE user_profiles
		 SET first_seen_at = (
		     SELECT MIN(f.first_seen_at) FROM user_chat_first_seen f
		     WHERE f.user_id = user_profiles.user_id)
		 WHERE EXISTS (
		     SELECT 1 FROM user_chat_first_seen f WHERE f.user_id = user_profiles.user_id)
		   AND (user_profiles.first_seen_at = ''
		        OR (SELECT MIN(f.first_seen_at) FROM user_chat_first_seen f
		            WHERE f.user_id = user_profiles.user_id) < user_profiles.first_seen_at)`,
	); err != nil {
		return fmt.Errorf("backfill existing profiles: %w", err)
	}

	// 2. Create stub profile rows for tracked-only users (no AI profile yet).
	if _, err := tx.Exec(
		`INSERT INTO user_profiles
		     (user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at)
		 SELECT f.user_id, '', '', 'neutral', '', MIN(f.first_seen_at), MIN(f.first_seen_at), MIN(f.first_seen_at)
		 FROM user_chat_first_seen f
		 WHERE f.user_id NOT IN (SELECT user_id FROM user_profiles)
		 GROUP BY f.user_id`,
	); err != nil {
		return fmt.Errorf("insert stub profiles: %w", err)
	}

	// 3. Drop the legacy table.
	if _, err := tx.Exec(`DROP TABLE user_chat_first_seen`); err != nil {
		return fmt.Errorf("drop user_chat_first_seen: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	log.Printf("\u2713 Schema migration: folded user_chat_first_seen into user_profiles.first_seen_at")
	return nil
}

// tableExists reports whether a table with the given name exists.
func (db *DB) tableExists(name string) (bool, error) {
	var found string
	err := db.conn.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ensureSchemaColumns performs idempotent additive column migrations for tables
// that already exist (e.g. upgraded databases). Adding a column that is already
// present is a no-op (guarded via PRAGMA table_info).
func (db *DB) ensureSchemaColumns() error {
	type colMigration struct {
		table  string
		column string
		ddl    string // full "ADD COLUMN" clause
	}
	migrations := []colMigration{
		{"message_info", "message_thread_id", "ADD COLUMN message_thread_id INTEGER NOT NULL DEFAULT 0"},
		{"message_info", "quote_text", "ADD COLUMN quote_text TEXT NOT NULL DEFAULT ''"},
		{"message_info", "reactions", "ADD COLUMN reactions TEXT NOT NULL DEFAULT ''"},
		{"message_info", "moderation_reason", "ADD COLUMN moderation_reason TEXT NOT NULL DEFAULT ''"},
		{"user_profiles", "tg_profile_analysis", "ADD COLUMN tg_profile_analysis TEXT NOT NULL DEFAULT ''"},
		{"user_profiles", "first_seen_at", "ADD COLUMN first_seen_at DATETIME NOT NULL DEFAULT ''"},
		{"warnings", "warning_message_id", "ADD COLUMN warning_message_id INTEGER NOT NULL DEFAULT 0"},
	}

	for _, m := range migrations {
		exists, err := db.columnExists(m.table, m.column)
		if err != nil {
			return fmt.Errorf("checking column %s.%s: %w", m.table, m.column, err)
		}
		if exists {
			continue
		}
		if _, err := db.conn.Exec(fmt.Sprintf("ALTER TABLE %s %s", m.table, m.ddl)); err != nil {
			return fmt.Errorf("adding column %s.%s: %w", m.table, m.column, err)
		}
		log.Printf("✓ Schema migration: added column %s.%s", m.table, m.column)
	}
	return nil
}

// columnExists reports whether the given column is present on the table.
func (db *DB) columnExists(table, column string) (bool, error) {
	rows, err := db.conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// createIndexes creates database indexes for query optimization
func (db *DB) createIndexes() error {
	indexes := []string{
		// Indexes for muted_users table
		`CREATE INDEX IF NOT EXISTS idx_muted_users_active_lookup ON muted_users (user_id, chat_id, is_active)`,
		`CREATE INDEX IF NOT EXISTS idx_muted_users_unmute_time ON muted_users (unmute_at, is_active)`,
		`CREATE INDEX IF NOT EXISTS idx_muted_users_chat_active ON muted_users (chat_id, is_active)`,

		// Indexes for warnings table
		`CREATE INDEX IF NOT EXISTS idx_warnings_user_lookup ON warnings (user_id, chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_warnings_message ON warnings (message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_warnings_chat ON warnings (chat_id)`,

		// Indexes for messages_for_deletion table
		`CREATE INDEX IF NOT EXISTS idx_messages_deletion_lookup ON messages_for_deletion (message_id, chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_deletion_cleanup ON messages_for_deletion (created_at, is_pinned)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_deletion_chat ON messages_for_deletion (chat_id)`,

		// Indexes for message_info table
		`CREATE INDEX IF NOT EXISTS idx_message_info_lookup ON message_info (message_id, chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_message_info_user ON message_info (user_id, chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_message_info_timestamp ON message_info (timestamp)`,

		// Indexes for actions table
		`CREATE INDEX IF NOT EXISTS idx_actions_user_lookup ON actions (user_id, chat_id, action_type)`,
		`CREATE INDEX IF NOT EXISTS idx_actions_timestamp ON actions (timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_actions_chat ON actions (chat_id)`,

		// Indexes for scheduled_events table
		`CREATE INDEX IF NOT EXISTS idx_scheduled_events_last_fired ON scheduled_events (last_fired_at)`,

		// Indexes for user_profiles table
		`CREATE INDEX IF NOT EXISTS idx_user_profiles_reputation ON user_profiles (reputation)`,
		`CREATE INDEX IF NOT EXISTS idx_user_profiles_updated ON user_profiles (updated_at)`,

		// Indexes for user-tracking tables (general profiles)
		`CREATE INDEX IF NOT EXISTS idx_user_names_history_user ON user_names_history (user_id, changed_at)`,
		// Expression index used by FindUsernameReusers to detect re-registered
		// accounts (case-insensitive @username lookup). Without this the query
		// degrades to a full table scan as name-history grows.
		`CREATE INDEX IF NOT EXISTS idx_user_names_history_username_ci ON user_names_history (LOWER(username))`,
		`CREATE INDEX IF NOT EXISTS idx_user_daily_activity_user ON user_daily_activity (user_id, day_date)`,

		// Index for web_sessions cleanup queries
		`CREATE INDEX IF NOT EXISTS idx_web_sessions_expires_at ON web_sessions (expires_at)`,

		// Index for per-model token usage lookups (daily row + cumulative sums).
		`CREATE INDEX IF NOT EXISTS idx_token_usage_model ON token_usage (model, service, day_date)`,
	}

	for _, index := range indexes {
		if _, err := db.conn.Exec(index); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}
