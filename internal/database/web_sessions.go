// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"database/sql"
	"errors"
	"time"
)

// Web UI session-token hash storage.
//
// Session token hashes are persisted so multiple container instances sharing
// the same remote database can validate the same web UI auth token without
// storing the bearer token itself.

// SaveWebSession stores a web UI session token hash with its expiration time.
func (db *DB) SaveWebSession(tokenHash string, expiresAt time.Time) error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(
			`INSERT INTO web_sessions (token, created_at, expires_at) VALUES (?, ?, ?)
				ON CONFLICT(token) DO UPDATE SET expires_at = excluded.expires_at`,
			tokenHash, time.Now().UTC(), expiresAt.UTC())
		return err
	}, "save web session")
}

// GetWebSessionExpiry returns the expiration time for the given token hash.
// Returns (zero time, nil) when the token does not exist.
func (db *DB) GetWebSessionExpiry(tokenHash string) (time.Time, error) {
	var expiresAt string
	err := db.conn.QueryRow(`SELECT expires_at FROM web_sessions WHERE token = ?`, tokenHash).Scan(&expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	return parseTime(expiresAt), nil
}

// DeleteWebSession removes a single session token hash from the store.
func (db *DB) DeleteWebSession(tokenHash string) error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(`DELETE FROM web_sessions WHERE token = ?`, tokenHash)
		return err
	}, "delete web session")
}

// DeleteExpiredWebSessions removes all sessions whose expires_at is in the past.
func (db *DB) DeleteExpiredWebSessions() error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(`DELETE FROM web_sessions WHERE expires_at < ?`, time.Now().UTC())
		return err
	}, "delete expired web sessions")
}
