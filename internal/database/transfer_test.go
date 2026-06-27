// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Pure helpers ──

func TestTableImportStrategy(t *testing.T) {
	assert.Equal(t, importMerge, tableImportStrategy("message_info"))
	assert.Equal(t, importMerge, tableImportStrategy("messages_for_deletion"))
	assert.Equal(t, importMergeActions, tableImportStrategy("actions"))
	assert.Equal(t, importMergeActions, tableImportStrategy("warnings"))
	assert.Equal(t, importMergeActions, tableImportStrategy("user_names_history"))
	assert.Equal(t, importMergeDailyActivity, tableImportStrategy("user_daily_activity"))
	assert.Equal(t, importMergeNewest, tableImportStrategy("scheduled_events"))
	assert.Equal(t, importMergeUserProfiles, tableImportStrategy("user_profiles"))
	assert.Equal(t, importMergeForumTopics, tableImportStrategy("forum_topics"))
	assert.Equal(t, importMergeTokenUsage, tableImportStrategy("token_usage"))
	assert.Equal(t, importReplace, tableImportStrategy("config_values"))
	assert.Equal(t, importReplace, tableImportStrategy("muted_users"))
}

// TestTransferTableCoverage fails when a table created by createTables is not
// accounted for in the transfer logic: every schema table must be listed in
// either transferTableNames (it gets copied) or transferExcludedTables (it is
// intentionally skipped). This is the guard that stops a newly added table from
// silently being dropped from backups, clones and migrations - which is exactly
// how token_usage was missed.
func TestTransferTableCoverage(t *testing.T) {
	db := newTestDB(t)

	rows, err := db.conn.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	require.NoError(t, err)
	defer rows.Close()

	created := make(map[string]bool)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		created[name] = true
	}
	require.NoError(t, rows.Err())

	inTransfer := make(map[string]bool, len(transferTableNames))
	for _, n := range transferTableNames {
		inTransfer[n] = true
	}

	// 1. Every schema table must be either transferred or explicitly excluded.
	var uncovered []string
	for name := range created {
		if !inTransfer[name] && !transferExcludedTables[name] {
			uncovered = append(uncovered, name)
		}
	}
	assert.Emptyf(t, uncovered,
		"tables created by the schema but missing from BOTH transferTableNames and "+
			"transferExcludedTables - add each to one of them (copy it, or document why it is "+
			"intentionally skipped): %v", uncovered)

	// 2. Every declared transfer table must actually exist (catch typos/renames).
	for _, n := range transferTableNames {
		assert.Truef(t, created[n], "transferTableNames lists %q, which is not a real schema table", n)
	}
}

func TestMakeQMarks(t *testing.T) {
	assert.Equal(t, []string{}, makeQMarks(0))
	assert.Equal(t, []string{"?", "?", "?"}, makeQMarks(3))
}
func TestQuoteSQLiteIdentifier(t *testing.T) {
	q, err := quoteSQLiteIdentifier("table")
	require.NoError(t, err)
	assert.Equal(t, `"table"`, q)

	q, err = quoteSQLiteIdentifier(`we"ird`)
	require.NoError(t, err)
	assert.Equal(t, `"we""ird"`, q)

	_, err = quoteSQLiteIdentifier("")
	require.Error(t, err)
	_, err = quoteSQLiteIdentifier("with\x00null")
	require.Error(t, err)
}

func TestQuoteSQLiteIdentifierList(t *testing.T) {
	out, err := quoteSQLiteIdentifierList([]string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, `"a", "b"`, out)

	_, err = quoteSQLiteIdentifierList([]string{"a", ""})
	require.Error(t, err)
}

func TestSanitizeSQLiteColumnType(t *testing.T) {
	out, err := sanitizeSQLiteColumnType("")
	require.NoError(t, err)
	assert.Equal(t, "TEXT", out)

	out, err = sanitizeSQLiteColumnType("INTEGER")
	require.NoError(t, err)
	assert.Equal(t, "INTEGER", out)

	out, err = sanitizeSQLiteColumnType("VARCHAR (255)")
	require.NoError(t, err)
	assert.Equal(t, "VARCHAR (255)", out)

	_, err = sanitizeSQLiteColumnType("TEXT; DROP TABLE x")
	require.Error(t, err)
	_, err = sanitizeSQLiteColumnType(strings.Repeat("A", 100))
	require.Error(t, err)
}

func TestValidateSQLiteDatabase(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, validateSQLiteDatabase(db.conn))
}

// ── Export / Import round-trip ──

func TestExportImportRoundTrip(t *testing.T) {
	src := newTestDB(t)
	now := time.Now().UTC()

	// Seed a variety of tables.
	require.NoError(t, src.SetConfigValue("bot_token", "secret"))
	require.NoError(t, src.AddMutedUser(&MutedUser{
		UserID: 1, ChatID: -1, MutedBy: 2, MutedAt: now,
		UnmuteAt: now.Add(time.Hour), IsActive: true,
	}))
	require.NoError(t, src.AddWarning(&Warning{UserID: 1, ChatID: -1, WarnedAt: now, MessageID: 5}))
	require.NoError(t, src.LogAction(&Action{
		UserID: 1, AdminID: 2, AdminName: "a", ActionType: "mute", ChatID: -1, MessageID: 5, Timestamp: now,
	}))
	storeMsg(t, src, 5, -1, 1, "u", "hi", now)
	require.NoError(t, src.UpsertUserProfile(&UserProfile{UserID: 1, Username: "u", Profile: "p", Reputation: "good"}))
	require.NoError(t, src.EnsureScheduledEventExists("daily", "08:00"))
	require.NoError(t, src.RecordTokenUsage("gpt", "azure", now.Format("2006-01-02"), 10, 5))

	// Export (with config).
	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, true)
	require.NoError(t, err)
	require.FileExists(t, path)
	assert.True(t, strings.HasPrefix(filepath.Base(path), tempDBPrefix))

	// Import into a fresh destination DB.
	dst := newTestDB(t)
	require.NoError(t, dst.ImportFromLocalFile(path, true))

	// Verify a representative sample survived the trip.
	tok, err := dst.GetConfigValue("bot_token")
	require.NoError(t, err)
	assert.Equal(t, "secret", tok)

	prof, err := dst.GetUserProfile(1)
	require.NoError(t, err)
	require.NotNil(t, prof)
	assert.Equal(t, "p", prof.Profile)

	msg, err := dst.GetMessageInfo(5, -1)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "hi", msg.Text)

	ev, err := dst.GetScheduledEvent("daily")
	require.NoError(t, err)
	require.NotNil(t, ev)

	// muted_users is fully replaced on import; verify the row was copied via a
	// parser-free COUNT (timestamp text round-trips are covered elsewhere).
	muteCount, err := dst.GetActiveMuteCount()
	require.NoError(t, err)
	assert.Equal(t, int64(1), muteCount)

	// token_usage must round-trip too. This table was previously absent from the
	// transfer list, so it silently never copied - TestTransferTableCoverage now
	// guards against that class of bug.
	tokStats, err := dst.GetTokenUsageStats(now.Format("2006-01-02"))
	require.NoError(t, err)
	require.Len(t, tokStats, 1)
	assert.Equal(t, "gpt", tokStats[0].Model)
	assert.Equal(t, "azure", tokStats[0].Service)
	assert.Equal(t, int64(10), tokStats[0].TotalInput)
	assert.Equal(t, int64(5), tokStats[0].TotalOutput)
}

func TestExportImport_SkipConfig(t *testing.T) {
	src := newTestDB(t)
	require.NoError(t, src.SetConfigValue("bot_token", "secret"))
	storeMsg(t, src, 1, -1, 1, "u", "x", time.Now().UTC())

	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, false)
	require.NoError(t, err)

	dst := newTestDB(t)
	require.NoError(t, dst.SetConfigValue("bot_token", "destination-secret"))
	require.NoError(t, dst.ImportFromLocalFile(path, false))

	// config_values must be untouched when includeConfig=false.
	tok, err := dst.GetConfigValue("bot_token")
	require.NoError(t, err)
	assert.Equal(t, "destination-secret", tok)

	// Non-config data still imported.
	msg, err := dst.GetMessageInfo(1, -1)
	require.NoError(t, err)
	assert.NotNil(t, msg)
}

func TestImportMergeStrategies(t *testing.T) {
	src := newTestDB(t)
	now := time.Now().UTC()
	// Two messages same day in source -> activity count 2.
	for i := 1; i <= 2; i++ {
		_, err := src.RecordIncomingMessage(
			&MessageInfo{MessageID: i, ChatID: -1, UserID: 7, Username: "u", Text: "a", Timestamp: now},
			IncomingMessageOpts{TrackProfile: true, Username: "u", DisplayName: "U", DayDate: "2026-06-09"},
		)
		require.NoError(t, err)
	}
	require.NoError(t, src.UpsertUserProfile(&UserProfile{UserID: 7, Username: "u", Profile: "src", Reputation: "good"}))

	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, false)
	require.NoError(t, err)

	// Destination already has activity count 1 for the same (user, day) and a profile.
	dst := newTestDB(t)
	_, err = dst.RecordIncomingMessage(
		&MessageInfo{MessageID: 9, ChatID: -1, UserID: 7, Username: "u", Text: "b", Timestamp: now},
		IncomingMessageOpts{TrackProfile: true, Username: "u", DisplayName: "U", DayDate: "2026-06-09"},
	)
	require.NoError(t, err)

	require.NoError(t, dst.ImportFromLocalFile(path, false))

	// Daily activity merge keeps the larger of the two counts (MAX semantics).
	act, err := dst.GetUserDailyActivityRange(7, []string{"2026-06-09"})
	require.NoError(t, err)
	require.Len(t, act, 1)
	assert.Equal(t, 2, act[0].Count)

	// user_profiles upserted.
	prof, err := dst.GetUserProfile(7)
	require.NoError(t, err)
	require.NotNil(t, prof)
}

func TestImportFromLocalFile_BadPath(t *testing.T) {
	dst := newTestDB(t)
	err := dst.ImportFromLocalFile(filepath.Join(t.TempDir(), "does-not-exist.db"), false)
	require.Error(t, err)
}
func TestCleanupTempExports(t *testing.T) {
	dir := t.TempDir()
	// Create a temp-export-named file and an unrelated file.
	exp := filepath.Join(dir, tempDBPrefix+"abc.db")
	require.NoError(t, os.WriteFile(exp, []byte("x"), 0600))
	keep := filepath.Join(dir, "keepme.txt")
	require.NoError(t, os.WriteFile(keep, []byte("y"), 0600))

	CleanupTempExports(dir)

	assert.NoFileExists(t, exp)
	assert.FileExists(t, keep)

	// Non-existent dir is a no-op (no panic).
	assert.NotPanics(t, func() { CleanupTempExports(filepath.Join(dir, "nope")) })
}
