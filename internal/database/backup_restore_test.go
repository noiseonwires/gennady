// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file specifies the behavior of the database backup/restore
// (dump/restore) subsystem from the operator's point of view:
//
//   - Backing up and restoring into an empty database must lose NO user data.
//   - Restoring a backup into a database that already has data is ADDITIVE:
//     existing rows are never deleted, and conflicting rows resolve by a
//     documented, safe rule (newest wins / higher count wins / dedupe).
//   - Ephemeral, machine-local web session tokens are intentionally excluded
//     from backups - they must not travel between instances via a restore.
//     Token usage counters, by contrast, ARE backed up so cost/usage history
//     survives a database migration or clone.
//
// These are written as executable specifications so a future change to the
// transfer logic that would silently drop or corrupt data fails loudly here.

// dumpTable returns a deterministic, content-based snapshot of a table: one
// sorted string per row, "col=value;" pairs, with the given columns excluded.
// Comparing two dumps proves the meaningful data is identical regardless of
// row order or surrogate keys.
func dumpTable(t *testing.T, db *DB, table string, excludeCols ...string) []string {
	t.Helper()
	exclude := make(map[string]bool, len(excludeCols))
	for _, c := range excludeCols {
		exclude[c] = true
	}
	rows, err := db.conn.Query("SELECT * FROM " + table)
	require.NoError(t, err)
	defer rows.Close()

	cols, err := rows.Columns()
	require.NoError(t, err)

	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		var sb strings.Builder
		for i, c := range cols {
			if exclude[c] {
				continue
			}
			fmt.Fprintf(&sb, "%s=%v;", c, vals[i])
		}
		out = append(out, sb.String())
	}
	require.NoError(t, rows.Err())
	sort.Strings(out)
	return out
}

func tableCount(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, db.conn.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

// seedAllBackedUpTables fills every table that participates in backups with a
// few rows of representative data. Timestamps use UTC so the SQLite
// datetime()-normalized columns compare byte-for-byte across a round-trip.
func seedAllBackedUpTables(t *testing.T, db *DB) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)

	require.NoError(t, db.SetAllConfigValues(map[string]string{
		"bot_token":     "secret-token",
		"language":      "en",
		"admin.chat_id": "-100123",
	}))

	require.NoError(t, db.AddMutedUser(&MutedUser{
		UserID: 1, Username: "alice", ChatID: -100, MutedBy: 9, MutedAt: now,
		UnmuteAt: now.Add(time.Hour), Reason: "spam", IsActive: true, MessageID: 11,
	}))
	require.NoError(t, db.AddMutedUser(&MutedUser{
		UserID: 2, Username: "bob", ChatID: -100, MutedBy: 9, MutedAt: now.Add(time.Minute),
		UnmuteAt: now.Add(2 * time.Hour), Reason: "cruel", IsActive: true, MessageID: 12, IsCruel: true,
	}))

	require.NoError(t, db.AddWarning(&Warning{
		UserID: 1, Username: "alice", ChatID: -100, WarnedBy: 9, WarnedAt: now, Reason: "rude", MessageID: 11,
	}))

	require.NoError(t, db.AddMessageForDeletionWithPinnedStatus(11, -100, false))
	require.NoError(t, db.AddMessageForDeletionWithPinnedStatus(12, -100, true))

	require.NoError(t, db.LogAction(&Action{
		UserID: 1, Username: "alice", AdminID: 9, AdminName: "admin",
		ActionType: "mute", Duration: 60, Reason: "spam", ChatID: -100, MessageID: 11, Timestamp: now,
	}))

	// message_info + general-profile tracking (names, first-seen, daily activity)
	_, err := db.RecordIncomingMessage(
		&MessageInfo{MessageID: 11, ChatID: -100, UserID: 1, Username: "alice", Text: "hello", Timestamp: now},
		IncomingMessageOpts{TrackProfile: true, Username: "alice", DisplayName: "Alice", DayDate: "2026-06-09"},
	)
	require.NoError(t, err)
	_, err = db.RecordIncomingMessage(
		&MessageInfo{MessageID: 12, ChatID: -100, UserID: 2, Username: "bob", Text: "world", Timestamp: now},
		IncomingMessageOpts{TrackProfile: true, Username: "bob", DisplayName: "Bob", DayDate: "2026-06-09"},
	)
	require.NoError(t, err)

	require.NoError(t, db.EnsureScheduledEventExists("daily_summary", "20:00"))
	require.NoError(t, db.RecordEventFiredAt("daily_summary", "20:00", now))

	require.NoError(t, db.UpsertUserProfile(&UserProfile{
		UserID: 1, Username: "alice", Profile: "generally helpful", Reputation: "good",
	}))

	require.NoError(t, db.UpsertForumTopic(-100, 7, "Support"))
	require.NoError(t, db.UpsertForumTopic(-100, 8, "Off-Topic"))

	require.NoError(t, db.RecordTokenUsage("gpt-4o", "moderation", "2026-06-09", 1200, 340))
	require.NoError(t, db.RecordTokenUsage("gpt-4o-mini", "summary", "2026-06-09", 800, 120))
}

// backedUpTables lists the tables a backup is expected to carry, paired with
// the columns to ignore when comparing content (surrogate auto-increment ids,
// bookkeeping timestamps, and lock state that is reset on restore by design).
var backedUpTables = []struct {
	name    string
	exclude []string
}{
	{"config_values", []string{"updated_at"}}, // updated_at is write-time bookkeeping
	{"muted_users", nil},
	{"warnings", []string{"id"}}, // id is a surrogate AUTOINCREMENT key
	{"messages_for_deletion", nil},
	{"actions", []string{"id"}},
	{"message_info", nil},
	{"scheduled_events", []string{"started_at"}}, // lock is intentionally reset to NULL
	{"user_profiles", nil},
	{"user_names_history", []string{"id"}},
	{"user_daily_activity", nil},
	{"token_usage", nil},
	{"forum_topics", []string{"updated_at"}}, // updated_at is write-time bookkeeping
}

// TestBackupRestore_IntoEmptyDB_LosesNoData is the headline guarantee: a full
// export of a populated database, restored into a brand-new empty database,
// reproduces every table's content exactly.
func TestBackupRestore_IntoEmptyDB_LosesNoData(t *testing.T) {
	src := newTestDB(t)
	seedAllBackedUpTables(t, src)

	// Snapshot the source before the round-trip.
	before := make(map[string][]string)
	for _, tbl := range backedUpTables {
		before[tbl.name] = dumpTable(t, src, tbl.name, tbl.exclude...)
		require.NotEmpty(t, before[tbl.name], "seed should have populated %s", tbl.name)
	}

	// Dump to a backup file, then restore into a fresh, empty database.
	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, true)
	require.NoError(t, err)

	dst := newTestDB(t)
	require.NoError(t, dst.ImportFromLocalFile(path, true))

	// Every table must match the source content exactly.
	for _, tbl := range backedUpTables {
		after := dumpTable(t, dst, tbl.name, tbl.exclude...)
		assert.Equal(t, before[tbl.name], after, "table %s changed across backup/restore", tbl.name)
	}
}

// TestBackup_ExcludesWebSessions documents that web session token hashes are
// deliberately NOT part of a backup - they are machine-local auth state and must
// not travel between instances via a restore. Token usage counters, by
// contrast, ARE backed up (see backedUpTables) so cost/usage history survives a
// migration or clone.
func TestBackup_ExcludesWebSessions(t *testing.T) {
	src := newTestDB(t)
	require.NoError(t, src.SaveWebSession("sha256:abc", time.Now().Add(time.Hour)))
	require.NoError(t, src.RecordTokenUsage("gpt", "azure", "2026-06-09", 100, 50))

	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, true)
	require.NoError(t, err)

	dst := newTestDB(t)
	require.NoError(t, dst.ImportFromLocalFile(path, true))

	// Web sessions must not cross over; token usage must.
	assert.Equal(t, 0, tableCount(t, dst, "web_sessions"))
	assert.Equal(t, 1, tableCount(t, dst, "token_usage"))
}

// TestRestore_IsAdditive_NeverDeletesExistingData is the safety contract for
// restoring a backup onto a database that is already in use: importing must
// only ADD missing rows; it must never wipe out data the destination already
// has.
func TestRestore_IsAdditive_NeverDeletesExistingData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	// Backup contains one user's history.
	src := newTestDB(t)
	storeMsg(t, src, 1, -100, 1, "alice", "from backup", now)
	require.NoError(t, src.AddWarning(&Warning{UserID: 1, ChatID: -100, WarnedAt: now, Reason: "old", MessageID: 1}))

	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, false)
	require.NoError(t, err)

	// Destination already has a DIFFERENT user's live data.
	dst := newTestDB(t)
	storeMsg(t, dst, 99, -100, 2, "bob", "live message", now)
	require.NoError(t, dst.AddWarning(&Warning{UserID: 2, ChatID: -100, WarnedAt: now, Reason: "live", MessageID: 99}))

	require.NoError(t, dst.ImportFromLocalFile(path, false))

	// Both the pre-existing live data AND the restored backup data must be present.
	live, err := dst.GetMessageInfo(99, -100)
	require.NoError(t, err)
	require.NotNil(t, live, "pre-existing live message must survive a restore")
	assert.Equal(t, "live message", live.Text)

	restored, err := dst.GetMessageInfo(1, -100)
	require.NoError(t, err)
	require.NotNil(t, restored, "backed-up message must be added by the restore")
	assert.Equal(t, "from backup", restored.Text)

	assert.Equal(t, 2, tableCount(t, dst, "warnings"), "both warnings must coexist")
}

// TestRestore_ExistingMessage_IsNotOverwritten documents the conflict rule for
// message_info / messages_for_deletion: the destination's existing row wins
// (INSERT OR IGNORE), so a restore can never clobber a newer live message with
// a stale backup copy that happens to share its key.
func TestRestore_ExistingMessage_IsNotOverwritten(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	src := newTestDB(t)
	storeMsg(t, src, 5, -100, 1, "alice", "STALE backup text", now)

	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, false)
	require.NoError(t, err)

	dst := newTestDB(t)
	storeMsg(t, dst, 5, -100, 1, "alice", "current live text", now)

	require.NoError(t, dst.ImportFromLocalFile(path, false))

	got, err := dst.GetMessageInfo(5, -100)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "current live text", got.Text, "live row must win over a stale backup row")
}

// TestRestore_ScheduledEvents_KeepNewestFiredTime documents that restoring a
// backup must not cause an already-fired event to run again: the most recent
// last_fired_at from either side is kept.
func TestRestore_ScheduledEvents_KeepNewestFiredTime(t *testing.T) {
	older := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 9, 8, 0, 0, 0, time.UTC)

	// Backup has an OLD fired time.
	src := newTestDB(t)
	require.NoError(t, src.EnsureScheduledEventExists("daily", "08:00"))
	require.NoError(t, src.RecordEventFiredAt("daily", "08:00", older))

	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, false)
	require.NoError(t, err)

	// Destination already fired it more recently.
	dst := newTestDB(t)
	require.NoError(t, dst.EnsureScheduledEventExists("daily", "08:00"))
	require.NoError(t, dst.RecordEventFiredAt("daily", "08:00", newer))

	require.NoError(t, dst.ImportFromLocalFile(path, false))

	ev, err := dst.GetScheduledEvent("daily")
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.WithinDuration(t, newer, ev.LastFiredAt, time.Second,
		"the newer fired time must be kept so the event does not re-run")
}

// TestRestore_UserProfiles_KeepNewestProfile documents that a restore must not
// overwrite a freshly regenerated AI profile with an older one from a backup.
func TestRestore_UserProfiles_KeepNewestProfile(t *testing.T) {
	src := newTestDB(t)
	require.NoError(t, src.UpsertUserProfile(&UserProfile{UserID: 1, Username: "alice", Profile: "OLD analysis", Reputation: "neutral"}))
	// Force the backup row to have an older updated_at.
	_, err := src.conn.Exec(`UPDATE user_profiles SET updated_at = ? WHERE user_id = 1`, "2026-01-01 00:00:00")
	require.NoError(t, err)

	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, false)
	require.NoError(t, err)

	dst := newTestDB(t)
	require.NoError(t, dst.UpsertUserProfile(&UserProfile{UserID: 1, Username: "alice", Profile: "NEW analysis", Reputation: "good"}))
	_, err = dst.conn.Exec(`UPDATE user_profiles SET updated_at = ? WHERE user_id = 1`, "2026-06-09 00:00:00")
	require.NoError(t, err)

	require.NoError(t, dst.ImportFromLocalFile(path, false))

	prof, err := dst.GetUserProfile(1)
	require.NoError(t, err)
	require.NotNil(t, prof)
	assert.Equal(t, "NEW analysis", prof.Profile, "the newer profile must be kept")
}

// TestRestore_Twice_DoesNotDuplicateActions documents that re-applying the same
// backup is idempotent for the auto-increment tables: actions/warnings are
// deduplicated by their natural key, so a double restore does not double the
// audit log.
func TestRestore_Twice_DoesNotDuplicateActions(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	src := newTestDB(t)
	require.NoError(t, src.LogAction(&Action{
		UserID: 1, AdminID: 9, AdminName: "admin", ActionType: "mute",
		ChatID: -100, MessageID: 11, Timestamp: now,
	}))

	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, false)
	require.NoError(t, err)

	dst := newTestDB(t)
	require.NoError(t, dst.ImportFromLocalFile(path, false))
	require.NoError(t, dst.ImportFromLocalFile(path, false)) // apply the same backup again

	assert.Equal(t, 1, tableCount(t, dst, "actions"),
		"re-importing the same backup must not duplicate the action")
}

// TestRestore_WithoutConfig_LeavesConfigUntouched documents that an operator
// can restore moderation data without overwriting the destination's own
// configuration: with includeConfig=false the config_values table is skipped.
func TestRestore_WithoutConfig_LeavesConfigUntouched(t *testing.T) {
	src := newTestDB(t)
	require.NoError(t, src.SetConfigValue("bot_token", "backup-token"))
	storeMsg(t, src, 1, -100, 1, "alice", "x", time.Now().UTC())

	dir := t.TempDir()
	path, err := src.ExportToLocalFile(dir, false) // export already drops config
	require.NoError(t, err)

	dst := newTestDB(t)
	require.NoError(t, dst.SetConfigValue("bot_token", "live-token"))

	require.NoError(t, dst.ImportFromLocalFile(path, false))

	tok, err := dst.GetConfigValue("bot_token")
	require.NoError(t, err)
	assert.Equal(t, "live-token", tok, "the destination's own config must be preserved")
}
