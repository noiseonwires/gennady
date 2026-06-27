// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"database/sql"
	"errors"
	"time"
)

// ForumTopic is a cached forum-topic name learned from forum_topic_created /
// forum_topic_edited service messages.
type ForumTopic struct {
	ChatID    int64
	ThreadID  int
	Name      string
	UpdatedAt time.Time
}

// UpsertForumTopic stores (or updates) the human-readable name for a forum
// topic. Empty names and the main area (thread_id 0) are ignored by callers.
func (db *DB) UpsertForumTopic(chatID int64, threadID int, name string) error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(
			`INSERT INTO forum_topics (chat_id, thread_id, name, updated_at)
			 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(chat_id, thread_id)
			 DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`,
			chatID, threadID, name)
		return err
	}, "upsert forum topic")
}

// GetForumTopicName returns the cached name for a topic, or "" (with ok=false)
// when the topic has not been seen.
func (db *DB) GetForumTopicName(chatID int64, threadID int) (string, bool, error) {
	var name string
	err := db.conn.QueryRow(
		`SELECT name FROM forum_topics WHERE chat_id = ? AND thread_id = ?`,
		chatID, threadID).Scan(&name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return name, true, nil
}

// ListAllForumTopics returns every cached topic name. Used to warm the
// in-memory directory on startup.
func (db *DB) ListAllForumTopics() ([]ForumTopic, error) {
	rows, err := db.conn.Query(
		`SELECT chat_id, thread_id, name, updated_at FROM forum_topics`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ForumTopic
	for rows.Next() {
		var t ForumTopic
		if err := rows.Scan(&t.ChatID, &t.ThreadID, &t.Name, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
