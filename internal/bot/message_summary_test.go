// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// summaryConfig enables AI message summaries for moderation chat -100 with a
// usable model and a low MinLength so short fixtures cross the threshold.
func summaryConfig(b *Bot) {
	b.config.AI.Enabled = true
	b.config.AI.MessageSummaries.Enabled = true
	b.config.AI.MessageSummaries.MinLength = 20
	b.config.AI.MessageSummaries.UseFullModel = true
	b.config.AI.MessageSummaries.Prompt = config.PromptPair{System: "summarize", User: "summarize {{message}}"}
	b.config.AI.FullModel = fullModelConfigs()
	b.config.AI.LightModel = fullModelConfigs()
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
}

// A message longer than MinLength gets a "TL;DR" reply, and that reply is queued
// for automatic deletion.
func TestMessageSummary_LongMessage_PostsTLDRAndQueuesDeletion(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"short version"}}]}`))
	})
	summaryConfig(b)

	b.generateMessageSummary(testMessage(-100, 7, 55, strings.Repeat("a", 50)))

	require.Len(t, tg.SentMessages, 1, "a long message must get a summary reply")
	assert.Contains(t, tg.SentMessages[0].Text, "TL;DR")
	assert.Contains(t, tg.SentMessages[0].Text, "short version")
	assert.Equal(t, 55, tg.SentMessages[0].ReplyToMessageID)

	// The summary reply (mock message id 1) is queued for deletion.
	inQueue, err := b.db.IsMessageInDeletionQueue(1, -100)
	require.NoError(t, err)
	assert.True(t, inQueue, "the summary reply must be queued for deletion")
}

// A message at or below MinLength is not summarized (no AI call, no reply).
func TestMessageSummary_ShortMessage_NoSummary(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	})
	summaryConfig(b)

	b.generateMessageSummary(testMessage(-100, 7, 55, "too short"))

	assert.Empty(t, tg.SentMessages)
	assert.Equal(t, 0, rt.count(), "a short message must not reach the summary model")
}

// A (chat, topic) excluded from summaries is not summarized even when long.
func TestMessageSummary_ExcludedTopic_NoSummary(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	})
	summaryConfig(b)
	b.config.AI.MessageSummaries.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}

	b.generateMessageSummary(testMessage(-100, 7, 55, strings.Repeat("a", 50)))

	assert.Empty(t, tg.SentMessages, "an excluded (chat, topic) must not be summarized")
}

// The bot never summarizes its own messages.
func TestMessageSummary_BotOwnMessage_NoSummary(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	})
	summaryConfig(b)

	b.generateMessageSummary(testMessage(-100, b.botSelf.ID, 55, strings.Repeat("a", 50)))

	assert.Empty(t, tg.SentMessages)
}

// The summary feature (runMessageSummary) skips users on the exclusion list.
func TestMessageSummary_ExcludedUser_FeatureSkips(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	})
	summaryConfig(b)
	b.config.AI.MessageSummaries.ExcludedUserIDs = []int64{7}

	mc := b.newInboundContext(testMessage(-100, 7, 55, strings.Repeat("a", 50)), false)
	mc.Enhanced = strings.Repeat("a", 50)

	b.runMessageSummary(mc)

	require.Never(t, func() bool { return tg.sentCount() > 0 }, 150*time.Millisecond, 15*time.Millisecond,
		"an excluded user's long message must not be summarized")
}

// An AI failure during summary generation degrades quietly: no reply is posted
// and no (nonexistent) summary message is queued for deletion.
func TestMessageSummary_AIFailure_NoReplyNoQueue(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	summaryConfig(b)

	b.generateMessageSummary(testMessage(-100, 7, 55, strings.Repeat("a", 50)))

	assert.Empty(t, tg.SentMessages, "a failed summary must not post a reply")
	inQueue, err := b.db.IsMessageInDeletionQueue(1, -100)
	require.NoError(t, err)
	assert.False(t, inQueue, "a failed summary must not queue a nonexistent message")
}
