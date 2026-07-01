// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin down ai.content_moderation.full_model_first_messages: for a
// user's first N messages in a moderation chat, a message the light model
// cleared is double-checked with the full model, and acted on if the full model
// flags it.

// shouldFullModelDoubleCheck is gated on the configured N and the user's
// recorded message count in the chat (the current message is already recorded
// when moderation runs, so a count of N is still the N-th "first" message).
func TestShouldFullModelDoubleCheck(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.ContentModeration.FullModelFirstMessages = 2

	store := func(id int) {
		require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
			MessageID: id, ChatID: -100, UserID: 7, Text: "m", Timestamp: time.Now(),
		}))
	}

	// No messages recorded yet (count 0) → within the window.
	assert.True(t, b.shouldFullModelDoubleCheck(7, -100))

	// Two recorded messages (count 2 == N) → still within the window.
	store(1)
	store(2)
	assert.True(t, b.shouldFullModelDoubleCheck(7, -100))

	// A third message (count 3 > N) → window has passed.
	store(3)
	assert.False(t, b.shouldFullModelDoubleCheck(7, -100))

	// The count is per chat: messages in another chat don't extend the window.
	assert.True(t, b.shouldFullModelDoubleCheck(7, -200))

	// Disabled when N <= 0.
	b.config.AI.ContentModeration.FullModelFirstMessages = 0
	assert.False(t, b.shouldFullModelDoubleCheck(7, -100))

	// A zero user ID never qualifies.
	b.config.AI.ContentModeration.FullModelFirstMessages = 5
	assert.False(t, b.shouldFullModelDoubleCheck(0, -100))
}

// When the light model clears an early-message author's message, the full model
// double-check runs and its verdict is acted on (here: a delete rule).
func TestBehavior_FullModelFirstMessages_CatchesWhatLightModelMissed(t *testing.T) {
	var calls int32
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		// First call is the light model (clean); the second is the full-model
		// double-check (flags spam).
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`)) // "no" = clean
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"spam\nsubtle promo"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.AI.ContentModeration.FullModelFirstMessages = 3
	b.config.AI.ContentModeration.Rules = []config.ModerationRule{
		{Trigger: "spam", Action: "delete", Description: "spam"},
	}

	b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "hi all, check my channel"), false))

	assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "light model then full-model double-check")
	assert.Equal(t, 2, rt.count())
	require.Len(t, tg.DeletedIDs, 1, "the full-model double-check verdict must be acted on")
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
}

// With the feature disabled (default 0), a message the light model cleared is
// not re-checked: only one AI call is made and nothing is acted on.
func TestBehavior_FullModelFirstMessages_DisabledByDefault(t *testing.T) {
	var calls int32
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`)) // always clean
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	// FullModelFirstMessages left at 0 (disabled).

	b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "hi all"), false))

	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "only the light model runs when disabled")
	assert.Equal(t, 1, rt.count())
	assert.Empty(t, tg.DeletedIDs)
}

// Once the author is past their first N messages, a light-model-clean message
// is no longer double-checked, even if the full model would have flagged it.
func TestBehavior_FullModelFirstMessages_StopsAfterWindow(t *testing.T) {
	var calls int32
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`)) // light: clean
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"spam\nwould flag"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.AI.ContentModeration.FullModelFirstMessages = 1

	// Pre-record one prior message so this incoming one is the user's 2nd
	// (count becomes 2 > 1 after it is recorded).
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 50, ChatID: -100, UserID: 7, Text: "earlier", Timestamp: time.Now(),
	}))

	b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "hi all"), false))

	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "no double-check once past the first N messages")
	assert.Equal(t, 1, rt.count())
	assert.Empty(t, tg.DeletedIDs)
}
