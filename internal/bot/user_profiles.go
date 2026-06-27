// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"strings"
	"time"

	"gennadium/internal/database"
	"gennadium/internal/i18n"
)

// runUserProfilesUpdate is the scheduled task that generates/updates user profiles.
// It iterates over all moderation chats, finds active users since the last run,
// gathers their messages and moderation history, and uses AI to generate or update profiles.
func (b *Bot) runUserProfilesUpdate() {
	if !b.config.AI.Enabled || !b.config.AI.UserProfiles.Enabled {
		return
	}

	log.Printf("👤 Starting user profiles update...")

	// Determine lookback window: since the last successful run, or full DB retention on first run
	retentionHours := b.config.DatabaseCleanup.MessageRetentionHours
	if retentionHours <= 0 {
		retentionHours = 168 // fallback to 7 days
	}
	since := time.Now().Add(-time.Duration(retentionHours) * time.Hour)
	if event, err := b.db.GetScheduledEvent("user_profiles"); err == nil && event != nil {
		if !event.LastFiredAt.IsZero() && event.LastFiredAt.Year() >= 2020 {
			since = event.LastFiredAt
			log.Printf("👤 Using messages since last run: %s", since.Format("2006-01-02 15:04"))
		}
	}

	chatIDs := b.config.Moderation.ChatIDs.All()
	if len(chatIDs) == 0 {
		log.Printf("👤 No moderation chats configured, skipping profile update")
		return
	}

	// Bulk-fetch all messages and moderation data in 3 DB queries
	allData, err := b.db.GetAllProfileData(chatIDs, since)
	if err != nil {
		log.Printf("👤 Error fetching profile data: %v", err)
		return
	}

	if len(allData) == 0 {
		log.Printf("👤 No active users since %s", since.Format("2006-01-02 15:04"))
		return
	}

	log.Printf("👤 Processing %d active users across %d chat(s)", len(allData), len(chatIDs))

	botUserID := b.botSelf.ID
	totalProcessed := 0
	totalErrors := 0

	for userID, pd := range allData {
		if userID == botUserID {
			continue
		}
		if b.isUserWhitelisted(userID) {
			continue
		}
		if b.config.AI.UserProfiles.SkipForeverMutedUsers && b.isUserForeverMuted(userID, chatIDs) {
			log.Printf("👤 Skipping profile for forever-muted user %d (%s)", userID, pd.Username)
			continue
		}

		if err := b.generateOrUpdateUserProfile(pd, since); err != nil {
			log.Printf("👤 Error processing profile for user %d (%s): %v", userID, pd.Username, err)
			totalErrors++
		} else {
			totalProcessed++
		}
	}

	log.Printf("👤 User profiles update complete: %d processed, %d errors", totalProcessed, totalErrors)
}

// isUserForeverMuted reports whether the user has an active permanent ("forever")
// mute in any of the given chats. Forever mutes are stored with an unmute time
// far in the future (≈100 years), so we treat any active mute that expires more
// than 10 years from now as permanent.
func (b *Bot) isUserForeverMuted(userID int64, chatIDs []int64) bool {
	foreverThreshold := time.Now().Add(ForeverMuteDetectionThreshold)
	for _, chatID := range chatIDs {
		mute, err := b.db.GetActiveMuteInfo(userID, chatID)
		if err != nil {
			log.Printf("👤 Error checking mute status for user %d in chat %d: %v", userID, chatID, err)
			continue
		}
		if mute != nil && mute.UnmuteAt.After(foreverThreshold) {
			return true
		}
	}
	return false
}

// generateOrUpdateUserProfile generates a new profile or updates an existing one for a user.
func (b *Bot) generateOrUpdateUserProfile(pd *database.ProfileData, since time.Time) error {
	if len(pd.Messages) == 0 {
		return nil
	}

	// Build message digest for AI
	messageDigest := b.buildMessageDigest(pd.Messages, pd.Warnings, pd.Mutes, pd.Cleared, pd.MessageActions, since)

	// Check for existing profile (1 DB query per user - cached after first lookup)
	existingProfile, err := b.db.GetUserProfile(pd.UserID)
	if err != nil {
		return fmt.Errorf("failed to get existing profile: %w", err)
	}

	var profileText string
	if existingProfile != nil {
		profileText, err = b.updateUserProfileWithAI(existingProfile, messageDigest, pd.Username)
	} else {
		profileText, err = b.generateUserProfileWithAI(messageDigest, pd.Username)
	}
	if err != nil {
		return fmt.Errorf("AI profile generation failed: %w", err)
	}

	reputation := extractReputation(profileText)

	profile := &database.UserProfile{
		UserID:     pd.UserID,
		Username:   pd.Username,
		Profile:    profileText,
		Reputation: reputation,
	}

	if err := b.db.UpsertUserProfile(profile); err != nil {
		return fmt.Errorf("failed to save profile: %w", err)
	}

	b.invalidateProfileCache(pd.UserID)

	log.Printf("👤 Profile %s for user %d (%s) [reputation: %s]",
		map[bool]string{true: "updated", false: "created"}[existingProfile != nil],
		pd.UserID, pd.Username, reputation)
	return nil
}

// buildMessageDigest creates a text summary of user's messages and moderation actions for AI analysis.
func (b *Bot) buildMessageDigest(messages []database.MessageInfo, warnings int, mutes int, cleared int, messageActions map[int][]database.MessageAction, since time.Time) string {
	var sb strings.Builder

	sb.WriteString(i18n.Tf("profile.digest_messages_header", len(messages)) + "\n")
	for i, msg := range messages {
		if i >= 50 {
			sb.WriteString(i18n.Tf("profile.digest_more_messages", len(messages)-50) + "\n")
			break
		}
		ts := msg.Timestamp.Format("15:04")
		text := msg.Text
		if len(text) > 300 {
			text = text[:300] + "..."
		}

		// Build per-message moderation annotation
		var annotation string
		if actions, ok := messageActions[msg.MessageID]; ok && len(actions) > 0 {
			labels := make([]string, 0, len(actions))
			for _, a := range actions {
				var label string
				switch a.Type {
				case "warn":
					label = i18n.T("profile.digest_annotation_warned")
				case "mute":
					label = i18n.T("profile.digest_annotation_muted")
				case "cleared":
					label = i18n.T("profile.digest_annotation_cleared")
				default:
					label = "⚡ " + a.Type
				}
				if reason := strings.TrimSpace(a.Reason); reason != "" {
					label += i18n.Tf("profile.digest_annotation_reason", reason)
				}
				labels = append(labels, label)
			}
			annotation = " [" + strings.Join(labels, "; ") + "]"
		}

		// Include reply context if available
		if msg.ReplyToMessageID != nil {
			replyTo, err := b.db.GetMessageInfo(*msg.ReplyToMessageID, msg.ChatID)
			if err == nil && replyTo != nil {
				replyText := replyTo.Text
				if len(replyText) > 150 {
					replyText = replyText[:150] + "..."
				}
				replyAuthor := replyTo.Username
				if replyAuthor == "" {
					replyAuthor = fmt.Sprintf("user_%d", replyTo.UserID)
				}
				replyCtx := i18n.Tf("profile.digest_replying_to", replyAuthor, replyText)
				sb.WriteString(fmt.Sprintf("[%s] (%s) %s%s\n", ts, replyCtx, text, annotation))
				continue
			}
		}

		sb.WriteString(fmt.Sprintf("[%s] %s%s\n", ts, text, annotation))
	}

	hours := int(time.Since(since).Hours())
	var period string
	if hours <= 24 {
		period = i18n.T("profile.digest_period_24h")
	} else {
		period = i18n.Tf("profile.digest_period_days", hours/24)
	}
	sb.WriteString(i18n.Tf("profile.digest_moderation_stats", period, warnings, mutes, cleared))

	return sb.String()
}

// generateUserProfileWithAI creates a new user profile using AI.
func (b *Bot) generateUserProfileWithAI(messageDigest string, username string) (string, error) {
	cfg := b.config.AI.UserProfiles
	if cfg.Prompt.System == "" || cfg.Prompt.User == "" {
		return "", fmt.Errorf("user_profiles.prompt.system and prompt.user must be configured")
	}

	replacements := map[string]string{
		"username": username,
		"messages": messageDigest,
	}

	systemPrompt := applyReplacements(cfg.Prompt.System, replacements)
	userPrompt := applyReplacements(cfg.Prompt.User, replacements)

	b.dumpPromptToLog("user_profile_new", systemPrompt, userPrompt)

	return b.callAzureOpenAIWithRetries("user_profile", userPrompt, systemPrompt, b.config.AI.LightModel, 300, 3)
}

// updateUserProfileWithAI updates an existing user profile with new data.
func (b *Bot) updateUserProfileWithAI(existing *database.UserProfile, messageDigest string, username string) (string, error) {
	cfg := b.config.AI.UserProfiles
	if cfg.UpdatePrompt.System == "" || cfg.UpdatePrompt.User == "" {
		return "", fmt.Errorf("user_profiles.update_prompt.system and update_prompt.user must be configured")
	}

	replacements := map[string]string{
		"username":         username,
		"messages":         messageDigest,
		"existing_profile": existing.Profile,
	}

	systemPrompt := applyReplacements(cfg.UpdatePrompt.System, replacements)
	userPrompt := applyReplacements(cfg.UpdatePrompt.User, replacements)

	b.dumpPromptToLog("user_profile_update", systemPrompt, userPrompt)

	return b.callAzureOpenAIWithRetries("user_profile", userPrompt, systemPrompt, b.config.AI.LightModel, 300, 3)
}

// extractReputation parses the reputation value from AI-generated profile text.
func extractReputation(profileText string) string {
	lower := strings.ToLower(profileText)

	// Look for explicit "Reputation: xxx" pattern
	for _, prefix := range []string{"reputation:", "reputation -", "репутация:"} {
		if idx := strings.LastIndex(lower, prefix); idx != -1 {
			rest := strings.TrimSpace(lower[idx+len(prefix):])
			// Take first word
			word := strings.Fields(rest)
			if len(word) > 0 {
				switch {
				case strings.Contains(word[0], "good") || strings.Contains(word[0], "хорош"):
					return "good"
				case strings.Contains(word[0], "bad") || strings.Contains(word[0], "плох"):
					return "bad"
				}
			}
		}
	}

	return "neutral"
}

// getUserProfileForModeration returns a formatted user profile block for use in
// moderation prompts and admin notifications. The block may include:
//   - AI-generated profile line (username, reputation, profile text) if the
//     ai.user_profiles feature is enabled and a profile exists for the user;
//   - 7-day moderation stats (warnings / mutes / total actions) for the given chat;
//   - general-profile line (activity chart + earliest first-seen date) from the
//     user_profiles general tracker.
//
// Returns an empty string only when none of the above produces any content.
func (b *Bot) getUserProfileForModeration(userID int64, chatID int64) string {
	var lines []string

	// Admin indicator
	if b.isUserAdmin(userID) {
		lines = append(lines, "⚡ This user is a chat ADMIN.")
	}

	// New-user marker: a stable, self-expiring signal that this author's first
	// observed message was recent (within new_user_window_hours). Injected the
	// same way as the profile-screening findings so the moderation model knows
	// the author is new to the chat.
	if b.isNewUser(userID) {
		lines = append(lines, i18n.T("mod.user_new"))
	}

	// AI-generated profile line (optional, cached). Also surfaced when the
	// new-user profile screening is enabled, since that feature records its
	// notices into the same profile row even if the AI profile generator
	// (ai.user_profiles) is turned off.
	if b.config.AI.UserProfiles.Enabled ||
		b.config.AI.ContentModeration.NewUserProfileCheckEnabled {
		profile := b.lookupCachedUserProfile(userID)
		if profile != nil {
			// Prominent, language-agnostic flag plus the concrete screening
			// findings when the automated new-member profile screening marked this
			// user suspicious, so the moderation model treats the author
			// accordingly regardless of which sub-check fired.
			if analysis := strings.TrimSpace(profile.TgProfileAnalysis); analysis != "" {
				lines = append(lines, i18n.T("mod.user_profile_suspicious"))
				lines = append(lines, analysis)
			}
			// AI-generated behavior profile line (only when one exists).
			if strings.TrimSpace(profile.Profile) != "" {
				lines = append(lines, i18n.Tf("mod.user_profile", profile.Username, profile.Reputation, profile.Profile))
			}
		}
	}

	// 7-day moderation stats line (same format as the moderation report).
	if chatID != 0 {
		recentWarnings, _ := b.db.GetRecentUserWarnings(userID, chatID)
		recentMutes, _ := b.db.GetRecentUserActionsByType(userID, chatID, "mute")
		recentActions, _ := b.db.GetRecentUserActions(userID, chatID)
		if recentWarnings > 0 || recentMutes > 0 || recentActions > 0 {
			lines = append(lines, i18n.Tf("mod.violation_info", recentWarnings, recentMutes, recentActions))
		}
	}

	// General-profile line (activity chart + first-seen). Empty if the general
	// tracker is disabled or the user has no data.
	if generalInfo := b.formatGeneralProfileForModeration(userID); generalInfo != "" {
		lines = append(lines, generalInfo)
	}

	// Username-reuse warning: if the user's current @username was previously
	// held by other user_id(s) and any of those prior holders has at least one
	// "mute" action on record in this chat, surface that to the moderator AI.
	// Gated on the general user_profiles tracker (which populates the history
	// table this check relies on).
	if b.config.UserProfiles.Enabled {
		if reuseLine := b.buildMutedUsernameReuseLine(userID, chatID); reuseLine != "" {
			lines = append(lines, reuseLine)
		}
	}

	return strings.Join(lines, "\n")
}

// getUserReputationForModeration returns a compact reputation block for the
// {{user_reputation}} moderation placeholder. It is a token-cheap alternative
// to the full {{user_profile}} block: it surfaces a new-user marker (when the
// author is within the new-user window), the AI reputation score and, when
// present, a note that an automated screening check marked the profile
// suspicious. Returns an empty string when none of those apply.
func (b *Bot) getUserReputationForModeration(userID int64) string {
	var lines []string

	// New-user marker: surfaced regardless of the AI profile / screening
	// features, since it derives only from first-seen tracking.
	if b.isNewUser(userID) {
		lines = append(lines, i18n.T("mod.user_new"))
	}

	if b.config.AI.UserProfiles.Enabled ||
		b.config.AI.ContentModeration.NewUserProfileCheckEnabled {
		if profile := b.lookupCachedUserProfile(userID); profile != nil {
			if profile.Reputation != "" {
				lines = append(lines, i18n.Tf("mod.user_reputation", profile.Reputation))
			}
			// Surface the new-member profile screening result in the compact block too,
			// together with the concrete sub-check finding(s) the screening recorded.
			if analysis := strings.TrimSpace(profile.TgProfileAnalysis); analysis != "" {
				lines = append(lines, i18n.T("mod.user_reputation_suspicious"))
				for _, reason := range strings.Split(analysis, "\n") {
					if reason = strings.TrimSpace(reason); reason != "" {
						lines = append(lines, "• "+reason)
					}
				}
			}
		}
	}

	return strings.Join(lines, "\n")
}

// newUserWindow returns the configured "new user" window as a duration,
// defaulting to 24h when unset or non-positive.
func (b *Bot) newUserWindow() time.Duration {
	hours := b.config.AI.ContentModeration.NewUserWindowHours
	if hours <= 0 {
		hours = 24
	}
	return time.Duration(hours) * time.Hour
}

// isNewUser reports whether the user's earliest observed message across all
// chats falls within the configured new-user window (default 24h). It is a
// stable, self-expiring signal: it relies only on the recorded first-seen
// timestamp, so a user automatically stops being "new" once the window elapses.
// Returns false when the general user-profile tracker is disabled or the user
// has no first-seen record yet.
func (b *Bot) isNewUser(userID int64) bool {
	if userID == 0 || b.db == nil {
		return false
	}
	firstSeen, err := b.db.GetUserFirstSeen(userID)
	if err != nil || firstSeen.IsZero() {
		return false
	}
	return b.timeNow().Sub(firstSeen) < b.newUserWindow()
}

// newUserRulesFor returns the configured new-user rules text for the
// {{new_user_rules}} moderation placeholder, but only when the user is still
// within the new-user window. Returns "" when no rules are configured or the
// user is not new, so the placeholder expands to nothing for established users.
func (b *Bot) newUserRulesFor(userID int64) string {
	rules := strings.TrimSpace(b.config.AI.ContentModeration.NewUserRules)
	if rules == "" {
		return ""
	}
	if !b.isNewUser(userID) {
		return ""
	}
	return b.config.AI.ContentModeration.NewUserRules
}

// buildMutedUsernameReuseLine returns a one-line warning describing prior
// holders of the user's current @username that have a mute history in chatID.
// Returns "" when the user has no @username, no prior holders, or none of the
// prior holders has ever been muted in this chat.
func (b *Bot) buildMutedUsernameReuseLine(userID, chatID int64) string {
	username, err := b.db.GetLatestUsername(userID)
	if err != nil || username == "" {
		return ""
	}
	reusers, err := b.db.FindUsernameReusers(username, userID)
	if err != nil || len(reusers) == 0 {
		return ""
	}

	type mutedPrior struct {
		entry database.UsernameReuseEntry
		mutes int
	}
	var muted []mutedPrior
	totalMutes := 0
	for _, r := range reusers {
		// Prefer per-chat history; fall back to all-chats if chatID is 0.
		cnt, cerr := b.db.CountUserMutesInChat(r.UserID, chatID)
		if cerr != nil || cnt <= 0 {
			continue
		}
		muted = append(muted, mutedPrior{entry: r, mutes: cnt})
		totalMutes += cnt
	}
	if len(muted) == 0 {
		return ""
	}

	const maxShown = 3
	parts := make([]string, 0, len(muted))
	for i, m := range muted {
		if i >= maxShown {
			parts = append(parts, fmt.Sprintf("+%d more", len(muted)-maxShown))
			break
		}
		name := strings.TrimSpace(m.entry.DisplayName)
		if name == "" {
			name = "(no display name)"
		}
		parts = append(parts, fmt.Sprintf("%s (id %d, %d mute(s))", name, m.entry.UserID, m.mutes))
	}

	return i18n.Tf("mod.username_reuse_muted_priors", username, len(muted), totalMutes, strings.Join(parts, "; "))
}

// lookupCachedUserProfile returns the AI-generated profile for userID, consulting
// the in-memory cache first and falling back to the DB. Returns nil when the
// user has no profile (the negative result is also cached, with a TTL).
func (b *Bot) lookupCachedUserProfile(userID int64) *database.UserProfile {
	if cached, ok := b.profileCache.get(userID); ok {
		return cached
	}

	profile, err := b.db.GetUserProfile(userID)
	if err != nil {
		return nil
	}

	b.profileCache.put(userID, profile)
	return profile
}

// invalidateProfileCache removes a single entry from the profile cache.
func (b *Bot) invalidateProfileCache(userID int64) {
	b.profileCache.delete(userID)
}
