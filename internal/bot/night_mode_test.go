// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enableNightModeNow configures a night-mode window that is guaranteed to be
// active at the current moment (a 4-hour window centered on now), independent of
// the wall-clock time the test runs at.
func enableNightModeNow(c *config.Config) {
	now := time.Now()
	c.NightMode = config.NightModeConfig{
		Enabled:   true,
		StartTime: now.Add(-2 * time.Hour).Format("15:04"),
		EndTime:   now.Add(2 * time.Hour).Format("15:04"),
	}
}

func TestHandleNightModeMessage(t *testing.T) {
	b, tg := newMockBot(t)
	msg := testMessage(-100, 7, 55, "hello at night")

	b.handleNightModeMessage(msg, false)

	// A notice was sent as a reply to the offending message.
	require.Len(t, tg.SentMessages, 1)
	assert.Equal(t, 55, tg.SentMessages[0].ReplyToMessageID)
	assert.Equal(t, int64(-100), tg.SentMessages[0].ChatID)
	assert.NotEmpty(t, tg.SentMessages[0].Text, "a default notice must be used when none is configured")

	// The original message was deleted.
	require.Len(t, tg.DeletedIDs, 1)
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
}

func TestHandleNightModeMessage_CustomText(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.NightMode.Message = "shhh, quiet hours"

	b.handleNightModeMessage(testMessage(-100, 7, 55, "x"), false)

	require.Len(t, tg.SentMessages, 1)
	assert.Equal(t, "shhh, quiet hours", tg.SentMessages[0].Text)
}

func TestHandleNightModeMessage_MutedUsesDistinctNotice(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.NightMode.Message = "shhh, quiet hours"

	b.handleNightModeMessage(testMessage(-100, 7, 55, "x"), true)

	require.Len(t, tg.SentMessages, 1)
	assert.NotEqual(t, "shhh, quiet hours", tg.SentMessages[0].Text,
		"a muted user must get the built-in mute notice, not the generic message")
	assert.NotEmpty(t, tg.SentMessages[0].Text)
}

func TestHandleNightModeMessage_CustomMutedText(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.NightMode.Message = "generic notice"
	b.config.NightMode.MutedMessage = "muted till morning"

	b.handleNightModeMessage(testMessage(-100, 7, 55, "x"), true)

	require.Len(t, tg.SentMessages, 1)
	assert.Equal(t, "muted till morning", tg.SentMessages[0].Text)
}

func TestDeleteMessageAfter(t *testing.T) {
	b, tg := newMockBot(t)
	b.deleteMessageAfter(-100, 42, 5*time.Millisecond)

	require.Eventually(t, func() bool {
		tg.mu.Lock()
		defer tg.mu.Unlock()
		return len(tg.DeletedIDs) == 1 && tg.DeletedIDs[0] == [2]int64{-100, 42}
	}, time.Second, 5*time.Millisecond)

	// A zero message id is a no-op (nothing to delete).
	b.deleteMessageAfter(-100, 0, time.Millisecond)
}

func TestStageNightMode_DeletesAndStops(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	enableNightModeNow(b.config)

	mc := b.newInboundContext(testMessage(-100, 7, 55, "hello"), false)
	dir := b.stageNightMode(mc)

	assert.Equal(t, Stop, dir, "night mode must stop the pipeline so the message is never recorded")
	require.Len(t, tg.DeletedIDs, 1)
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
	require.Len(t, tg.SentMessages, 1)
	assert.Equal(t, 55, tg.SentMessages[0].ReplyToMessageID)
}

func TestStageNightMode_ExemptEditsAndService(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	enableNightModeNow(b.config)

	// An edited message is exempt.
	mcEdit := b.newInboundContext(testMessage(-100, 7, 55, "x"), true)
	assert.Equal(t, Continue, b.stageNightMode(mcEdit))

	// A service message is exempt.
	svc := testMessage(-100, 7, 56, "")
	svc.NewChatTitle = "renamed"
	mcSvc := b.newInboundContext(svc, false)
	assert.Equal(t, Continue, b.stageNightMode(mcSvc))

	assert.Empty(t, tg.DeletedIDs)
	assert.Empty(t, tg.SentMessages)
}

func TestStageNightMode_InactiveContinues(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	// Night mode left disabled -> the stage is a no-op.
	mc := b.newInboundContext(testMessage(-100, 7, 55, "x"), false)
	assert.Equal(t, Continue, b.stageNightMode(mc))
	assert.Empty(t, tg.DeletedIDs)
	assert.Empty(t, tg.SentMessages)
}

func TestStageNightMode_ExemptSuperAdmin(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.Admin.SuperAdminUserID = 7
	enableNightModeNow(b.config)

	mc := b.newInboundContext(testMessage(-100, 7, 55, "hi"), false)
	assert.Equal(t, Continue, b.stageNightMode(mc))
	assert.Empty(t, tg.DeletedIDs, "the super-admin must be able to speak during night mode")
	assert.Empty(t, tg.SentMessages)
}

func TestStageNightMode_ExemptChatAdmin(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	tg.GetMemberFunc = func(chatID, userID int64) (telegram.ChatMember, error) {
		return telegram.ChatMember{User: &telegram.User{ID: userID}, Status: telegram.StatusAdministrator}, nil
	}
	enableNightModeNow(b.config)

	mc := b.newInboundContext(testMessage(-100, 7, 55, "hi"), false)
	assert.Equal(t, Continue, b.stageNightMode(mc))
	assert.Empty(t, tg.DeletedIDs, "a chat admin must be able to speak during night mode")
	assert.Empty(t, tg.SentMessages)
}

func TestStageNightMode_ExemptTelegramServiceAccount(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	enableNightModeNow(b.config)

	mc := b.newInboundContext(testMessage(-100, TelegramServiceUserID, 55, "channel post"), false)
	assert.Equal(t, Continue, b.stageNightMode(mc))
	assert.Empty(t, tg.DeletedIDs, "the Telegram service account must never be deleted by night mode")
	assert.Empty(t, tg.SentMessages)
}

func TestStageNightMode_FloodMutesAfterThreshold(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	enableNightModeNow(b.config)
	b.config.NightMode.MuteAfterMessages = 2

	// Posting exactly the threshold must not mute.
	for _, id := range []int{55, 56} {
		mc := b.newInboundContext(testMessage(-100, 7, id, "spam"), false)
		require.Equal(t, Stop, b.stageNightMode(mc))
	}
	muted, err := b.db.IsUserMuted(7, -100)
	require.NoError(t, err)
	assert.False(t, muted, "posting exactly N messages must not mute")
	assert.Empty(t, tg.Restrictions)

	// The next message exceeds the threshold -> mute until the window ends.
	mc := b.newInboundContext(testMessage(-100, 7, 57, "spam"), false)
	require.Equal(t, Stop, b.stageNightMode(mc))

	muted, err = b.db.IsUserMuted(7, -100)
	require.NoError(t, err)
	assert.True(t, muted, "posting more than N messages must mute the user")
	require.NotEmpty(t, tg.Restrictions)
	assert.Equal(t, int64(7), tg.Restrictions[0].UserID)
	assert.Equal(t, int64(-100), tg.Restrictions[0].ChatID)
	assert.Greater(t, tg.Restrictions[0].UntilDate, int64(0), "the mute must have an until date (the window end)")
}

func TestStageNightMode_FloodDisabledByDefault(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	enableNightModeNow(b.config)
	// MuteAfterMessages left 0 (disabled).

	for _, id := range []int{55, 56, 57, 58} {
		mc := b.newInboundContext(testMessage(-100, 7, id, "spam"), false)
		require.Equal(t, Stop, b.stageNightMode(mc))
	}
	muted, err := b.db.IsUserMuted(7, -100)
	require.NoError(t, err)
	assert.False(t, muted, "the flood-mute must stay off when mute_after_messages is 0")
	assert.Empty(t, tg.Restrictions)
}

func TestRegisterNightModeMessage_PerUserAndFiresOnce(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	enableNightModeNow(b.config)
	b.config.NightMode.MuteAfterMessages = 2
	now := time.Now()

	// User 7 crosses the threshold on the third message and only then.
	assert.False(t, b.registerNightModeMessage(testMessage(-100, 7, 1, "x"), now))
	assert.False(t, b.registerNightModeMessage(testMessage(-100, 7, 2, "x"), now))
	assert.True(t, b.registerNightModeMessage(testMessage(-100, 7, 3, "x"), now), "third message exceeds N")
	assert.False(t, b.registerNightModeMessage(testMessage(-100, 7, 4, "x"), now), "the mute fires exactly once")

	// A different user is counted independently.
	assert.False(t, b.registerNightModeMessage(testMessage(-100, 8, 5, "x"), now))
}
