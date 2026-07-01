// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the edited-message path (recordMessage → recordEditedMessage):
// an edit whose text is unchanged is a no-op reaction event and must skip
// re-moderation; a substantive edit records a diff and is re-moderated; an edit
// of a message the bot never stored is still moderated.

// An "edited" event whose text matches what we already stored is a reaction
// event, not a real edit: it must not re-run AI moderation.
func TestEditedMessage_UnchangedText_SkipsReModeration(t *testing.T) {
	var calls int32
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)

	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 55, ChatID: -100, UserID: 7, Text: "hello world", Timestamp: time.Now(),
	}))

	moderated := b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "hello world"), true))

	assert.False(t, moderated)
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls), "an unchanged edit must not be re-moderated")
	assert.Equal(t, 0, rt.count())
}

// A substantive edit updates the stored text, records an edit diff in
// extra_info, and re-runs moderation on the new content.
func TestEditedMessage_ChangedText_RecordsDiffAndReModerates(t *testing.T) {
	var calls int32
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)

	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 55, ChatID: -100, UserID: 7, Text: "the original text here", Timestamp: time.Now(),
	}))

	b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "a completely different message"), true))

	assert.GreaterOrEqual(t, int(atomic.LoadInt32(&calls)), 1, "a substantive edit must be re-moderated")

	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	assert.Equal(t, "a completely different message", stored.Text)
	assert.Contains(t, stored.ExtraInfo, "Edit diff", "a substantive edit must record a diff in extra_info")
}

// An edit of a message that was never stored (e.g. a very old message) is still
// moderated, even though there's nothing to update.
func TestEditedMessage_NotInDB_StillModerated(t *testing.T) {
	var calls int32
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)

	// No prior record for message 55.
	b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "edited but never stored"), true))

	assert.GreaterOrEqual(t, int(atomic.LoadInt32(&calls)), 1, "an edit of an unknown message is still moderated")
}
