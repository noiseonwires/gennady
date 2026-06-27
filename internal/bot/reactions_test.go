// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"
	"time"

	"gennadium/internal/database"
	tgbotapi "gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactionsJSONRoundTrip(t *testing.T) {
	// Empty map encodes to "".
	assert.Equal(t, "", reactionsToJSON(map[string]int{}))
	// Zero / negative counts are dropped.
	assert.Equal(t, "", reactionsToJSON(map[string]int{"👍": 0, "🔥": -2}))

	// Keys are sorted so equal states produce identical strings.
	encoded := reactionsToJSON(map[string]int{"🔥": 1, "👍": 3})
	assert.Equal(t, `{"👍":3,"🔥":1}`, encoded)

	// Decode round-trips the surviving entries.
	decoded := reactionsFromJSON(encoded)
	assert.Equal(t, map[string]int{"👍": 3, "🔥": 1}, decoded)

	// Empty / invalid input decodes to an empty map (never nil).
	assert.Equal(t, map[string]int{}, reactionsFromJSON(""))
	assert.Equal(t, map[string]int{}, reactionsFromJSON("garbage"))
}

func TestFormatReactionsForContext(t *testing.T) {
	assert.Equal(t, "", formatReactionsForContext(""))
	assert.Equal(t, "", formatReactionsForContext(`{}`))

	// Ordered by descending count, then emoji.
	tag := formatReactionsForContext(`{"🔥":1,"👍":3}`)
	assert.Equal(t, " [reactions: 👍3 🔥1]", tag)
}

func TestHandleMessageReactionCount_StoresAggregate(t *testing.T) {
	b, _ := newMockBot(t)
	now := time.Now()
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 20, ChatID: -100, UserID: 5, Username: "alice", Text: "hi", Timestamp: now,
	}))

	b.handleMessageReactionCount(&tgbotapi.MessageReactionCountUpdated{
		Chat:      tgbotapi.Chat{ID: -100},
		MessageID: 20,
		Reactions: []tgbotapi.ReactionCount{
			{Emoji: "👍", Count: 4},
			{Emoji: "🔥", Count: 2},
			{Emoji: "💩", Count: 0}, // dropped (zero count)
		},
	})

	got, err := b.db.GetMessageInfo(20, -100)
	require.NoError(t, err)
	assert.Equal(t, `{"👍":4,"🔥":2}`, got.Reactions)
}

func TestHandleMessageReactionCount_UntrackedNoOp(t *testing.T) {
	b, _ := newMockBot(t)
	// No panic / error when the message isn't tracked.
	b.handleMessageReactionCount(&tgbotapi.MessageReactionCountUpdated{
		Chat:      tgbotapi.Chat{ID: -100},
		MessageID: 999,
		Reactions: []tgbotapi.ReactionCount{{Emoji: "👍", Count: 1}},
	})
	got, _ := b.db.GetMessageInfo(999, -100)
	assert.Nil(t, got)
}

func TestHandleMessageReaction_AppliesDelta(t *testing.T) {
	b, _ := newMockBot(t)
	now := time.Now()
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 21, ChatID: -100, UserID: 5, Username: "alice", Text: "hi",
		Reactions: `{"👍":2}`, Timestamp: now,
	}))

	// A user switches their reaction 👍 → 🔥: 👍 decremented, 🔥 added.
	b.handleMessageReaction(&tgbotapi.MessageReactionUpdated{
		Chat:        tgbotapi.Chat{ID: -100},
		MessageID:   21,
		OldReaction: []string{"👍"},
		NewReaction: []string{"🔥"},
	})

	got, err := b.db.GetMessageInfo(21, -100)
	require.NoError(t, err)
	assert.Equal(t, `{"👍":1,"🔥":1}`, got.Reactions)
}

func TestHandleMessageReaction_DropsEmojiAtZero(t *testing.T) {
	b, _ := newMockBot(t)
	now := time.Now()
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 22, ChatID: -100, UserID: 5, Username: "alice", Text: "hi",
		Reactions: `{"👍":1}`, Timestamp: now,
	}))

	// The only 👍 is removed: the map becomes empty ("").
	b.handleMessageReaction(&tgbotapi.MessageReactionUpdated{
		Chat:        tgbotapi.Chat{ID: -100},
		MessageID:   22,
		OldReaction: []string{"👍"},
		NewReaction: nil,
	})

	got, err := b.db.GetMessageInfo(22, -100)
	require.NoError(t, err)
	assert.Equal(t, "", got.Reactions)
}

func TestHandleMessageReaction_UntrackedNoOp(t *testing.T) {
	b, _ := newMockBot(t)
	// No tracked message → no-op, no panic.
	b.handleMessageReaction(&tgbotapi.MessageReactionUpdated{
		Chat:        tgbotapi.Chat{ID: -100},
		MessageID:   123,
		NewReaction: []string{"👍"},
	})
	got, _ := b.db.GetMessageInfo(123, -100)
	assert.Nil(t, got)
}
