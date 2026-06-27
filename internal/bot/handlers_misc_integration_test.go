// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"

	tgbotapi "gennadium/internal/telegram"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- message_purge ------------------------------------------------------------

func TestPurgePeriodSince(t *testing.T) {
	_, ok := purgePeriodSince("1h")
	assert.True(t, ok)
	_, ok = purgePeriodSince("1d")
	assert.True(t, ok)
	zero, ok := purgePeriodSince("all")
	assert.True(t, ok)
	assert.True(t, zero.IsZero())
	_, ok = purgePeriodSince("nope")
	assert.False(t, ok)
	_, ok = purgePeriodSince("garbage")
	assert.False(t, ok)
}

func TestDeleteUserMessagesPromptKeyboard(t *testing.T) {
	kb := deleteUserMessagesPromptKeyboard(-100, 7)
	// 2 rows of 2 buttons (1h/1d, all/nope).
	require.Len(t, kb.Rows, 2)
	require.Len(t, kb.Rows[0], 2)
	assert.Contains(t, kb.Rows[0][0].CallbackData, "delmsg_1h_-100_7")
	assert.Contains(t, kb.Rows[1][1].CallbackData, "delmsg_nope_-100_7")
}

func TestHandleDeleteUserMessagesAction_Nope(t *testing.T) {
	b, tg := adminBot(t)
	q := testCallbackQuery(1, -100, 55, "delmsg_nope_-100_7")
	b.handleDeleteUserMessagesAction(q, []string{"delmsg", "nope", "-100", "7"})
	// Declined -> the card is edited with a notice, no deletes.
	assert.Empty(t, tg.DeletedIDs)
	assert.NotEmpty(t, tg.EditedTexts)
}

func TestHandleDeleteUserMessagesAction_All(t *testing.T) {
	b, tg := adminBot(t)
	// Seed two messages from the user so deleteUserMessagesSince has targets.
	for _, id := range []int{10, 11} {
		require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
			MessageID: id, ChatID: -100, UserID: 7, Username: "u", Text: "x", Timestamp: time.Now(),
		}))
	}
	q := testCallbackQuery(1, -100, 55, "delmsg_all_-100_7")
	b.handleDeleteUserMessagesAction(q, []string{"delmsg", "all", "-100", "7"})

	// Both messages deleted via the port.
	assert.GreaterOrEqual(t, len(tg.DeletedIDs), 2)
}

func TestDeleteUserMessagesSince(t *testing.T) {
	b, tg := newMockBot(t)
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 10, ChatID: -100, UserID: 7, Username: "u", Text: "x", Timestamp: time.Now(),
	}))
	n := b.deleteUserMessagesSince(7, -100, time.Time{})
	assert.Equal(t, 1, n)
	require.Len(t, tg.DeletedIDs, 1)
}

// --- cruel_mute ---------------------------------------------------------------

func TestHandleCruelMuteIfActive(t *testing.T) {
	b, tg := newMockBot(t)
	// Give the user an active cruel mute.
	require.NoError(t, b.db.AddMutedUserSafely(&database.MutedUser{
		UserID: 7, ChatID: -100, MutedAt: time.Now(), UnmuteAt: time.Now().Add(time.Hour),
		Reason: "cmute", IsActive: true, IsCruel: true,
	}))

	msg := testMessage(-100, 7, 55, "should be deleted")
	handled := b.handleCruelMuteIfActive(msg)
	assert.True(t, handled)
	require.Len(t, tg.DeletedIDs, 1)
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
}

func TestHandleCruelMuteIfActive_NotCruel(t *testing.T) {
	b, _ := newMockBot(t)
	// Regular (non-cruel) mute -> not handled here.
	require.NoError(t, b.db.AddMutedUserSafely(&database.MutedUser{
		UserID: 7, ChatID: -100, MutedAt: time.Now(), UnmuteAt: time.Now().Add(time.Hour),
		Reason: "mute", IsActive: true, IsCruel: false,
	}))
	assert.False(t, b.handleCruelMuteIfActive(testMessage(-100, 7, 55, "x")))
}

func TestHandleCruelMuteIfActive_NoMute(t *testing.T) {
	b, _ := newMockBot(t)
	assert.False(t, b.handleCruelMuteIfActive(testMessage(-100, 7, 55, "x")))
	// nil / anonymous message.
	assert.False(t, b.handleCruelMuteIfActive(nil))
}

func TestHandleCruelMuteAction(t *testing.T) {
	b, _ := adminBot(t)
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 55, ChatID: -100, UserID: 7, Username: "bad", Text: "x", Timestamp: time.Now(),
	}))
	q := testCallbackQuery(1, -100, 55, "cmute_60_-100_55_7_0")
	b.handleCruelMuteAction(q, []string{"cmute", "60", "-100", "55", "7", "0"})

	// The cruel mute is recorded as an active mute.
	muted, err := b.db.IsUserMuted(7, -100)
	require.NoError(t, err)
	assert.True(t, muted)
}

func TestDescribeCruelMutedMessage(t *testing.T) {
	short := describeCruelMutedMessage(&tgbotapi.Message{Text: "hi"})
	assert.Equal(t, "hi", short)

	// Caption fallback.
	cap := describeCruelMutedMessage(&tgbotapi.Message{Caption: "a caption"})
	assert.Equal(t, "a caption", cap)
}

// --- super-admin commands -----------------------------------------------------

// command builds a /command message with the proper bot_command entity so
// message.Command() / message.CommandArguments() parse correctly.
func command(chatID, userID int64, text string) *tgbotapi.Message {
	cmdLen := len(text)
	if sp := indexByte(text, ' '); sp >= 0 {
		cmdLen = sp
	}
	return &tgbotapi.Message{
		MessageID: 1,
		Chat:      tgbotapi.Chat{ID: chatID, Type: "private"},
		From:      &tgbotapi.User{ID: userID},
		Text:      text,
		Entities:  []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: cmdLen}},
	}
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func TestHandleSuperAdminCommand_Delete(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.SuperAdminUserID = 1
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	msg := command(1, 1, "/delete 55 -100")
	handled := b.handleSuperAdminCommand(msg)
	assert.True(t, handled)
	require.Len(t, tg.DeletedIDs, 1)
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
}

func TestHandleSuperAdminCommand_Block(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.SuperAdminUserID = 1
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	msg := command(1, 1, "/block 7 -100")
	handled := b.handleSuperAdminCommand(msg)
	assert.True(t, handled)
	require.Len(t, tg.Bans, 1)
	assert.Equal(t, [2]int64{-100, 7}, tg.Bans[0])
}

func TestHandleSuperAdminCommand_Mute(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.SuperAdminUserID = 1
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	msg := command(1, 1, "/mute 7 -100")
	handled := b.handleSuperAdminCommand(msg)
	assert.True(t, handled)
	// The super-admin /mute applies a Telegram restriction (no DB mute record).
	require.Len(t, tg.Restrictions, 1)
	assert.Equal(t, int64(7), tg.Restrictions[0].UserID)
	assert.False(t, tg.Restrictions[0].Permissions.CanSendMessages)
}

func TestHandleSuperAdminCommand_MissingArg(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.SuperAdminUserID = 1
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	msg := command(1, 1, "/delete")
	handled := b.handleSuperAdminCommand(msg)
	assert.True(t, handled) // command recognized, but no-op due to missing arg
	assert.Empty(t, tg.DeletedIDs)
}
