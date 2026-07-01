// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

// transferTables lists all tables that should be copied during export/import.
var transferTableNames = []string{
	"config_values",
	"muted_users",
	"warnings",
	"messages_for_deletion",
	"actions",
	"message_info",
	"scheduled_events",
	"user_profiles",
	"user_names_history",
	"user_daily_activity",
	"token_usage",
	"forum_topics",
	"moderation_stats",
}

// transferExcludedTables lists tables that are intentionally NOT copied during
// export/import/clone, each with the reason it is skipped:
//
//   - web_sessions: ephemeral web UI auth token hashes that are host/instance
//     specific. Copying them between databases would leak login sessions across
//     deployments, and they expire on their own anyway.
//   - sqlite_sequence: internal SQLite bookkeeping for AUTOINCREMENT columns,
//     recreated automatically; it must never be copied verbatim.
//
// Every table created by createTables must appear in either transferTableNames
// or this set. TestTransferTableCoverage enforces that invariant so a newly
// added table cannot silently be dropped from backups, clones and migrations -
// the test fails until the author makes an explicit transfer/exclude decision.
var transferExcludedTables = map[string]bool{
	"web_sessions":    true,
	"sqlite_sequence": true,
}

// importStrategy defines how a table is handled during import.
type importStrategy int

const (
	// importReplace deletes existing rows and replaces with imported data.
	importReplace importStrategy = iota
	// importMerge adds missing rows without deleting existing ones (INSERT OR IGNORE).
	importMerge
	// importMergeNewest keeps the row with the most recent timestamp from either source.
	importMergeNewest
	// importMergeActions adds missing action/warning rows, skipping duplicates by natural key.
	importMergeActions
	// importMergeUserProfiles upserts user_profiles, keeping the row with the most recent updated_at.
	importMergeUserProfiles
	// importMergeDailyActivity merges user_daily_activity by keeping the larger
	// count on (user_id, day_date) conflicts. Per-day counters are monotonic
	// per source, so MAX is a safe upper bound (see mergeDailyActivityTx).
	importMergeDailyActivity
	// importMergeForumTopics upserts forum_topics, keeping the row with the most
	// recent updated_at on (chat_id, thread_id) conflicts.
	importMergeForumTopics
	// importMergeTokenUsage merges token_usage by keeping the larger input/output
	// counts on (model, service, day_date) conflicts. Per-day counters are
	// monotonic per source, so MAX is a safe upper bound and keeps re-imports
	// idempotent (see mergeTokenUsageTx).
	importMergeTokenUsage
	// importMergeModerationStats merges moderation_stats by keeping the larger
	// count on (stat, day_date) conflicts. Per-day counters are monotonic per
	// source, so MAX is a safe upper bound and keeps re-imports idempotent.
	importMergeModerationStats
)

// tableImportStrategy returns the import strategy for each table.
func tableImportStrategy(table string) importStrategy {
	switch table {
	case "message_info", "messages_for_deletion":
		return importMerge
	case "actions":
		return importMergeActions
	case "warnings":
		return importMergeActions
	case "user_names_history":
		return importMergeActions
	case "user_daily_activity":
		return importMergeDailyActivity
	case "forum_topics":
		return importMergeForumTopics
	case "scheduled_events":
		return importMergeNewest
	case "user_profiles":
		return importMergeUserProfiles
	case "token_usage":
		return importMergeTokenUsage
	case "moderation_stats":
		return importMergeModerationStats
	default:
		return importReplace // config_values, muted_users
	}
}

const tempDBPrefix = "moderation_export_"

var sqliteColumnTypePattern = regexp.MustCompile(`^[A-Za-z0-9_ (),]+$`)

// ExportToLocalFile copies all data from the current database (local or remote)
// into a new local SQLite file and returns the file path.
// When includeConfig is false, the config_values table is skipped.
func (db *DB) ExportToLocalFile(dir string, includeConfig bool) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, tempDBPrefix+"*.db")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	// Open a fresh local SQLite at the temp path
	localConn, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to open temp db: %w", err)
	}
	defer localConn.Close()

	localDB := &DB{conn: localConn, provider: ProviderLocal}
	if err := localDB.createTables(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to create tables in temp db: %w", err)
	}

	// Copy each table from source → temp local
	for _, table := range transferTableNames {
		if !includeConfig && table == "config_values" {
			continue
		}
		if err := copyTable(db.conn, localConn, table); err != nil {
			os.Remove(tmpPath)
			return "", fmt.Errorf("failed to copy table %s: %w", table, err)
		}
	}

	log.Printf("Database exported to %s (includeConfig=%v)", tmpPath, includeConfig)
	return tmpPath, nil
}

// ImportFromLocalFile reads a local SQLite file and merges its data into the
// current database (local or remote). The merge strategy is table-specific:
//   - message_info, messages_for_deletion: add missing rows, keep existing (INSERT OR IGNORE)
//   - actions, warnings: add missing rows, skip duplicates by natural key
//   - scheduled_events: keep the most recent last_fired_at from either source
//   - user_profiles: keep the row with the most recent updated_at from either source
//   - forum_topics: keep the topic name with the most recent updated_at
//   - muted_users, config_values: full replace with imported data
//
// When includeConfig is false, the config_values table is skipped.
func (db *DB) ImportFromLocalFile(localPath string, includeConfig bool) error {
	localConn, err := sql.Open("sqlite", localPath)
	if err != nil {
		return fmt.Errorf("failed to open uploaded db: %w", err)
	}
	defer localConn.Close()
	if err := validateSQLiteDatabase(localConn); err != nil {
		return fmt.Errorf("uploaded db failed integrity check: %w", err)
	}

	// Ensure destination tables/columns exist for any new schema in the source
	if err := syncSchema(localConn, db.conn); err != nil {
		return fmt.Errorf("failed to sync schema: %w", err)
	}

	// Use a transaction so the import is atomic - no partial data on failure
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, table := range transferTableNames {
		if !includeConfig && table == "config_values" {
			continue
		}

		strategy := tableImportStrategy(table)
		quotedTable, err := quoteSQLiteIdentifier(table)
		if err != nil {
			return fmt.Errorf("invalid table name %q: %w", table, err)
		}
		switch strategy {
		case importReplace:
			if _, err := tx.Exec(fmt.Sprintf("DELETE FROM %s", quotedTable)); err != nil {
				return fmt.Errorf("failed to clear table %s: %w", table, err)
			}
			if err := copyTableTx(localConn, tx, table); err != nil {
				return fmt.Errorf("failed to import table %s: %w", table, err)
			}

		case importMerge:
			if err := mergeTableTx(localConn, tx, table); err != nil {
				return fmt.Errorf("failed to merge table %s: %w", table, err)
			}

		case importMergeActions:
			if err := mergeAutoIncrementTableTx(localConn, tx, table); err != nil {
				return fmt.Errorf("failed to merge table %s: %w", table, err)
			}

		case importMergeNewest:
			if err := mergeScheduledEventsTx(localConn, tx); err != nil {
				return fmt.Errorf("failed to merge scheduled_events: %w", err)
			}

		case importMergeUserProfiles:
			if err := mergeUserProfilesTx(localConn, tx); err != nil {
				return fmt.Errorf("failed to merge user_profiles: %w", err)
			}

		case importMergeDailyActivity:
			if err := mergeDailyActivityTx(localConn, tx); err != nil {
				return fmt.Errorf("failed to merge user_daily_activity: %w", err)
			}

		case importMergeForumTopics:
			if err := mergeForumTopicsTx(localConn, tx); err != nil {
				return fmt.Errorf("failed to merge forum_topics: %w", err)
			}

		case importMergeTokenUsage:
			if err := mergeTokenUsageTx(localConn, tx); err != nil {
				return fmt.Errorf("failed to merge token_usage: %w", err)
			}

		case importMergeModerationStats:
			if err := mergeModerationStatsTx(localConn, tx); err != nil {
				return fmt.Errorf("failed to merge moderation_stats: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit import transaction: %w", err)
	}

	log.Printf("Database imported from %s (includeConfig=%v)", localPath, includeConfig)
	return nil
}

// CleanupTempExports removes temporary export files from the given directory.
func CleanupTempExports(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), tempDBPrefix) {
			path := filepath.Join(dir, e.Name())
			if err := os.Remove(path); err != nil {
				log.Printf("Failed to remove temp export %s: %v", path, err)
			} else {
				log.Printf("Cleaned up temp export: %s", path)
			}
		}
	}
}

// copyTable selects all rows from src table and inserts them into dst table.
// Columns are discovered dynamically from the intersection of both schemas.
func copyTable(src, dst *sql.DB, table string) error {
	tx, err := dst.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if err := copyTableTx(src, tx, table); err != nil {
		return err
	}
	return tx.Commit()
}

// copyTableTx selects all rows from src table and inserts them into an existing transaction.
// Only columns present in both the source and destination are transferred.
func copyTableTx(src *sql.DB, tx *sql.Tx, table string) error {
	srcCols, err := getTableColumns(src, table)
	if err != nil || len(srcCols) == 0 {
		// Source table doesn't exist or has no columns - skip silently
		return nil
	}

	// Get destination columns via the transaction's connection
	dstCols, err := getTableColumnsTx(tx, table)
	if err != nil || len(dstCols) == 0 {
		return nil
	}

	// Use only columns that exist in both source and destination
	dstSet := make(map[string]bool, len(dstCols))
	for _, c := range dstCols {
		dstSet[c] = true
	}
	var cols []string
	for _, c := range srcCols {
		if dstSet[c] {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return nil
	}

	colList, err := quoteSQLiteIdentifierList(cols)
	if err != nil {
		return err
	}
	placeholders := strings.Join(makeQMarks(len(cols)), ", ")
	quotedTable, err := quoteSQLiteIdentifier(table)
	if err != nil {
		return err
	}

	rows, err := src.Query(fmt.Sprintf("SELECT %s FROM %s", colList, quotedTable))
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	stmt, err := tx.Prepare(fmt.Sprintf("INSERT OR REPLACE INTO %s (%s) VALUES (%s)", quotedTable, colList, placeholders))
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if _, err := stmt.Exec(values...); err != nil {
			return fmt.Errorf("insert: %w", err)
		}
	}
	return rows.Err()
}

// mergeTableTx inserts rows from src into an existing transaction using INSERT OR IGNORE.
// Existing rows (by primary key) are preserved; only missing rows are added.
func mergeTableTx(src *sql.DB, tx *sql.Tx, table string) error {
	srcCols, err := getTableColumns(src, table)
	if err != nil || len(srcCols) == 0 {
		return nil
	}

	dstCols, err := getTableColumnsTx(tx, table)
	if err != nil || len(dstCols) == 0 {
		return nil
	}

	dstSet := make(map[string]bool, len(dstCols))
	for _, c := range dstCols {
		dstSet[c] = true
	}
	var cols []string
	for _, c := range srcCols {
		if dstSet[c] {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return nil
	}

	colList, err := quoteSQLiteIdentifierList(cols)
	if err != nil {
		return err
	}
	placeholders := strings.Join(makeQMarks(len(cols)), ", ")
	quotedTable, err := quoteSQLiteIdentifier(table)
	if err != nil {
		return err
	}

	rows, err := src.Query(fmt.Sprintf("SELECT %s FROM %s", colList, quotedTable))
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	stmt, err := tx.Prepare(fmt.Sprintf("INSERT OR IGNORE INTO %s (%s) VALUES (%s)", quotedTable, colList, placeholders))
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if _, err := stmt.Exec(values...); err != nil {
			return fmt.Errorf("insert: %w", err)
		}
	}
	return rows.Err()
}

// mergeAutoIncrementTableTx merges rows from tables with AUTOINCREMENT id (actions, warnings).
// It skips the id column and inserts only rows whose natural key doesn't already exist.
// For actions: natural key is (user_id, chat_id, action_type, timestamp).
// For warnings: natural key is (user_id, chat_id, warned_at, message_id).
func mergeAutoIncrementTableTx(src *sql.DB, tx *sql.Tx, table string) error {
	srcCols, err := getTableColumns(src, table)
	if err != nil || len(srcCols) == 0 {
		return nil
	}

	dstCols, err := getTableColumnsTx(tx, table)
	if err != nil || len(dstCols) == 0 {
		return nil
	}

	// Build column intersection, excluding "id" (auto-generated)
	dstSet := make(map[string]bool, len(dstCols))
	for _, c := range dstCols {
		dstSet[c] = true
	}
	var cols []string
	for _, c := range srcCols {
		if c == "id" {
			continue
		}
		if dstSet[c] {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return nil
	}

	// Determine the natural key columns for duplicate detection
	var naturalKey []string
	switch table {
	case "actions":
		naturalKey = []string{"user_id", "chat_id", "action_type", "timestamp"}
	case "warnings":
		naturalKey = []string{"user_id", "chat_id", "warned_at", "message_id"}
	case "user_names_history":
		naturalKey = []string{"user_id", "username", "display_name", "changed_at"}
	}

	colList, err := quoteSQLiteIdentifierList(cols)
	if err != nil {
		return err
	}
	placeholders := strings.Join(makeQMarks(len(cols)), ", ")
	quotedTable, err := quoteSQLiteIdentifier(table)
	if err != nil {
		return err
	}

	rows, err := src.Query(fmt.Sprintf("SELECT %s FROM %s", colList, quotedTable))
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	// Build index map: col name → position in cols slice
	colIdx := make(map[string]int, len(cols))
	for i, c := range cols {
		colIdx[c] = i
	}

	// Prepare existence check if we have a natural key
	var checkStmt *sql.Stmt
	if len(naturalKey) > 0 {
		var whereParts []string
		for _, k := range naturalKey {
			quotedKey, err := quoteSQLiteIdentifier(k)
			if err != nil {
				return err
			}
			whereParts = append(whereParts, quotedKey+" = ?")
		}
		checkQuery := fmt.Sprintf("SELECT 1 FROM %s WHERE %s LIMIT 1", quotedTable, strings.Join(whereParts, " AND "))
		checkStmt, err = tx.Prepare(checkQuery)
		if err != nil {
			return fmt.Errorf("prepare check: %w", err)
		}
		defer checkStmt.Close()
	}

	insertStmt, err := tx.Prepare(fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quotedTable, colList, placeholders))
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer insertStmt.Close()

	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		// Check for duplicate by natural key
		if checkStmt != nil {
			keyVals := make([]interface{}, len(naturalKey))
			for i, k := range naturalKey {
				keyVals[i] = values[colIdx[k]]
			}
			var exists int
			if err := checkStmt.QueryRow(keyVals...).Scan(&exists); err == nil {
				continue // duplicate found, skip
			}
		}

		if _, err := insertStmt.Exec(values...); err != nil {
			return fmt.Errorf("insert: %w", err)
		}
	}
	return rows.Err()
}

// mergeScheduledEventsTx merges scheduled_events keeping the most recent
// last_fired_at from either source to avoid re-running events.
func mergeScheduledEventsTx(src *sql.DB, tx *sql.Tx) error {
	rows, err := src.Query("SELECT event_name, scheduled_time, last_fired_at, started_at FROM scheduled_events")
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var eventName, scheduledTime, srcFiredAt string
		var srcStartedAt *string
		if err := rows.Scan(&eventName, &scheduledTime, &srcFiredAt, &srcStartedAt); err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		// Check if destination already has this event
		var dstFiredAt string
		err := tx.QueryRow("SELECT last_fired_at FROM scheduled_events WHERE event_name = ?", eventName).Scan(&dstFiredAt)
		if errors.Is(err, sql.ErrNoRows) {
			// Event doesn't exist in destination - insert it; clear started_at to avoid stale locks
			if _, err := tx.Exec(
				"INSERT INTO scheduled_events (event_name, scheduled_time, last_fired_at, started_at) VALUES (?, ?, ?, NULL)",
				eventName, scheduledTime, srcFiredAt,
			); err != nil {
				return fmt.Errorf("insert event %s: %w", eventName, err)
			}
			continue
		} else if err != nil {
			return fmt.Errorf("check event %s: %w", eventName, err)
		}

		// Both exist - keep the most recent last_fired_at; clear started_at to avoid stale locks
		if srcFiredAt > dstFiredAt {
			if _, err := tx.Exec(
				"UPDATE scheduled_events SET last_fired_at = ?, started_at = NULL WHERE event_name = ?",
				srcFiredAt, eventName,
			); err != nil {
				return fmt.Errorf("update event %s: %w", eventName, err)
			}
		}
	}
	return rows.Err()
}

// mergeUserProfilesTx merges user_profiles, keeping the row with the most
// recent updated_at from either source. New profiles in source are inserted;
// existing profiles are updated only if the source row is newer. The
// first_seen_at column is reconciled independently of updated_at - the earliest
// non-empty value from either source always wins.
func mergeUserProfilesTx(src *sql.DB, tx *sql.Tx) error {
	rows, err := src.Query(`SELECT user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at FROM user_profiles`)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var userID int64
		var username *string
		var profile, reputation, tgProfileAnalysis, srcFirstSeen, createdAt, updatedAt string
		if err := rows.Scan(&userID, &username, &profile, &reputation, &tgProfileAnalysis, &srcFirstSeen, &createdAt, &updatedAt); err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		var dstUpdatedAt, dstFirstSeen string
		err := tx.QueryRow("SELECT updated_at, first_seen_at FROM user_profiles WHERE user_id = ?", userID).Scan(&dstUpdatedAt, &dstFirstSeen)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.Exec(
				`INSERT INTO user_profiles (user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				userID, username, profile, reputation, tgProfileAnalysis, srcFirstSeen, createdAt, updatedAt,
			); err != nil {
				return fmt.Errorf("insert profile %d: %w", userID, err)
			}
			continue
		} else if err != nil {
			return fmt.Errorf("check profile %d: %w", userID, err)
		}

		earliestFirstSeen := earlierFirstSeen(srcFirstSeen, dstFirstSeen)
		if updatedAt > dstUpdatedAt {
			if _, err := tx.Exec(
				`UPDATE user_profiles SET username = ?, profile = ?, reputation = ?, tg_profile_analysis = ?, first_seen_at = ?, created_at = ?, updated_at = ? WHERE user_id = ?`,
				username, profile, reputation, tgProfileAnalysis, earliestFirstSeen, createdAt, updatedAt, userID,
			); err != nil {
				return fmt.Errorf("update profile %d: %w", userID, err)
			}
		} else if earliestFirstSeen != dstFirstSeen {
			if _, err := tx.Exec(
				`UPDATE user_profiles SET first_seen_at = ? WHERE user_id = ?`,
				earliestFirstSeen, userID,
			); err != nil {
				return fmt.Errorf("update first_seen %d: %w", userID, err)
			}
		}
	}
	return rows.Err()
}

// earlierFirstSeen returns the earlier of two first_seen_at values, treating an
// empty string as "unknown" (never preferred over a real timestamp). Comparison
// is lexical, matching how timestamps are stored and ordered elsewhere.
func earlierFirstSeen(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// mergeDailyActivityTx merges user_daily_activity rows from src into the
// destination, keeping the larger count on (user_id, day_date) conflicts
// (counts are running per-day totals, never decreasing, so MAX is a safe
// per-source upper bound).
func mergeDailyActivityTx(src *sql.DB, tx *sql.Tx) error {
	rows, err := src.Query(`SELECT user_id, day_date, count FROM user_daily_activity`)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	stmt, err := tx.Prepare(`INSERT INTO user_daily_activity (user_id, day_date, count)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id, day_date) DO UPDATE SET count = MAX(count, excluded.count)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		var userID int64
		var day string
		var count int
		if err := rows.Scan(&userID, &day, &count); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if _, err := stmt.Exec(userID, day, count); err != nil {
			return fmt.Errorf("upsert daily activity (user=%d day=%s): %w", userID, day, err)
		}
	}
	return rows.Err()
}

// mergeTokenUsageTx merges token_usage rows from src into the destination,
// keeping the larger input/output counts on (model, service, day_date)
// conflicts. Per-day counters are monotonic per source, so MAX is a safe upper
// bound; it also keeps re-imports idempotent (re-importing the same data never
// inflates the totals, unlike a SUM merge).
func mergeTokenUsageTx(src *sql.DB, tx *sql.Tx) error {
	rows, err := src.Query(`SELECT model, service, day_date, input_tokens, output_tokens FROM token_usage`)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	stmt, err := tx.Prepare(`INSERT INTO token_usage (model, service, day_date, input_tokens, output_tokens)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(model, service, day_date) DO UPDATE SET
			input_tokens = MAX(input_tokens, excluded.input_tokens),
			output_tokens = MAX(output_tokens, excluded.output_tokens)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		var model, service, day string
		var inTok, outTok int64
		if err := rows.Scan(&model, &service, &day, &inTok, &outTok); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if _, err := stmt.Exec(model, service, day, inTok, outTok); err != nil {
			return fmt.Errorf("upsert token usage (model=%s service=%s day=%s): %w", model, service, day, err)
		}
	}
	return rows.Err()
}

// mergeModerationStatsTx merges moderation_stats rows from src into the
// destination, keeping the larger count on (stat, day_date) conflicts. Per-day
// counters are monotonic per source, so MAX is a safe upper bound and keeps
// re-imports idempotent.
func mergeModerationStatsTx(src *sql.DB, tx *sql.Tx) error {
	rows, err := src.Query(`SELECT stat, day_date, count FROM moderation_stats`)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	stmt, err := tx.Prepare(`INSERT INTO moderation_stats (stat, day_date, count)
		VALUES (?, ?, ?)
		ON CONFLICT(stat, day_date) DO UPDATE SET
			count = MAX(count, excluded.count)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		var stat, day string
		var count int64
		if err := rows.Scan(&stat, &day, &count); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if _, err := stmt.Exec(stat, day, count); err != nil {
			return fmt.Errorf("upsert moderation stat (stat=%s day=%s): %w", stat, day, err)
		}
	}
	return rows.Err()
}

// mergeForumTopicsTx merges forum_topics, keeping the row with the most recent
// updated_at from either source on (chat_id, thread_id) conflicts. New topics
// in the source are inserted; existing topics are updated only when the source
// row is newer.
func mergeForumTopicsTx(src *sql.DB, tx *sql.Tx) error {
	rows, err := src.Query(`SELECT chat_id, thread_id, name, updated_at FROM forum_topics`)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var chatID int64
		var threadID int
		var name, updatedAt string
		if err := rows.Scan(&chatID, &threadID, &name, &updatedAt); err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		var dstUpdatedAt string
		err := tx.QueryRow(
			"SELECT updated_at FROM forum_topics WHERE chat_id = ? AND thread_id = ?",
			chatID, threadID).Scan(&dstUpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.Exec(
				`INSERT INTO forum_topics (chat_id, thread_id, name, updated_at) VALUES (?, ?, ?, ?)`,
				chatID, threadID, name, updatedAt,
			); err != nil {
				return fmt.Errorf("insert topic %d/%d: %w", chatID, threadID, err)
			}
			continue
		} else if err != nil {
			return fmt.Errorf("check topic %d/%d: %w", chatID, threadID, err)
		}

		if updatedAt > dstUpdatedAt {
			if _, err := tx.Exec(
				`UPDATE forum_topics SET name = ?, updated_at = ? WHERE chat_id = ? AND thread_id = ?`,
				name, updatedAt, chatID, threadID,
			); err != nil {
				return fmt.Errorf("update topic %d/%d: %w", chatID, threadID, err)
			}
		}
	}
	return rows.Err()
}

// getTableColumns returns column names for a table using PRAGMA table_info.
func getTableColumns(db *sql.DB, table string) ([]string, error) {
	quotedTable, err := quoteSQLiteIdentifier(table)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quotedTable))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// getTableColumnsTx returns column names for a table within a transaction.
func getTableColumnsTx(tx *sql.Tx, table string) ([]string, error) {
	quotedTable, err := quoteSQLiteIdentifier(table)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", quotedTable))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// syncSchema ensures the destination DB has all tables and columns that exist
// in the source DB. Missing tables are created; missing columns are added via
// ALTER TABLE ADD COLUMN.
func syncSchema(src, dst *sql.DB) error {
	for _, table := range transferTableNames {
		srcCols, err := getTableColumnsWithTypes(src, table)
		if err != nil || len(srcCols) == 0 {
			continue // table doesn't exist in source
		}

		dstCols, err := getTableColumns(dst, table)
		if err != nil {
			return fmt.Errorf("reading dst schema for %s: %w", table, err)
		}

		if len(dstCols) == 0 {
			// Table doesn't exist in destination - skip (createTables already ran)
			continue
		}

		// Find columns in source but not in destination
		dstSet := make(map[string]bool, len(dstCols))
		for _, c := range dstCols {
			dstSet[c] = true
		}
		for _, sc := range srcCols {
			if !dstSet[sc.name] {
				quotedTable, err := quoteSQLiteIdentifier(table)
				if err != nil {
					return err
				}
				quotedColumn, err := quoteSQLiteIdentifier(sc.name)
				if err != nil {
					log.Printf("Warning: skipping invalid column %s.%s: %v", table, sc.name, err)
					continue
				}
				colType, err := sanitizeSQLiteColumnType(sc.colType)
				if err != nil {
					log.Printf("Warning: skipping column %s.%s with unsafe type %q: %v", table, sc.name, sc.colType, err)
					continue
				}
				alter := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", quotedTable, quotedColumn, colType)
				if _, err := dst.Exec(alter); err != nil {
					log.Printf("Warning: could not add column %s.%s: %v", table, sc.name, err)
				} else {
					log.Printf("Schema sync: added column %s.%s (%s)", table, sc.name, sc.colType)
				}
			}
		}
	}
	return nil
}

type colInfo struct {
	name    string
	colType string
}

// getTableColumnsWithTypes returns column names and types.
func getTableColumnsWithTypes(db *sql.DB, table string) ([]colInfo, error) {
	quotedTable, err := quoteSQLiteIdentifier(table)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quotedTable))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []colInfo
	for rows.Next() {
		var cid int
		var name, ct string
		var notNull, pk int
		var dfltValue *string
		if err := rows.Scan(&cid, &name, &ct, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, colInfo{name: name, colType: ct})
	}
	return cols, rows.Err()
}

func makeQMarks(n int) []string {
	q := make([]string, n)
	for i := range q {
		q[i] = "?"
	}
	return q
}

func validateSQLiteDatabase(conn *sql.DB) error {
	var result string
	if err := conn.QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(result)) != "ok" {
		return fmt.Errorf("quick_check returned %q", result)
	}
	return nil
}

func quoteSQLiteIdentifier(name string) (string, error) {
	if strings.TrimSpace(name) == "" || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("invalid SQLite identifier")
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
}

func quoteSQLiteIdentifierList(names []string) (string, error) {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		q, err := quoteSQLiteIdentifier(name)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, q)
	}
	return strings.Join(quoted, ", "), nil
}

func sanitizeSQLiteColumnType(colType string) (string, error) {
	colType = strings.TrimSpace(colType)
	if colType == "" {
		return "TEXT", nil
	}
	if len(colType) > 80 || !sqliteColumnTypePattern.MatchString(colType) {
		return "", fmt.Errorf("unsafe SQLite column type")
	}
	return colType, nil
}
