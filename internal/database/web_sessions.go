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
	return db.SaveWebSessionActor(tokenHash, expiresAt, 0, "")
}

// SaveWebSessionActor stores a web UI session and its authenticated actor.
func (db *DB) SaveWebSessionActor(tokenHash string, expiresAt time.Time, actorID int64, actorName string) error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(
			`INSERT INTO web_sessions (token, created_at, expires_at, actor_id, actor_name) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(token) DO UPDATE SET
					expires_at = excluded.expires_at,
					actor_id = excluded.actor_id,
					actor_name = excluded.actor_name`,
			tokenHash, time.Now().UTC(), expiresAt.UTC(), actorID, actorName)
		return err
	}, "save web session")
}

// GetWebSession returns a session's expiry and authenticated actor. A missing
// token returns zero values and no error.
func (db *DB) GetWebSession(tokenHash string) (time.Time, int64, string, error) {
	var expiresAt string
	var actorID int64
	var actorName string
	err := db.conn.QueryRow(
		`SELECT expires_at, actor_id, actor_name FROM web_sessions WHERE token = ?`, tokenHash,
	).Scan(&expiresAt, &actorID, &actorName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, 0, "", nil
		}
		return time.Time{}, 0, "", err
	}
	return parseTime(expiresAt), actorID, actorName, nil
}

// GetWebSessionExpiry returns the expiration time for the given token hash.
// Returns (zero time, nil) when the token does not exist.
func (db *DB) GetWebSessionExpiry(tokenHash string) (time.Time, error) {
	expiresAt, _, _, err := db.GetWebSession(tokenHash)
	return expiresAt, err
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
