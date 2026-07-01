// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB spins up a fresh local SQLite database backed by a temp file and
// registers cleanup. A file (rather than :memory:) is used because the
// database/sql connection pool can otherwise hand out independent in-memory
// databases per connection.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := InitLocal(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestResolveProvider(t *testing.T) {
	assert.Equal(t, ProviderLocal, ResolveProvider("local", "", ""))
	assert.Equal(t, ProviderRemote, ResolveProvider("remote", "", ""))
	assert.Equal(t, ProviderRemote, ResolveProvider("", "libsql://x", "token"))
	assert.Equal(t, ProviderLocal, ResolveProvider("", "", ""))
	assert.Equal(t, ProviderLocal, ResolveProvider("", "libsql://x", "")) // url but no token
	assert.Equal(t, ProviderLocal, ResolveProvider("LOCAL", "", ""))      // case-insensitive
}

func TestInitAndMetadata(t *testing.T) {
	db := newTestDB(t)
	assert.True(t, db.IsLocal())
	assert.False(t, db.IsRemote())
	assert.Equal(t, ProviderLocal, db.Provider())
}

func TestInit_UnsupportedProvider(t *testing.T) {
	_, err := Init(Config{Provider: "bogus", URL: "x", AuthToken: "y"})
	// "bogus" with url+token resolves to remote and tries to open libsql -
	// which fails fast on a malformed DSN, so we expect an error path here.
	// Either way, Init must not panic.
	_ = err
}

func TestOpenRemote_RequiresURLAndToken(t *testing.T) {
	// OpenRemote must reject configs that do not describe a reachable remote
	// before attempting any network I/O.
	_, err := OpenRemote(Config{})
	require.Error(t, err)

	_, err = OpenRemote(Config{URL: "libsql://x"}) // url but no token
	require.Error(t, err)

	_, err = OpenRemote(Config{AuthToken: "token"}) // token but no url
	require.Error(t, err)
}

func TestMuteLifecycle(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	m := &MutedUser{
		UserID:   100,
		Username: "bob",
		ChatID:   -500,
		MutedBy:  1,
		MutedAt:  now,
		UnmuteAt: now.Add(time.Hour),
		Reason:   "spam",
		IsActive: true,
	}
	require.NoError(t, db.AddMutedUser(m))

	muted, err := db.IsUserMuted(100, -500)
	require.NoError(t, err)
	assert.True(t, muted)

	info, err := db.GetActiveMuteInfo(100, -500)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "spam", info.Reason)

	active, err := db.GetActiveMutedUsers()
	require.NoError(t, err)
	assert.Len(t, active, 1)

	require.NoError(t, db.UnmuteUser(100, -500))
	muted, err = db.IsUserMuted(100, -500)
	require.NoError(t, err)
	assert.False(t, muted)
}

func TestExpiredMutes(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	require.NoError(t, db.AddMutedUser(&MutedUser{
		UserID: 7, ChatID: -1, MutedAt: now.Add(-2 * time.Hour),
		UnmuteAt: now.Add(-time.Hour), IsActive: true,
	}))
	expired, err := db.GetExpiredMutes()
	require.NoError(t, err)
	assert.Len(t, expired, 1)
}

func TestAddMutedUserSafely_ReplacesActive(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	require.NoError(t, db.AddMutedUserSafely(&MutedUser{
		UserID: 9, ChatID: -1, MutedAt: now, UnmuteAt: now.Add(time.Hour), Reason: "first", IsActive: true,
	}))
	require.NoError(t, db.AddMutedUserSafely(&MutedUser{
		UserID: 9, ChatID: -1, MutedAt: now, UnmuteAt: now.Add(2 * time.Hour), Reason: "second", IsActive: true,
	}))
	active, err := db.GetActiveMutedUsers()
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, "second", active[0].Reason)
}

func TestWarnings(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AddWarning(&Warning{
		UserID: 1, Username: "u", ChatID: -1, WarnedBy: 2, WarnedAt: time.Now(), Reason: "rude", MessageID: 55,
	}))
	has, err := db.HasWarningForMessage(1, 55)
	require.NoError(t, err)
	assert.True(t, has)

	has, err = db.HasWarningForMessage(1, 99)
	require.NoError(t, err)
	assert.False(t, has)

	require.NoError(t, db.RemoveWarningForMessage(1, 55))
	has, err = db.HasWarningForMessage(1, 55)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestMessageInfo(t *testing.T) {
	db := newTestDB(t)
	reply := 10
	info := &MessageInfo{
		MessageID: 20, ChatID: -1, UserID: 5, Username: "u", Text: "hello",
		ReplyToMessageID: &reply, Timestamp: time.Now(), ExtraInfo: "extra",
	}
	require.NoError(t, db.StoreMessageInfo(info))

	got, err := db.GetMessageInfo(20, -1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "hello", got.Text)
	require.NotNil(t, got.ReplyToMessageID)
	assert.Equal(t, 10, *got.ReplyToMessageID)
	assert.Equal(t, "extra", got.ExtraInfo)
}

func TestDeletionQueue(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AddMessageForDeletion(30, -1))

	in, err := db.IsMessageInDeletionQueue(30, -1)
	require.NoError(t, err)
	assert.True(t, in)

	// Old messages query (created_at < future) returns the queued message.
	old, err := db.GetOldMessages(time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(old), 1)

	require.NoError(t, db.RemoveMessageFromDeletion(30, -1))
	in, err = db.IsMessageInDeletionQueue(30, -1)
	require.NoError(t, err)
	assert.False(t, in)
}

func TestLogAction(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.LogAction(&Action{
		UserID: 1, Username: "u", AdminID: 2, AdminName: "admin",
		ActionType: "mute", Duration: 30, Reason: "spam", ChatID: -1, MessageID: 7, Timestamp: time.Now(),
	}))
}

func TestConfigValues(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.SetConfigValue("bot_token", "abc"))
	v, err := db.GetConfigValue("bot_token")
	require.NoError(t, err)
	assert.Equal(t, "abc", v)

	require.NoError(t, db.SetAllConfigValues(map[string]string{"a": "1", "b": "2"}))
	all, err := db.GetAllConfigValues()
	require.NoError(t, err)
	assert.Equal(t, "1", all["a"])
	assert.Equal(t, "2", all["b"])
}

func TestWebSessions(t *testing.T) {
	db := newTestDB(t)
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, db.SaveWebSession("hash1", exp))

	got, err := db.GetWebSessionExpiry("hash1")
	require.NoError(t, err)
	assert.WithinDuration(t, exp, got, 2*time.Second)

	// Unknown token -> zero time, no error.
	got, err = db.GetWebSessionExpiry("nope")
	require.NoError(t, err)
	assert.True(t, got.IsZero())

	require.NoError(t, db.DeleteWebSession("hash1"))
	got, err = db.GetWebSessionExpiry("hash1")
	require.NoError(t, err)
	assert.True(t, got.IsZero())

	// Expired session cleanup.
	require.NoError(t, db.SaveWebSession("old", time.Now().Add(-time.Hour)))
	require.NoError(t, db.DeleteExpiredWebSessions())
	got, err = db.GetWebSessionExpiry("old")
	require.NoError(t, err)
	assert.True(t, got.IsZero())
}

func TestTokenUsage(t *testing.T) {
	db := newTestDB(t)
	today := "2026-06-09"
	require.NoError(t, db.RecordTokenUsage("gpt", "azure_openai", today, 100, 50))
	require.NoError(t, db.RecordTokenUsage("gpt", "azure_openai", today, 10, 5))
	// Zero-token call is ignored.
	require.NoError(t, db.RecordTokenUsage("gpt", "azure_openai", today, 0, 0))

	stats, err := db.GetTokenUsageStats(today)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, int64(110), stats[0].DailyInput)
	assert.Equal(t, int64(55), stats[0].DailyOutput)
	assert.Equal(t, int64(110), stats[0].TotalInput)
}

func TestModerationStats(t *testing.T) {
	db := newTestDB(t)
	today := "2026-06-29"
	yesterday := "2026-06-28"
	dayBefore := "2026-06-27"

	require.NoError(t, db.IncrementModerationStat(ModStatReceived, today, 1))
	require.NoError(t, db.IncrementModerationStat(ModStatReceived, today, 4))
	require.NoError(t, db.IncrementModerationStat(ModStatReceived, yesterday, 3))
	require.NoError(t, db.IncrementModerationStat(ModStatLightFlagged, today, 2))
	require.NoError(t, db.IncrementModerationStat(ModStatManualAction, dayBefore, 1))
	// Non-positive delta is ignored.
	require.NoError(t, db.IncrementModerationStat(ModStatReceived, today, 0))

	rows, err := db.GetModerationStats(today, yesterday, dayBefore)
	require.NoError(t, err)

	byStat := map[string]ModerationStatBuckets{}
	for _, r := range rows {
		byStat[r.Stat] = r
	}
	require.Len(t, rows, 6)
	assert.Equal(t, int64(5), byStat[ModStatReceived].Today)
	assert.Equal(t, int64(3), byStat[ModStatReceived].Yesterday)
	assert.Equal(t, int64(8), byStat[ModStatReceived].AllTime)
	assert.Equal(t, int64(2), byStat[ModStatLightFlagged].Today)
	assert.Equal(t, int64(1), byStat[ModStatManualAction].DayBefore)
	assert.Equal(t, int64(0), byStat[ModStatFullConfirmed].AllTime)
}

func TestUserProfiles(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.UpsertUserProfile(&UserProfile{
		UserID: 1, Username: "u", Profile: "nice person", Reputation: "good",
	}))

	got, err := db.GetUserProfile(1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "nice person", got.Profile)
	assert.Equal(t, "good", got.Reputation)

	// Upsert updates the existing row.
	require.NoError(t, db.UpsertUserProfile(&UserProfile{
		UserID: 1, Username: "u", Profile: "changed", Reputation: "neutral",
	}))
	got, err = db.GetUserProfile(1)
	require.NoError(t, err)
	assert.Equal(t, "changed", got.Profile)

	// Unknown profile -> nil, nil.
	got, err = db.GetUserProfile(999)
	require.NoError(t, err)
	assert.Nil(t, got)

	all, err := db.GetAllUserProfiles()
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestAppendTgProfileAnalysis(t *testing.T) {
	db := newTestDB(t)
	// First finding creates the row; the analysis lands in tg_profile_analysis
	// while the behavior profile stays empty.
	require.NoError(t, db.AppendTgProfileAnalysis(1, "u", []string{"flagged photo detected"}))
	got, err := db.GetUserProfile(1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Contains(t, got.TgProfileAnalysis, "flagged photo detected")
	assert.Equal(t, "", got.Profile)

	// Idempotent: the same finding is not duplicated.
	require.NoError(t, db.AppendTgProfileAnalysis(1, "u", []string{"flagged photo detected"}))
	got, err = db.GetUserProfile(1)
	require.NoError(t, err)
	assert.Equal(t, 1, countOccurrences(got.TgProfileAnalysis, "flagged photo detected"))

	// A distinct finding is appended on its own line.
	require.NoError(t, db.AppendTgProfileAnalysis(1, "u", []string{"AI: spam promo"}))
	got, err = db.GetUserProfile(1)
	require.NoError(t, err)
	assert.Contains(t, got.TgProfileAnalysis, "flagged photo detected")
	assert.Contains(t, got.TgProfileAnalysis, "AI: spam promo")
}

func countOccurrences(s, sub string) int {
	count := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			count++
		}
	}
	return count
}
