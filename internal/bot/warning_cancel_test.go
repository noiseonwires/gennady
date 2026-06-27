// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"
	"time"

	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// When a warning is cancelled, the bot must delete the exact warning reply it
// recorded at warn time - not merely the most recent bot reply to the offending
// message, which could be an unrelated message such as a creative reply sent
// after the warning.

func TestDeleteBotWarningReply_PrefersRecordedWarningMessage(t *testing.T) {
	b, tg := newMockBot(t)
	chatID := int64(-100)
	offending := 55
	warningMsgID := 60
	creativeMsgID := 70

	// The bot's warning reply to the offending message (sent first).
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: warningMsgID, ChatID: chatID, UserID: b.botSelf.ID, Username: "bot",
		Text: "warning", ReplyToMessageID: &offending, Timestamp: time.Now().Add(-time.Minute),
	}))
	// A later creative reply to the SAME offending message (most recent reply).
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: creativeMsgID, ChatID: chatID, UserID: b.botSelf.ID, Username: "bot",
		Text: "witty reply", ReplyToMessageID: &offending, Timestamp: time.Now(),
	}))

	b.deleteBotWarningReply(chatID, 7, offending, warningMsgID)

	require.Len(t, tg.DeletedIDs, 1)
	assert.Equal(t, [2]int64{chatID, int64(warningMsgID)}, tg.DeletedIDs[0],
		"must delete the recorded warning message, not the more recent creative reply")
}

func TestDeleteBotWarningReply_LegacyFallbackToBotReply(t *testing.T) {
	b, tg := newMockBot(t)
	chatID := int64(-100)
	offending := 55
	warningMsgID := 60

	// Only the warning reply exists; no recorded id (legacy warning).
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: warningMsgID, ChatID: chatID, UserID: b.botSelf.ID, Username: "bot",
		Text: "warning", ReplyToMessageID: &offending, Timestamp: time.Now(),
	}))

	b.deleteBotWarningReply(chatID, 7, offending, 0)

	require.Len(t, tg.DeletedIDs, 1)
	assert.Equal(t, [2]int64{chatID, int64(warningMsgID)}, tg.DeletedIDs[0],
		"with no recorded id, fall back to the bot's reply to the offending message")
}
