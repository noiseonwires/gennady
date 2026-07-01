// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"testing"
	"time"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These scenarios pin down how the creative-reply path (creativeReply)
// coordinates with content moderation:
//   - a creative-reply trigger (reply to a bot message or a bot mention) whose
//     own message was flagged by moderation must NOT receive a friendly reply;
//   - a mention replying to one of the bot's own messages is conversation aimed
//     at the bot (not a complaint about another user), so it still receives a
//     creative reply once moderation has cleared it;
//   - a genuine complaint (mention replying to *another* user's message) still
//     receives a creative reply - the cross-model re-moderation that ran first
//     (synchronously, in handleBotMention) is reflected in its chain context;
//   - an ordinary reply to a bot message (no moderation involvement) still
//     receives a creative reply.

// creativeModerationConfig enables creative replies in moderation chat -100 and
// wires a usable full model + sane rate limits so handleCreativeFollowUp can run.
func creativeModerationConfig(b *Bot) {
	b.config.AI.Enabled = true
	b.config.AI.CreativeReplies.Enabled = true
	b.config.AI.CreativeReplies.UseFullModel = true
	b.config.AI.CreativeReplies.MaxMessages = 10
	b.config.AI.CreativeReplies.TimeWindow = 24
	b.config.AI.CreativeReplies.Prompt = config.PromptPair{
		System: "be witty",
		User:   "reply to {{message}} {{context}}",
	}
	b.config.AI.FullModel = fullModelConfigs()
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.moderatedMsgs = make(map[string]time.Time)
}

// A creative-reply trigger whose own message was flagged by moderation is
// skipped: we don't reward a rule-breaking message with a friendly reply.
func TestCreativeReply_SkipsWhenMessageFlaggedByModeration(t *testing.T) {
	b, tg := newMockBot(t)
	creativeModerationConfig(b)

	// The message replies to one of the bot's own messages (a creative-reply
	// trigger) but has already been actioned by content moderation.
	msg := testMessage(-100, 7, 100, "you are dumb")
	msg.ReplyToMessage = testMessage(-100, b.botSelf.ID, 50, "bot said something")

	// moderated=true: the moderate stage flagged and actioned this message.
	b.creativeReply(msg, msg, true)

	assert.Empty(t, tg.SentMessages, "a flagged message must not receive a creative reply")
}

// A bot mention replying to one of the bot's own messages is conversation aimed
// at the bot (not a complaint about another user). Once moderation has cleared
// the message it must still receive a creative reply, matching the owner's
// intent that the bot answers (and claps back at) messages addressed to it.
func TestCreativeReply_ReplyToBotWithMentionGeneratesReply(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a witty reply"}}]}`))
	})
	creativeModerationConfig(b)

	msg := testMessage(-100, 7, 100, "@testbot what do you think")
	msg.ReplyToMessage = testMessage(-100, b.botSelf.ID, 50, "bot said something")

	b.creativeReply(msg, msg, false)

	require.Len(t, tg.SentMessages, 1, "a mention replying to the bot is conversation aimed at the bot and must receive a creative reply")
	assert.Equal(t, 100, tg.SentMessages[0].ReplyToMessageID)
	assert.Contains(t, tg.SentMessages[0].Text, "a witty reply")
}

// A genuine complaint (mention replying to another user's message) still
// receives a creative reply once the (synchronous, earlier) re-moderation has
// run, so the response can reflect the moderation outcome in its chain context.
func TestCreativeReply_ComplaintStillGeneratesReply(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a witty reply"}}]}`))
	})
	creativeModerationConfig(b)

	msg := testMessage(-100, 9, 100, "@testbot look at this")
	msg.ReplyToMessage = testMessage(-100, 7, 55, "the reported message")

	b.creativeReply(msg, msg, false)

	require.Len(t, tg.SentMessages, 1, "a complaint must still receive a creative reply")
	assert.Equal(t, int64(-100), tg.SentMessages[0].ChatID)
	assert.Equal(t, 100, tg.SentMessages[0].ReplyToMessageID)
	assert.Contains(t, tg.SentMessages[0].Text, "a witty reply")
}

// An ordinary reply to a bot message (no mention, no moderation involvement)
// still receives a creative reply - the core follow-up feature is preserved.
func TestCreativeReply_PlainReplyToBotGeneratesReply(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a witty reply"}}]}`))
	})
	creativeModerationConfig(b)

	msg := testMessage(-100, 7, 100, "thanks for that")
	msg.ReplyToMessage = testMessage(-100, b.botSelf.ID, 50, "bot said something")

	b.creativeReply(msg, msg, false)

	require.Len(t, tg.SentMessages, 1, "an ordinary reply to the bot must still receive a creative reply")
	assert.Equal(t, 100, tg.SentMessages[0].ReplyToMessageID)
	assert.Contains(t, tg.SentMessages[0].Text, "a witty reply")
}
