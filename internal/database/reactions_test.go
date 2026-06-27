// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreMessageReactions_RoundTrip(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	storeMsg(t, db, 10, -100, 5, "alice", "hello", now)

	// Store a reactions map and read it back verbatim.
	affected, err := db.StoreMessageReactions(10, -100, `{"👍":3,"🔥":1}`)
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	got, err := db.GetMessageInfo(10, -100)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, `{"👍":3,"🔥":1}`, got.Reactions)
}

func TestStoreMessageReactions_UntrackedMessageIsNoOp(t *testing.T) {
	db := newTestDB(t)
	// No message_info row exists for (99, -100): the update affects 0 rows.
	affected, err := db.StoreMessageReactions(99, -100, `{"👍":1}`)
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)
}

func TestStoreMessageReactions_PreservedAcrossEdit(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	storeMsg(t, db, 11, -100, 5, "alice", "orig", now)

	_, err := db.StoreMessageReactions(11, -100, `{"👍":2}`)
	require.NoError(t, err)

	// Editing the message text must not clobber the reactions column.
	require.NoError(t, db.UpdateMessageInfo(&MessageInfo{
		MessageID: 11, ChatID: -100, Username: "alice", Text: "edited",
	}))

	got, err := db.GetMessageInfo(11, -100)
	require.NoError(t, err)
	assert.Equal(t, "edited", got.Text)
	assert.Equal(t, `{"👍":2}`, got.Reactions, "edit must preserve reactions")
}

func TestFormatReactionsTag(t *testing.T) {
	// Empty / invalid input yields no tag.
	assert.Equal(t, "", formatReactionsTag(""))
	assert.Equal(t, "", formatReactionsTag("not-json"))

	// Ordered by descending count, then emoji.
	tag := formatReactionsTag(`{"🔥":1,"👍":3}`)
	assert.Equal(t, " [reactions: 👍3 🔥1]", tag)
}

func TestFormatReactionsDisplay(t *testing.T) {
	// Empty / invalid input yields an empty string.
	assert.Equal(t, "", FormatReactionsDisplay(""))
	assert.Equal(t, "", FormatReactionsDisplay("not-json"))
	assert.Equal(t, "", FormatReactionsDisplay(`{"👍":0}`))

	// Ordered by descending count, then emoji; space-separated for the UI.
	assert.Equal(t, "👍 3   🔥 1", FormatReactionsDisplay(`{"🔥":1,"👍":3}`))
}
