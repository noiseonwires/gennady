// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"gennadium/internal/config"

	tgbotapi "gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin down that the per-feature scopes resolve independently of the
// moderation scope, and that config toggles compose the way the pipeline gates
// expect.

// Moderation can be off for a chat while message summaries are independently on:
// the resolved Scope reflects both.
func TestScope_ModerationOffSummariesOn_AreIndependent(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.Moderation.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}
	b.config.AI.Enabled = true
	b.config.AI.MessageSummaries.Enabled = true

	require.False(t, b.config.IsModerationActive(-100, 0))
	require.True(t, b.config.IsMessageSummaryActive(-100, 0))

	sc := b.resolveScope(testMessage(-100, 7, 55, "x"))
	assert.False(t, sc.Moderate, "moderation must be off for the excluded chat")
	assert.True(t, sc.Summarize, "summaries must be independently on")
}

// A message in a chat excluded from moderation is recorded but never sent to the
// moderation model.
func TestScope_ModerationExcluded_RecordsButDoesNotModerate(t *testing.T) {
	var calls int32
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"мат\nbad"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.Moderation.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}

	b.handleMessage(testMessage(-100, 7, 55, "anything"), false)

	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	require.NotNil(t, stored, "an excluded-from-moderation message must still be recorded")
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls), "an excluded (chat, topic) must never be moderated")
}

// AI may be enabled while content moderation is disabled: messages are recorded
// but not moderated.
func TestScope_AIEnabledModerationDisabled_NotModerated(t *testing.T) {
	var calls int32
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"мат\nbad"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.AI.ContentModeration.Enabled = false

	b.handleMessage(testMessage(-100, 7, 55, "anything"), false)

	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls), "content moderation disabled means no moderation call")
}

// Vision/OCR enhancement is moderation-only: it is skipped when the (chat, topic)
// is excluded from moderation, even with a photo and vision enabled.
func TestScope_VisionSkippedWhenTopicExcluded(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.AI.Enabled = true
	b.config.AI.ContentModeration.VisionEnabled = true
	b.config.Moderation.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}

	msg := testMessage(-100, 7, 55, "")
	msg.Photo = []tgbotapi.PhotoSize{{FileID: "abc"}}
	mc := b.newInboundContext(msg, false)

	b.stageEnhance(mc)

	require.False(t, mc.Scope.Moderate)
	assert.Equal(t, 0, rt.count(), "vision/OCR must be skipped when the (chat, topic) is excluded from moderation")
}

// A whitelisted user's message skips both vision and AI moderation, yet is still
// recorded for chat history / reply context.
func TestScope_WhitelistedUser_SkipsVisionAndModeration_StillRecorded(t *testing.T) {
	var calls int32
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"мат\nbad"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.AI.ContentModeration.VisionEnabled = true
	b.config.Admin.WhitelistUserIDs = []int64{7}

	msg := testMessage(-100, 7, 55, "anything")
	msg.Photo = []tgbotapi.PhotoSize{{FileID: "abc"}}
	b.handleMessage(msg, false)

	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	require.NotNil(t, stored, "a whitelisted user's message must still be recorded")
	assert.Equal(t, 0, rt.count(), "a whitelisted user's message must not hit vision")
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls), "a whitelisted user's message must not be moderated")
}

// A moderation-active chat can still have a feature (here: summaries) disabled by
// its own scope: the message is moderated but not summarized.
func TestScope_FeatureExcludedButModerationActive(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.AI.Enabled = true
	b.config.AI.MessageSummaries.Enabled = true
	b.config.AI.MessageSummaries.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}

	sc := b.resolveScope(testMessage(-100, 7, 55, "x"))
	assert.True(t, sc.Moderate, "moderation stays active")
	assert.False(t, sc.Summarize, "summaries are independently excluded")
}

// The whitelist gates only moderation (and vision), not independent features: a
// whitelisted user's message is still queued for deletion.
func TestScope_WhitelistedUser_StillQueuedForDeletion(t *testing.T) {
	b, _ := newMockBot(t)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.MessageDeletion.Enabled = true
	b.config.Admin.WhitelistUserIDs = []int64{7}

	b.handleMessage(testMessage(-100, 7, 55, "whitelisted message"), false)

	inQueue, err := b.db.IsMessageInDeletionQueue(55, -100)
	require.NoError(t, err)
	assert.True(t, inQueue, "the whitelist must not disable independent deletion")
}
