// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"

	tgbotapi "gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These scenarios pin down the link-complaint flow: addressing the bot (by
// mention or by replying to it) with a t.me link pointing at a message of the
// same chat reports that linked message exactly like replying to it does -
// cross-model re-moderation first, manual admin card as the fallback. Links to
// other chats, to messages the bot never recorded, or to the bot's own messages
// are not complaints.

func TestSameChatMessageLinkTarget(t *testing.T) {
	public := tgbotapi.Chat{ID: -100, Username: "testchat"}
	private := tgbotapi.Chat{ID: -1001234567890}

	cases := []struct {
		name     string
		text     string
		chat     tgbotapi.Chat
		wantID   int
		wantSame bool
	}{
		{"public link to this chat", "look https://t.me/testchat/55", public, 55, true},
		{"public link is case-insensitive", "https://t.me/TestChat/55", public, 55, true},
		{"public link with topic", "https://t.me/testchat/12/55", public, 55, true},
		{"public link to another chat", "https://t.me/otherchat/55", public, 55, false},
		{"private link to this chat", "https://t.me/c/1234567890/55", private, 55, true},
		{"private link to another chat", "https://t.me/c/999/55", private, 55, false},
		{"private link when chat has a username", "https://t.me/c/1234567890/55", public, 55, false},
		{"no link", "just chatting", public, 0, false},
		{"public link but chat has no username", "https://t.me/testchat/55", private, 55, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, same := sameChatMessageLinkTarget(tc.text, tc.chat)
			assert.Equal(t, tc.wantSame, same)
			if same {
				assert.Equal(t, tc.wantID, id)
			}
		})
	}
}

// newLinkComplaint stores the reported message (id 55, from offender user 7) in
// the DB and returns the inbound complaint (id 100 from reporter user 9) that
// links to it, in public moderation chat -100 (@testchat).
func newLinkComplaint(t *testing.T, b *Bot, text string) *tgbotapi.Message {
	t.Helper()
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 55,
		ChatID:    -100,
		UserID:    7,
		Username:  "offender",
		Text:      "the reported message",
		Timestamp: time.Now(),
	}))
	complaint := testMessage(-100, 9, 100, text)
	complaint.Chat.Username = "testchat"
	return complaint
}

// A mention carrying a link to a message of the same chat reports that message:
// with every model clearing it, the bot escalates to the manual admin card.
func TestLinkComplaint_Mention_AllModelsClear_FallsBackToManual(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.config.Admin.ChatID = -999
	b.moderatedMsgs = make(map[string]time.Time)

	b.handleBotMention(newLinkComplaint(t, b, "@testbot https://t.me/testchat/55"))

	assert.GreaterOrEqual(t, rt.count(), 1, "the linked message must be re-checked by the AI before escalating")
	assert.True(t, adminCardSent(tg, -999), "a clean cross-model verdict must fall back to the manual admin card")
	assert.Empty(t, tg.DeletedIDs, "a cleared message must not be auto-deleted")
}

// When a model flags the linked message, the bot acts on it automatically and
// does not also fall back to manual moderation.
func TestLinkComplaint_ModelFlags_ActsAutomatically(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"spam\nobvious spam"}}]}`))
	})
	moderationConfig(b)
	noNotify := false
	b.config.AI.ContentModeration.Rules = []config.ModerationRule{
		{Trigger: "spam", Action: "delete", NotifyAdmin: &noNotify},
	}
	b.config.Admin.ChatID = -999
	b.moderatedMsgs = make(map[string]time.Time)

	require.True(t, b.handleLinkModerationTrigger(newLinkComplaint(t, b, "@testbot https://t.me/testchat/55")))

	require.Len(t, tg.DeletedIDs, 1, "a flagged linked message must be auto-deleted")
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
	assert.False(t, adminCardSent(tg, -999), "an auto-handled violation must not also post the manual complaint card")
}

// Replying to the bot with a link to a message of the same chat reports it too,
// without needing a mention.
func TestLinkComplaint_ReplyToBot_FallsBackToManual(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.config.Admin.ChatID = -999
	b.moderatedMsgs = make(map[string]time.Time)

	complaint := newLinkComplaint(t, b, "https://t.me/testchat/55")
	complaint.ReplyToMessage = testMessage(-100, b.botSelf.ID, 90, "bot message")

	require.True(t, b.handleLinkModerationTrigger(complaint))
	assert.True(t, adminCardSent(tg, -999))
}

// A link pointing at another chat is not a complaint about this chat.
func TestLinkComplaint_OtherChatLink_Ignored(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.config.Admin.ChatID = -999
	b.moderatedMsgs = make(map[string]time.Time)

	assert.False(t, b.handleLinkModerationTrigger(newLinkComplaint(t, b, "@testbot https://t.me/otherchat/55")))
	assert.Equal(t, 0, rt.count(), "a link to another chat must not trigger re-moderation")
	assert.False(t, adminCardSent(tg, -999))
}

// A link to a message the bot never recorded can't be reported.
func TestLinkComplaint_UnknownMessage_Ignored(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.config.Admin.ChatID = -999
	b.moderatedMsgs = make(map[string]time.Time)

	assert.False(t, b.handleLinkModerationTrigger(newLinkComplaint(t, b, "@testbot https://t.me/testchat/56")))
	assert.Equal(t, 0, rt.count())
	assert.False(t, adminCardSent(tg, -999))
}

// Linking one of the bot's own messages is conversation with the bot, not a
// complaint.
func TestLinkComplaint_BotOwnMessage_Ignored(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.config.Admin.ChatID = -999
	b.moderatedMsgs = make(map[string]time.Time)

	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 70,
		ChatID:    -100,
		UserID:    b.botSelf.ID,
		Username:  "testbot",
		Text:      "bot message",
		Timestamp: time.Now(),
	}))
	complaint := testMessage(-100, 9, 100, "@testbot https://t.me/testchat/70")
	complaint.Chat.Username = "testchat"

	assert.False(t, b.handleLinkModerationTrigger(complaint))
	assert.Equal(t, 0, rt.count())
	assert.False(t, adminCardSent(tg, -999))
}
