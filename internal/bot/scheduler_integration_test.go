// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduledTaskBackoff(t *testing.T) {
	assert.Equal(t, 5*time.Minute, scheduledTaskBackoff(1))
	assert.Equal(t, 10*time.Minute, scheduledTaskBackoff(2))
	assert.Equal(t, 15*time.Minute, scheduledTaskBackoff(3))
	assert.Equal(t, 20*time.Minute, scheduledTaskBackoff(99))
}

func TestLockTimeout(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.ScheduledEvents.LockTimeoutMinutes = 15
	assert.Equal(t, 15*time.Minute, b.lockTimeout())
}

func TestBuildAllTasks(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.Enabled = true
	b.config.AI.MorningGreeting.Enabled = true
	b.config.AI.MorningGreeting.Time = "07:15"
	b.config.AI.DailySummary.Enabled = true
	b.config.AI.DailySummary.Time = "21:00"
	b.config.AI.UserProfiles.Enabled = true
	b.config.AI.UserProfiles.Time = "03:00"
	b.config.AI.Rss.Feeds = []config.RssFeed{
		{Name: "Feed", URL: "https://x.com/rss", Time: "08:00", Enabled: true},
		{Name: "Disabled", URL: "https://y.com/rss", Time: "08:00", Enabled: false},
	}
	b.config.MessageDeletion.Enabled = true
	b.config.MessageDeletion.CleanupIntervalHours = 3
	b.config.DatabaseCleanup.CleanupIntervalHours = 12

	tasks := b.buildAllTasks()
	assert.Contains(t, tasks, "morning_greeting")
	assert.Contains(t, tasks, "daily_summary")
	assert.Contains(t, tasks, "user_profiles")
	assert.Contains(t, tasks, "message_cleanup")
	assert.Contains(t, tasks, "database_cleanup")
	assert.Contains(t, tasks, rssTaskName("https://x.com/rss"))
	// Disabled feed is excluded.
	assert.NotContains(t, tasks, rssTaskName("https://y.com/rss"))

	// Kinds are correct.
	assert.Equal(t, taskDaily, tasks["morning_greeting"].Kind)
	assert.Equal(t, taskInterval, tasks["message_cleanup"].Kind)
	assert.Equal(t, taskInterval, tasks["database_cleanup"].Kind)
	assert.True(t, tasks["daily_summary"].SeedOnFirstRun)
}

func TestBuildAllTasks_AIDisabled(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.Enabled = false
	b.config.MessageDeletion.Enabled = true
	b.config.MessageDeletion.CleanupIntervalHours = 3

	tasks := b.buildAllTasks()
	// No AI tasks, but cleanup tasks remain.
	assert.NotContains(t, tasks, "morning_greeting")
	assert.NotContains(t, tasks, "daily_summary")
	assert.Contains(t, tasks, "message_cleanup")
}

func TestExecuteWithLock_Success(t *testing.T) {
	b, _ := newMockBot(t)
	require.NoError(t, b.db.EnsureScheduledEventExists("test_event", "12:00"))
	ran := false
	ok := b.executeWithLock("test_event", "12:00", func() { ran = true })
	assert.True(t, ok)
	assert.True(t, ran)

	// Event recorded as fired.
	ev, err := b.db.GetScheduledEvent("test_event")
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.False(t, ev.LastFiredAt.IsZero())
}

func TestExecuteWithLock_PanicReleases(t *testing.T) {
	b, _ := newMockBot(t)
	require.NoError(t, b.db.EnsureScheduledEventExists("panicky", "12:00"))
	ok := b.executeWithLock("panicky", "12:00", func() { panic("boom") })
	assert.False(t, ok, "panicking task is not a success")

	// Lock released -> StartedAt cleared so it can be retried.
	ev, err := b.db.GetScheduledEvent("panicky")
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.Nil(t, ev.StartedAt)
}

func TestExecuteWithLock_AlreadyClaimed(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.ScheduledEvents.LockTimeoutMinutes = 60
	// The lock is an UPDATE, so the event row must exist first.
	require.NoError(t, b.db.EnsureScheduledEventExists("busy", "12:00"))
	// Claim the lock once (holds it).
	claimed, err := b.db.TryClaimScheduledEvent("busy", time.Hour)
	require.NoError(t, err)
	require.True(t, claimed)

	// A second execute cannot claim it -> task does not run.
	ran := false
	ok := b.executeWithLock("busy", "12:00", func() { ran = true })
	assert.False(t, ok)
	assert.False(t, ran)
}

func TestCheckIntervalTaskDue_RunsWhenOverdue(t *testing.T) {
	b, _ := newMockBot(t)
	// Seed an old last-fired so the interval is overdue.
	require.NoError(t, b.db.RecordEventFiredAt("iv", "every 1h", time.Now().Add(-2*time.Hour)))

	ran := false
	def := scheduledTaskDef{Kind: taskInterval, Interval: time.Hour, Task: func() { ran = true }}
	b.checkIntervalTaskDue("iv", def)
	assert.True(t, ran)
}

func TestCheckIntervalTaskDue_SkipsWhenRecent(t *testing.T) {
	b, _ := newMockBot(t)
	require.NoError(t, b.db.RecordEventFiredAt("iv2", "every 1h", time.Now()))

	ran := false
	def := scheduledTaskDef{Kind: taskInterval, Interval: time.Hour, Task: func() { ran = true }}
	b.checkIntervalTaskDue("iv2", def)
	assert.False(t, ran)
}

func TestCheckExpiredMutes(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.ChatID = -999
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	// An already-expired regular mute.
	require.NoError(t, b.db.AddMutedUserSafely(&database.MutedUser{
		UserID: 7, ChatID: -100, MutedAt: time.Now().Add(-2 * time.Hour),
		UnmuteAt: time.Now().Add(-time.Hour), Reason: "x", IsActive: true,
	}))

	b.checkExpiredMutes()

	// User unmuted in DB and unrestricted via Telegram.
	muted, err := b.db.IsUserMuted(7, -100)
	require.NoError(t, err)
	assert.False(t, muted)
	require.NotEmpty(t, tg.Restrictions)
	assert.True(t, tg.Restrictions[len(tg.Restrictions)-1].Permissions.CanSendMessages)
}

func TestCheckExpiredMutes_CruelSkipsUnrestrict(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.ChatID = -999
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	require.NoError(t, b.db.AddMutedUserSafely(&database.MutedUser{
		UserID: 8, ChatID: -100, MutedAt: time.Now().Add(-2 * time.Hour),
		UnmuteAt: time.Now().Add(-time.Hour), Reason: "x", IsActive: true, IsCruel: true,
	}))

	b.checkExpiredMutes()

	muted, err := b.db.IsUserMuted(8, -100)
	require.NoError(t, err)
	assert.False(t, muted)
	// Cruel mute does not issue a Telegram restriction on expiry.
	assert.Empty(t, tg.Restrictions)
}

func TestCleanupOldMessages(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.ChatID = -999
	b.config.MessageDeletion.ChatDeletionRetentionHours = 1

	// Queue a message whose created_at is unambiguously older than the cutoff in
	// any timezone (SQLite datetime() stores it UTC-normalized, so an old UTC
	// timestamp is reliably selected by GetOldMessages on a UTC CI runner too).
	// cleanupOldMessages then deletes everything GetOldMessages returns and
	// removes it from the queue.
	old := time.Now().UTC().Add(-48 * time.Hour)
	_, err := b.db.RecordIncomingMessage(
		&database.MessageInfo{MessageID: 55, ChatID: -100, Timestamp: old},
		database.IncomingMessageOpts{AddToDeletion: true},
	)
	require.NoError(t, err)
	b.cleanupOldMessages()

	require.NotEmpty(t, tg.DeletedIDs)
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
	// Queue entry removed after deletion.
	inQueue, err := b.db.IsMessageInDeletionQueue(55, -100)
	require.NoError(t, err)
	assert.False(t, inQueue)
}

func TestPerformDatabaseCleanup(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.DatabaseCleanup.MessageRetentionHours = 72
	b.config.DatabaseCleanup.WarningRetentionHours = 72
	b.config.DatabaseCleanup.ActionRetentionHours = 72
	// Should run without error on an empty DB.
	b.performDatabaseCleanup()
}

func TestTriggerScheduledEvents(t *testing.T) {
	b, _ := newMockBot(t)
	// No tasks enabled -> just exercises the trigger path without panic.
	b.TriggerScheduledEvents()
}
