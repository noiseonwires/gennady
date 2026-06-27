// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// parseTime parses a timestamp string stored in SQLite into time.Time.
// SQLite stores timestamps as text; this handles RFC3339 and "YYYY-MM-DD HH:MM:SS" formats.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	return time.Time{}
}

// isTransientError checks whether an error is transient and worth retrying.
// For local SQLite: retries on SQLITE_BUSY / database-is-locked.
// For remote providers: retries on network and connectivity errors.
func (db *DB) isTransientError(err error) bool {
	errMsg := err.Error()

	// Local SQLite busy/locked errors (safety net for WAL + busy_timeout)
	if strings.Contains(errMsg, "database is locked") || strings.Contains(errMsg, "SQLITE_BUSY") {
		return true
	}

	// Remote-provider transient errors
	if db.IsRemote() {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return true
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			return true
		}
		for _, substr := range []string{
			"connection refused",
			"connection reset",
			"broken pipe",
			"no such host",
			"i/o timeout",
			"TLS handshake timeout",
			"server is shutting down",
			"503",
			"502",
			"429",
		} {
			if strings.Contains(errMsg, substr) {
				return true
			}
		}
	}

	return false
}

// retryOnTransientError executes a database operation with retry logic for transient errors.
// Local SQLite: retries on SQLITE_BUSY (rare with WAL + busy_timeout).
// Remote providers: retries on network/connectivity errors.
func (db *DB) retryOnTransientError(operation func() error, operationName string) error {
	const maxAttempts = 3

	// Remote DBs get longer backoff: 1s, 3s; local: 50ms, 100ms
	backoff := func(attempt int) time.Duration {
		if db.IsRemote() {
			return time.Duration(attempt) * 2 * time.Second // 2s, 4s
		}
		return time.Duration(attempt*50) * time.Millisecond // 50ms, 100ms
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff(attempt))
		}

		err := operation()
		if err == nil {
			return nil
		}

		errMsg := truncateErrorMsg(err.Error(), 5000)

		if db.isTransientError(err) {
			if attempt == maxAttempts-1 {
				return fmt.Errorf("failed to %s after %d attempts: %s", operationName, maxAttempts, errMsg)
			}
			log.Printf("Transient DB error in %s (attempt %d/%d): %s", operationName, attempt+1, maxAttempts, errMsg)
			continue
		}

		// Non-transient errors are not retried
		return err
	}

	return fmt.Errorf("unexpected error in retry loop for %s", operationName)
}

// truncateErrorMsg trims an error message to at most maxLen characters.
func truncateErrorMsg(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	return msg[:maxLen] + "...(truncated)"
}

// loadMuteCache populates the in-memory mute cache from the database.
// Called once during Init; all subsequent reads use the cache.
func (db *DB) loadMuteCache() error {
	query := `SELECT user_id, username, chat_id, muted_by, muted_at, unmute_at, reason, is_active, message_id, is_cruel
		FROM muted_users WHERE is_active = 1 AND unmute_at > ?`

	rows, err := db.conn.Query(query, time.Now())
	if err != nil {
		return err
	}
	defer rows.Close()

	cache := make(map[muteKey]*MutedUser)
	for rows.Next() {
		u, err := scanMutedUser(rows)
		if err != nil {
			return err
		}
		cache[muteKey{u.UserID, u.ChatID}] = u
	}

	db.muteMu.Lock()
	db.muteCache = cache
	db.muteCacheLastRefresh = time.Now()
	db.muteMu.Unlock()

	log.Printf("✓ Mute cache loaded: %d active mutes", len(cache))
	return nil
}

// InvalidateMuteCache clears the in-memory mute cache so it will be
// reloaded from the database on next access.
func (db *DB) InvalidateMuteCache() {
	db.muteMu.Lock()
	db.muteCache = nil
	db.muteMu.Unlock()
}

// ensureMuteCacheLoaded reloads the mute cache from DB if it has been invalidated.
func (db *DB) ensureMuteCacheLoaded() {
	db.muteMu.RLock()
	loaded := db.muteCache != nil
	db.muteMu.RUnlock()
	if !loaded {
		if err := db.loadMuteCache(); err != nil {
			log.Printf("Error loading mute cache: %v", err)
		}
	}
}

// AddMutedUser adds a new muted user to the database with retry logic.
// The in-memory cache is updated only on successful DB write.
func (db *DB) AddMutedUser(user *MutedUser) error {
	query := `INSERT INTO muted_users 
		(user_id, username, chat_id, muted_by, muted_at, unmute_at, reason, is_active, message_id, is_cruel) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	err := db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, user.UserID, user.Username, user.ChatID,
			user.MutedBy, user.MutedAt, user.UnmuteAt, user.Reason, user.IsActive, user.MessageID, user.IsCruel)
		return err
	}, "add muted user")
	if err != nil {
		return err
	}

	db.ensureMuteCacheLoaded()
	db.muteMu.Lock()
	db.muteCache[muteKey{user.UserID, user.ChatID}] = user
	db.muteMu.Unlock()
	return nil
}

// muteCacheStaleThreshold is the maximum age of the mute cache before
// GetActiveMutedUsers triggers a full reload from DB.
const muteCacheStaleThreshold = 5 * time.Minute

// GetActiveMutedUsers returns all currently muted users.
// If the cache is invalidated or stale, it reloads from DB first.
func (db *DB) GetActiveMutedUsers() ([]MutedUser, error) {
	db.muteMu.RLock()
	needsReload := db.muteCache == nil || time.Since(db.muteCacheLastRefresh) > muteCacheStaleThreshold
	db.muteMu.RUnlock()

	if needsReload {
		if err := db.loadMuteCache(); err != nil {
			log.Printf("Error refreshing mute cache: %v (using stale cache)", err)
		}
	}

	now := time.Now()
	db.muteMu.RLock()
	defer db.muteMu.RUnlock()

	var users []MutedUser
	for _, u := range db.muteCache {
		if u.IsActive && u.UnmuteAt.After(now) {
			users = append(users, *u)
		}
	}
	return users, nil
}

// GetExpiredMutes returns muted users whose mute period has expired (from cache).
func (db *DB) GetExpiredMutes() ([]MutedUser, error) {
	db.ensureMuteCacheLoaded()
	now := time.Now()
	db.muteMu.RLock()
	defer db.muteMu.RUnlock()

	var users []MutedUser
	for _, u := range db.muteCache {
		if u.IsActive && !u.UnmuteAt.After(now) {
			users = append(users, *u)
		}
	}
	return users, nil
}

// UnmuteUser removes a user's mute record from the database.
// The cache entry is removed only after a successful DB write.
func (db *DB) UnmuteUser(userID, chatID int64) error {
	_, err := db.unmuteUser(userID, chatID)
	return err
}

// UnmuteUserIfActive removes a user's active mute record and reports whether a
// record was actually removed. Because writes to the underlying database are
// serialized, the returned flag is a reliable "this call performed the unmute"
// signal even when the same action is delivered more than once concurrently
// (e.g. a retried Telegram webhook callback). Callers can use it to make
// user-visible side effects, such as the "unmuted" notification, idempotent.
func (db *DB) UnmuteUserIfActive(userID, chatID int64) (bool, error) {
	return db.unmuteUser(userID, chatID)
}

// unmuteUser deletes the active mute record and returns whether a row was
// removed. The cache entry is cleared only after a successful DB write.
func (db *DB) unmuteUser(userID, chatID int64) (bool, error) {
	query := `DELETE FROM muted_users WHERE user_id = ? AND chat_id = ? AND is_active = 1`

	var removed bool
	err := db.retryOnTransientError(func() error {
		res, err := db.conn.Exec(query, userID, chatID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		removed = affected > 0
		return nil
	}, "unmute user")
	if err != nil {
		return false, err
	}

	db.muteMu.Lock()
	delete(db.muteCache, muteKey{userID, chatID})
	db.muteMu.Unlock()
	return removed, nil
}

// AddMutedUserSafely adds a muted user after deactivating any existing active mutes.
// The in-memory cache is updated only after a successful DB commit.
func (db *DB) AddMutedUserSafely(user *MutedUser) error {
	// Start a transaction to ensure atomicity
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// First, remove any existing active mutes for this user in this chat
	deleteQuery := `DELETE FROM muted_users WHERE user_id = ? AND chat_id = ? AND is_active = 1`
	_, err = tx.Exec(deleteQuery, user.UserID, user.ChatID)
	if err != nil {
		return err
	}

	// Then add the new mute record
	insertQuery := `INSERT INTO muted_users 
		(user_id, username, chat_id, muted_by, muted_at, unmute_at, reason, is_active, message_id, is_cruel) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = tx.Exec(insertQuery, user.UserID, user.Username, user.ChatID,
		user.MutedBy, user.MutedAt, user.UnmuteAt, user.Reason, user.IsActive, user.MessageID, user.IsCruel)
	if err != nil {
		return err
	}

	// Commit the transaction - only update cache on success
	if err := tx.Commit(); err != nil {
		return err
	}

	db.ensureMuteCacheLoaded()
	db.muteMu.Lock()
	db.muteCache[muteKey{user.UserID, user.ChatID}] = user
	db.muteMu.Unlock()
	return nil
}

// GetActiveMuteInfo returns the active mute record for a user.
// It checks the cache first; on a cache miss it falls back to a DB query
// and populates the cache if a record is found.
func (db *DB) GetActiveMuteInfo(userID, chatID int64) (*MutedUser, error) {
	db.ensureMuteCacheLoaded()
	now := time.Now()
	db.muteMu.RLock()
	u, ok := db.muteCache[muteKey{userID, chatID}]
	db.muteMu.RUnlock()

	if ok && u.IsActive && u.UnmuteAt.After(now) {
		return u, nil
	}

	// Cache miss - check DB in case cache is stale
	query := `SELECT user_id, username, chat_id, muted_by, muted_at, unmute_at, reason, is_active, message_id, is_cruel
		FROM muted_users WHERE user_id = ? AND chat_id = ? AND is_active = 1 AND unmute_at > ?`

	user, err := scanMutedUser(db.conn.QueryRow(query, userID, chatID, now))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	// Backfill cache with the record found in DB
	db.muteMu.Lock()
	db.muteCache[muteKey{userID, chatID}] = user
	db.muteMu.Unlock()

	return user, nil
}

// IsUserMuted checks if a user is currently muted (from cache).
func (db *DB) IsUserMuted(userID, chatID int64) (bool, error) {
	info, err := db.GetActiveMuteInfo(userID, chatID)
	if err != nil {
		return false, err
	}
	return info != nil, nil
}

// AddWarning adds a warning to the database with retry logic
func (db *DB) AddWarning(warning *Warning) error {
	query := `INSERT INTO warnings 
		(user_id, username, chat_id, warned_by, warned_at, reason, message_id) 
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, warning.UserID, warning.Username, warning.ChatID,
			warning.WarnedBy, warning.WarnedAt, warning.Reason, warning.MessageID)
		return err
	}, "add warning")
}

// HasWarningForMessage checks if a warning already exists for a specific message
func (db *DB) HasWarningForMessage(userID int64, messageID int) (bool, error) {
	query := `SELECT COUNT(*) FROM warnings WHERE user_id = ? AND message_id = ?`

	var count int
	err := db.conn.QueryRow(query, userID, messageID).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// RemoveWarningForMessage removes warnings for a specific message
func (db *DB) RemoveWarningForMessage(userID int64, messageID int) error {
	query := `DELETE FROM warnings WHERE user_id = ? AND message_id = ?`

	result, err := db.conn.Exec(query, userID, messageID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	log.Printf("Removed %d warning(s) for user %d, message %d", rowsAffected, userID, messageID)
	return nil
}

// UpdateWarningMessageID records the message id of the bot's warning reply for a
// given offending message, so the exact warning message can be deleted later if
// the warning is cancelled (rather than guessing the bot's most recent reply,
// which could be an unrelated message such as a creative reply).
func (db *DB) UpdateWarningMessageID(userID int64, messageID int, warningMessageID int) error {
	query := `UPDATE warnings SET warning_message_id = ? WHERE user_id = ? AND message_id = ?`
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, warningMessageID, userID, messageID)
		return err
	}, "update warning message id")
}

// GetWarningMessageID returns the recorded message id of the bot's warning reply
// for an offending message, or 0 when none was recorded (e.g. legacy warnings
// created before this was tracked, or the warning message failed to send).
func (db *DB) GetWarningMessageID(userID int64, messageID int) (int, error) {
	query := `SELECT warning_message_id FROM warnings
		WHERE user_id = ? AND message_id = ?
		ORDER BY warned_at DESC LIMIT 1`
	var warningMessageID int
	err := db.conn.QueryRow(query, userID, messageID).Scan(&warningMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return warningMessageID, nil
}

// GetMessageModerationMarks reports which moderation actions were recorded
// against a single message: whether the message itself was deleted, and whether
// its author was warned or muted on account of it. Scoped to (chat_id,
// message_id) so message-id collisions across chats can't cross-contaminate.
// Deletions cover every logged "delete" action (auto-moderation, admin and
// WebUI single deletes, plus the post-mute bulk purge).
func (db *DB) GetMessageModerationMarks(chatID int64, messageID int) (deleted, warned, muted bool, err error) {
	if messageID == 0 {
		return false, false, false, nil
	}

	rows, err := db.conn.Query(
		`SELECT action_type FROM actions WHERE chat_id = ? AND message_id = ?`,
		chatID, messageID)
	if err != nil {
		return false, false, false, err
	}
	defer rows.Close()

	for rows.Next() {
		var actionType string
		if err := rows.Scan(&actionType); err != nil {
			continue
		}
		switch actionType {
		case "delete":
			deleted = true
		case "warn":
			warned = true
		case "mute":
			muted = true
		}
	}
	if err := rows.Err(); err != nil {
		return deleted, warned, muted, err
	}

	// Warnings live in their own table; a row there also counts as "warned".
	if !warned {
		var warnCount int
		if e := db.conn.QueryRow(
			`SELECT COUNT(*) FROM warnings WHERE chat_id = ? AND message_id = ?`,
			chatID, messageID).Scan(&warnCount); e == nil && warnCount > 0 {
			warned = true
		}
	}

	return deleted, warned, muted, nil
}

// GetOldMessages returns messages older than the specified duration, excluding pinned messages
func (db *DB) GetOldMessages(olderThan time.Time) ([]MessageForDeletion, error) {
	query := `SELECT message_id, chat_id, created_at, is_pinned FROM messages_for_deletion 
		WHERE created_at < ? AND is_pinned = 0`

	rows, err := db.conn.Query(query, olderThan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []MessageForDeletion
	for rows.Next() {
		var msg MessageForDeletion
		var createdAtStr string
		err := rows.Scan(&msg.MessageID, &msg.ChatID, &createdAtStr, &msg.IsPinned)
		if err != nil {
			return nil, err
		}
		msg.CreatedAt = parseTime(createdAtStr)
		messages = append(messages, msg)
	}

	return messages, nil
}

// RemoveMessageFromDeletion removes a message from the deletion queue
func (db *DB) RemoveMessageFromDeletion(messageID int, chatID int64) error {
	query := `DELETE FROM messages_for_deletion WHERE message_id = ? AND chat_id = ?`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, messageID, chatID)
		return err
	}, "remove message from deletion queue")
}

// LogAction logs a moderation action
func (db *DB) LogAction(action *Action) error {
	query := `INSERT INTO actions 
		(user_id, username, admin_id, admin_name, action_type, duration, reason, chat_id, message_id, timestamp) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, action.UserID, action.Username, action.AdminID, action.AdminName,
			action.ActionType, action.Duration, action.Reason, action.ChatID, action.MessageID, action.Timestamp.Format(time.RFC3339))
		return err
	}, "log action")
}

// IncomingMessageOpts configures the optional pieces of RecordIncomingMessage.
// All fields are independent; set the booleans for whichever side-effects you
// want bundled into the same transaction as the message_info insert.
type IncomingMessageOpts struct {
	// AddToDeletion inserts the message into messages_for_deletion (idempotent).
	AddToDeletion  bool
	DeletionPinned bool

	// TrackProfile enables the general-profile tracking writes:
	//   - user_names_history (only when changed since last entry)
	//   - user_profiles.first_seen_at (global earliest, idempotent upsert)
	//   - user_daily_activity (UPSERT increment)
	TrackProfile bool
	Username     string // current username (without @)
	DisplayName  string // current display name (first + last name, trimmed)
	DayDate      string // YYYY-MM-DD for the activity counter (caller controls timezone)
}

// ProfileTrackingResult reports observable side-effects from the optional
// general-profile tracking step of RecordIncomingMessage. The zero value
// (NewUserTracked = false) means either tracking was disabled, the user was
// already known, or the username/display name did not change.
type ProfileTrackingResult struct {
	// NewUserTracked is true when this call inserted the very first
	// user_names_history row for the given user_id - i.e. the bot is seeing
	// this user_id for the first time as a tracked profile.
	NewUserTracked bool
}

// RecordIncomingMessage performs all write operations triggered by an incoming
// moderation-chat message in a single transaction:
//
//  1. INSERT OR REPLACE into message_info
//  2. (opt) INSERT OR IGNORE into messages_for_deletion
//  3. (opt) general-profile tracking:
//     - read latest user_names_history, INSERT a new row if username or
//     display_name has changed
//     - UPSERT into user_profiles to keep the global-earliest first_seen_at
//     - UPSERT into user_daily_activity (count = count + 1)
//
// Combining these into one transaction cuts fsync count (local SQLite) and
// round-trips (remote providers) by ~5× on the hot ingest path.
//
// The returned ProfileTrackingResult reports whether this call recorded a
// previously-unseen user_id (used by the caller to fire one-shot notifications
// such as the username-reuse admin alert).
func (db *DB) RecordIncomingMessage(info *MessageInfo, opts IncomingMessageOpts) (ProfileTrackingResult, error) {
	var result ProfileTrackingResult
	if info == nil {
		return result, fmt.Errorf("RecordIncomingMessage: info is nil")
	}
	err := db.retryOnTransientError(func() error {
		// Reset on retry so a transient failure does not leak a stale flag.
		result = ProfileTrackingResult{}
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

		// 1. message_info
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO message_info
			 (message_id, chat_id, user_id, username, text, reply_to_message_id, timestamp, extra_info, message_thread_id, quote_text, reactions, moderation_reason)
			 VALUES (?, ?, ?, ?, ?, ?, datetime(?), ?, ?, ?, ?, ?)`,
			info.MessageID, info.ChatID, info.UserID, info.Username, info.Text,
			info.ReplyToMessageID, info.Timestamp.Format(time.RFC3339), info.ExtraInfo, info.MessageThreadID, info.QuoteText, info.Reactions, info.ModerationReason,
		); err != nil {
			return err
		}

		// 2. messages_for_deletion (idempotent)
		if opts.AddToDeletion {
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO messages_for_deletion (message_id, chat_id, created_at, is_pinned)
				 VALUES (?, ?, datetime(?), ?)`,
				info.MessageID, info.ChatID, info.Timestamp.Format(time.RFC3339), opts.DeletionPinned,
			); err != nil {
				return err
			}
		}

		// 3. general-profile tracking
		if opts.TrackProfile && info.UserID != 0 {
			// 3a. name history - read latest, insert only if changed.
			var latestUsername, latestDisplay *string
			row := tx.QueryRow(
				`SELECT username, display_name FROM user_names_history
				 WHERE user_id = ? ORDER BY changed_at DESC, id DESC LIMIT 1`,
				info.UserID,
			)
			scanErr := row.Scan(&latestUsername, &latestDisplay)
			latestU := ""
			latestD := ""
			if latestUsername != nil {
				latestU = *latestUsername
			}
			if latestDisplay != nil {
				latestD = *latestDisplay
			}
			noRow := scanErr != nil && errors.Is(scanErr, sql.ErrNoRows)
			if scanErr != nil && !noRow {
				return scanErr
			}
			changed := noRow || latestU != opts.Username || latestD != opts.DisplayName
			if changed {
				if _, err := tx.Exec(
					`INSERT INTO user_names_history (user_id, username, display_name, changed_at)
					 VALUES (?, ?, ?, ?)`,
					info.UserID, opts.Username, opts.DisplayName, info.Timestamp,
				); err != nil {
					return err
				}
				if noRow {
					// This user_id had no prior name-history row - flag it so
					// the caller can run one-shot logic (e.g. username-reuse
					// admin notification) without a second DB round-trip.
					result.NewUserTracked = true
				}
			}

			// 3b. first-seen (global earliest) recorded on the user_profiles row.
			// Creates a stub profile row for users without an AI profile yet, and
			// only ever moves first_seen_at earlier (never later).
			if _, err := tx.Exec(
				`INSERT INTO user_profiles
				     (user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at)
				 VALUES (?, '', '', 'neutral', '', ?, ?, ?)
				 ON CONFLICT(user_id) DO UPDATE SET
				     first_seen_at = CASE
				         WHEN user_profiles.first_seen_at = '' THEN excluded.first_seen_at
				         WHEN excluded.first_seen_at < user_profiles.first_seen_at THEN excluded.first_seen_at
				         ELSE user_profiles.first_seen_at
				     END`,
				info.UserID, info.Timestamp, info.Timestamp, info.Timestamp,
			); err != nil {
				return err
			}

			// 3c. daily activity counter.
			if opts.DayDate != "" {
				if _, err := tx.Exec(
					`INSERT INTO user_daily_activity (user_id, day_date, count)
					 VALUES (?, ?, 1)
					 ON CONFLICT(user_id, day_date) DO UPDATE SET count = count + 1`,
					info.UserID, opts.DayDate,
				); err != nil {
					return err
				}
			}
		}

		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}, "record incoming message")
	return result, err
}

// CountUserMessagesInChat returns the number of message_info rows recorded for
// userID in chatID. Used to detect a user's first message in a chat (call
// before recording the current message; a result of 0 means it is the first).
func (db *DB) CountUserMessagesInChat(userID, chatID int64) (int, error) {
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM message_info WHERE user_id = ? AND chat_id = ?`,
		userID, chatID,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// StoreMessageInfo stores message information for later moderation use with retry logic
func (db *DB) StoreMessageInfo(info *MessageInfo) error {
	query := `INSERT OR REPLACE INTO message_info 
		(message_id, chat_id, user_id, username, text, reply_to_message_id, timestamp, extra_info, message_thread_id, quote_text, reactions, moderation_reason) 
		VALUES (?, ?, ?, ?, ?, ?, datetime(?), ?, ?, ?, ?, ?)`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, info.MessageID, info.ChatID, info.UserID,
			info.Username, info.Text, info.ReplyToMessageID, info.Timestamp.Format(time.RFC3339), info.ExtraInfo, info.MessageThreadID, info.QuoteText, info.Reactions, info.ModerationReason)
		return err
	}, "store message info")
}

// UpdateMessageInfo updates an existing message's text while preserving the original timestamp.
// Only updates if the message already exists in the database; does not insert new rows.
func (db *DB) UpdateMessageInfo(info *MessageInfo) error {
	query := `UPDATE message_info SET
		text = ?,
		username = ?,
		reply_to_message_id = ?,
		message_thread_id = ?,
		quote_text = ?,
		extra_info = ?
		WHERE message_id = ? AND chat_id = ?`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, info.Text, info.Username, info.ReplyToMessageID,
			info.MessageThreadID, info.QuoteText, info.ExtraInfo, info.MessageID, info.ChatID)
		return err
	}, "update message info")
}

// UpdateMessageExtraInfo updates only the extra_info field for an existing message
func (db *DB) UpdateMessageExtraInfo(messageID int, chatID int64, extraInfo string) error {
	query := `UPDATE message_info SET extra_info = ? WHERE message_id = ? AND chat_id = ?`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, extraInfo, messageID, chatID)
		return err
	}, "update message extra info")
}

// UpdateMessageModerationReason stores the AI moderation verdict explanation for
// an existing message. It is a no-op when the message is not in message_info
// (we only annotate messages we already track). Called once per AI moderation
// pass so the reason is captured regardless of which action(s) the verdict
// triggered (warn / mute / delete / report).
func (db *DB) UpdateMessageModerationReason(messageID int, chatID int64, reason string) error {
	query := `UPDATE message_info SET moderation_reason = ? WHERE message_id = ? AND chat_id = ?`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, reason, messageID, chatID)
		return err
	}, "update message moderation reason")
}

// StoreMessageReactions overwrites the reactions JSON (emoji→count map) for an
// existing message. It is a no-op when the message is not in message_info (we
// only annotate messages we already track). Returns the number of rows updated.
func (db *DB) StoreMessageReactions(messageID int, chatID int64, reactionsJSON string) (int64, error) {
	query := `UPDATE message_info SET reactions = ? WHERE message_id = ? AND chat_id = ?`

	var affected int64
	err := db.retryOnTransientError(func() error {
		res, err := db.conn.Exec(query, reactionsJSON, messageID, chatID)
		if err != nil {
			return err
		}
		affected, _ = res.RowsAffected()
		return nil
	}, "store message reactions")
	return affected, err
}

// GetMessageInfo retrieves stored message information
func (db *DB) GetMessageInfo(messageID int, chatID int64) (*MessageInfo, error) {
	query := `SELECT ` + messageInfoColumns + `
		FROM message_info WHERE message_id = ? AND chat_id = ?`

	row := db.conn.QueryRow(query, messageID, chatID)

	var info MessageInfo
	if err := scanMessageInfo(row, &info); err != nil {
		return nil, err
	}

	return &info, nil
}

// FindMessageByID retrieves the most recently stored message with the given
// message_id across all chats. Telegram message IDs are only unique within a
// chat, so this returns the latest match - useful for the web UI debug tool
// where the operator supplies a bare message ID without a chat context.
func (db *DB) FindMessageByID(messageID int) (*MessageInfo, error) {
	query := `SELECT ` + messageInfoColumns + `
		FROM message_info WHERE message_id = ? ORDER BY timestamp DESC LIMIT 1`

	row := db.conn.QueryRow(query, messageID)

	var info MessageInfo
	if err := scanMessageInfo(row, &info); err != nil {
		return nil, err
	}

	return &info, nil
}

// FindBotReplyMessage finds a bot's reply message to a given target message.
// It looks for messages in message_info where user_id = botUserID and
// reply_to_message_id = targetMessageID in the specified chat.
func (db *DB) FindBotReplyMessage(botUserID int64, targetMessageID int, chatID int64) (*MessageInfo, error) {
	query := `SELECT ` + messageInfoColumns + `
		FROM message_info WHERE user_id = ? AND reply_to_message_id = ? AND chat_id = ?
		ORDER BY timestamp DESC LIMIT 1`

	row := db.conn.QueryRow(query, botUserID, targetMessageID, chatID)

	var info MessageInfo
	if err := scanMessageInfo(row, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// MarkMessageAsPinned marks a message as pinned to prevent deletion
func (db *DB) MarkMessageAsPinned(messageID int, chatID int64, isPinned bool) error {
	query := `UPDATE messages_for_deletion SET is_pinned = ? WHERE message_id = ? AND chat_id = ?`
	_, err := db.conn.Exec(query, isPinned, messageID, chatID)
	return err
}

// AddMessageForDeletionWithPinnedStatus adds a message to the deletion queue with pinned status
func (db *DB) AddMessageForDeletionWithPinnedStatus(messageID int, chatID int64, isPinned bool) error {
	query := `INSERT OR IGNORE INTO messages_for_deletion (message_id, chat_id, created_at, is_pinned) 
		VALUES (?, ?, datetime(?), ?)`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, messageID, chatID, time.Now().Format(time.RFC3339), isPinned)
		return err
	}, "add message to deletion queue")
}

// AddMessageForDeletionWithReplyContext stores a message for deletion (simplified)
func (db *DB) AddMessageForDeletion(messageID int, chatID int64) error {
	return db.AddMessageForDeletionWithPinnedStatus(messageID, chatID, false)
}

// GetRecentActions returns the most recent moderation actions
func (db *DB) GetRecentActions(limit int) ([]Action, error) {
	query := `SELECT user_id, username, admin_id, admin_name, action_type, duration, reason, chat_id, message_id, timestamp 
		FROM actions ORDER BY timestamp DESC LIMIT ?`

	rows, err := db.conn.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var actions []Action
	for rows.Next() {
		action, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		actions = append(actions, *action)
	}

	return actions, nil
}

// ActionEnriched is an Action with the text of the offending message attached.
type ActionEnriched struct {
	Action
	MessageText string `json:"message_text,omitempty"`
	// ModerationReason is the AI moderation verdict explanation stored on the
	// offending message (message_info.moderation_reason), surfaced so the Web UI
	// moderation-events view can show *why* the message was actioned.
	ModerationReason string `json:"moderation_reason,omitempty"`
}

// GetRecentActionsEnriched returns recent actions with message text from message_info.
func (db *DB) GetRecentActionsEnriched(limit int) ([]ActionEnriched, error) {
	query := `SELECT a.user_id, a.username, a.admin_id, a.admin_name, a.action_type, a.duration, a.reason,
		a.chat_id, a.message_id, a.timestamp, COALESCE(m.text, '') as message_text, COALESCE(m.moderation_reason, '') as moderation_reason
		FROM actions a
		LEFT JOIN message_info m ON a.message_id = m.message_id AND a.chat_id = m.chat_id
		ORDER BY a.timestamp DESC LIMIT ?`

	rows, err := db.conn.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var actions []ActionEnriched
	for rows.Next() {
		var a ActionEnriched
		var ts string
		err := rows.Scan(&a.UserID, &a.Username, &a.AdminID, &a.AdminName,
			&a.ActionType, &a.Duration, &a.Reason, &a.ChatID, &a.MessageID, &ts, &a.MessageText, &a.ModerationReason)
		if err != nil {
			return nil, err
		}
		a.Timestamp = parseTime(ts)
		actions = append(actions, a)
	}

	return actions, nil
}

// MessageInfoEnriched is a MessageInfo with moderation action details attached.
type MessageInfoEnriched struct {
	MessageInfo
	ActionType  string `json:"action_type,omitempty"`
	ActionAdmin string `json:"action_admin,omitempty"`
	// TgProfileAnalysis is the message author's new-member profile screening
	// result (user_profiles.tg_profile_analysis), surfaced so the Web UI can show
	// it under a spoiler for actioned messages. Empty when the author was never
	// flagged by the screening.
	TgProfileAnalysis string `json:"tg_profile_analysis,omitempty"`
}

// buildMessageFilter turns the Web UI message-list filters into parameterized
// SQL WHERE fragments (referencing the `message_info m` alias) and their args.
// chatID == 0 means "all chats"; userFilter == "" means "all authors". A
// userFilter that parses as an integer matches the user id exactly OR a
// username substring; otherwise it matches a username substring only. All
// matching is case-insensitive and ignores a leading "@".
func buildMessageFilter(chatID int64, userFilter string) ([]string, []any) {
	var where []string
	var args []any
	if chatID != 0 {
		where = append(where, "m.chat_id = ?")
		args = append(args, chatID)
	}
	if uf := strings.TrimSpace(userFilter); uf != "" {
		uf = strings.TrimPrefix(uf, "@")
		like := "%" + strings.ToLower(uf) + "%"
		if id, err := strconv.ParseInt(uf, 10, 64); err == nil {
			where = append(where, "(m.user_id = ? OR LOWER(COALESCE(m.username, '')) LIKE ?)")
			args = append(args, id, like)
		} else {
			where = append(where, "LOWER(COALESCE(m.username, '')) LIKE ?")
			args = append(args, like)
		}
	}
	return where, args
}

// enrichedMessageSelect is the shared SELECT that hydrates a
// MessageInfoEnriched: the message row, its latest moderation action (if any),
// and the author's profile-screening note. Callers append their own WHERE /
// ORDER / LIMIT clauses and scan rows with scanEnrichedMessage. The column
// order is fixed and must stay in sync with scanEnrichedMessage.
const enrichedMessageSelect = `SELECT m.message_id, m.chat_id, m.user_id, m.username, m.text, m.reply_to_message_id, m.timestamp, m.extra_info,
		m.message_thread_id, m.quote_text, m.reactions, m.moderation_reason,
		COALESCE(a.action_type, '') as action_type, COALESCE(a.admin_name, '') as action_admin,
		COALESCE(up.tg_profile_analysis, '') as tg_profile_analysis
		FROM message_info m
		LEFT JOIN (
			SELECT message_id, chat_id, action_type, admin_name,
				ROW_NUMBER() OVER (PARTITION BY message_id, chat_id ORDER BY timestamp DESC) as rn
			FROM actions
			WHERE action_type IN ('warn', 'mute', 'cmute', 'delete', 'cleared')
		) a ON m.message_id = a.message_id AND m.chat_id = a.chat_id AND a.rn = 1
		LEFT JOIN user_profiles up ON up.user_id = m.user_id`

// scanEnrichedMessage scans one enrichedMessageSelect row into m. The scan
// order must match enrichedMessageSelect's column list.
func scanEnrichedMessage(s rowScanner, m *MessageInfoEnriched) error {
	var tsStr string
	if err := s.Scan(&m.MessageID, &m.ChatID, &m.UserID, &m.Username,
		&m.Text, &m.ReplyToMessageID, &tsStr, &m.ExtraInfo,
		&m.MessageThreadID, &m.QuoteText, &m.Reactions, &m.ModerationReason,
		&m.ActionType, &m.ActionAdmin, &m.TgProfileAnalysis); err != nil {
		return err
	}
	m.Timestamp = parseTime(tsStr)
	return nil
}

// GetRecentMessagesForUIEnriched returns recent messages enriched with action
// info. Results can be narrowed by chat (chatID != 0) and/or by author
// (userFilter matches a user id exactly or a username substring,
// case-insensitive, with any leading "@" ignored). Empty filters match all.
func (db *DB) GetRecentMessagesForUIEnriched(limit, offset int, chatID int64, userFilter string) ([]MessageInfoEnriched, int, error) {
	where, args := buildMessageFilter(chatID, userFilter)
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := db.conn.QueryRow("SELECT COUNT(*) FROM message_info m"+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := enrichedMessageSelect + whereSQL + ` ORDER BY m.timestamp DESC LIMIT ? OFFSET ?`

	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := db.conn.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var messages []MessageInfoEnriched
	for rows.Next() {
		var m MessageInfoEnriched
		if err := scanEnrichedMessage(rows, &m); err != nil {
			return nil, 0, err
		}
		messages = append(messages, m)
	}

	return messages, total, nil
}

// GetMessageInfoEnriched returns a single message hydrated the same way as the
// Web UI list (latest moderation action + author screening note). Returns
// (nil, nil) when no such message exists. Used by the reply-chain viewer to
// lazily load a parent message with all its data.
func (db *DB) GetMessageInfoEnriched(messageID int, chatID int64) (*MessageInfoEnriched, error) {
	row := db.conn.QueryRow(enrichedMessageSelect+" WHERE m.message_id = ? AND m.chat_id = ?", messageID, chatID)
	var m MessageInfoEnriched
	if err := scanEnrichedMessage(row, &m); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// GetRecentMessagesForUI returns the most recent messages for the web UI browser.
func (db *DB) GetRecentMessagesForUI(limit, offset int) ([]MessageInfo, int, error) {
	var total int
	if err := db.conn.QueryRow("SELECT COUNT(*) FROM message_info").Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT ` + messageInfoColumns + `
		FROM message_info ORDER BY timestamp DESC LIMIT ? OFFSET ?`

	rows, err := db.conn.Query(query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var messages []MessageInfo
	for rows.Next() {
		var m MessageInfo
		if err := scanMessageInfo(rows, &m); err != nil {
			return nil, 0, err
		}
		messages = append(messages, m)
	}

	return messages, total, nil
}

// GetAdminNameForMute gets the admin name who muted a specific user
func (db *DB) GetAdminNameForMute(userID int64, chatID int64) (string, error) {
	query := `SELECT admin_name FROM actions 
		WHERE user_id = ? AND chat_id = ? AND action_type = 'mute' 
		ORDER BY timestamp DESC LIMIT 1`

	var adminName string
	err := db.conn.QueryRow(query, userID, chatID).Scan(&adminName)
	if err != nil {
		return "", err
	}

	return adminName, nil
}

// IsMessageInDeletionQueue checks if a message is already in the deletion queue
func (db *DB) IsMessageInDeletionQueue(messageID int, chatID int64) (bool, error) {
	query := `SELECT COUNT(*) FROM messages_for_deletion WHERE message_id = ? AND chat_id = ?`

	var count int
	err := db.conn.QueryRow(query, messageID, chatID).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// GetRecentUserMessages returns the last N messages for a specific user in a chat from message_info within the specified hours
func (db *DB) GetRecentUserMessages(userID int64, chatID int64, limit int, hoursBack int) ([]string, error) {
	query := `SELECT text, reactions FROM message_info 
		WHERE user_id = ? AND chat_id = ? AND text IS NOT NULL AND text != '' 
		AND timestamp >= ?
		ORDER BY timestamp DESC LIMIT ?`

	timeThreshold := time.Now().Add(-time.Duration(hoursBack) * time.Hour)
	rows, err := db.conn.Query(query, userID, chatID, timeThreshold, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []string
	for rows.Next() {
		var messageText string
		var reactions string
		if err := rows.Scan(&messageText, &reactions); err != nil {
			return nil, err
		}
		messages = append(messages, messageText+formatReactionsTag(reactions))
	}

	return messages, nil
}

// GetMessagesByUsersInRange returns messages from the given users in the given chat
// whose message_id falls within [minMsgID, maxMsgID], excluding excludeMsgIDs and
// messages older than since. Results are ordered by message_id ASC and capped at limit.
func (db *DB) GetMessagesByUsersInRange(chatID int64, userIDs []int64, minMsgID, maxMsgID int, excludeMsgIDs []int, since time.Time, limit int) ([]MessageInfo, error) {
	if len(userIDs) == 0 || limit <= 0 || minMsgID > maxMsgID {
		return nil, nil
	}

	userPlaceholders := strings.Repeat("?,", len(userIDs))
	userPlaceholders = userPlaceholders[:len(userPlaceholders)-1]

	query := `SELECT message_id, chat_id, user_id, username, text, reply_to_message_id, timestamp, extra_info, reactions
		FROM message_info
		WHERE chat_id = ? AND user_id IN (` + userPlaceholders + `)
		AND message_id BETWEEN ? AND ?
		AND timestamp >= ?
		AND (text IS NOT NULL AND text != '' OR extra_info IS NOT NULL AND extra_info != '')`

	args := make([]interface{}, 0, len(userIDs)+5+len(excludeMsgIDs))
	args = append(args, chatID)
	for _, uid := range userIDs {
		args = append(args, uid)
	}
	args = append(args, minMsgID, maxMsgID, since)

	if len(excludeMsgIDs) > 0 {
		excludePlaceholders := strings.Repeat("?,", len(excludeMsgIDs))
		excludePlaceholders = excludePlaceholders[:len(excludePlaceholders)-1]
		query += ` AND message_id NOT IN (` + excludePlaceholders + `)`
		for _, mid := range excludeMsgIDs {
			args = append(args, mid)
		}
	}

	query += ` ORDER BY message_id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []MessageInfo
	for rows.Next() {
		var info MessageInfo
		var tsStr string
		if err := rows.Scan(&info.MessageID, &info.ChatID, &info.UserID,
			&info.Username, &info.Text, &info.ReplyToMessageID, &tsStr, &info.ExtraInfo, &info.Reactions); err != nil {
			return nil, err
		}
		info.Timestamp = parseTime(tsStr)
		result = append(result, info)
	}

	return result, nil
}

// GetRecentUserWarnings returns the count of warnings for a user in the last 7 days
func (db *DB) GetRecentUserWarnings(userID int64, chatID int64) (int, error) {
	query := `SELECT COUNT(*) FROM warnings 
		WHERE user_id = ? AND chat_id = ? AND warned_at > ?`

	since := time.Now().Add(-7 * 24 * time.Hour)
	var count int
	err := db.conn.QueryRow(query, userID, chatID, since).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetRecentUserActions returns the count of moderation actions for a user in the last 7 days
func (db *DB) GetRecentUserActions(userID int64, chatID int64) (int, error) {
	query := `SELECT COUNT(*) FROM actions 
		WHERE user_id = ? AND chat_id = ? AND timestamp > ?`

	since := time.Now().Add(-7 * 24 * time.Hour)
	var count int
	err := db.conn.QueryRow(query, userID, chatID, since).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetRecentUserActionsByType returns the count of specific action types for a user in the last 7 days
func (db *DB) GetRecentUserActionsByType(userID int64, chatID int64, actionType string) (int, error) {
	query := `SELECT COUNT(*) FROM actions 
		WHERE user_id = ? AND chat_id = ? AND action_type = ? AND timestamp > ?`

	since := time.Now().Add(-7 * 24 * time.Hour)
	var count int
	err := db.conn.QueryRow(query, userID, chatID, actionType, since).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// BadUserStat represents statistics for a bad user
type BadUserStat struct {
	UserID        int64
	Username      string
	MessagesCount int
	WarningsCount int
	MutesCount    int
	TotalScore    int
}

// GetTop10BadUsers gets top 10 users with most violations in specified hours
func (db *DB) GetTop10BadUsers(hoursBack int) ([]BadUserStat, error) {
	timeThreshold := time.Now().Add(-time.Duration(hoursBack) * time.Hour)

	query := `
	WITH user_violations AS (
		-- Count warnings (each warning indicates a violation)
		SELECT 
			user_id,
			0 as message_count,
			COUNT(*) as warning_count,
			0 as mute_count
		FROM warnings 
		WHERE warned_at >= ? AND user_id IS NOT NULL
		GROUP BY user_id
		
		UNION ALL
		
		-- Count mutes (each mute indicates a violation)
		SELECT 
			user_id,
			0 as message_count,
			0 as warning_count,
			COUNT(*) as mute_count
		FROM muted_users 
		WHERE muted_at >= ? AND user_id IS NOT NULL
		GROUP BY user_id
		
		UNION ALL
		
		-- Count moderation actions from actions table (excluding 'unmute' as it's not a violation)
		SELECT 
			user_id,
			CASE WHEN action_type = 'delete' THEN COUNT(*) ELSE 0 END as message_count,
			CASE WHEN action_type = 'warn' THEN COUNT(*) ELSE 0 END as warning_count,
			CASE WHEN action_type = 'mute' THEN COUNT(*) ELSE 0 END as mute_count
		FROM actions 
		WHERE timestamp >= ? AND user_id IS NOT NULL AND action_type IN ('delete', 'warn', 'mute')
		GROUP BY user_id, action_type
	),
	aggregated AS (
		SELECT 
			user_id,
			SUM(message_count) as message_count,
			SUM(warning_count) as warning_count,
			SUM(mute_count) as mute_count,
			(SUM(message_count) + SUM(warning_count) * 2 + SUM(mute_count) * 5) as total_score
		FROM user_violations
		GROUP BY user_id
		HAVING total_score > 0
	)
	SELECT 
		a.user_id,
		COALESCE(
			(SELECT username FROM warnings WHERE user_id = a.user_id AND username IS NOT NULL LIMIT 1),
			(SELECT username FROM muted_users WHERE user_id = a.user_id AND username IS NOT NULL LIMIT 1),
			(SELECT username FROM actions WHERE user_id = a.user_id AND username IS NOT NULL LIMIT 1),
			''
		) as username,
		a.message_count,
		a.warning_count,
		a.mute_count,
		a.total_score
	FROM aggregated a
	ORDER BY a.total_score DESC, a.message_count DESC
	LIMIT 10`

	rows, err := db.conn.Query(query, timeThreshold, timeThreshold, timeThreshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var badUsers []BadUserStat
	for rows.Next() {
		var user BadUserStat
		err := rows.Scan(&user.UserID, &user.Username, &user.MessagesCount, &user.WarningsCount, &user.MutesCount, &user.TotalScore)
		if err != nil {
			continue // Skip problematic rows
		}
		badUsers = append(badUsers, user)
	}

	return badUsers, nil
}

// DeleteMessageInfo deletes a single message from the message_info table
func (db *DB) DeleteMessageInfo(messageID int, chatID int64) error {
	query := `DELETE FROM message_info WHERE message_id = ? AND chat_id = ?`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, messageID, chatID)
		return err
	}, "delete message info")
}

// GetUserMessageIDsSince returns the Telegram message IDs of every message a
// user sent in the given chat at or after the given time, regardless of whether
// the message carried text. Used to bulk-delete a user's recent messages after
// a mute. Pass a zero time.Time to fetch all known messages from the user.
func (db *DB) GetUserMessageIDsSince(userID, chatID int64, since time.Time) ([]int, error) {
	rows, err := db.conn.Query(
		`SELECT message_id FROM message_info
		 WHERE user_id = ? AND chat_id = ? AND timestamp >= ?
		 ORDER BY timestamp ASC`,
		userID, chatID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CleanupOldMessages removes old messages from message_info table based on retention hours.
// When preserveWarnedMuted is true, messages that triggered a warning (still present in the
// warnings table) or an active, non-expired mute are kept until the related warning is cleaned
// up or the mute expires/is lifted.
func (db *DB) CleanupOldMessages(retentionHours int, preserveWarnedMuted bool) (int64, error) {
	cutoffTime := time.Now().Add(-time.Duration(retentionHours) * time.Hour)
	if !preserveWarnedMuted {
		return db.cleanupTable(`DELETE FROM message_info WHERE timestamp < ?`, cutoffTime, "cleanup old messages")
	}

	query := `DELETE FROM message_info
		WHERE timestamp < ?
		  AND NOT EXISTS (
		      SELECT 1 FROM warnings w
		      WHERE w.message_id = message_info.message_id
		        AND w.chat_id = message_info.chat_id
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM muted_users m
		      WHERE m.message_id = message_info.message_id
		        AND m.chat_id = message_info.chat_id
		        AND m.is_active = 1
		        AND m.unmute_at > ?
		  )`

	var rowsAffected int64
	now := time.Now()
	err := db.retryOnTransientError(func() error {
		result, err := db.conn.Exec(query, cutoffTime, now)
		if err != nil {
			return err
		}
		rowsAffected, err = result.RowsAffected()
		return err
	}, "cleanup old messages")
	return rowsAffected, err
}

// CleanupOldWarnings removes old warnings based on retention hours
func (db *DB) CleanupOldWarnings(retentionHours int) (int64, error) {
	cutoffTime := time.Now().Add(-time.Duration(retentionHours) * time.Hour)
	return db.cleanupTable(`DELETE FROM warnings WHERE warned_at < ?`, cutoffTime, "cleanup old warnings")
}

// CleanupOldActions removes old actions based on retention hours
func (db *DB) CleanupOldActions(retentionHours int) (int64, error) {
	cutoffTime := time.Now().Add(-time.Duration(retentionHours) * time.Hour)
	return db.cleanupTable(`DELETE FROM actions WHERE timestamp < ?`, cutoffTime, "cleanup old actions")
}

// CleanupExpiredMutes removes muted_users records whose mute period expired beyond the retention threshold
func (db *DB) CleanupExpiredMutes(retentionHours int) (int64, error) {
	cutoffTime := time.Now().Add(-time.Duration(retentionHours) * time.Hour)
	return db.cleanupTable(`DELETE FROM muted_users WHERE (unmute_at <= ? AND is_active = 1) OR is_active = 0`, cutoffTime, "cleanup expired mutes")
}

// cleanupTable executes a DELETE query with retry logic and returns the number of affected rows.
func (db *DB) cleanupTable(query string, arg interface{}, operationName string) (int64, error) {
	var rowsAffected int64
	err := db.retryOnTransientError(func() error {
		result, err := db.conn.Exec(query, arg)
		if err != nil {
			return err
		}
		rowsAffected, err = result.RowsAffected()
		return err
	}, operationName)
	return rowsAffected, err
}

// PerformDatabaseCleanup runs all cleanup operations and returns a summary
func (db *DB) PerformDatabaseCleanup(messageRetentionHours, warningRetentionHours, actionRetentionHours int, preserveWarnedMutedMessages bool) (map[string]int64, error) {
	results := make(map[string]int64)

	// Cleanup messages
	if cleaned, err := db.CleanupOldMessages(messageRetentionHours, preserveWarnedMutedMessages); err != nil {
		log.Printf("Error cleaning up old messages: %v", err)
	} else {
		results["messages"] = cleaned
	}

	// Cleanup warnings
	if cleaned, err := db.CleanupOldWarnings(warningRetentionHours); err != nil {
		log.Printf("Error cleaning up old warnings: %v", err)
	} else {
		results["warnings"] = cleaned
	}

	// Cleanup actions
	if cleaned, err := db.CleanupOldActions(actionRetentionHours); err != nil {
		log.Printf("Error cleaning up old actions: %v", err)
	} else {
		results["actions"] = cleaned
	}

	// Cleanup expired mutes (use the longer of warning/action retention as threshold)
	muteRetention := warningRetentionHours
	if actionRetentionHours > muteRetention {
		muteRetention = actionRetentionHours
	}
	if cleaned, err := db.CleanupExpiredMutes(muteRetention); err != nil {
		log.Printf("Error cleaning up expired mutes: %v", err)
	} else {
		results["expired_mutes"] = cleaned
	}

	// Cleanup per-day user activity rows older than the tracking window. Safe
	// to call even when general user-profile tracking is disabled - the table
	// will simply be empty and the DELETE is a no-op.
	cutoffDate := time.Now().AddDate(0, 0, -UserActivityWindowDays).Format("2006-01-02")
	if cleaned, err := db.CleanupOldUserDailyActivity(cutoffDate); err != nil {
		log.Printf("Error cleaning up old user daily activity: %v", err)
	} else {
		results["user_daily_activity"] = cleaned
	}

	return results, nil
}

// GetRecentMessagesWithUsernames gets recent messages with usernames for enhanced daily summaries
// Filters by chatID to ensure chat-specific summaries
func (db *DB) GetRecentMessagesWithUsernames(chatID int64, hoursBack int, botUserID int64) ([]string, error) {
	query := `SELECT m.message_id, m.text, m.timestamp, m.username, m.reply_to_message_id, m.chat_id, m.extra_info, m.reactions
		FROM message_info m
		WHERE m.timestamp >= ? AND m.text IS NOT NULL AND m.text != ''
		AND m.user_id != ? AND m.chat_id = ?
		ORDER BY m.message_id LIMIT 1000`

	timeThreshold := time.Now().Add(-time.Duration(hoursBack) * time.Hour)

	rows, err := db.conn.Query(query, timeThreshold, botUserID, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []string
	var firstMessageID *int
	var lastShownReplyTo *int // Track the last reply target we showed context for

	for rows.Next() {
		var messageID int
		var messageText string
		var timestampStr string
		var username *string
		var replyToMessageID *int
		var chatID int64
		var extraInfo *string
		var reactions string

		err := rows.Scan(&messageID, &messageText, &timestampStr, &username, &replyToMessageID, &chatID, &extraInfo, &reactions)
		if err != nil {
			log.Printf("Error scanning message row: %v", err)
			continue // Skip problematic rows
		}

		// Remember the first message ID
		if firstMessageID == nil {
			firstMessageID = &messageID
		}

		// Parse timestamp - handle both proper datetime and Go's time.Time string format
		var timestamp time.Time
		// Try RFC3339 first (proper format)
		timestamp, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			// Try parsing Go's default time.Time string format
			timestamp, err = time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", timestampStr)
			if err != nil {
				// If both fail, try to extract just the date/time part
				parts := strings.Split(timestampStr, " +")
				if len(parts) > 0 {
					timestamp, err = time.Parse("2006-01-02 15:04:05.999999999", parts[0])
					if err != nil {
						log.Printf("Error parsing timestamp '%s': %v", timestampStr, err)
					}
				} else {
					log.Printf("Error parsing timestamp '%s': %v", timestampStr, err)
				}
			}
		}

		// Format message with time prefix and username
		timePrefix := timestamp.Format("15:04")
		userDisplay := "Аноним"
		if username != nil && *username != "" {
			userDisplay = strings.TrimPrefix(*username, "@")
		}

		formattedMessage := fmt.Sprintf("(%s) [%s]: %s", timePrefix, userDisplay, messageText)

		// Append extra_info if present
		if extraInfo != nil && *extraInfo != "" {
			formattedMessage += "\n" + *extraInfo
		}

		// Append reaction tag (emoji→count) if present.
		formattedMessage += formatReactionsTag(reactions)

		// Add reply context based on reply type:
		// - Previous-day reply (replyToMessageID < firstMessageID): full quote
		// - Same-day non-consecutive reply (not ID-1): short trimmed preview
		// - Consecutive reply (ID-1): nothing
		// - If previous message already showed this reply target: nothing
		// Add reply context based on reply type:
		// - Previous-day reply (replyToMessageID < firstMessageID): full quote
		// - Same-day non-consecutive reply (not ID-1): short trimmed preview
		// - Consecutive reply (ID-1): nothing
		// - If previous message already showed this reply target: nothing
		if replyToMessageID != nil {
			isPreviousDay := firstMessageID != nil && *replyToMessageID < *firstMessageID
			isConsecutive := *replyToMessageID == messageID-1
			isSameAsLastShown := lastShownReplyTo != nil && *lastShownReplyTo == *replyToMessageID

			if (isPreviousDay || !isConsecutive) && !isSameAsLastShown {
				replyQuery := `SELECT text, username, extra_info FROM message_info WHERE message_id = ? AND chat_id = ?`
				var replyText string
				var replyUsername *string
				var replyExtraInfo *string
				if err := db.conn.QueryRow(replyQuery, *replyToMessageID, chatID).Scan(&replyText, &replyUsername, &replyExtraInfo); err == nil {
					replyContent := composeReplyContext(replyText, replyExtraInfo)

					if replyContent != "" {
						replyUserDisplay := "Аноним"
						if replyUsername != nil && *replyUsername != "" {
							replyUserDisplay = strings.TrimPrefix(*replyUsername, "@")
						}

						if isPreviousDay {
							// Full quote for previous-day replies
							formattedMessage += fmt.Sprintf(" (ответ на [%s]: \"%s\")", replyUserDisplay, replyContent)
						} else {
							// Short trimmed preview for same-day non-consecutive replies
							trimmedReply := replyContent
							runes := []rune(replyContent)
							if len(runes) > 50 {
								trimmedReply = string(runes[:50]) + "..."
							}
							formattedMessage += fmt.Sprintf(" (ответ на [%s]: \"%s\")", replyUserDisplay, trimmedReply)
						}
						// Update last shown reply target
						lastShownReplyTo = replyToMessageID
					}
				}
			}
		}

		messages = append(messages, formattedMessage)
	}

	log.Printf("GetRecentMessagesWithUsernames: Retrieved %d messages from database", len(messages))

	return messages, nil
}

// VacuumDatabase runs VACUUM to reclaim space and optimize the database.
// Skipped for remote providers where VACUUM is unsupported or unnecessary.
func (db *DB) VacuumDatabase() error {
	if db.IsRemote() {
		log.Printf("Skipping VACUUM: not supported on remote database provider")
		return nil
	}

	log.Printf("Starting database VACUUM operation...")
	start := time.Now()

	// VACUUM cannot be run inside a transaction, so we call it directly
	_, err := db.conn.Exec("VACUUM")
	if err != nil {
		return fmt.Errorf("failed to vacuum database: %v", err)
	}

	duration := time.Since(start)
	log.Printf("Database VACUUM completed in %v", duration)

	return nil
}

// GetLatestBotMessageID returns the message ID of the most recent bot message in the moderation chat
func (db *DB) GetLatestBotMessageID(botUserID int64, moderationChatID int64) (int, error) {
	query := `SELECT message_id FROM message_info 
		WHERE user_id = ? AND chat_id = ? 
		ORDER BY timestamp DESC LIMIT 1`

	var messageID int
	err := db.conn.QueryRow(query, botUserID, moderationChatID).Scan(&messageID)
	if err != nil {
		return 0, err // Return 0 if no bot messages found
	}

	return messageID, nil
}

// GetRecentBotMessageCount returns the count of bot messages in the specified time window
func (db *DB) GetRecentBotMessageCount(botUserID int64, chatID int64, hoursBack int) (int, error) {
	query := `SELECT COUNT(*) FROM message_info 
		WHERE user_id = ? AND chat_id = ? AND timestamp >= ?`

	timeThreshold := time.Now().Add(-time.Duration(hoursBack) * time.Hour)

	var count int
	err := db.conn.QueryRow(query, botUserID, chatID, timeThreshold).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

var linkOnlyRegex = regexp.MustCompile(`(?i)^(https?://|www\.)\S+$`)

func composeReplyContext(text string, extraInfo *string) string {
	trimmedText := strings.TrimSpace(text)
	var trimmedExtra string
	if extraInfo != nil {
		trimmedExtra = strings.TrimSpace(*extraInfo)
	}

	if trimmedText != "" {
		if trimmedExtra != "" && linkOnlyRegex.MatchString(trimmedText) {
			return fmt.Sprintf("%s - [Краткое содержание ссылки] %s", trimmedText, trimmedExtra)
		}
		return trimmedText
	}

	if trimmedExtra != "" {
		return "[Краткое содержание ссылки] " + trimmedExtra
	}

	return ""
}

// GetTableStats returns row counts for all known tables.
func (db *DB) GetTableStats() (map[string]int64, error) {
	tables := []string{"muted_users", "warnings", "messages_for_deletion", "message_info", "actions", "scheduled_events", "user_profiles", "user_names_history", "user_daily_activity", "token_usage"}
	counts := make(map[string]int64, len(tables))
	for _, table := range tables {
		var count int64
		err := db.conn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		if err != nil {
			counts[table] = -1
			continue
		}
		counts[table] = count
	}
	return counts, nil
}

// GetActiveMuteCount returns the number of currently active mutes.
func (db *DB) GetActiveMuteCount() (int64, error) {
	var count int64
	err := db.conn.QueryRow("SELECT COUNT(*) FROM muted_users WHERE is_active = 1").Scan(&count)
	return count, err
}

// EnsureScheduledEventExists inserts a placeholder record if the event is not yet tracked.
// This makes the event visible in the UI before it fires for the first time.
func (db *DB) EnsureScheduledEventExists(eventName, scheduledTime string) error {
	query := `INSERT INTO scheduled_events (event_name, scheduled_time, last_fired_at)
		VALUES (?, ?, ?)
		ON CONFLICT(event_name) DO UPDATE SET scheduled_time = excluded.scheduled_time`
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, eventName, scheduledTime, time.Time{})
		return err
	}, "ensure event exists")
}

// RecordEventFired upserts a scheduled event record with the current time.
func (db *DB) RecordEventFired(eventName, scheduledTime string) error {
	return db.RecordEventFiredAt(eventName, scheduledTime, time.Now())
}

// RecordEventFiredAt upserts a scheduled event record with a specific timestamp.
// Also clears started_at to release any active lock.
func (db *DB) RecordEventFiredAt(eventName, scheduledTime string, firedAt time.Time) error {
	query := `INSERT INTO scheduled_events (event_name, scheduled_time, last_fired_at, started_at)
		VALUES (?, ?, ?, NULL)
		ON CONFLICT(event_name) DO UPDATE SET scheduled_time = excluded.scheduled_time, last_fired_at = excluded.last_fired_at, started_at = NULL`

	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, eventName, scheduledTime, firedAt)
		return err
	}, "record event fired")
}

// TryClaimScheduledEvent attempts to atomically claim a scheduled event for execution.
// Returns true if the claim was successful (this instance should execute the task).
// The claim fails if another instance already holds the lock (started_at is set and not stale).
// staleTimeout defines how long a started_at timestamp is considered valid before
// assuming the holder crashed and allowing re-claim.
func (db *DB) TryClaimScheduledEvent(eventName string, staleTimeout time.Duration) (bool, error) {
	staleThreshold := time.Now().Add(-staleTimeout)
	now := time.Now()

	query := `UPDATE scheduled_events
		SET started_at = ?
		WHERE event_name = ? AND (started_at IS NULL OR started_at < ?)`

	var rowsAffected int64
	err := db.retryOnTransientError(func() error {
		result, err := db.conn.Exec(query, now, eventName, staleThreshold)
		if err != nil {
			return err
		}
		rowsAffected, _ = result.RowsAffected()
		return nil
	}, "claim scheduled event")

	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

// ReleaseScheduledEvent clears the started_at lock without updating last_fired_at.
// Use this when a task fails and should be retried later.
func (db *DB) ReleaseScheduledEvent(eventName string) error {
	query := `UPDATE scheduled_events SET started_at = NULL WHERE event_name = ?`
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, eventName)
		return err
	}, "release scheduled event")
}

// GetScheduledEvent retrieves a scheduled event record by name.
// Returns nil if the event has never been tracked.
func (db *DB) GetScheduledEvent(eventName string) (*ScheduledEvent, error) {
	query := `SELECT event_name, scheduled_time, last_fired_at, started_at FROM scheduled_events WHERE event_name = ?`

	var event ScheduledEvent
	var lastFiredAtStr string
	var startedAtStr sql.NullString
	err := db.conn.QueryRow(query, eventName).Scan(&event.EventName, &event.ScheduledTime, &lastFiredAtStr, &startedAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	event.LastFiredAt = parseTime(lastFiredAtStr)
	if startedAtStr.Valid {
		t := parseTime(startedAtStr.String)
		event.StartedAt = &t
	}
	return &event, nil
}

// GetAllScheduledEvents returns all tracked scheduled events.
func (db *DB) GetAllScheduledEvents() ([]ScheduledEvent, error) {
	query := `SELECT event_name, scheduled_time, last_fired_at, started_at FROM scheduled_events`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []ScheduledEvent
	for rows.Next() {
		var event ScheduledEvent
		var lastFiredAtStr string
		var startedAtStr sql.NullString
		if err := rows.Scan(&event.EventName, &event.ScheduledTime, &lastFiredAtStr, &startedAtStr); err != nil {
			return nil, err
		}
		event.LastFiredAt = parseTime(lastFiredAtStr)
		if startedAtStr.Valid {
			t := parseTime(startedAtStr.String)
			event.StartedAt = &t
		}
		events = append(events, event)
	}
	return events, nil
}

// PruneScheduledEvents removes scheduled event records whose names are not in the given active set.
func (db *DB) PruneScheduledEvents(activeNames []string) (int64, error) {
	if len(activeNames) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(activeNames))
	args := make([]interface{}, len(activeNames))
	for i, name := range activeNames {
		placeholders[i] = "?"
		args[i] = name
	}

	query := fmt.Sprintf(`DELETE FROM scheduled_events WHERE event_name NOT IN (%s)`,
		strings.Join(placeholders, ","))

	var rowsAffected int64
	err := db.retryOnTransientError(func() error {
		result, err := db.conn.Exec(query, args...)
		if err != nil {
			return err
		}
		rowsAffected, _ = result.RowsAffected()
		return nil
	}, "prune scheduled events")
	return rowsAffected, err
}
