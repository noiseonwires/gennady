// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// General (non-AI) user profile tracking: name history, first-seen-in-chat
// records and per-day activity. Used by moderation summaries, the web API
// and scheduled cleanup.
//
// Note: per-message writes are performed in a single transaction by
// RecordIncomingMessage in operations.go.

// GetUserNameHistory returns all recorded (username, display_name) entries for a user, oldest first.
func (db *DB) GetUserNameHistory(userID int64) ([]UserNameHistory, error) {
	rows, err := db.conn.Query(
		`SELECT username, display_name, changed_at FROM user_names_history
		 WHERE user_id = ? ORDER BY changed_at ASC, id ASC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []UserNameHistory
	for rows.Next() {
		var h UserNameHistory
		var username, displayName *string
		var changedAt string
		if err := rows.Scan(&username, &displayName, &changedAt); err != nil {
			return nil, err
		}
		if username != nil {
			h.Username = *username
		}
		if displayName != nil {
			h.DisplayName = *displayName
		}
		h.ChangedAt = parseTime(changedAt)
		result = append(result, h)
	}
	return result, nil
}

// GetLatestUsername returns the most recently recorded username for userID
// from user_names_history. Returns ("", nil) when the user has no history
// or no non-empty username on file.
func (db *DB) GetLatestUsername(userID int64) (string, error) {
	var username *string
	err := db.conn.QueryRow(
		`SELECT username FROM user_names_history
		 WHERE user_id = ? AND username IS NOT NULL AND username != ''
		 ORDER BY changed_at DESC, id DESC LIMIT 1`, userID,
	).Scan(&username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if username == nil {
		return "", nil
	}
	return *username, nil
}

// CountUserMutesInChat returns the total number of "mute" actions ever
// recorded for userID in chatID. Pass chatID == 0 to count across all chats.
// Used to flag prior holders of a re-used @username who have a moderation
// history under their (now defunct) user_id.
func (db *DB) CountUserMutesInChat(userID, chatID int64) (int, error) {
	var count int
	var err error
	if chatID == 0 {
		err = db.conn.QueryRow(
			`SELECT COUNT(*) FROM actions WHERE user_id = ? AND action_type = 'mute'`,
			userID,
		).Scan(&count)
	} else {
		err = db.conn.QueryRow(
			`SELECT COUNT(*) FROM actions WHERE user_id = ? AND chat_id = ? AND action_type = 'mute'`,
			userID, chatID,
		).Scan(&count)
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}

// UsernameReuseEntry describes one prior holder of a username found in
// user_names_history (used to detect re-registrations: same @username but a
// different numeric user_id).
type UsernameReuseEntry struct {
	UserID      int64     `json:"user_id"`
	Username    string    `json:"username"`     // original case as last recorded
	DisplayName string    `json:"display_name"` // latest display name on file
	LastUsedAt  time.Time `json:"last_used_at"` // most recent timestamp this user_id was seen with the username
}

// FindUsernameReusers returns every user_id (other than excludeUserID) that has
// previously been recorded in user_names_history with the given username. The
// match is case-insensitive (Telegram usernames are case-insensitive). Each
// returned entry reflects the latest known display_name and most recent
// changed_at for that user_id. Results are ordered most-recent first.
//
// An empty username returns an empty slice (no meaningful comparison).
func (db *DB) FindUsernameReusers(username string, excludeUserID int64) ([]UsernameReuseEntry, error) {
	username = strings.TrimSpace(strings.TrimPrefix(username, "@"))
	if username == "" {
		return nil, nil
	}
	rows, err := db.conn.Query(
		`SELECT user_id, username, display_name, changed_at
		 FROM user_names_history
		 WHERE LOWER(username) = LOWER(?) AND user_id != ?
		 ORDER BY changed_at DESC, id DESC`,
		username, excludeUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Dedupe by user_id, keeping the first (most recent) row we see for each.
	seen := make(map[int64]bool)
	var result []UsernameReuseEntry
	for rows.Next() {
		var uid int64
		var u, d *string
		var changedAt string
		if err := rows.Scan(&uid, &u, &d, &changedAt); err != nil {
			return nil, err
		}
		if seen[uid] {
			continue
		}
		seen[uid] = true
		entry := UsernameReuseEntry{
			UserID:     uid,
			LastUsedAt: parseTime(changedAt),
		}
		if u != nil {
			entry.Username = *u
		}
		if d != nil {
			entry.DisplayName = *d
		}
		// If the most-recent row for this user_id has no display name (rare -
		// older entries when the user had only a username), walk the history
		// to find the latest non-empty one.
		if entry.DisplayName == "" {
			var fallback string
			_ = db.conn.QueryRow(
				`SELECT display_name FROM user_names_history
				 WHERE user_id = ? AND display_name IS NOT NULL AND display_name != ''
				 ORDER BY changed_at DESC, id DESC LIMIT 1`,
				uid,
			).Scan(&fallback)
			entry.DisplayName = fallback
		}
		result = append(result, entry)
	}
	return result, nil
}

// GetUserFirstSeen returns the global-earliest time any message from the user
// was observed (across all chats), as recorded on the user_profiles row.
// Returns the zero time when the user has no recorded first-seen.
func (db *DB) GetUserFirstSeen(userID int64) (time.Time, error) {
	var firstSeenAt string
	err := db.conn.QueryRow(
		`SELECT first_seen_at FROM user_profiles WHERE user_id = ?`, userID,
	).Scan(&firstSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return parseTime(firstSeenAt), nil
}

// GetUserDailyActivityRange returns daily message counts for a user for each of the
// given day-keys (YYYY-MM-DD strings). Days with no activity are returned with count=0.
// The output preserves the input order of dayDates.
func (db *DB) GetUserDailyActivityRange(userID int64, dayDates []string) ([]UserDailyActivity, error) {
	out := make([]UserDailyActivity, len(dayDates))
	for i, d := range dayDates {
		out[i] = UserDailyActivity{Date: d, Count: 0}
	}
	if len(dayDates) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(dayDates))
	args := make([]interface{}, 0, len(dayDates)+1)
	args = append(args, userID)
	for i, d := range dayDates {
		placeholders[i] = "?"
		args = append(args, d)
	}
	query := fmt.Sprintf(
		`SELECT day_date, count FROM user_daily_activity
		 WHERE user_id = ? AND day_date IN (%s)`, strings.Join(placeholders, ","),
	)
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	indexByDate := make(map[string]int, len(dayDates))
	for i, d := range dayDates {
		indexByDate[d] = i
	}
	for rows.Next() {
		var d string
		var c int
		if err := rows.Scan(&d, &c); err != nil {
			continue
		}
		if idx, ok := indexByDate[d]; ok {
			out[idx].Count = c
		}
	}
	return out, nil
}

// CleanupOldUserDailyActivity removes per-day activity rows older than the given cutoff date (YYYY-MM-DD).
func (db *DB) CleanupOldUserDailyActivity(cutoffDate string) (int64, error) {
	return db.cleanupTable(`DELETE FROM user_daily_activity WHERE day_date < ?`, cutoffDate, "cleanup old user daily activity")
}

// GetAllTrackedUserIDs returns every user_id that has any general-profile tracking data
// (name history or daily activity rows).
func (db *DB) GetAllTrackedUserIDs() ([]int64, error) {
	rows, err := db.conn.Query(
		`SELECT DISTINCT user_id FROM (
			SELECT user_id FROM user_names_history
			UNION
			SELECT user_id FROM user_daily_activity
		)`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetAllUserNameHistory returns every (username, display_name) entry across all
// users, grouped by user_id and ordered oldest-first within each group. Used by
// bulk endpoints to avoid per-user queries.
func (db *DB) GetAllUserNameHistory() (map[int64][]UserNameHistory, error) {
	rows, err := db.conn.Query(
		`SELECT user_id, username, display_name, changed_at FROM user_names_history
		 ORDER BY user_id ASC, changed_at ASC, id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64][]UserNameHistory)
	for rows.Next() {
		var uid int64
		var h UserNameHistory
		var username, displayName *string
		var changedAt string
		if err := rows.Scan(&uid, &username, &displayName, &changedAt); err != nil {
			return nil, err
		}
		if username != nil {
			h.Username = *username
		}
		if displayName != nil {
			h.DisplayName = *displayName
		}
		h.ChangedAt = parseTime(changedAt)
		result[uid] = append(result[uid], h)
	}
	return result, nil
}

// GetAllUserDailyActivityRange returns daily message counts for every user for
// the given day-keys (YYYY-MM-DD strings). The result is keyed by user_id; each
// slice preserves the input order of dayDates and includes zero-count entries
// for days with no activity.
func (db *DB) GetAllUserDailyActivityRange(dayDates []string) (map[int64][]UserDailyActivity, error) {
	if len(dayDates) == 0 {
		return map[int64][]UserDailyActivity{}, nil
	}
	placeholders := make([]string, len(dayDates))
	args := make([]interface{}, len(dayDates))
	for i, d := range dayDates {
		placeholders[i] = "?"
		args[i] = d
	}
	query := fmt.Sprintf(
		`SELECT user_id, day_date, count FROM user_daily_activity
		 WHERE day_date IN (%s)`, strings.Join(placeholders, ","),
	)
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	indexByDate := make(map[string]int, len(dayDates))
	for i, d := range dayDates {
		indexByDate[d] = i
	}
	result := make(map[int64][]UserDailyActivity)
	for rows.Next() {
		var uid int64
		var d string
		var c int
		if err := rows.Scan(&uid, &d, &c); err != nil {
			continue
		}
		idx, ok := indexByDate[d]
		if !ok {
			continue
		}
		slice, exists := result[uid]
		if !exists {
			slice = make([]UserDailyActivity, len(dayDates))
			for i, dk := range dayDates {
				slice[i] = UserDailyActivity{Date: dk, Count: 0}
			}
			result[uid] = slice
		}
		slice[idx].Count = c
	}
	return result, nil
}

// DeleteUserTrackingData removes the general-profile tracking rows for a user
// (name history and per-day activity). The user_profiles row - including its
// first_seen_at column - is left untouched; callers that also want to remove it
// should call DeleteUserProfile.
func (db *DB) DeleteUserTrackingData(userID int64) error {
	return db.retryOnTransientError(func() error {
		if _, err := db.conn.Exec(`DELETE FROM user_names_history WHERE user_id = ?`, userID); err != nil {
			return err
		}
		if _, err := db.conn.Exec(`DELETE FROM user_daily_activity WHERE user_id = ?`, userID); err != nil {
			return err
		}
		return nil
	}, "delete user tracking data")
}
