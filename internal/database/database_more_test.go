// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Helpers (small builders to keep tests terse) ──

func storeMsg(t *testing.T, db *DB, msgID int, chatID, userID int64, username, text string, ts time.Time) {
	t.Helper()
	require.NoError(t, db.StoreMessageInfo(&MessageInfo{
		MessageID: msgID, ChatID: chatID, UserID: userID, Username: username,
		Text: text, Timestamp: ts,
	}))
}

func logAct(t *testing.T, db *DB, userID, chatID int64, actionType string, msgID int, ts time.Time) {
	t.Helper()
	require.NoError(t, db.LogAction(&Action{
		UserID: userID, Username: "u", AdminID: 9, AdminName: "admin",
		ActionType: actionType, Duration: 30, Reason: "r", ChatID: chatID,
		MessageID: msgID, Timestamp: ts,
	}))
}

// ── parseTime ──

func TestParseTime(t *testing.T) {
	assert.True(t, parseTime("").IsZero())
	assert.True(t, parseTime("garbage").IsZero())
	assert.False(t, parseTime("2026-06-09 12:30:00").IsZero())
	assert.False(t, parseTime("2026-06-09T12:30:00").IsZero())
	assert.False(t, parseTime("2026-06-09T12:30:00Z").IsZero())
}

// ── isTransientError / truncateErrorMsg ──

func TestIsTransientError(t *testing.T) {
	db := newTestDB(t)
	assert.True(t, db.isTransientError(assertErr("database is locked")))
	assert.True(t, db.isTransientError(assertErr("SQLITE_BUSY something")))
	assert.False(t, db.isTransientError(assertErr("syntax error")))
	// Remote-only substrings are not transient for a local DB.
	assert.False(t, db.isTransientError(assertErr("connection refused")))
}

func TestTruncateErrorMsg(t *testing.T) {
	assert.Equal(t, "short", truncateErrorMsg("short", 100))
	long := make([]byte, 50)
	for i := range long {
		long[i] = 'x'
	}
	out := truncateErrorMsg(string(long), 10)
	assert.Contains(t, out, "truncated")
	assert.Less(t, len(out), 50+len("...(truncated)")+1)
}

type strErr string

func (e strErr) Error() string { return string(e) }
func assertErr(s string) error { return strErr(s) }

// ── InvalidateMuteCache ──

func TestInvalidateMuteCache(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AddMutedUser(&MutedUser{
		UserID: 1, ChatID: -1, MutedBy: 2, MutedAt: time.Now(),
		UnmuteAt: time.Now().Add(time.Hour), IsActive: true,
	}))
	assert.NotPanics(t, func() { db.InvalidateMuteCache() })
	// After invalidation the cache reloads lazily; the mute is still active.
	muted, err := db.IsUserMuted(1, -1)
	require.NoError(t, err)
	assert.True(t, muted)
}

// ── Message operations ──

func TestRecordIncomingMessage(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now()
	info := &MessageInfo{MessageID: 1, ChatID: -10, UserID: 5, Username: "alice", Text: "hi", Timestamp: ts}
	res, err := db.RecordIncomingMessage(info, IncomingMessageOpts{
		AddToDeletion: true,
		TrackProfile:  true,
		Username:      "alice",
		DisplayName:   "Alice A",
		DayDate:       ts.Format("2006-01-02"),
	})
	require.NoError(t, err)
	assert.True(t, res.NewUserTracked)

	// message_info written
	got, err := db.GetMessageInfo(1, -10)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "hi", got.Text)

	// deletion queue written
	in, err := db.IsMessageInDeletionQueue(1, -10)
	require.NoError(t, err)
	assert.True(t, in)

	// name history written
	hist, err := db.GetUserNameHistory(5)
	require.NoError(t, err)
	require.Len(t, hist, 1)
	assert.Equal(t, "alice", hist[0].Username)

	// Second message, same name -> no new name-history row, NewUserTracked false.
	res2, err := db.RecordIncomingMessage(
		&MessageInfo{MessageID: 2, ChatID: -10, UserID: 5, Username: "alice", Text: "yo", Timestamp: ts},
		IncomingMessageOpts{TrackProfile: true, Username: "alice", DisplayName: "Alice A", DayDate: ts.Format("2006-01-02")},
	)
	require.NoError(t, err)
	assert.False(t, res2.NewUserTracked)
	hist, _ = db.GetUserNameHistory(5)
	assert.Len(t, hist, 1)

	// Changed display name -> new history row.
	_, err = db.RecordIncomingMessage(
		&MessageInfo{MessageID: 3, ChatID: -10, UserID: 5, Username: "alice", Text: "z", Timestamp: ts},
		IncomingMessageOpts{TrackProfile: true, Username: "alice", DisplayName: "Alice B", DayDate: ts.Format("2006-01-02")},
	)
	require.NoError(t, err)
	hist, _ = db.GetUserNameHistory(5)
	assert.Len(t, hist, 2)
}

func TestRecordIncomingMessage_NilInfo(t *testing.T) {
	db := newTestDB(t)
	_, err := db.RecordIncomingMessage(nil, IncomingMessageOpts{})
	require.Error(t, err)
}

func TestCountUserMessagesInChat(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	storeMsg(t, db, 1, -1, 7, "u", "a", now)
	storeMsg(t, db, 2, -1, 7, "u", "b", now)
	storeMsg(t, db, 3, -1, 8, "u", "c", now)
	n, err := db.CountUserMessagesInChat(7, -1)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	n, err = db.CountUserMessagesInChat(99, -1)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestUpdateMessageInfoAndExtra(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	storeMsg(t, db, 10, -1, 5, "u", "orig", now)

	require.NoError(t, db.UpdateMessageInfo(&MessageInfo{
		MessageID: 10, ChatID: -1, Username: "u", Text: "edited", ExtraInfo: "x",
	}))
	got, err := db.GetMessageInfo(10, -1)
	require.NoError(t, err)
	assert.Equal(t, "edited", got.Text)

	require.NoError(t, db.UpdateMessageExtraInfo(10, -1, "summary"))
	got, err = db.GetMessageInfo(10, -1)
	require.NoError(t, err)
	assert.Equal(t, "summary", got.ExtraInfo)
}

func TestMessageThreadIDRoundTrip(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	// StoreMessageInfo persists the forum topic id.
	require.NoError(t, db.StoreMessageInfo(&MessageInfo{
		MessageID: 7, ChatID: -100, UserID: 5, Username: "u",
		Text: "in topic", MessageThreadID: 50, Timestamp: now,
	}))
	got, err := db.GetMessageInfo(7, -100)
	require.NoError(t, err)
	assert.Equal(t, 50, got.MessageThreadID)

	// RecordIncomingMessage (the hot-path transactional insert) also persists it.
	require.NoError(t, func() error {
		_, e := db.RecordIncomingMessage(&MessageInfo{
			MessageID: 8, ChatID: -100, UserID: 5, Username: "u",
			Text: "another", MessageThreadID: 99, Timestamp: now,
		}, IncomingMessageOpts{})
		return e
	}())
	got, err = db.GetMessageInfo(8, -100)
	require.NoError(t, err)
	assert.Equal(t, 99, got.MessageThreadID)

	// UpdateMessageInfo preserves/updates the topic id.
	require.NoError(t, db.UpdateMessageInfo(&MessageInfo{
		MessageID: 7, ChatID: -100, Username: "u", Text: "edited", MessageThreadID: 51,
	}))
	got, err = db.GetMessageInfo(7, -100)
	require.NoError(t, err)
	assert.Equal(t, 51, got.MessageThreadID)

	// A main-area message stores 0.
	storeMsg(t, db, 9, -100, 5, "u", "main", now)
	got, err = db.GetMessageInfo(9, -100)
	require.NoError(t, err)
	assert.Equal(t, 0, got.MessageThreadID)
}

func TestFindMessageByID(t *testing.T) {
	db := newTestDB(t)
	storeMsg(t, db, 50, -1, 5, "u", "older", time.Now().Add(-time.Hour))
	storeMsg(t, db, 50, -2, 6, "u", "newer", time.Now())
	got, err := db.FindMessageByID(50)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "newer", got.Text)
}

func TestFindBotReplyMessage(t *testing.T) {
	db := newTestDB(t)
	reply := 100
	require.NoError(t, db.StoreMessageInfo(&MessageInfo{
		MessageID: 101, ChatID: -1, UserID: 999, Username: "bot", Text: "reply",
		ReplyToMessageID: &reply, Timestamp: time.Now(),
	}))
	got, err := db.FindBotReplyMessage(999, 100, -1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "reply", got.Text)
}

func TestWarningMessageIDRoundTrip(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AddWarning(&Warning{
		UserID: 7, ChatID: -100, WarnedBy: 1, WarnedAt: time.Now(),
		Reason: "r", MessageID: 55,
	}))

	// No warning message recorded yet -> 0.
	id, err := db.GetWarningMessageID(7, 55)
	require.NoError(t, err)
	assert.Equal(t, 0, id)

	// After recording, the exact warning reply id is returned.
	require.NoError(t, db.UpdateWarningMessageID(7, 55, 60))
	id, err = db.GetWarningMessageID(7, 55)
	require.NoError(t, err)
	assert.Equal(t, 60, id)

	// Unknown (user, message) pair -> 0, no error.
	id, err = db.GetWarningMessageID(7, 999)
	require.NoError(t, err)
	assert.Equal(t, 0, id)
}

func TestMarkMessageAsPinned(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.AddMessageForDeletion(5, -1))
	require.NoError(t, db.MarkMessageAsPinned(5, -1, true))
}

func TestDeleteMessageInfo(t *testing.T) {
	db := newTestDB(t)
	storeMsg(t, db, 1, -1, 5, "u", "x", time.Now())
	require.NoError(t, db.DeleteMessageInfo(1, -1))
	_, err := db.GetMessageInfo(1, -1)
	require.Error(t, err) // sql.ErrNoRows
}

func TestGetUserMessageIDsSince(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	storeMsg(t, db, 1, -1, 5, "u", "a", now.Add(-2*time.Hour))
	storeMsg(t, db, 2, -1, 5, "u", "b", now)
	ids, err := db.GetUserMessageIDsSince(5, -1, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, []int{2}, ids)
	// Zero time fetches all.
	ids, err = db.GetUserMessageIDsSince(5, -1, time.Time{})
	require.NoError(t, err)
	assert.Len(t, ids, 2)
}

// ── Actions / UI queries ──

func TestGetRecentActionsVariants(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	storeMsg(t, db, 7, -1, 5, "u", "offending text", now)
	logAct(t, db, 5, -1, "mute", 7, now)

	actions, err := db.GetRecentActions(10)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "mute", actions[0].ActionType)

	enriched, err := db.GetRecentActionsEnriched(10)
	require.NoError(t, err)
	require.Len(t, enriched, 1)
	assert.Equal(t, "offending text", enriched[0].MessageText)
}

func TestGetRecentMessagesForUI(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	storeMsg(t, db, 1, -1, 5, "u", "m1", now)
	storeMsg(t, db, 2, -1, 5, "u", "m2", now)
	logAct(t, db, 5, -1, "warn", 1, now)

	msgs, total, err := db.GetRecentMessagesForUI(10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, msgs, 2)

	enriched, total, err := db.GetRecentMessagesForUIEnriched(10, 0, 0, "")
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, enriched, 2)
	// One of them should carry the warn action.
	var found bool
	for _, m := range enriched {
		if m.MessageID == 1 {
			assert.Equal(t, "warn", m.ActionType)
			found = true
		}
	}
	assert.True(t, found)
}

func TestGetAdminNameForMute(t *testing.T) {
	db := newTestDB(t)
	logAct(t, db, 5, -1, "mute", 0, time.Now())
	name, err := db.GetAdminNameForMute(5, -1)
	require.NoError(t, err)
	assert.Equal(t, "admin", name)

	_, err = db.GetAdminNameForMute(999, -1)
	require.Error(t, err)
}

func TestGetRecentUserMessages(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	storeMsg(t, db, 1, -1, 5, "u", "recent", now)
	storeMsg(t, db, 2, -1, 5, "u", "old", now.Add(-48*time.Hour))
	msgs, err := db.GetRecentUserMessages(5, -1, 10, 24)
	require.NoError(t, err)
	assert.Equal(t, []string{"recent"}, msgs)
}

func TestGetMessagesByUsersInRange(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	storeMsg(t, db, 10, -1, 5, "u", "a", now)
	storeMsg(t, db, 11, -1, 6, "u", "b", now)
	storeMsg(t, db, 12, -1, 5, "u", "c", now)

	msgs, err := db.GetMessagesByUsersInRange(-1, []int64{5, 6}, 10, 12, []int{11}, now.Add(-time.Hour), 100)
	require.NoError(t, err)
	require.Len(t, msgs, 2) // 10 and 12, excluding 11
	assert.Equal(t, 10, msgs[0].MessageID)

	// Empty user list / bad range -> nil.
	got, err := db.GetMessagesByUsersInRange(-1, nil, 1, 5, nil, now, 10)
	require.NoError(t, err)
	assert.Nil(t, got)
	got, err = db.GetMessagesByUsersInRange(-1, []int64{5}, 5, 1, nil, now, 10)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetRecentUserCounts(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	require.NoError(t, db.AddWarning(&Warning{UserID: 5, ChatID: -1, WarnedAt: now, MessageID: 1}))
	logAct(t, db, 5, -1, "mute", 1, now)
	logAct(t, db, 5, -1, "warn", 2, now)

	w, err := db.GetRecentUserWarnings(5, -1)
	require.NoError(t, err)
	assert.Equal(t, 1, w)

	a, err := db.GetRecentUserActions(5, -1)
	require.NoError(t, err)
	assert.Equal(t, 2, a)

	byType, err := db.GetRecentUserActionsByType(5, -1, "mute")
	require.NoError(t, err)
	assert.Equal(t, 1, byType)
}

func TestGetTop10BadUsers(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	require.NoError(t, db.AddWarning(&Warning{UserID: 5, ChatID: -1, WarnedAt: now, MessageID: 1}))
	logAct(t, db, 5, -1, "mute", 1, now)
	stats, err := db.GetTop10BadUsers(24)
	require.NoError(t, err)
	assert.NotEmpty(t, stats)
}

// ── Cleanup ──

func TestCleanupOperations(t *testing.T) {
	db := newTestDB(t)
	old := time.Now().Add(-100 * time.Hour)
	storeMsg(t, db, 1, -1, 5, "u", "old", old)
	require.NoError(t, db.AddWarning(&Warning{UserID: 5, ChatID: -1, WarnedAt: old, MessageID: 99}))
	logAct(t, db, 5, -1, "mute", 1, old)

	n, err := db.CleanupOldMessages(1, false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	n, err = db.CleanupOldWarnings(1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	n, err = db.CleanupOldActions(1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	_, err = db.CleanupExpiredMutes(1)
	require.NoError(t, err)
}

func TestCleanupOldMessages_PreserveWarnedMuted(t *testing.T) {
	db := newTestDB(t)
	old := time.Now().Add(-100 * time.Hour)
	storeMsg(t, db, 1, -1, 5, "u", "warned", old)
	storeMsg(t, db, 2, -1, 5, "u", "free", old)
	require.NoError(t, db.AddWarning(&Warning{UserID: 5, ChatID: -1, WarnedAt: old, MessageID: 1}))

	n, err := db.CleanupOldMessages(1, true)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n) // only the un-warned message 2 deleted

	got, err := db.GetMessageInfo(1, -1)
	require.NoError(t, err)
	assert.NotNil(t, got) // warned message preserved
}

func TestPerformDatabaseCleanup(t *testing.T) {
	db := newTestDB(t)
	old := time.Now().Add(-1000 * time.Hour)
	storeMsg(t, db, 1, -1, 5, "u", "x", old)
	results, err := db.PerformDatabaseCleanup(1, 1, 1, false)
	require.NoError(t, err)
	assert.Contains(t, results, "messages")
	assert.Contains(t, results, "warnings")
	assert.Contains(t, results, "actions")
	assert.Contains(t, results, "expired_mutes")
	assert.Contains(t, results, "user_daily_activity")
}

func TestGetRecentMessagesWithUsernames(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	storeMsg(t, db, 1, -1, 5, "@alice", "hello world", now)
	storeMsg(t, db, 2, -1, 999, "@bot", "bot msg", now)
	msgs, err := db.GetRecentMessagesWithUsernames(-1, 24, 999)
	require.NoError(t, err)
	require.Len(t, msgs, 1) // bot message excluded
	assert.Contains(t, msgs[0], "alice")
	assert.Contains(t, msgs[0], "hello world")
}

func TestVacuumDatabase(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.VacuumDatabase())
}

func TestBotMessageQueries(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	storeMsg(t, db, 100, -1, 999, "bot", "m", now)
	id, err := db.GetLatestBotMessageID(999, -1)
	require.NoError(t, err)
	assert.Equal(t, 100, id)

	count, err := db.GetRecentBotMessageCount(999, -1, 24)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// No bot messages -> 0, error.
	_, err = db.GetLatestBotMessageID(123, -1)
	require.Error(t, err)
}

func TestComposeReplyContext(t *testing.T) {
	assert.Equal(t, "", composeReplyContext("", nil))
	assert.Equal(t, "plain", composeReplyContext("plain", nil))

	extra := "Article body"
	out := composeReplyContext("https://example.com/page", &extra)
	assert.Contains(t, out, "Article body")

	out = composeReplyContext("", &extra)
	assert.Contains(t, out, "Article body")
}

func TestGetTableStatsAndMuteCount(t *testing.T) {
	db := newTestDB(t)
	stats, err := db.GetTableStats()
	require.NoError(t, err)
	assert.Contains(t, stats, "message_info")
	assert.Contains(t, stats, "actions")

	require.NoError(t, db.AddMutedUser(&MutedUser{
		UserID: 1, ChatID: -1, MutedBy: 2, MutedAt: time.Now(),
		UnmuteAt: time.Now().Add(time.Hour), IsActive: true,
	}))
	count, err := db.GetActiveMuteCount()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// ── Scheduled events ──

func TestScheduledEventLifecycle(t *testing.T) {
	db := newTestDB(t)

	// Unknown event -> nil, nil.
	ev, err := db.GetScheduledEvent("daily")
	require.NoError(t, err)
	assert.Nil(t, ev)

	require.NoError(t, db.EnsureScheduledEventExists("daily", "08:00"))
	ev, err = db.GetScheduledEvent("daily")
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.Equal(t, "08:00", ev.ScheduledTime)

	// Claim succeeds the first time.
	claimed, err := db.TryClaimScheduledEvent("daily", time.Minute)
	require.NoError(t, err)
	assert.True(t, claimed)

	// Second claim fails (lock held, not stale).
	claimed, err = db.TryClaimScheduledEvent("daily", time.Minute)
	require.NoError(t, err)
	assert.False(t, claimed)

	// Release clears the lock; claim works again.
	require.NoError(t, db.ReleaseScheduledEvent("daily"))
	claimed, err = db.TryClaimScheduledEvent("daily", time.Minute)
	require.NoError(t, err)
	assert.True(t, claimed)

	// Record fired clears started_at and sets last_fired_at.
	fired := time.Now()
	require.NoError(t, db.RecordEventFiredAt("daily", "08:00", fired))
	ev, err = db.GetScheduledEvent("daily")
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.WithinDuration(t, fired, ev.LastFiredAt, 2*time.Second)
	assert.Nil(t, ev.StartedAt)

	require.NoError(t, db.RecordEventFired("daily", "09:00"))

	all, err := db.GetAllScheduledEvents()
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestPruneScheduledEvents(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.EnsureScheduledEventExists("keep", "08:00"))
	require.NoError(t, db.EnsureScheduledEventExists("drop", "09:00"))

	// Empty active set is a no-op.
	n, err := db.PruneScheduledEvents(nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	n, err = db.PruneScheduledEvents([]string{"keep"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	all, err := db.GetAllScheduledEvents()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "keep", all[0].EventName)
}

// ── profiles_tracking ──

func TestProfilesTracking(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now()
	// Two users, with name changes.
	_, err := db.RecordIncomingMessage(
		&MessageInfo{MessageID: 1, ChatID: -1, UserID: 5, Username: "alice", Text: "a", Timestamp: ts},
		IncomingMessageOpts{TrackProfile: true, Username: "alice", DisplayName: "Alice", DayDate: ts.Format("2006-01-02")},
	)
	require.NoError(t, err)
	_, err = db.RecordIncomingMessage(
		&MessageInfo{MessageID: 2, ChatID: -2, UserID: 6, Username: "bob", Text: "b", Timestamp: ts},
		IncomingMessageOpts{TrackProfile: true, Username: "bob", DisplayName: "Bob", DayDate: ts.Format("2006-01-02")},
	)
	require.NoError(t, err)

	// GetLatestUsername
	u, err := db.GetLatestUsername(5)
	require.NoError(t, err)
	assert.Equal(t, "alice", u)
	u, err = db.GetLatestUsername(999)
	require.NoError(t, err)
	assert.Equal(t, "", u)

	// First seen (global earliest)
	fs, err := db.GetUserFirstSeen(5)
	require.NoError(t, err)
	assert.False(t, fs.IsZero())

	// Daily activity
	day := ts.Format("2006-01-02")
	act, err := db.GetUserDailyActivityRange(5, []string{day, "2000-01-01"})
	require.NoError(t, err)
	require.Len(t, act, 2)
	assert.Equal(t, 1, act[0].Count)
	assert.Equal(t, 0, act[1].Count)
	// Empty range
	act, err = db.GetUserDailyActivityRange(5, nil)
	require.NoError(t, err)
	assert.Empty(t, act)

	// Tracked user IDs
	ids, err := db.GetAllTrackedUserIDs()
	require.NoError(t, err)
	assert.ElementsMatch(t, []int64{5, 6}, ids)

	// Bulk fetchers
	nameHist, err := db.GetAllUserNameHistory()
	require.NoError(t, err)
	assert.Len(t, nameHist[5], 1)

	allAct, err := db.GetAllUserDailyActivityRange([]string{day})
	require.NoError(t, err)
	assert.Equal(t, 1, allAct[5][0].Count)
	// Empty day list
	empty, err := db.GetAllUserDailyActivityRange(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// Mute counts
	logAct(t, db, 5, -1, "mute", 1, ts)
	logAct(t, db, 5, -2, "mute", 2, ts)
	all, err := db.CountUserMutesInChat(5, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, all)
	inChat, err := db.CountUserMutesInChat(5, -1)
	require.NoError(t, err)
	assert.Equal(t, 1, inChat)

	// Delete tracking data
	require.NoError(t, db.DeleteUserTrackingData(5))
	ids, err = db.GetAllTrackedUserIDs()
	require.NoError(t, err)
	assert.ElementsMatch(t, []int64{6}, ids)
}

func TestFindUsernameReusers(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now()
	_, err := db.RecordIncomingMessage(
		&MessageInfo{MessageID: 1, ChatID: -1, UserID: 5, Username: "shared", Text: "a", Timestamp: ts},
		IncomingMessageOpts{TrackProfile: true, Username: "shared", DisplayName: "Old Holder", DayDate: ts.Format("2006-01-02")},
	)
	require.NoError(t, err)

	// Same username, different user id.
	reusers, err := db.FindUsernameReusers("shared", 6)
	require.NoError(t, err)
	require.Len(t, reusers, 1)
	assert.Equal(t, int64(5), reusers[0].UserID)
	assert.Equal(t, "Old Holder", reusers[0].DisplayName)

	// Excluding the only holder returns nothing.
	reusers, err = db.FindUsernameReusers("shared", 5)
	require.NoError(t, err)
	assert.Empty(t, reusers)

	// Empty username -> nil.
	reusers, err = db.FindUsernameReusers("", 0)
	require.NoError(t, err)
	assert.Nil(t, reusers)
}

func TestCleanupOldUserDailyActivity(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now()
	_, err := db.RecordIncomingMessage(
		&MessageInfo{MessageID: 1, ChatID: -1, UserID: 5, Username: "u", Text: "a", Timestamp: ts},
		IncomingMessageOpts{TrackProfile: true, Username: "u", DisplayName: "U", DayDate: "2000-01-01"},
	)
	require.NoError(t, err)
	n, err := db.CleanupOldUserDailyActivity("2010-01-01")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

// ── user_profiles extended queries ──

func TestUserProfileQueries(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	chats := []int64{-1, -2}
	storeMsg(t, db, 1, -1, 5, "alice", "hello", now)
	storeMsg(t, db, 2, -2, 5, "alice", "world", now)
	require.NoError(t, db.AddWarning(&Warning{UserID: 5, ChatID: -1, WarnedAt: now, MessageID: 1}))
	logAct(t, db, 5, -1, "mute", 1, now)
	logAct(t, db, 5, -1, "cleared", 2, now)

	since := now.Add(-time.Hour)

	users, err := db.GetActiveUsersSince(chats, since)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, int64(5), users[0].UserID)
	// Empty chat list -> nil.
	got, err := db.GetActiveUsersSince(nil, since)
	require.NoError(t, err)
	assert.Nil(t, got)

	msgs, err := db.GetUserMessagesSince(5, chats, since)
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	gotMsgs, err := db.GetUserMessagesSince(5, nil, since)
	require.NoError(t, err)
	assert.Nil(t, gotMsgs)

	w, err := db.GetUserWarningsSince(5, since)
	require.NoError(t, err)
	assert.Equal(t, 1, w)

	m, err := db.GetUserMutesSince(5, since)
	require.NoError(t, err)
	assert.Equal(t, 1, m)

	c, err := db.GetUserClearedSince(5, since)
	require.NoError(t, err)
	assert.Equal(t, 1, c)

	acts, err := db.GetUserMessageActions(5, since)
	require.NoError(t, err)
	assert.Contains(t, acts[1], "mute")
	assert.Contains(t, acts[1], "warn")
	assert.Contains(t, acts[2], "cleared")

	profileData, err := db.GetAllProfileData(chats, since)
	require.NoError(t, err)
	require.Contains(t, profileData, int64(5))
	assert.Equal(t, 1, profileData[5].Warnings)
	assert.Equal(t, 1, profileData[5].Mutes)
	assert.Equal(t, 1, profileData[5].Cleared)
	// Empty chats -> nil.
	gotPD, err := db.GetAllProfileData(nil, since)
	require.NoError(t, err)
	assert.Nil(t, gotPD)
}

func TestDeleteUserProfile(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.UpsertUserProfile(&UserProfile{UserID: 5, Username: "u", Profile: "p", Reputation: "good"}))
	require.NoError(t, db.DeleteUserProfile(5))
	got, err := db.GetUserProfile(5)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetMessageModerationMarks(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()

	// message_id 0 short-circuits to no marks.
	d, w, m, err := db.GetMessageModerationMarks(-100, 0)
	require.NoError(t, err)
	assert.False(t, d || w || m)

	// A message with no actions has no marks.
	d, w, m, err = db.GetMessageModerationMarks(-100, 1)
	require.NoError(t, err)
	assert.False(t, d || w || m)

	// delete + mute actions on message 1, plus a warning (warnings table).
	logAct(t, db, 5, -100, "delete", 1, now)
	logAct(t, db, 5, -100, "mute", 1, now)
	require.NoError(t, db.AddWarning(&Warning{UserID: 5, ChatID: -100, WarnedAt: now, MessageID: 1}))

	d, w, m, err = db.GetMessageModerationMarks(-100, 1)
	require.NoError(t, err)
	assert.True(t, d, "deleted")
	assert.True(t, w, "warned")
	assert.True(t, m, "muted")

	// "warn" recorded via the actions table (not the warnings table) also counts.
	logAct(t, db, 5, -100, "warn", 2, now)
	d, w, m, err = db.GetMessageModerationMarks(-100, 2)
	require.NoError(t, err)
	assert.False(t, d)
	assert.True(t, w)
	assert.False(t, m)

	// Scoped to chat_id: the same message id in a different chat is unaffected.
	d, w, m, err = db.GetMessageModerationMarks(-200, 1)
	require.NoError(t, err)
	assert.False(t, d || w || m)
}
