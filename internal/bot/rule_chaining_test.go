// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stacking moderation rules expresses combined actions: every matched rule's
// action is dispatched in declaration order. These complement the existing
// warn+report test with the other meaningful combinations and the fallback.

// delete + warn: the message is removed AND the warning is recorded.
func TestRuleChaining_DeletePlusWarn_BothExecute(t *testing.T) {
	b, tg := behaviorBot(t)
	rules := []config.ModerationRule{
		{Trigger: "x", Action: "delete", Description: "remove"},
		{Trigger: "x", Action: "warn", Description: "and warn"},
	}

	b.handleBadWordDetected(testMessage(-100, 7, 55, "bad"), "bad", false, rules, "")

	require.Len(t, tg.DeletedIDs, 1, "the delete action must run")
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])

	has, err := b.db.HasWarningForMessage(7, 55)
	require.NoError(t, err)
	assert.True(t, has, "the warn action must also run")
}

// mute + report: the user is restricted AND the admins get a card.
func TestRuleChaining_MutePlusReport_BothExecute(t *testing.T) {
	b, tg := behaviorBot(t)
	b.config.AI.ContentModeration.DefaultMuteMinutes = 30
	rules := []config.ModerationRule{
		{Trigger: "x", Action: "mute", Description: "mute"},
		{Trigger: "x", Action: "report", Description: "escalate"},
	}

	b.handleBadWordDetected(testMessage(-100, 7, 55, "bad"), "bad", false, rules, "reason")

	require.Len(t, tg.Restrictions, 1, "the mute action must run")
	assert.Equal(t, int64(7), tg.Restrictions[0].UserID)
	assert.True(t, adminCardSent(tg, -999), "the report action must also run")
}

// A rule with an unrecognized action falls back to a safe report card.
func TestRuleChaining_UnknownAction_FallsBackToReport(t *testing.T) {
	b, tg := behaviorBot(t)
	rules := []config.ModerationRule{{Trigger: "x", Action: "explode", Description: "???"}}

	b.handleBadWordDetected(testMessage(-100, 7, 55, "bad"), "bad", false, rules, "reason")

	assert.Empty(t, tg.DeletedIDs, "an unknown action must not delete")
	assert.Empty(t, tg.Restrictions, "an unknown action must not mute")
	assert.True(t, adminCardSent(tg, -999), "an unknown action must fall back to a report card")
}
