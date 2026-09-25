// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReputationScoreDecayCapsAndIsolation(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	policy := ReputationPolicy{
		MinScore: -30, MaxScore: 30,
		CommunityMin: -8, CommunityMax: 24,
		AIMin: -3, AIMax: 3,
		RecoveryMax: 5,
	}

	require.NoError(t, db.UpsertReputationEvent(ReputationEvent{
		SourceKey: "reaction:-100:1", UserID: 7, ChatID: -100,
		Kind: "reaction", Component: ReputationComponentCommunity,
		Value: 10, HalfLifeDays: 30, OccurredAt: now.Add(-30 * 24 * time.Hour),
	}))
	require.NoError(t, db.UpsertReputationEvent(ReputationEvent{
		SourceKey: "ai:7", UserID: 7, ChatID: 0,
		Kind: "ai_profile", Component: ReputationComponentAI, Value: 2,
	}))

	score, err := db.GetReputationScore(7, -100, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, 7, score.Score, 0.01)
	assert.Equal(t, "good", score.Grade)

	// Replacing the same source changes, rather than duplicates, its evidence.
	require.NoError(t, db.UpsertReputationEvent(ReputationEvent{
		SourceKey: "reaction:-100:1", UserID: 7, ChatID: -100,
		Kind: "reaction", Component: ReputationComponentCommunity, Value: 100,
		OccurredAt: now,
	}))
	score, err = db.GetReputationScore(7, -100, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, 26, score.Score, 0.01) // community cap 24 + global AI 2

	otherChat, err := db.GetReputationScore(7, -200, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, 2, otherChat.Score, 0.01)
	assert.Equal(t, "neutral", otherChat.Grade)

	require.NoError(t, db.DeleteReputationEvent("reaction:-100:1"))
	score, err = db.GetReputationScore(7, -100, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, 2, score.Score, 0.01)
}

func TestReputationGrade(t *testing.T) {
	assert.Equal(t, "very_bad", ReputationGrade(-20))
	assert.Equal(t, "bad", ReputationGrade(-19.9))
	assert.Equal(t, "neutral", ReputationGrade(-5))
	assert.Equal(t, "neutral", ReputationGrade(5))
	assert.Equal(t, "good", ReputationGrade(5.1))
	assert.Equal(t, "very_good", ReputationGrade(20))
}

func TestReputationPolicyWeightsExistingEvents(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	require.NoError(t, db.UpsertReputationEvent(ReputationEvent{
		SourceKey: "incident:-100:9", UserID: 8, ChatID: -100,
		Kind: "warning", Component: ReputationComponentModeration, Value: 1,
		OccurredAt: now,
	}))

	policy := ReputationPolicy{MinScore: -30, MaxScore: 30, Weights: map[string]float64{"warning": -5}}
	score, err := db.GetReputationScore(8, -100, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, -5, score.Score, 0.01)

	policy.Weights["warning"] = -7
	score, err = db.GetReputationScore(8, -100, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, -7, score.Score, 0.01)
}

func TestGetReputationScoresMatchesSingleScore(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	policy := ReputationPolicy{
		MinScore: -30, MaxScore: 30,
		CommunityMin: -8, CommunityMax: 24,
		AIMin: -3, AIMax: 3, RecoveryMax: 5,
		Weights: map[string]float64{
			"warning":           -5,
			"positive_reaction": 0.2,
			"ai_good":           1.5,
		},
	}
	for _, event := range []ReputationEvent{
		{SourceKey: "ai:1", UserID: 1, ChatID: 0, Kind: "ai_good", Component: ReputationComponentAI, Value: 1, OccurredAt: now},
		{SourceKey: "warn:1", UserID: 1, ChatID: -100, Kind: "warning", Component: ReputationComponentModeration, Value: 1, OccurredAt: now},
		{SourceKey: "react:1", UserID: 1, ChatID: -200, Kind: "positive_reaction", Component: ReputationComponentCommunity, Value: 5, OccurredAt: now},
		{SourceKey: "warn:2", UserID: 2, ChatID: -100, Kind: "warning", Component: ReputationComponentModeration, Value: 1, OccurredAt: now},
	} {
		require.NoError(t, db.UpsertReputationEvent(event))
	}

	bulk, err := db.GetReputationScores([]int64{1, 2}, []int64{-100, -200}, policy, now)
	require.NoError(t, err)
	for _, userID := range []int64{1, 2} {
		for _, chatID := range []int64{-100, -200} {
			single, singleErr := db.GetReputationScore(userID, chatID, policy, now)
			require.NoError(t, singleErr)
			assert.Equal(t, single, bulk[userID][chatID])
		}
	}

	empty, err := db.GetReputationScores(nil, []int64{-100}, policy, now)
	require.NoError(t, err)
	assert.Empty(t, empty)
	empty, err = db.GetReputationScores([]int64{1}, nil, policy, now)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestProfileScreeningReputationIsGlobalPersistentAndCapped(t *testing.T) {
	db := newTestDB(t)
	policy := ReputationPolicy{
		MinScore: -30, MaxScore: 30,
		ProfileScreeningMin: -5,
		Weights: map[string]float64{
			"profile_screening_text": -2, "profile_screening_photo": -3,
		},
	}
	require.NoError(t, db.AppendTgProfileAnalysis(21, "screened", []string{
		"suspicious bio",
		"⚠️ New member's profile photo was flagged by content safety",
		"⚠️ фото личного канала нового участника помечено системой контент-безопасности",
	}))
	// Duplicate findings do not increase the persistent characteristic.
	require.NoError(t, db.AppendTgProfileAnalysis(21, "screened", []string{"suspicious bio"}))

	for _, chatID := range []int64{-100, -200} {
		score, err := db.GetReputationScore(21, chatID, policy, time.Now())
		require.NoError(t, err)
		assert.InDelta(t, -5, score.Score, 0.001)
		assert.InDelta(t, -5, score.Components[ReputationComponentProfileScreening], 0.001)
	}

	require.NoError(t, db.DeleteUserProfile(21))
	score, err := db.GetReputationScore(21, -100, policy, time.Now())
	require.NoError(t, err)
	assert.InDelta(t, 0, score.Score, 0.001)
}

func TestProfileScreeningReputationBackfill(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	_, err := db.conn.Exec(`INSERT INTO user_profiles
		(user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at)
		VALUES (22, 'legacy', '', 'neutral', ?, '', ?, ?)`, "bio finding\nphoto finding", now, now)
	require.NoError(t, err)
	require.NoError(t, db.syncProfileScreeningReputationEvents())

	policy := ReputationPolicy{
		MinScore: -30, MaxScore: 30, ProfileScreeningMin: -5,
		Weights: map[string]float64{
			"profile_screening_text": -2, "profile_screening_photo": -3,
		},
	}
	score, err := db.GetReputationScore(22, -100, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, -4, score.Score, 0.001)
}

func TestProfileScreeningReputationSyncIsIncremental(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	_, err := db.conn.Exec(`INSERT INTO user_profiles
		(user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at)
		VALUES (23, 'existing', '', 'neutral', ?, '', ?, ?)`, "bio finding", now, now)
	require.NoError(t, err)
	require.NoError(t, db.syncProfileScreeningReputationEvents())

	const sentinelUpdatedAt = "2000-01-01 00:00:00"
	_, err = db.conn.Exec(`UPDATE reputation_events SET updated_at = ? WHERE source_key = ?`,
		sentinelUpdatedAt, profileScreeningSourceKey(23, reputationKindProfileScreeningText))
	require.NoError(t, err)

	var expectedUpdatedAt string
	require.NoError(t, db.conn.QueryRow(`SELECT updated_at FROM reputation_events WHERE source_key = ?`,
		profileScreeningSourceKey(23, reputationKindProfileScreeningText)).Scan(&expectedUpdatedAt))
	require.NoError(t, db.syncProfileScreeningReputationEvents())
	var updatedAt string
	require.NoError(t, db.conn.QueryRow(`SELECT updated_at FROM reputation_events WHERE source_key = ?`,
		profileScreeningSourceKey(23, reputationKindProfileScreeningText)).Scan(&updatedAt))
	assert.Equal(t, expectedUpdatedAt, updatedAt)
}

func TestProfileScreeningReputationSyncRemovesStaleEvents(t *testing.T) {
	db := newTestDB(t)
	sourceKey := profileScreeningSourceKey(24, reputationKindProfileScreeningText)
	_, err := db.conn.Exec(`INSERT INTO reputation_events
		(source_key, user_id, chat_id, kind, component, value, severity, half_life_days, occurred_at, updated_at)
		VALUES (?, 24, 0, ?, ?, 1, 0, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		sourceKey, reputationKindProfileScreeningText, ReputationComponentProfileScreening)
	require.NoError(t, err)

	require.NoError(t, db.syncProfileScreeningReputationEvents())
	var count int
	require.NoError(t, db.conn.QueryRow(`SELECT COUNT(*) FROM reputation_events WHERE source_key = ?`, sourceKey).Scan(&count))
	assert.Zero(t, count)
}

func TestProfileScreeningReputationSyncBatchesBackfill(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	tx, err := db.conn.Begin()
	require.NoError(t, err)
	for userID := int64(1000); userID < 1080; userID++ {
		_, err := tx.Exec(`INSERT INTO user_profiles
			(user_id, username, profile, reputation, tg_profile_analysis, first_seen_at, created_at, updated_at)
			VALUES (?, '', '', 'neutral', 'bio finding', '', ?, ?)`, userID, now, now)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	require.NoError(t, db.syncProfileScreeningReputationEvents())
	var count int
	require.NoError(t, db.conn.QueryRow(`SELECT COUNT(*) FROM reputation_events
		WHERE kind = ?`, reputationKindProfileScreeningText).Scan(&count))
	assert.Equal(t, 80, count)
}

func TestProfileScreeningFindingCounts(t *testing.T) {
	text, photo := profileScreeningFindingCounts(strings.Join([]string{
		"suspicious bio",
		"⚠️ New member's profile photo was flagged by content safety",
		"⚠️ фото профиля нового участника помечено системой контент-безопасности",
		"",
	}, "\n"))
	assert.Equal(t, 1, text)
	assert.Equal(t, 2, photo)
}

func TestCleanReputationRecoveryIsRateLimitedAndStopsAtNeutral(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	policy := ReputationPolicy{
		MinScore: -30, MaxScore: 30, RecoveryMax: 5,
		Weights: map[string]float64{"warning": -5, "clean_recovery": 1},
	}
	require.NoError(t, db.UpsertReputationEvent(ReputationEvent{
		SourceKey: "incident:-100:9", UserID: 9, ChatID: -100,
		Kind: "warning", Component: ReputationComponentModeration, Value: 1,
		OccurredAt: now,
	}))

	for i := 0; i < 4; i++ {
		emitted, err := db.RecordCleanReputationMessage(9, -100, 2, 10, now)
		require.NoError(t, err)
		assert.Equal(t, i%2 == 1, emitted)
	}
	score, err := db.GetReputationScore(9, -100, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, -3, score.Score, 0.01)

	for i := 0; i < 10; i++ {
		_, err := db.RecordCleanReputationMessage(9, -100, 2, 10, now)
		require.NoError(t, err)
	}
	score, err = db.GetReputationScore(9, -100, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, 0, score.Score, 0.01)

	require.NoError(t, db.ResetReputationRecovery(9, -100))
	score, err = db.GetReputationScore(9, -100, policy, now)
	require.NoError(t, err)
	assert.InDelta(t, -5, score.Score, 0.01)
}
