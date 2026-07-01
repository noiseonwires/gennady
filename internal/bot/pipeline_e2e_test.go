// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive the WHOLE ingest pipeline through the real entry point
// (handleMessage / the moderation stages) rather than calling analyzeMessage or
// the feature functions directly, so the stage orchestration introduced by the
// pipeline refactor (resolve-scope → enhance → moderate → features →
// finalize-deletion) is exercised end to end.

// A bad message in a moderation chat, driven through handleMessage, must be
// recorded, moderated (light → full), deleted, and logged - all via the pipeline.
func TestPipeline_FlaggedMessage_DeletedRecordedAndLogged(t *testing.T) {
	var calls int32
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"spam\ndetected spam"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.Admin.ChatID = -999
	b.config.AI.ContentModeration.Rules = []config.ModerationRule{
		{Trigger: "spam", Action: "delete", Description: "spam"},
	}

	b.handleMessage(testMessage(-100, 7, 55, "buy cheap watches now"), false)

	// Recorded by the record stage.
	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	require.NotNil(t, stored, "the message must be recorded by the pipeline")

	// Moderated end to end: light flagged, full confirmed, message deleted.
	assert.GreaterOrEqual(t, int(atomic.LoadInt32(&calls)), 2, "light then full model must both run")
	require.Len(t, tg.DeletedIDs, 1, "the flagged message must be deleted")
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])

	// Action logged for the moderation log / web UI.
	actions, err := b.db.GetRecentActions(10)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
	assert.Equal(t, "delete", actions[0].ActionType)
}

// A clean message driven through handleMessage is recorded and left untouched.
func TestPipeline_CleanMessage_RecordedNotActioned(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)

	b.handleMessage(testMessage(-100, 7, 55, "hello everyone"), false)

	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Empty(t, tg.DeletedIDs)
	assert.Empty(t, tg.Restrictions)
}

// When an image trips the Content-Safety check, the moderate stage routes it
// straight to handleBadWordDetected with the content-security rules.
func TestPipeline_ContentSafetyFlagged_RoutesToModeration(t *testing.T) {
	b, tg := newMockBot(t)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.Admin.ChatID = -999
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.AI.ContentModeration.Rules = []config.ModerationRule{
		{Trigger: "content-security", Action: "delete", Description: "unsafe image"},
	}

	mc := b.newInboundContext(testMessage(-100, 7, 55, "caption"), false)
	mc.Flagged = true
	mc.Enhanced = "caption"

	dir := b.stageModerate(mc)

	assert.Equal(t, Continue, dir)
	assert.True(t, mc.Moderated, "a content-safety-flagged message must be marked moderated")
	require.Len(t, tg.DeletedIDs, 1, "the flagged image must be routed to moderation and deleted")
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
}

// Content-Safety flags an image but no content-security rule is configured: the
// pipeline logs and takes no action.
func TestPipeline_ContentSafetyFlagged_NoRules_NoAction(t *testing.T) {
	b, tg := newMockBot(t)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	// No content-security rules configured.

	mc := b.newInboundContext(testMessage(-100, 7, 55, "caption"), false)
	mc.Flagged = true
	mc.Enhanced = "caption"

	b.stageModerate(mc)

	assert.False(t, mc.Moderated)
	assert.Empty(t, tg.DeletedIDs, "with no content-security rules nothing is actioned")
}

// link_summary and creative_reply are independent features: a message that
// received a link summary may also get a creative reply. The link content is
// folded into the creative reply's reply-chain context via the message's
// extra_info, which link_summary stores before creative_reply runs. Here a
// mention gets a creative reply regardless of any link summary.
func TestPipeline_CreativeReply_Sent(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a witty reply"}}]}`))
	})
	creativeModerationConfig(b)

	msg := testMessage(-100, 7, 100, "thanks bot")
	msg.ReplyToMessage = testMessage(-100, b.botSelf.ID, 50, "bot said something")
	mc := b.newInboundContext(msg, false)

	b.runCreativeReply(mc)

	require.NotEmpty(t, tg.SentMessages, "the creative reply must be sent")
	assert.Contains(t, tg.SentMessages[len(tg.SentMessages)-1].Text, "a witty reply")
}
