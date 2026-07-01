// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import "strings"

// setDefaults fills in zero-valued fields with their documented defaults.
// Called from Load() after env overrides so explicit YAML / env values take
// precedence over defaults.
func setDefaults(config *Config) {
	// Resolve database provider. Recognised explicit values ("local", "remote")
	// are kept as-is (lower-cased). For empty or unknown values, auto-detect:
	// if both URL and AuthToken are set, treat as "remote"; otherwise "local".
	provider := strings.ToLower(strings.TrimSpace(config.Database.Provider))
	if provider != "local" && provider != "remote" {
		if strings.TrimSpace(config.Database.URL) != "" && strings.TrimSpace(config.Database.AuthToken) != "" {
			provider = "remote"
		} else {
			provider = "local"
		}
	}
	config.Database.Provider = provider
	if config.Database.Path == "" {
		config.Database.Path = "./db/moderation.db"
	}
	// Reaction emoji defaults
	if config.Reactions.SuspiciousMessage == "" {
		config.Reactions.SuspiciousMessage = "🤔"
	}
	if config.Reactions.BadMessage == "" {
		config.Reactions.BadMessage = "🍌"
	}
	if config.Reactions.ContentFilter == "" {
		config.Reactions.ContentFilter = "🥴"
	}
	if config.Reactions.CreativeReplyLimit == "" {
		config.Reactions.CreativeReplyLimit = "🥱"
	}
	if config.Reactions.ExtractingLink == "" {
		config.Reactions.ExtractingLink = "✍"
	}
	if config.Reactions.ExtractLinkFailed == "" {
		config.Reactions.ExtractLinkFailed = "🌚"
	}
	if config.Reactions.UserMuted == "" {
		config.Reactions.UserMuted = "🤮"
	}
	if config.Reactions.ReportAcknowledged == "" {
		config.Reactions.ReportAcknowledged = "👌"
	}
	if config.Reactions.CreativeReplyError == "" {
		config.Reactions.CreativeReplyError = "😐"
	}
	// Message deletion defaults
	if config.MessageDeletion.ChatDeletionRetentionHours == 0 {
		config.MessageDeletion.ChatDeletionRetentionHours = 3
	}
	if config.MessageDeletion.CleanupIntervalHours == 0 {
		config.MessageDeletion.CleanupIntervalHours = 3
	}
	// Database cleanup defaults
	if config.DatabaseCleanup.CleanupIntervalHours == 0 {
		config.DatabaseCleanup.CleanupIntervalHours = 24
	}
	if config.DatabaseCleanup.MessageRetentionHours == 0 {
		config.DatabaseCleanup.MessageRetentionHours = 168 // 7 days
	}
	if config.DatabaseCleanup.WarningRetentionHours == 0 {
		config.DatabaseCleanup.WarningRetentionHours = 168
	}
	if config.DatabaseCleanup.ActionRetentionHours == 0 {
		config.DatabaseCleanup.ActionRetentionHours = 168
	}
	// Update-processing defaults
	if config.UpdateProcessing.Workers <= 0 {
		config.UpdateProcessing.Workers = 1
	}
	if config.UpdateProcessing.StatsIntervalSeconds == 0 {
		config.UpdateProcessing.StatsIntervalSeconds = 600
	}
	// Scheduled events defaults
	if config.ScheduledEvents.MissedEventMaxDelayMinutes == 0 {
		config.ScheduledEvents.MissedEventMaxDelayMinutes = 60
	}
	// Auto-moderation: DefaultMuteMinutes is intentionally not defaulted here.
	// 0 means "mute forever" (documented behavior); the example config sets 60
	// explicitly for new users who want a finite default.
	if config.ScheduledEvents.LockTimeoutMinutes == 0 {
		config.ScheduledEvents.LockTimeoutMinutes = 15
	}
	// AI feature defaults
	if config.AI.MorningGreeting.Time == "" {
		config.AI.MorningGreeting.Time = "08:00"
	}
	if config.AI.DailySummary.Time == "" {
		config.AI.DailySummary.Time = "02:00"
	}
	if config.AI.CreativeReplies.MaxMessages == 0 {
		config.AI.CreativeReplies.MaxMessages = 3
	}
	if config.AI.CreativeReplies.TimeWindow == 0 {
		config.AI.CreativeReplies.TimeWindow = 3
	}
	if config.AI.CreativeReplies.ReplyChainDepth == 0 {
		config.AI.CreativeReplies.ReplyChainDepth = 5
	}
	if config.AI.CreativeReplies.ReplyChainMaxAgeHours == 0 {
		config.AI.CreativeReplies.ReplyChainMaxAgeHours = 6
	}
	if config.AI.LinkSummaries.MaxExtractedContentLength == 0 {
		config.AI.LinkSummaries.MaxExtractedContentLength = 4096
	}
	if config.AI.MessageSummaries.MinLength == 0 {
		config.AI.MessageSummaries.MinLength = 1000
	}
	// External data defaults
	if config.AI.ExternalData.WeatherLatitude == 0 && config.AI.ExternalData.WeatherLongitude == 0 {
		config.AI.ExternalData.WeatherLatitude = 50.088
		config.AI.ExternalData.WeatherLongitude = 14.4208
	}
	if config.AI.ExternalData.HolidaysCountry == "" {
		config.AI.ExternalData.HolidaysCountry = "CZ"
	}
	if config.AI.ExternalData.WikipediaLanguage == "" {
		config.AI.ExternalData.WikipediaLanguage = "cs"
	}
	if config.AI.ExternalData.TranslateWikipedia == nil {
		defaultTrue := true
		config.AI.ExternalData.TranslateWikipedia = &defaultTrue
	}
	// RSS translation prompt falls back to generic translation prompt
	if config.AI.Rss.TranslationPrompt.System == "" && config.AI.Rss.TranslationPrompt.User == "" {
		config.AI.Rss.TranslationPrompt = config.AI.TranslationPrompt
	}
	// Server defaults
	if config.Server.ListenAddr == "" {
		config.Server.ListenAddr = "0.0.0.0"
	}
	if config.Server.ListenPort == 0 {
		config.Server.ListenPort = 8080
	}
	// Web UI defaults
	if config.WebUI.PathPrefix == "" {
		config.WebUI.PathPrefix = "/admin"
	}
	if config.WebUI.ModeratorPathPrefix == "" {
		config.WebUI.ModeratorPathPrefix = "/mod"
	}
	if config.WebUI.OTPEnabled == nil {
		defaultTrue := true
		config.WebUI.OTPEnabled = &defaultTrue
	}
	// OCR.space defaults (only the free public endpoint / engine / language;
	// the feature stays disabled unless ocrspace_enabled is set explicitly).
	if config.AI.ContentModeration.OCRSpaceURL == "" {
		config.AI.ContentModeration.OCRSpaceURL = "https://api.ocr.space/parse/image"
	}
	if config.AI.ContentModeration.OCRSpaceLanguage == "" {
		config.AI.ContentModeration.OCRSpaceLanguage = "eng"
	}
	if config.AI.ContentModeration.OCRSpaceEngine == 0 {
		config.AI.ContentModeration.OCRSpaceEngine = 2
	}
	// Window (in hours) during which a user counts as "new" after their first
	// observed message. Drives the new-user moderation marker and the
	// {{new_user_rules}} placeholder.
	if config.AI.ContentModeration.NewUserWindowHours == 0 {
		config.AI.ContentModeration.NewUserWindowHours = 24
	}
	// Cap on the quoted "in reply to" text length (UTF-8 runes) injected into
	// the moderation prompt's {{reply_to}} placeholder. 0 (unset) → 500.
	if config.AI.ContentModeration.ReplyContextMaxChars == 0 {
		config.AI.ContentModeration.ReplyContextMaxChars = 500
	}
	// When a user reports a message (reply + @bot) and cross-model
	// re-moderation clears it, fall back to manual moderation unless explicitly
	// disabled. Defaults to true.
	if config.AI.ContentModeration.ComplaintManualModeration == nil {
		defaultTrue := true
		config.AI.ContentModeration.ComplaintManualModeration = &defaultTrue
	}
	// New-user profile screening prompt falls back to a built-in default that
	// asks the model to flag promotional / scam / NSFW signals across a new
	// member's name, bio, profile photo and linked personal channel.
	if config.AI.ContentModeration.NewUserProfilePrompt.System == "" &&
		config.AI.ContentModeration.NewUserProfilePrompt.User == "" {
		config.AI.ContentModeration.NewUserProfilePrompt = PromptPair{
			System: "You are a moderation assistant screening a new chat member's public " +
				"profile. You are given their name, bio and a description of their " +
				"profile photo, and, if present, the name, description and photo of " +
				"their linked personal channel. Identify signals of spam, scams, " +
				"paid promotion, adult/NSFW content, hate speech, or other " +
				"policy-violating intent. Reply with a single short line. If " +
				"nothing is concerning, reply with exactly CLEAN. Otherwise reply " +
				"with a brief description of the concern.",
			User: "Analyze the following profile:\n\n{{profile_text}}",
		}
	}
}
