// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"database/sql"
	"errors"
	"time"
)

// WebsiteSnapshot is the last *reported* state of a watched page. Content is
// the extracted plain text every new fetch is diffed against; it is only
// replaced once a change has actually been reported, so slow, incremental
// edits accumulate instead of silently becoming the new baseline.
type WebsiteSnapshot struct {
	URL         string
	Content     string
	ContentHash string
	ChangedAt   time.Time
	CheckedAt   time.Time
}

// GetWebsiteSnapshot returns the stored snapshot for a URL, or nil when the
// page has never been fetched.
func (db *DB) GetWebsiteSnapshot(url string) (*WebsiteSnapshot, error) {
	var s WebsiteSnapshot
	err := db.conn.QueryRow(
		`SELECT url, content, content_hash, changed_at, checked_at
		 FROM website_snapshots WHERE url = ?`, url).
		Scan(&s.URL, &s.Content, &s.ContentHash, &s.ChangedAt, &s.CheckedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// SaveWebsiteSnapshot stores the content that future fetches are compared
// against, marking both changed_at and checked_at as now.
func (db *DB) SaveWebsiteSnapshot(url, content, contentHash string) error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(
			`INSERT INTO website_snapshots (url, content, content_hash, changed_at, checked_at)
			 VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			 ON CONFLICT(url) DO UPDATE SET
				content = excluded.content,
				content_hash = excluded.content_hash,
				changed_at = excluded.changed_at,
				checked_at = excluded.checked_at`,
			url, content, contentHash)
		return err
	}, "save website snapshot")
}

// TouchWebsiteSnapshot records that a page was fetched without a reportable
// change, leaving the stored baseline content untouched.
func (db *DB) TouchWebsiteSnapshot(url string) error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(
			`UPDATE website_snapshots SET checked_at = CURRENT_TIMESTAMP WHERE url = ?`, url)
		return err
	}, "touch website snapshot")
}
