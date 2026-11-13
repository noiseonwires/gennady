// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package database

import (
	"database/sql"
	"errors"
)

type MediaAnalysis struct {
	Description          string
	ContentSafetyFlagged bool
}

// GetMediaAnalysis returns nil on a cache miss. Entries have no expiration.
func (db *DB) GetMediaAnalysis(key string) (*MediaAnalysis, error) {
	var result MediaAnalysis
	err := db.conn.QueryRow(
		`SELECT description, content_safety_flagged FROM media_analysis_cache WHERE cache_key = ?`, key,
	).Scan(&result.Description, &result.ContentSafetyFlagged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (db *DB) SaveMediaAnalysis(key string, result MediaAnalysis) error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(
			`INSERT INTO media_analysis_cache (cache_key, description, content_safety_flagged)
			 VALUES (?, ?, ?)
			 ON CONFLICT(cache_key) DO UPDATE SET
				description = excluded.description,
				content_safety_flagged = excluded.content_safety_flagged`,
			key, result.Description, result.ContentSafetyFlagged)
		return err
	}, "save media analysis")
}
