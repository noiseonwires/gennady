// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"
	"time"

	"gennadium/internal/database"
	"gennadium/internal/i18n"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModerationMarksForMessage(t *testing.T) {
	b, _ := newMockBot(t)
	now := time.Now()

	// No actions → no marks.
	assert.Equal(t, "", b.moderationMarksForMessage(-100, 1))

	// Deleted + warned + muted all show up, in a single bracketed tag.
	require.NoError(t, b.db.LogAction(&database.Action{
		UserID: 5, ActionType: "delete", ChatID: -100, MessageID: 1, Timestamp: now,
	}))
	require.NoError(t, b.db.LogAction(&database.Action{
		UserID: 5, ActionType: "mute", ChatID: -100, MessageID: 1, Timestamp: now,
	}))
	require.NoError(t, b.db.AddWarning(&database.Warning{
		UserID: 5, ChatID: -100, WarnedAt: now, MessageID: 1,
	}))

	tag := b.moderationMarksForMessage(-100, 1)
	assert.Contains(t, tag, i18n.T("ai.mark_deleted"))
	assert.Contains(t, tag, i18n.T("ai.mark_warned"))
	assert.Contains(t, tag, i18n.T("ai.mark_muted"))
	assert.Equal(t, " [", tag[:2], "marks render as a bracketed inline tag")
}

// A message in the reply chain that was deleted/actioned carries its moderation
// marks into the creative-reply context.
func TestBuildReplyChainFromMessageID_IncludesModerationMarks(t *testing.T) {
	b, _ := newMockBot(t)
	now := time.Now()
	replyTo := 1

	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 1, ChatID: -100, UserID: 5, Username: "alice", Text: "first", Timestamp: now,
	}))
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 2, ChatID: -100, UserID: 6, Username: "bob", Text: "second",
		ReplyToMessageID: &replyTo, Timestamp: now,
	}))

	// Message 2 was deleted by moderation.
	require.NoError(t, b.db.LogAction(&database.Action{
		UserID: 6, ActionType: "delete", ChatID: -100, MessageID: 2, Timestamp: now,
	}))

	chain, entries := b.buildReplyChainFromMessageID(2, -100, 5)
	require.Len(t, entries, 2)

	joined := ""
	for _, line := range chain {
		joined += line + "\n"
	}
	assert.Contains(t, joined, "second")
	assert.Contains(t, joined, i18n.T("ai.mark_deleted"))
}
