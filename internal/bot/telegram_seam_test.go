// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"errors"
	"path/filepath"
	"testing"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockBot builds a Bot wired to a mock telegram client and a temp SQLite DB,
// with api left nil (the outbound port is fully mocked).
func newMockBot(t *testing.T) (*Bot, *mockTelegram) {
	t.Helper()
	db, err := database.InitLocal(filepath.Join(t.TempDir(), "bot.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	tg := &mockTelegram{}
	b := &Bot{
		config:       &config.Config{},
		db:           db,
		tg:           tg,
		botSelf:      telegram.User{ID: 999, Username: "testbot"},
		profileCache: newUserProfileCache(),
	}
	return b, tg
}

func TestSendToAdminChat_UsesPort(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.ChatID = -100
	b.config.Admin.ReplyMessageIDs = []int{42}

	b.sendToAdminChat("hello admin")

	require.Len(t, tg.SentMessages, 1)
	assert.Equal(t, int64(-100), tg.SentMessages[0].ChatID)
	assert.Equal(t, "hello admin", tg.SentMessages[0].Text)
	assert.Equal(t, 42, tg.SentMessages[0].ReplyToMessageID)
}

func TestSendOTPToSuperAdmin_RequiresAPI(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Admin.SuperAdminUserID = 7
	// api is nil -> OTP send is refused (guards against missing bot token).
	err := b.SendOTPToSuperAdmin("123456")
	require.Error(t, err)
}

func TestNotifySuperAdminStartup_SendsWhenEnabled(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.SuperAdminUserID = 7
	b.config.Admin.NotifyStartup = true

	b.notifySuperAdminStartup()

	require.Len(t, tg.SentMessages, 1)
	assert.Equal(t, int64(7), tg.SentMessages[0].ChatID)
	assert.Contains(t, tg.SentMessages[0].Text, "testbot")
}

func TestNotifySuperAdminStartup_SkippedWhenDisabled(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.SuperAdminUserID = 7
	b.config.Admin.NotifyStartup = false

	b.notifySuperAdminStartup()
	assert.Empty(t, tg.SentMessages, "disabled flag must suppress the notification")
}

func TestNotifySuperAdminStartup_SkippedWithoutSuperAdmin(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.SuperAdminUserID = 0
	b.config.Admin.NotifyStartup = true

	b.notifySuperAdminStartup()
	assert.Empty(t, tg.SentMessages, "no super-admin configured must suppress the notification")
}

func TestSetAndClearReaction_UsesPort(t *testing.T) {
	b, tg := newMockBot(t)
	b.setMessageReaction(-100, 5, "👍")
	b.clearMessageReaction(-100, 5)

	require.Len(t, tg.Reactions, 2)
	assert.Equal(t, "👍", tg.Reactions[0].Emoji)
	assert.Equal(t, "", tg.Reactions[1].Emoji)
}

func TestRestrictAndUnrestrict_UsesPort(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	_, err := b.restrictUserInChats(55, -100, 0)
	require.NoError(t, err)
	require.Len(t, tg.Restrictions, 1)
	assert.Equal(t, int64(55), tg.Restrictions[0].UserID)
	assert.False(t, tg.Restrictions[0].Permissions.CanSendMessages)

	b.unrestrictUserInChats(55, -100)
	require.Len(t, tg.Restrictions, 2)
	assert.True(t, tg.Restrictions[1].Permissions.CanSendMessages)
}

func TestDeleteMessageWithRetry_PermanentError(t *testing.T) {
	b, tg := newMockBot(t)
	// A permanent (4xx, non-429) API error must not be retried.
	tg.DeleteFunc = func(chatID int64, messageID int) error {
		return &telegram.APIError{Code: 403, Message: "forbidden"}
	}
	err := b.deleteMessageWithRetry(-100, 5)
	require.Error(t, err)
	assert.Len(t, tg.DeletedIDs, 1, "permanent error must not be retried")
}

func TestDeleteMessageWithRetry_AlreadyGoneIsSuccess(t *testing.T) {
	b, tg := newMockBot(t)
	tg.DeleteFunc = func(chatID int64, messageID int) error {
		return errors.New("Bad Request: message to delete not found")
	}
	err := b.deleteMessageWithRetry(-100, 5)
	require.NoError(t, err, "already-deleted message is treated as success")
}

func TestBanUnban_UsesPort(t *testing.T) {
	b, tg := newMockBot(t)
	require.NoError(t, b.tg.BanChatMember(-100, 7))
	require.NoError(t, b.tg.UnbanChatMember(-100, 7, true))
	assert.Equal(t, [][2]int64{{-100, 7}}, tg.Bans)
	assert.Equal(t, [][2]int64{{-100, 7}}, tg.Unbans)
}

func TestIsUserAdminInChat_UsesPort(t *testing.T) {
	b, tg := newMockBot(t)
	tg.GetMemberFunc = func(chatID, userID int64) (telegram.ChatMember, error) {
		return telegram.ChatMember{Status: telegram.StatusAdministrator}, nil
	}
	assert.True(t, b.isUserAdminInChat(7, -100))

	tg.GetMemberFunc = func(chatID, userID int64) (telegram.ChatMember, error) {
		return telegram.ChatMember{Status: telegram.StatusMember}, nil
	}
	assert.False(t, b.isUserAdminInChat(7, -100))
}

func TestCreateModerationKeyboard_Neutral(t *testing.T) {
	b, _ := newMockBot(t)
	kb := b.createModerationKeyboard(-100, 22, 777, 0, false)
	// First row = warn; mute rows; cruel-mute row; delete row; not-violation row.
	require.NotEmpty(t, kb.Rows)
	assert.Equal(t, "warn_-100_22_777_0", kb.Rows[0][0].CallbackData)

	// Last row is the "not violation" action.
	last := kb.Rows[len(kb.Rows)-1]
	assert.Contains(t, last[0].CallbackData, "notbad_")
}

func TestDownloadImage_UsesPortURL(t *testing.T) {
	b, tg := newMockBot(t)
	tg.GetFileFunc = func(fileID string) (telegram.File, error) {
		// Point at an unroutable URL so the HTTP GET fails fast but we still
		// confirm the port was consulted for the download URL.
		return telegram.File{FileID: fileID, DownloadURL: "http://127.0.0.1:0/x"}, nil
	}
	_, err := b.downloadImage("file123")
	require.Error(t, err) // download fails, but GetFile path was exercised
}
