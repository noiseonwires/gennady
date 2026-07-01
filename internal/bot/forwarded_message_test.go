// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"testing"
	"time"

	tgbotapi "gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A forwarded message must preserve its origin in the stored chat history (via a
// "<forwarded from: …>" marker), while the moderator still judges the raw
// content rather than the forwarding metadata.
func TestForwardedMessage_StoresOriginTag_ModeratesRawContent(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)

	msg := testMessage(-100, 7, 55, "actual content")
	msg.ForwardFrom = &tgbotapi.User{FirstName: "Origin", Username: "origuser"}

	b.analyzeMessage(b.newInboundContext(msg, false))

	// The stored history carries the forward-origin marker AND the content.
	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	assert.Equal(t, "<forwarded from: Origin (@origuser)> actual content", stored.Text)

	// The moderator was asked to judge the raw content (no forward metadata).
	require.GreaterOrEqual(t, rt.count(), 1, "a forwarded message must still be moderated")
	assert.Contains(t, rt.last().Body, "actual content")
}
