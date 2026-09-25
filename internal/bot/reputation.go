// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"strings"
	"time"

	"gennadium/internal/database"
)

const (
	reputationKindPositiveReaction      = "positive_reaction"
	reputationKindNegativeReaction      = "negative_reaction"
	reputationKindDelete                = "delete"
	reputationKindWarning               = "warning"
	reputationKindShortMute             = "short_mute"
	reputationKindLongMute              = "long_mute"
	reputationKindPermanentMute         = "permanent_mute"
	reputationKindCleanRecovery         = "clean_recovery"
	reputationKindProfileScreeningText  = "profile_screening_text"
	reputationKindProfileScreeningPhoto = "profile_screening_photo"
)

func (b *Bot) reputationEnabled() bool {
	return b.config.Reputation.Enabled == nil || *b.config.Reputation.Enabled
}

func configuredFloat(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

func (b *Bot) reputationPolicy() database.ReputationPolicy {
	cfg := b.config.Reputation
	weights := map[string]float64{
		reputationKindPositiveReaction:      configuredFloat(cfg.Weights.PositiveReaction, 0.2),
		reputationKindNegativeReaction:      configuredFloat(cfg.Weights.NegativeReaction, -0.25),
		reputationKindDelete:                configuredFloat(cfg.Weights.Delete, -3),
		reputationKindWarning:               configuredFloat(cfg.Weights.Warning, -5),
		reputationKindShortMute:             configuredFloat(cfg.Weights.ShortMute, -8),
		reputationKindLongMute:              configuredFloat(cfg.Weights.LongMute, -10),
		reputationKindPermanentMute:         configuredFloat(cfg.Weights.PermanentMute, -15),
		reputationKindCleanRecovery:         configuredFloat(cfg.Weights.CleanRecovery, 1),
		reputationKindProfileScreeningText:  configuredFloat(cfg.Weights.ProfileScreeningText, -2),
		reputationKindProfileScreeningPhoto: configuredFloat(cfg.Weights.ProfileScreeningPhoto, -3),
		"ai_very_bad":                       configuredFloat(cfg.Weights.AIVeryBad, -3),
		"ai_bad":                            configuredFloat(cfg.Weights.AIBad, -1.5),
		"ai_neutral":                        configuredFloat(cfg.Weights.AINeutral, 0),
		"ai_good":                           configuredFloat(cfg.Weights.AIGood, 1.5),
		"ai_very_good":                      configuredFloat(cfg.Weights.AIVeryGood, 3),
	}
	if cfg.AIProfileAffectsScore != nil && !*cfg.AIProfileAffectsScore {
		for _, kind := range []string{"ai_very_bad", "ai_bad", "ai_neutral", "ai_good", "ai_very_good"} {
			weights[kind] = 0
		}
	}
	reactionHalfLife := configuredFloat(cfg.HalfLives.Reaction, 90)
	muteHalfLife := configuredFloat(cfg.HalfLives.Mute, 180)
	return database.ReputationPolicy{
		MinScore:            configuredFloat(cfg.Limits.MinScore, -30),
		MaxScore:            configuredFloat(cfg.Limits.MaxScore, 30),
		CommunityMin:        configuredFloat(cfg.Limits.CommunityMin, -8),
		CommunityMax:        configuredFloat(cfg.Limits.CommunityMax, 24),
		AIMin:               configuredFloat(cfg.Limits.AIMin, -3),
		AIMax:               configuredFloat(cfg.Limits.AIMax, 3),
		ProfileScreeningMin: configuredFloat(cfg.Limits.ProfileScreeningMin, -5),
		RecoveryMax:         configuredFloat(cfg.Limits.RecoveryMax, 5),
		Weights:             weights,
		EventLimits: map[string]float64{
			reputationKindPositiveReaction: configuredFloat(cfg.Limits.ReactionPerMessage, 1),
			reputationKindNegativeReaction: configuredFloat(cfg.Limits.ReactionPerMessage, 1),
		},
		HalfLives: map[string]float64{
			reputationKindPositiveReaction: reactionHalfLife,
			reputationKindNegativeReaction: reactionHalfLife,
			reputationKindDelete:           configuredFloat(cfg.HalfLives.Delete, 45),
			reputationKindWarning:          configuredFloat(cfg.HalfLives.Warning, 90),
			reputationKindShortMute:        muteHalfLife,
			reputationKindLongMute:         muteHalfLife,
			reputationKindPermanentMute:    muteHalfLife,
			"ai_very_bad":                  configuredFloat(cfg.HalfLives.AI, 30),
			"ai_bad":                       configuredFloat(cfg.HalfLives.AI, 30),
			"ai_neutral":                   configuredFloat(cfg.HalfLives.AI, 30),
			"ai_good":                      configuredFloat(cfg.HalfLives.AI, 30),
			"ai_very_good":                 configuredFloat(cfg.HalfLives.AI, 30),
		},
		VeryBadAt:  configuredFloat(cfg.Thresholds.VeryBad, -20),
		BadBelow:   configuredFloat(cfg.Thresholds.Bad, -5),
		GoodAbove:  configuredFloat(cfg.Thresholds.Good, 5),
		VeryGoodAt: configuredFloat(cfg.Thresholds.VeryGood, 20),
	}
}

func reputationIncidentKey(userID, chatID int64, messageID int) string {
	return fmt.Sprintf("incident:%d:%d:%d", chatID, userID, messageID)
}

func (b *Bot) reputationScore(userID, chatID int64) (database.ReputationScore, error) {
	if !b.reputationEnabled() || b.db == nil {
		return database.ReputationScore{UserID: userID, ChatID: chatID, Grade: "neutral"}, nil
	}
	return b.db.GetReputationScore(userID, chatID, b.reputationPolicy(), time.Now())
}

func (b *Bot) reputationPromptValues(userID, chatID int64) (grade, score string) {
	result, err := b.reputationScore(userID, chatID)
	if err != nil {
		log.Printf("Reputation: error calculating score for user %d in chat %d: %v", userID, chatID, err)
		return "neutral", "0.0"
	}
	return result.Grade, fmt.Sprintf("%.1f", result.Score)
}

func (b *Bot) recordReputationAction(action *database.Action) {
	if !b.reputationEnabled() || b.db == nil || action == nil || action.UserID == 0 {
		return
	}
	key := reputationIncidentKey(action.UserID, action.ChatID, action.MessageID)
	if action.ActionType == "cleared" {
		if err := b.db.DeleteReputationEvent(key); err != nil {
			log.Printf("Reputation: error reversing %s for user %d: %v", action.ActionType, action.UserID, err)
		}
		return
	}
	if action.ActionType == "restore" {
		if err := b.db.DeleteReputationEventIfKind(key, reputationKindDelete); err != nil {
			log.Printf("Reputation: error reversing delete for user %d: %v", action.UserID, err)
		}
		return
	}
	if action.ActionType == "warning_removed" {
		if err := b.db.DeleteReputationEventIfKind(key, reputationKindWarning); err != nil {
			log.Printf("Reputation: error removing warning for user %d: %v", action.UserID, err)
		}
		return
	}

	kind, severity := "", 0
	switch action.ActionType {
	case "delete":
		kind, severity = reputationKindDelete, 1
	case "warn":
		kind, severity = reputationKindWarning, 2
	case "mute", "cmute":
		switch {
		case action.Duration == 0:
			kind, severity = reputationKindPermanentMute, 5
		case action.Duration >= 24*60:
			kind, severity = reputationKindLongMute, 4
		default:
			kind, severity = reputationKindShortMute, 3
		}
	default:
		return
	}

	occurredAt := action.Timestamp
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	if action.MessageID == 0 {
		key = fmt.Sprintf("incident:%d:%d:time:%d", action.ChatID, action.UserID, occurredAt.UnixNano())
	}
	if err := b.db.UpsertReputationEvent(database.ReputationEvent{
		SourceKey: key, UserID: action.UserID, ChatID: action.ChatID,
		Kind: kind, Component: database.ReputationComponentModeration,
		Value: 1, Severity: severity, OccurredAt: occurredAt,
	}); err != nil {
		log.Printf("Reputation: error recording %s for user %d: %v", action.ActionType, action.UserID, err)
		return
	}
	if err := b.db.ResetReputationRecovery(action.UserID, action.ChatID); err != nil {
		log.Printf("Reputation: error resetting recovery for user %d: %v", action.UserID, err)
	}
}

func (b *Bot) logReputationAction(action *database.Action) error {
	if err := b.db.LogAction(action); err != nil {
		return err
	}
	b.recordReputationAction(action)
	return nil
}

func (b *Bot) recordAIReputation(userID int64, grade string) {
	if !b.reputationEnabled() || b.db == nil || userID == 0 {
		return
	}
	kind := "ai_" + grade
	if _, ok := b.reputationPolicy().Weights[kind]; !ok {
		kind = "ai_neutral"
	}
	now := time.Now()
	if err := b.db.UpsertReputationEvent(database.ReputationEvent{
		SourceKey: fmt.Sprintf("ai:%d", userID), UserID: userID, ChatID: 0,
		Kind: kind, Component: database.ReputationComponentAI, Value: 1,
		OccurredAt: now,
	}); err != nil {
		log.Printf("Reputation: error recording AI assessment for user %d: %v", userID, err)
	}
}

func (b *Bot) recordCleanReputationMessage(userID, chatID int64) {
	if !b.reputationEnabled() || b.db == nil || userID == 0 {
		return
	}
	score, err := b.reputationScore(userID, chatID)
	if err != nil || score.Score >= 0 {
		return
	}
	perPoint := b.config.Reputation.CleanMessagesPerPoint
	if perPoint <= 0 {
		perPoint = 50
	}
	dailyMax := b.config.Reputation.CleanMessagesDailyMax
	if dailyMax <= 0 {
		dailyMax = 20
	}
	if _, err := b.db.RecordCleanReputationMessage(userID, chatID, perPoint, dailyMax, time.Now()); err != nil {
		log.Printf("Reputation: error recording clean message for user %d: %v", userID, err)
	}
}

func (b *Bot) recordReactionReputation(messageID int, chatID int64, counts map[string]int) {
	if !b.reputationEnabled() || b.db == nil {
		return
	}
	message, err := b.db.GetMessageInfo(messageID, chatID)
	if err != nil || message == nil || message.UserID == 0 {
		return
	}
	positiveSet := emojiSet(b.config.Reputation.PositiveReactionEmojis)
	negativeSet := emojiSet(b.config.Reputation.NegativeReactionEmojis)
	for _, emoji := range []string{
		b.config.Reactions.SuspiciousMessage, b.config.Reactions.BadMessage,
		b.config.Reactions.ContentFilter, b.config.Reactions.CreativeReplyLimit,
		b.config.Reactions.ExtractingLink, b.config.Reactions.ExtractLinkFailed,
		b.config.Reactions.UserMuted, b.config.Reactions.ReportAcknowledged,
		b.config.Reactions.CreativeReplyError,
	} {
		delete(positiveSet, normalizeReputationEmoji(emoji))
		delete(negativeSet, normalizeReputationEmoji(emoji))
	}

	positive, negative := 0, 0
	for emoji, count := range counts {
		normalized := normalizeReputationEmoji(emoji)
		if positiveSet[normalized] {
			positive += count
		}
		if negativeSet[normalized] {
			negative += count
		}
	}
	occurredAt := message.Timestamp
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	for _, event := range []database.ReputationEvent{
		{
			SourceKey: fmt.Sprintf("reaction:positive:%d:%d", chatID, messageID),
			UserID:    message.UserID, ChatID: chatID, Kind: reputationKindPositiveReaction,
			Component: database.ReputationComponentCommunity, Value: float64(positive), OccurredAt: occurredAt,
		},
		{
			SourceKey: fmt.Sprintf("reaction:negative:%d:%d", chatID, messageID),
			UserID:    message.UserID, ChatID: chatID, Kind: reputationKindNegativeReaction,
			Component: database.ReputationComponentCommunity, Value: float64(negative), OccurredAt: occurredAt,
		},
	} {
		if event.Value == 0 {
			if err := b.db.DeleteReputationEvent(event.SourceKey); err != nil {
				log.Printf("Reputation: error clearing reactions for message %d: %v", messageID, err)
			}
			continue
		}
		if err := b.db.UpsertReputationEvent(event); err != nil {
			log.Printf("Reputation: error recording reactions for message %d: %v", messageID, err)
		}
	}
}

func emojiSet(emojis []string) map[string]bool {
	set := make(map[string]bool, len(emojis))
	for _, emoji := range emojis {
		if normalized := normalizeReputationEmoji(emoji); normalized != "" {
			set[normalized] = true
		}
	}
	return set
}

func normalizeReputationEmoji(emoji string) string {
	return strings.ReplaceAll(strings.TrimSpace(emoji), "\ufe0f", "")
}
