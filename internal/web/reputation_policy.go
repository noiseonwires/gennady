// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"gennadium/internal/config"
	"gennadium/internal/database"
)

func webConfiguredFloat(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

func webReputationPolicy(cfg config.ReputationConfig) database.ReputationPolicy {
	reactionHalfLife := webConfiguredFloat(cfg.HalfLives.Reaction, 90)
	muteHalfLife := webConfiguredFloat(cfg.HalfLives.Mute, 180)
	aiProfileWeight := func(value *float64, fallback float64) float64 {
		if cfg.AIProfileAffectsScore != nil && !*cfg.AIProfileAffectsScore {
			return 0
		}
		return webConfiguredFloat(value, fallback)
	}
	return database.ReputationPolicy{
		MinScore:            webConfiguredFloat(cfg.Limits.MinScore, -30),
		MaxScore:            webConfiguredFloat(cfg.Limits.MaxScore, 30),
		CommunityMin:        webConfiguredFloat(cfg.Limits.CommunityMin, -8),
		CommunityMax:        webConfiguredFloat(cfg.Limits.CommunityMax, 24),
		AIMin:               webConfiguredFloat(cfg.Limits.AIMin, -3),
		AIMax:               webConfiguredFloat(cfg.Limits.AIMax, 3),
		ProfileScreeningMin: webConfiguredFloat(cfg.Limits.ProfileScreeningMin, -5),
		RecoveryMax:         webConfiguredFloat(cfg.Limits.RecoveryMax, 5),
		Weights: map[string]float64{
			"positive_reaction":       webConfiguredFloat(cfg.Weights.PositiveReaction, 0.2),
			"negative_reaction":       webConfiguredFloat(cfg.Weights.NegativeReaction, -0.25),
			"delete":                  webConfiguredFloat(cfg.Weights.Delete, -3),
			"warning":                 webConfiguredFloat(cfg.Weights.Warning, -5),
			"short_mute":              webConfiguredFloat(cfg.Weights.ShortMute, -8),
			"long_mute":               webConfiguredFloat(cfg.Weights.LongMute, -10),
			"permanent_mute":          webConfiguredFloat(cfg.Weights.PermanentMute, -15),
			"clean_recovery":          webConfiguredFloat(cfg.Weights.CleanRecovery, 1),
			"profile_screening_text":  webConfiguredFloat(cfg.Weights.ProfileScreeningText, -2),
			"profile_screening_photo": webConfiguredFloat(cfg.Weights.ProfileScreeningPhoto, -3),
			"ai_very_bad":             aiProfileWeight(cfg.Weights.AIVeryBad, -3),
			"ai_bad":                  aiProfileWeight(cfg.Weights.AIBad, -1.5),
			"ai_neutral":              aiProfileWeight(cfg.Weights.AINeutral, 0),
			"ai_good":                 aiProfileWeight(cfg.Weights.AIGood, 1.5),
			"ai_very_good":            aiProfileWeight(cfg.Weights.AIVeryGood, 3),
		},
		EventLimits: map[string]float64{
			"positive_reaction": webConfiguredFloat(cfg.Limits.ReactionPerMessage, 1),
			"negative_reaction": webConfiguredFloat(cfg.Limits.ReactionPerMessage, 1),
		},
		HalfLives: map[string]float64{
			"positive_reaction": reactionHalfLife,
			"negative_reaction": reactionHalfLife,
			"delete":            webConfiguredFloat(cfg.HalfLives.Delete, 45),
			"warning":           webConfiguredFloat(cfg.HalfLives.Warning, 90),
			"short_mute":        muteHalfLife,
			"long_mute":         muteHalfLife,
			"permanent_mute":    muteHalfLife,
			"ai_very_bad":       webConfiguredFloat(cfg.HalfLives.AI, 30),
			"ai_bad":            webConfiguredFloat(cfg.HalfLives.AI, 30),
			"ai_neutral":        webConfiguredFloat(cfg.HalfLives.AI, 30),
			"ai_good":           webConfiguredFloat(cfg.HalfLives.AI, 30),
			"ai_very_good":      webConfiguredFloat(cfg.HalfLives.AI, 30),
		},
		VeryBadAt:  webConfiguredFloat(cfg.Thresholds.VeryBad, -20),
		BadBelow:   webConfiguredFloat(cfg.Thresholds.Bad, -5),
		GoodAbove:  webConfiguredFloat(cfg.Thresholds.Good, 5),
		VeryGoodAt: webConfiguredFloat(cfg.Thresholds.VeryGood, 20),
	}
}
