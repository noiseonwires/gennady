// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"

	"gennadium/internal/config"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleMessage_BotOwnMessageStored(t *testing.T) {
	b, _ := newMockBot(t)
	b.botSelf = telegramUser(999, "thebot")

	// A message authored by the bot itself is stored for reply-chain context.
	msg := &tgbotapi.Message{
		MessageID: 10,
		Chat:      tgbotapi.Chat{ID: -100, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 999},
		Text:      "bot says hi",
	}
	b.handleMessage(msg, false)

	got, err := b.db.GetMessageInfo(10, -100)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "bot says hi", got.Text)
	assert.Equal(t, int64(999), got.UserID)
}

// A chat excluded from moderation (moderation.excluded_topics covering the whole
// chat via topic: -1 / TopicAny) must still have its messages recorded to the
// DB. Exclusion only turns off AI moderation, not message persistence or the
// other independently-scoped features.
func TestHandleMessage_ExcludedChatStillStoresMessage(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.Moderation.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}

	// Sanity: the (chat, topic) is genuinely excluded from moderation.
	require.False(t, b.config.IsModerationActive(-100, 0))

	msg := &tgbotapi.Message{
		MessageID: 11,
		Chat:      tgbotapi.Chat{ID: -100, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 42, Username: "alice"},
		Text:      "hello from an excluded chat",
	}
	b.handleMessage(msg, false)

	got, err := b.db.GetMessageInfo(11, -100)
	require.NoError(t, err)
	require.NotNil(t, got, "message in an excluded chat must still be stored")
	assert.Equal(t, "hello from an excluded chat", got.Text)
	assert.Equal(t, int64(42), got.UserID)
}

func TestIsBotMentioned(t *testing.T) {
	b, _ := newMockBot(t)
	b.botSelf = telegramUser(999, "thebot")

	// Plain-text mention.
	msg := &tgbotapi.Message{Chat: tgbotapi.Chat{ID: -100, Type: "supergroup"}, Text: "hey @thebot help"}
	assert.True(t, b.isBotMentioned(msg))

	// Entity mention.
	msgE := &tgbotapi.Message{
		Chat:     tgbotapi.Chat{ID: -100, Type: "supergroup"},
		Text:     "@thebot",
		Entities: []tgbotapi.MessageEntity{{Type: "mention", Offset: 0, Length: 7}},
	}
	assert.True(t, b.isBotMentioned(msgE))

	// No mention.
	assert.False(t, b.isBotMentioned(&tgbotapi.Message{Chat: tgbotapi.Chat{ID: -100, Type: "supergroup"}, Text: "no mention here"}))
}

func TestIsServiceMessage(t *testing.T) {
	b, _ := newMockBot(t)
	assert.True(t, b.isServiceMessage(&tgbotapi.Message{NewChatMembers: []tgbotapi.User{{ID: 1}}}))
	assert.True(t, b.isServiceMessage(&tgbotapi.Message{LeftChatMember: &tgbotapi.User{ID: 1}}))
	assert.True(t, b.isServiceMessage(&tgbotapi.Message{PinnedMessage: &tgbotapi.Message{}}))
	assert.True(t, b.isServiceMessage(&tgbotapi.Message{NewChatTitle: "new"}))
	assert.False(t, b.isServiceMessage(&tgbotapi.Message{Text: "regular"}))
}

func TestHandlePrivateMessage_NonAdminGetsAbout(t *testing.T) {
	b, tg := newMockBot(t)
	// No super-admin configured and GetChatMember returns non-admin -> not admin.
	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat:      tgbotapi.Chat{ID: 42, Type: "private"},
		From:      &tgbotapi.User{ID: 42},
		Text:      "hi",
	}
	b.handlePrivateMessage(msg)

	require.Len(t, tg.SentMessages, 1)
	assert.Equal(t, int64(42), tg.SentMessages[0].ChatID)
	assert.Contains(t, tg.SentMessages[0].Text, "👋")
}

func TestHandleCommand_HelpInPrivate(t *testing.T) {
	b, tg := newMockBot(t)
	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat:      tgbotapi.Chat{ID: 42, Type: "private"},
		From:      &tgbotapi.User{ID: 42},
		Text:      "/help",
		Entities:  []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
	}
	b.handleCommand(msg)
	require.Len(t, tg.SentMessages, 1)
	assert.Contains(t, tg.SentMessages[0].Text, "gennady")
}

func TestDescribeUpdateType_Handler(t *testing.T) {
	assert.Equal(t, "message", describeUpdateType(tgbotapi.Update{Message: &tgbotapi.Message{}}))
	assert.Equal(t, "callback_query", describeUpdateType(tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{}}))
}

func TestShouldAddToDeleteQueue(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.MessageDeletion.Enabled = true
	b.config.MessageDeletion.IncludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}

	msg := &tgbotapi.Message{MessageID: 5, Chat: tgbotapi.Chat{ID: -100}}
	assert.True(t, b.shouldAddToDeleteQueue(msg))

	// Not a moderation chat -> false.
	other := &tgbotapi.Message{MessageID: 5, Chat: tgbotapi.Chat{ID: -999}}
	assert.False(t, b.shouldAddToDeleteQueue(other))
}

// telegramUser builds a neutral telegram.User for botSelf assignment in tests.
func telegramUser(id int64, username string) telegram.User {
	return telegram.User{ID: id, Username: username}
}
