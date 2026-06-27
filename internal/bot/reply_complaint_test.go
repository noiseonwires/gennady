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

// These scenarios pin down the reply-complaint flow (handleReplyModerationTrigger):
// when a user reports a message by replying to it and mentioning the bot in a
// moderation chat, the bot first re-runs AI moderation across every configured
// model and only falls back to manual moderation (the admin decision card) when
// every model clears the message - unless that manual fallback is disabled in
// config, in which case a clean cross-model verdict ends the complaint silently.

// newReplyComplaint stores the reported message (id 55, from offender user 7)
// in the DB and returns the inbound "@bot" complaint (id 100 from reporter user
// 9) replying to it, in moderation chat -100.
func newReplyComplaint(t *testing.T, b *Bot) *tgbotapi.Message {
	t.Helper()
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 55,
		ChatID:    -100,
		UserID:    7,
		Username:  "offender",
		Text:      "the reported message",
		Timestamp: time.Now(),
	}))
	complaint := testMessage(-100, 9, 100, "@bot look at this")
	complaint.ReplyToMessage = testMessage(-100, 7, 55, "the reported message")
	return complaint
}

// When every model clears the reported message, the bot escalates to manual
// moderation by posting the admin decision card (the default behavior).
func TestReplyComplaint_AllModelsClear_FallsBackToManual(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.config.Admin.ChatID = -999
	b.moderatedMsgs = make(map[string]time.Time)

	b.handleReplyModerationTrigger(newReplyComplaint(t, b))

	assert.GreaterOrEqual(t, rt.count(), 1, "the reported message must be re-checked by the AI before escalating")
	assert.True(t, adminCardSent(tg, -999), "a clean cross-model verdict must fall back to the manual admin card")
	assert.Empty(t, tg.DeletedIDs, "a cleared message must not be auto-deleted")
}

// With manual moderation disabled, a clean cross-model verdict ends the
// complaint silently: no admin card is posted.
func TestReplyComplaint_AllModelsClear_ManualDisabled_NoCard(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.config.Admin.ChatID = -999
	b.moderatedMsgs = make(map[string]time.Time)
	disabled := false
	b.config.AI.ContentModeration.ComplaintManualModeration = &disabled

	b.handleReplyModerationTrigger(newReplyComplaint(t, b))

	assert.GreaterOrEqual(t, rt.count(), 1, "the reported message must still be re-checked by the AI")
	assert.False(t, adminCardSent(tg, -999), "manual moderation is disabled, so no admin card must be posted")
	assert.Empty(t, tg.DeletedIDs)
}

// When a model flags the reported message, the bot acts on it automatically
// (here: deletes it) and does NOT also fall back to manual moderation. The
// delete rule's admin notification is disabled so that any message reaching the
// admin chat would unambiguously mean the manual complaint card was posted.
func TestReplyComplaint_ModelFlags_ActsAutomatically(t *testing.T) {
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

	b.handleReplyModerationTrigger(newReplyComplaint(t, b))

	require.Len(t, tg.DeletedIDs, 1, "a flagged reported message must be auto-deleted")
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
	assert.False(t, adminCardSent(tg, -999), "an auto-handled violation must not also post the manual complaint card")
}

// A reported message the bot never recorded can't be re-moderated, so the
// complaint falls straight through to manual moderation without calling the AI.
func TestReplyComplaint_ReportedMessageNotStored_FallsBackToManual(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.config.Admin.ChatID = -999
	b.moderatedMsgs = make(map[string]time.Time)

	// Deliberately do NOT store message 55 in the DB.
	complaint := testMessage(-100, 9, 100, "@bot look at this")
	complaint.ReplyToMessage = testMessage(-100, 7, 55, "unseen text")
	b.handleReplyModerationTrigger(complaint)

	assert.Equal(t, 0, rt.count(), "an unrecorded message can't be re-moderated, so the AI must not be called")
	assert.True(t, adminCardSent(tg, -999), "it must still reach manual moderation")
}
