// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

// rowScanner is the minimal interface satisfied by both *sql.Row and *sql.Rows.
// It lets the helpers below be used from QueryRow() and rows.Next() loops alike.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanMutedUser populates a MutedUser from a row that selects, in order:
//
//	user_id, username, chat_id, muted_by, muted_at, unmute_at,
//	reason, is_active, message_id, is_cruel
func scanMutedUser(s rowScanner) (*MutedUser, error) {
	var u MutedUser
	var mutedAtStr, unmuteAtStr string
	if err := s.Scan(
		&u.UserID, &u.Username, &u.ChatID,
		&u.MutedBy, &mutedAtStr, &unmuteAtStr,
		&u.Reason, &u.IsActive, &u.MessageID, &u.IsCruel,
	); err != nil {
		return nil, err
	}
	u.MutedAt = parseTime(mutedAtStr)
	u.UnmuteAt = parseTime(unmuteAtStr)
	return &u, nil
}

// scanAction populates an Action from a row that selects, in order:
//
//	user_id, username, admin_id, admin_name,
//	action_type, duration, reason, chat_id, message_id, timestamp
func scanAction(s rowScanner) (*Action, error) {
	var a Action
	var ts string
	if err := s.Scan(
		&a.UserID, &a.Username, &a.AdminID, &a.AdminName,
		&a.ActionType, &a.Duration, &a.Reason, &a.ChatID, &a.MessageID, &ts,
	); err != nil {
		return nil, err
	}
	a.Timestamp = parseTime(ts)
	return &a, nil
}

// messageInfoColumns is the canonical SELECT list for MessageInfo. Keep callers
// using this constant so the column order stays in lock-step with the scanner.
const messageInfoColumns = `message_id, chat_id, user_id, username, text, reply_to_message_id, timestamp, extra_info, message_thread_id, quote_text, reactions, moderation_reason`

// scanMessageInfo populates a MessageInfo from a row that selects
// messageInfoColumns in order.
func scanMessageInfo(s rowScanner, m *MessageInfo) error {
	var tsStr string
	if err := s.Scan(
		&m.MessageID, &m.ChatID, &m.UserID, &m.Username,
		&m.Text, &m.ReplyToMessageID, &tsStr, &m.ExtraInfo, &m.MessageThreadID,
		&m.QuoteText, &m.Reactions, &m.ModerationReason,
	); err != nil {
		return err
	}
	m.Timestamp = parseTime(tsStr)
	return nil
}
