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

// AI-generated user profiles: per-user summaries and the bulk data-fetching
// helpers used by the profile-generation scheduler.

// UpsertUserProfile inserts or updates a user profile. The first_seen_at column
// is maintained on the message-ingest path, so it is set only on a fresh insert
// (empty = unknown) and left untouched on conflict.
func (db *DB) UpsertUserProfile(profile *UserProfile) error {
	now := time.Now()
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(`INSERT INTO user_profiles (user_id, username, profile, reputation, first_seen_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, '', ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET
				username = excluded.username,
				profile = excluded.profile,
				reputation = excluded.reputation,
				updated_at = excluded.updated_at`,
			profile.UserID, profile.Username, profile.Profile, profile.Reputation, now, now)
		return err
	}, "upsert user profile")
}

// AppendTgProfileAnalysis appends new-member profile screening findings to a
// user's tg_profile_analysis field, creating the profile row (reputation
// "neutral", empty behavior profile) if it does not yet exist. Each finding is
// stored on its own line; findings already present are skipped so repeated
// screening runs don't duplicate entries. This field is kept separate from the
// AI-generated behavior `profile` so the screening sub-check results and the
// behavior profile never overwrite each other.
func (db *DB) AppendTgProfileAnalysis(userID int64, username string, findings []string) error {
	cleaned := make([]string, 0, len(findings))
	for _, f := range findings {
		if f = strings.TrimSpace(f); f != "" {
			cleaned = append(cleaned, f)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	now := time.Now()
	return db.retryOnTransientError(func() error {
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

		var existing string
		err = tx.QueryRow(
			`SELECT tg_profile_analysis FROM user_profiles WHERE user_id = ?`, userID,
		).Scan(&existing)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(
				`INSERT INTO user_profiles (user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				userID, username, "", "neutral", strings.Join(cleaned, "\n"), "", now, now,
			); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			// Append only findings not already recorded (line-level dedup).
			present := make(map[string]bool)
			for _, ln := range strings.Split(existing, "\n") {
				present[strings.TrimSpace(ln)] = true
			}
			var toAdd []string
			for _, f := range cleaned {
				if !present[f] {
					toAdd = append(toAdd, f)
				}
			}
			if len(toAdd) == 0 {
				// Nothing new - leave the row untouched.
				if err := tx.Commit(); err != nil {
					return err
				}
				committed = true
				return nil
			}
			newAnalysis := strings.Join(toAdd, "\n")
			if strings.TrimSpace(existing) != "" {
				newAnalysis = existing + "\n" + newAnalysis
			}
			if _, err := tx.Exec(
				`UPDATE user_profiles SET tg_profile_analysis = ?, updated_at = ? WHERE user_id = ?`,
				newAnalysis, now, userID,
			); err != nil {
				return err
			}
		}

		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}, "append tg profile analysis")
}

// GetUserProfile retrieves a user profile by user_id.
func (db *DB) GetUserProfile(userID int64) (*UserProfile, error) {
	query := `SELECT user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at
		FROM user_profiles WHERE user_id = ?`

	var p UserProfile
	var firstSeenAt, createdAt, updatedAt string
	err := db.conn.QueryRow(query, userID).Scan(
		&p.UserID, &p.Username, &p.Profile, &p.Reputation, &p.TgProfileAnalysis, &firstSeenAt, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	p.FirstSeenAt = parseTime(firstSeenAt)
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}

// GetAllUserProfiles returns all user profiles.
func (db *DB) GetAllUserProfiles() ([]UserProfile, error) {
	query := `SELECT user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at
		FROM user_profiles ORDER BY updated_at DESC`

	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []UserProfile
	for rows.Next() {
		var p UserProfile
		var firstSeenAt, createdAt, updatedAt string
		if err := rows.Scan(&p.UserID, &p.Username, &p.Profile, &p.Reputation, &p.TgProfileAnalysis, &firstSeenAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.FirstSeenAt = parseTime(firstSeenAt)
		p.CreatedAt = parseTime(createdAt)
		p.UpdatedAt = parseTime(updatedAt)
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// GetActiveUsersSince returns distinct user_id/username pairs that sent messages since the given time in the given chats.
func (db *DB) GetActiveUsersSince(chatIDs []int64, since time.Time) ([]struct {
	UserID   int64
	Username string
}, error) {
	if len(chatIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(chatIDs))
	args := make([]interface{}, 0, len(chatIDs)+1)
	for i, id := range chatIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, since)

	query := fmt.Sprintf(`SELECT DISTINCT user_id, username FROM message_info
		WHERE chat_id IN (%s) AND timestamp >= ? AND text IS NOT NULL AND text != ''
		ORDER BY user_id`, strings.Join(placeholders, ","))

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []struct {
		UserID   int64
		Username string
	}
	for rows.Next() {
		var u struct {
			UserID   int64
			Username string
		}
		var username *string
		if err := rows.Scan(&u.UserID, &username); err != nil {
			return nil, err
		}
		if username != nil {
			u.Username = *username
		}
		users = append(users, u)
	}
	return users, nil
}

// GetUserMessagesSince returns messages for a specific user across given chats since the given time.
func (db *DB) GetUserMessagesSince(userID int64, chatIDs []int64, since time.Time) ([]MessageInfo, error) {
	if len(chatIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(chatIDs))
	args := []interface{}{userID}
	for i, id := range chatIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, since)

	query := fmt.Sprintf(`SELECT message_id, chat_id, user_id, username, text, reply_to_message_id, timestamp, extra_info
		FROM message_info
		WHERE user_id = ? AND chat_id IN (%s) AND timestamp >= ? AND text IS NOT NULL AND text != ''
		ORDER BY timestamp ASC`, strings.Join(placeholders, ","))

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []MessageInfo
	for rows.Next() {
		var m MessageInfo
		var ts string
		var username, extraInfo *string
		if err := rows.Scan(&m.MessageID, &m.ChatID, &m.UserID, &username, &m.Text, &m.ReplyToMessageID, &ts, &extraInfo); err != nil {
			return nil, err
		}
		if username != nil {
			m.Username = *username
		}
		if extraInfo != nil {
			m.ExtraInfo = *extraInfo
		}
		m.Timestamp = parseTime(ts)
		messages = append(messages, m)
	}
	return messages, nil
}

// GetUserWarningsSince returns the count of warnings for a user across all chats since the given time.
func (db *DB) GetUserWarningsSince(userID int64, since time.Time) (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM warnings WHERE user_id = ? AND warned_at >= ?`,
		userID, since).Scan(&count)
	return count, err
}

// GetUserMutesSince returns the count of mutes for a user across all chats since the given time.
func (db *DB) GetUserMutesSince(userID int64, since time.Time) (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM actions WHERE user_id = ? AND action_type = 'mute' AND timestamp >= ?`,
		userID, since).Scan(&count)
	return count, err
}

// GetUserClearedSince returns the count of "not a violation" (cleared) actions for a user across all chats since the given time.
func (db *DB) GetUserClearedSince(userID int64, since time.Time) (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM actions WHERE user_id = ? AND action_type = 'cleared' AND timestamp >= ?`,
		userID, since).Scan(&count)
	return count, err
}

// GetUserMessageActions returns moderation actions (warn, mute, cleared) keyed by message_id for a user since the given time.
// Each message_id maps to a slice of action types (e.g. ["warn"], ["mute"], ["cleared"]).
func (db *DB) GetUserMessageActions(userID int64, since time.Time) (map[int][]string, error) {
	result := make(map[int][]string)

	// Get actions from the actions table (mute, cleared, etc.)
	rows, err := db.conn.Query(
		`SELECT message_id, action_type FROM actions
		WHERE user_id = ? AND timestamp >= ? AND message_id != 0`,
		userID, since)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	for rows.Next() {
		var msgID int
		var actionType string
		if err := rows.Scan(&msgID, &actionType); err != nil {
			continue
		}
		result[msgID] = append(result[msgID], actionType)
	}

	// Get warnings from the warnings table
	wRows, err := db.conn.Query(
		`SELECT message_id FROM warnings
		WHERE user_id = ? AND warned_at >= ? AND message_id IS NOT NULL`,
		userID, since)
	if err != nil {
		return result, nil // return what we have so far
	}
	defer wRows.Close()

	for wRows.Next() {
		var msgID int
		if err := wRows.Scan(&msgID); err != nil {
			continue
		}
		// Avoid duplicating if actions table already has a "warn" for this message
		hasWarn := false
		for _, a := range result[msgID] {
			if a == "warn" {
				hasWarn = true
				break
			}
		}
		if !hasWarn {
			result[msgID] = append(result[msgID], "warn")
		}
	}

	return result, nil
}

// DeleteUserProfile removes a user profile.
func (db *DB) DeleteUserProfile(userID int64) error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(`DELETE FROM user_profiles WHERE user_id = ?`, userID)
		return err
	}, "delete user profile")
}

// ProfileData holds all data needed for user profile generation, grouped by user.
type ProfileData struct {
	UserID   int64
	Username string
	Messages []MessageInfo
	Warnings int
	Mutes    int
	Cleared  int
	// MessageActions maps message_id to moderation actions for per-message annotations
	MessageActions map[int][]MessageAction
}

// MessageAction describes a single moderation action taken on a message,
// including its type (warn/mute/cleared) and the reason/rule recorded for it.
type MessageAction struct {
	Type   string
	Reason string
}

// GetAllProfileData fetches all messages, warnings, mutes, cleared counts, and per-message
// actions since the given time across the given chats in bulk (3 DB queries total),
// then groups the results by user_id. Bot user and whitelisted users should be filtered by the caller.
func (db *DB) GetAllProfileData(chatIDs []int64, since time.Time) (map[int64]*ProfileData, error) {
	if len(chatIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(chatIDs))
	chatArgs := make([]interface{}, len(chatIDs))
	for i, id := range chatIDs {
		placeholders[i] = "?"
		chatArgs[i] = id
	}
	chatIn := strings.Join(placeholders, ",")

	result := make(map[int64]*ProfileData)

	// Helper to ensure a user entry exists
	ensureUser := func(userID int64, username string) *ProfileData {
		pd, ok := result[userID]
		if !ok {
			pd = &ProfileData{
				UserID:         userID,
				Username:       username,
				MessageActions: make(map[int][]MessageAction),
			}
			result[userID] = pd
		}
		if username != "" && pd.Username == "" {
			pd.Username = username
		}
		return pd
	}

	// ── Query 1: All messages ──
	msgArgs := append(chatArgs, since)
	msgQuery := fmt.Sprintf(`SELECT message_id, chat_id, user_id, username, text, reply_to_message_id, timestamp, extra_info
		FROM message_info
		WHERE chat_id IN (%s) AND timestamp >= ? AND text IS NOT NULL AND text != ''
		ORDER BY timestamp ASC`, chatIn)

	rows, err := db.conn.Query(msgQuery, msgArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	for rows.Next() {
		var m MessageInfo
		var ts string
		var username, extraInfo *string
		if err := rows.Scan(&m.MessageID, &m.ChatID, &m.UserID, &username, &m.Text, &m.ReplyToMessageID, &ts, &extraInfo); err != nil {
			rows.Close()
			return nil, err
		}
		if username != nil {
			m.Username = *username
		}
		if extraInfo != nil {
			m.ExtraInfo = *extraInfo
		}
		m.Timestamp = parseTime(ts)
		pd := ensureUser(m.UserID, m.Username)
		pd.Messages = append(pd.Messages, m)
	}
	rows.Close()

	if len(result) == 0 {
		return result, nil
	}

	// ── Query 2: All actions (mute, cleared, warn) from actions table ──
	actArgs := []interface{}{since}
	actQuery := `SELECT user_id, action_type, message_id, reason FROM actions
		WHERE timestamp >= ? AND message_id != 0`

	actRows, err := db.conn.Query(actQuery, actArgs...)
	if err != nil {
		return result, nil // partial result is OK
	}
	for actRows.Next() {
		var userID int64
		var actionType string
		var msgID int
		var reason sql.NullString
		if err := actRows.Scan(&userID, &actionType, &msgID, &reason); err != nil {
			continue
		}
		pd, ok := result[userID]
		if !ok {
			continue // skip users not in message set
		}
		pd.MessageActions[msgID] = append(pd.MessageActions[msgID], MessageAction{Type: actionType, Reason: reason.String})
		switch actionType {
		case "mute":
			pd.Mutes++
		case "cleared":
			pd.Cleared++
		case "warn":
			pd.Warnings++
		}
	}
	actRows.Close()

	// ── Query 3: Warnings from warnings table ──
	warnArgs := []interface{}{since}
	warnQuery := `SELECT user_id, message_id, reason FROM warnings WHERE warned_at >= ? AND message_id IS NOT NULL`

	warnRows, err := db.conn.Query(warnQuery, warnArgs...)
	if err != nil {
		return result, nil
	}
	for warnRows.Next() {
		var userID int64
		var msgID int
		var reason sql.NullString
		if err := warnRows.Scan(&userID, &msgID, &reason); err != nil {
			continue
		}
		pd, ok := result[userID]
		if !ok {
			continue
		}
		// Avoid double-counting if already counted from actions table
		hasWarn := false
		for _, a := range pd.MessageActions[msgID] {
			if a.Type == "warn" {
				hasWarn = true
				break
			}
		}
		if !hasWarn {
			pd.MessageActions[msgID] = append(pd.MessageActions[msgID], MessageAction{Type: "warn", Reason: reason.String})
			pd.Warnings++
		}
	}
	warnRows.Close()

	return result, nil
}
