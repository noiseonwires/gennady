// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"strconv"
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCallbackQuery builds a callback query from an admin user with a backing
// message in the given chat.
func testCallbackQuery(adminID, chatID int64, messageID int, data string) *tgbotapi.CallbackQuery {
	return &tgbotapi.CallbackQuery{
		ID:   "cbq1",
		From: &tgbotapi.User{ID: adminID, FirstName: "Admin", Username: "admin"},
		Data: data,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Chat:      tgbotapi.Chat{ID: chatID, Type: "supergroup"},
			Text:      "report card",
		},
	}
}

// adminBot returns a bot whose super-admin is configured, so isUserAdmin passes
// without any Telegram lookups.
func adminBot(t *testing.T) (*Bot, *mockTelegram) {
	b, tg := newMockBot(t)
	b.config.Admin.ChatID = -999
	b.config.Admin.SuperAdminUserID = 1
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	return b, tg
}

func TestHandleCallbackQuery_UnauthorizedIgnored(t *testing.T) {
	b, tg := adminBot(t)
	// A non-admin user (id 2, super-admin is 1) triggers a callback.
	q := testCallbackQuery(2, -100, 55, "warn_-100_55_7_0")
	q.From.ID = 2
	// GetChatMember returns non-admin so the auth check fails.
	tg.GetMemberFunc = func(chatID, userID int64) (telegram.ChatMember, error) {
		return telegram.ChatMember{Status: telegram.StatusMember}, nil
	}

	b.handleCallbackQuery(q)

	// No handler ran (no answer sent) because the user is not an admin.
	assert.Empty(t, tg.Answered)
}

func TestHandleCallbackQuery_AnswersAfterDispatch(t *testing.T) {
	b, tg := adminBot(t)
	q := testCallbackQuery(1, -100, 55, "notbad_-100_55_7_0")

	b.handleCallbackQuery(q)

	// The callback was answered to clear the Telegram spinner.
	require.Len(t, tg.Answered, 1)
	assert.Equal(t, "cbq1", tg.Answered[0])
}

func TestHandleCallbackQuery_DuplicateSuppressed(t *testing.T) {
	b, tg := adminBot(t)
	q := testCallbackQuery(1, -100, 55, "notbad_-100_55_7_0")

	// First delivery runs the action.
	b.handleCallbackQuery(q)
	sentAfterFirst := len(tg.SentMessages)
	require.NotZero(t, sentAfterFirst, "first delivery should dispatch the action")
	require.Len(t, tg.Answered, 1)

	// Telegram redelivers the exact same callback (identical query ID), as it
	// does on a webhook retry. The action must NOT run a second time, so no
	// additional messages/notifications are sent.
	b.handleCallbackQuery(q)
	assert.Equal(t, sentAfterFirst, len(tg.SentMessages), "duplicate callback must not send more messages")
	assert.Len(t, tg.Answered, 2, "duplicate is still answered to clear the spinner")
}

func TestHandleCallbackQuery_InvalidData(t *testing.T) {
	b, tg := adminBot(t)
	q := testCallbackQuery(1, -100, 55, "x") // < 2 parts
	b.handleCallbackQuery(q)
	assert.Empty(t, tg.Answered)
}

func TestHandleMuteAction(t *testing.T) {
	b, tg := adminBot(t)
	q := testCallbackQuery(1, -100, 55, "mute_30_-100_55_7_0")

	b.handleMuteAction(q, []string{"mute", "30", "-100", "55", "7", "0"})

	// User muted in DB.
	muted, err := b.db.IsUserMuted(7, -100)
	require.NoError(t, err)
	assert.True(t, muted)
	// Telegram restriction applied.
	require.Len(t, tg.Restrictions, 1)
	assert.Equal(t, int64(7), tg.Restrictions[0].UserID)
}

func TestHandleMuteAction_SelfGuard(t *testing.T) {
	b, tg := adminBot(t)
	q := testCallbackQuery(1, -100, 55, "")
	// Target user == bot itself -> guarded, sends sarcastic reply, no mute.
	userID := b.botSelf.ID
	q.Data = "mute"
	b.handleMuteAction(q, []string{"mute", "30", "-100", "55",
		itoa(userID), "0"})

	muted, _ := b.db.IsUserMuted(userID, -100)
	assert.False(t, muted)
	assert.Empty(t, tg.Restrictions)
}

func TestHandleUnmuteAction(t *testing.T) {
	b, tg := adminBot(t)
	// Pre-mute a user.
	require.NoError(t, b.db.AddMutedUserSafely(&database.MutedUser{
		UserID: 7, ChatID: -100, MutedAt: time.Now(), UnmuteAt: time.Now().Add(time.Hour),
		Reason: "x", IsActive: true,
	}))

	q := testCallbackQuery(1, -100, 55, "unmute_7_-100")
	b.handleUnmuteAction(q, []string{"unmute", "7", "-100"})

	muted, err := b.db.IsUserMuted(7, -100)
	require.NoError(t, err)
	assert.False(t, muted)
	// Unrestrict was issued (a permissive restriction).
	require.NotEmpty(t, tg.Restrictions)
	assert.True(t, tg.Restrictions[len(tg.Restrictions)-1].Permissions.CanSendMessages)
}

func TestHandleWarningAction(t *testing.T) {
	b, tg := adminBot(t)
	// Store the offending message so username/text can be resolved.
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 55, ChatID: -100, UserID: 7, Username: "bad", Text: "bad text", Timestamp: time.Now(),
	}))
	q := testCallbackQuery(1, -100, 55, "warn_-100_55_7_0")

	b.handleWarningAction(q, []string{"warn", "-100", "55", "7", "0"})

	// Warning recorded.
	has, err := b.db.HasWarningForMessage(7, 55)
	require.NoError(t, err)
	assert.True(t, has)
	// A warning reply + admin notice were sent.
	assert.NotEmpty(t, tg.SentMessages)
}

func TestHandleDeleteWarningAction(t *testing.T) {
	b, _ := adminBot(t)
	// Pre-existing warning.
	require.NoError(t, b.db.AddWarning(&database.Warning{
		UserID: 7, ChatID: -100, WarnedAt: time.Now(), MessageID: 55,
	}))
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 55, ChatID: -100, UserID: 7, Username: "bad", Text: "x", Timestamp: time.Now(),
	}))

	q := testCallbackQuery(1, -100, 55, "delwarn_-100_55_7_0")
	b.handleDeleteWarningAction(q, []string{"delwarn", "-100", "55", "7", "0"})

	has, err := b.db.HasWarningForMessage(7, 55)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestHandleDeleteAction(t *testing.T) {
	b, tg := adminBot(t)
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 55, ChatID: -100, UserID: 7, Username: "bad", Text: "x", Timestamp: time.Now(),
	}))
	q := testCallbackQuery(1, -100, 55, "delete_-100_55_7_0")

	b.handleDeleteAction(q, []string{"delete", "-100", "55", "7", "0"})

	require.Len(t, tg.DeletedIDs, 1)
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
}

func TestHandleNotBadAction(t *testing.T) {
	b, tg := adminBot(t)
	require.NoError(t, b.db.AddWarning(&database.Warning{
		UserID: 7, ChatID: -100, WarnedAt: time.Now(), MessageID: 55,
	}))
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 55, ChatID: -100, UserID: 7, Username: "bad", Text: "x", Timestamp: time.Now(),
	}))
	q := testCallbackQuery(1, -100, 55, "notbad_-100_55_7_0")

	b.handleNotBadAction(q, []string{"notbad", "-100", "55", "7", "0"})

	// Reaction cleared and warning removed.
	require.NotEmpty(t, tg.Reactions)
	assert.Equal(t, "", tg.Reactions[len(tg.Reactions)-1].Emoji)
	has, err := b.db.HasWarningForMessage(7, 55)
	require.NoError(t, err)
	assert.False(t, has)
}

// itoa formats an int64 as a base-10 string for callback-data construction.
func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
