// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"
	"time"

	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReputationIncidentKeepsHighestSeverityAndReversesSafely(t *testing.T) {
	b, _ := newMockBot(t)
	now := time.Now()
	warning := &database.Action{UserID: 7, ChatID: -100, MessageID: 11, ActionType: "warn", Timestamp: now}
	b.recordReputationAction(warning)
	score, err := b.reputationScore(7, -100)
	require.NoError(t, err)
	assert.InDelta(t, -5, score.Score, 0.01)

	mute := &database.Action{UserID: 7, ChatID: -100, MessageID: 11, ActionType: "mute", Duration: 60, Timestamp: now}
	b.recordReputationAction(mute)
	score, err = b.reputationScore(7, -100)
	require.NoError(t, err)
	assert.InDelta(t, -8, score.Score, 0.01)

	// Removing the warning must not erase the higher-severity mute.
	b.recordReputationAction(&database.Action{UserID: 7, ChatID: -100, MessageID: 11, ActionType: "warning_removed"})
	score, err = b.reputationScore(7, -100)
	require.NoError(t, err)
	assert.InDelta(t, -8, score.Score, 0.01)

	b.recordReputationAction(&database.Action{UserID: 7, ChatID: -100, MessageID: 11, ActionType: "cleared"})
	score, err = b.reputationScore(7, -100)
	require.NoError(t, err)
	assert.InDelta(t, 0, score.Score, 0.01)
}

func TestReactionReputationUsesCurrentSnapshotAndExcludesBotEmoji(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Reputation.PositiveReactionEmojis = []string{"👍"}
	b.config.Reputation.NegativeReactionEmojis = []string{"👎", "🤮"}
	b.config.Reactions.UserMuted = "🤮"
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 12, ChatID: -100, UserID: 8, Timestamp: time.Now(),
	}))

	b.recordReactionReputation(12, -100, map[string]int{"👍": 3, "👎": 1, "🤮": 5})
	score, err := b.reputationScore(8, -100)
	require.NoError(t, err)
	assert.InDelta(t, 0.35, score.Score, 0.01)

	// The same source is replaced when reactions are removed.
	b.recordReactionReputation(12, -100, map[string]int{"👎": 2})
	score, err = b.reputationScore(8, -100)
	require.NoError(t, err)
	assert.InDelta(t, -0.5, score.Score, 0.01)
}

func TestReputationOptOut(t *testing.T) {
	b, _ := newMockBot(t)
	disabled := false
	b.config.Reputation.Enabled = &disabled
	b.recordReputationAction(&database.Action{UserID: 9, ChatID: -100, MessageID: 13, ActionType: "warn"})
	score, err := b.reputationScore(9, -100)
	require.NoError(t, err)
	assert.InDelta(t, 0, score.Score, 0.01)
}

func TestAIProfileReputationCanBeExcludedFromScore(t *testing.T) {
	b, _ := newMockBot(t)
	b.recordAIReputation(12, "very_bad")

	score, err := b.reputationScore(12, -100)
	require.NoError(t, err)
	assert.InDelta(t, -3, score.Score, 0.01)

	disabled := false
	b.config.Reputation.AIProfileAffectsScore = &disabled
	score, err = b.reputationScore(12, -100)
	require.NoError(t, err)
	assert.InDelta(t, 0, score.Score, 0.01)
}

func TestAIAutoActionDoesNotFeedReputation(t *testing.T) {
	b, _ := newMockBot(t)
	ctx := &webModContext{
		userID: 10, chatID: -100, messageID: 14,
		adminID: b.botSelf.ID, suppressReputation: true,
	}
	b.logAction(ctx, "warn", "AI verdict", 0)
	score, err := b.reputationScore(10, -100)
	require.NoError(t, err)
	assert.InDelta(t, 0, score.Score, 0.01)

	actions, err := b.db.GetRecentUserActions(10, -100)
	require.NoError(t, err)
	assert.Equal(t, 1, actions)
}

func TestProfileReputationShowsScoreAndAIVerdictSeparately(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.UserProfiles.Enabled = true
	require.NoError(t, b.db.UpsertUserProfile(&database.UserProfile{
		UserID: 11, Username: "mixed", Profile: "AI profile", Reputation: "very_bad",
	}))
	b.recordAIReputation(11, "very_bad")
	positiveWeight := 10.0
	reactionLimit := 20.0
	b.config.Reputation.Weights.PositiveReaction = &positiveWeight
	b.config.Reputation.Limits.ReactionPerMessage = &reactionLimit
	require.NoError(t, b.db.UpsertReputationEvent(database.ReputationEvent{
		UserID: 11, ChatID: -100, Kind: reputationKindPositiveReaction,
		Component: database.ReputationComponentCommunity,
		SourceKey: "test:positive", Value: 1, OccurredAt: time.Now(),
	}))

	compact := b.getUserReputationForModeration(11, -100)
	assert.Contains(t, compact, "Score reputation: good (score 7.0)")
	assert.Contains(t, compact, "very_bad")

	profile := b.getUserProfileForModeration(11, -100)
	assert.Contains(t, profile, "Score reputation: good (score 7.0)")
	assert.Contains(t, profile, "very_bad")
}
