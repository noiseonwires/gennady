// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "gennadium/internal/telegram"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/i18n"
	"gennadium/internal/telegram"
)

// WebUI-facing moderation helpers. These reuse the same DB operations and
// Telegram API calls as the inline admin keyboard (see callbacks.go and
// cruel_mute.go) but take plain IDs instead of a CallbackQuery, so they can
// be invoked from HTTP handlers in the web package.

// webModContext bundles the per-action context shared by every WebUI
// moderation entry point: the target user, source chat / message, and the
// admin identity used for DB records and notifications.
type webModContext struct {
	userID    int64
	chatID    int64
	messageID int
	username  string
	adminID   int64
	adminName string
}

// newWebModContext validates the target and resolves usernames once.
// messageID may be 0 when the action is not tied to a specific message.
func (b *Bot) newWebModContext(userID, chatID int64, messageID int) (*webModContext, error) {
	if !b.config.IsModerationChat(chatID) {
		return nil, fmt.Errorf("chat %d is not a moderation chat", chatID)
	}
	if userID == 0 {
		return nil, fmt.Errorf("user_id is required")
	}
	adminID := b.config.Admin.SuperAdminUserID

	username := ""
	if messageID > 0 {
		if _, u, _, err := b.getUserInfoFromMessage(chatID, messageID); err == nil && u != "" {
			username = u
		}
	}
	if username == "" {
		username = b.getUserDisplayName(userID)
	}

	return &webModContext{
		userID:    userID,
		chatID:    chatID,
		messageID: messageID,
		username:  username,
		adminID:   adminID,
		adminName: b.getUserDisplayName(adminID),
	}, nil
}

// logAction records a moderation action in the DB. Errors are logged, not
// returned, since they should not abort the user-visible action.
func (b *Bot) logAction(ctx *webModContext, actionType, reason string, durationMinutes int) {
	action := &database.Action{
		UserID:     ctx.userID,
		Username:   ctx.username,
		AdminID:    ctx.adminID,
		AdminName:  ctx.adminName,
		ActionType: actionType,
		Duration:   durationMinutes,
		Reason:     reason,
		ChatID:     ctx.chatID,
		MessageID:  ctx.messageID,
		Timestamp:  time.Now(),
	}
	if err := b.db.LogAction(action); err != nil {
		log.Printf("Error logging WebUI %s action: %v", actionType, err)
	}
}

// muteDeadline returns the unmute time and reason string for the given
// duration. durationMinutes==0 means forever (≈100 years).
func muteDeadline(durationMinutes int, isCruel bool) (time.Time, string) {
	if durationMinutes == 0 {
		key := "mute.reason_forever"
		if isCruel {
			key = "cmute.reason_forever"
		}
		return time.Now().Add(ForeverMuteDuration), i18n.T(key)
	}
	key := "mute.reason_minutes"
	if isCruel {
		key = "cmute.reason_minutes"
	}
	return time.Now().Add(time.Duration(durationMinutes) * time.Minute), i18n.Tf(key, durationMinutes)
}

// applyMute is the shared implementation behind WebMuteUser and
// WebCruelMuteUser: it records the mute, optionally restricts via Telegram,
// logs the action and sends an admin-chat notification.
func (b *Bot) applyMute(userID, chatID int64, messageID, durationMinutes int, isCruel bool) error {
	ctx, err := b.newWebModContext(userID, chatID, messageID)
	if err != nil {
		return err
	}

	muteUntil, reasonText := muteDeadline(durationMinutes, isCruel)

	mutedUser := &database.MutedUser{
		UserID:    ctx.userID,
		Username:  ctx.username,
		ChatID:    ctx.chatID,
		MutedBy:   ctx.adminID,
		MutedAt:   time.Now(),
		UnmuteAt:  muteUntil,
		Reason:    reasonText,
		IsActive:  true,
		MessageID: ctx.messageID,
		IsCruel:   isCruel,
	}
	if err := b.db.AddMutedUserSafely(mutedUser); err != nil {
		return fmt.Errorf("add muted user: %w", err)
	}

	// Cruel mutes are enforced by auto-deleting messages on arrival, so no
	// Telegram-level restriction is applied here.
	if !isCruel {
		if _, muteErr := b.restrictUserInChats(ctx.userID, ctx.chatID, muteUntil.Unix()); muteErr != nil {
			// Roll back DB record so state stays consistent.
			if unmuteErr := b.db.UnmuteUser(ctx.userID, ctx.chatID); unmuteErr != nil {
				log.Printf("WebUI: error rolling back mute record after Telegram failure: %v", unmuteErr)
			}
			return fmt.Errorf("telegram restrict failed: %w", muteErr)
		}
	}

	actionType := "mute"
	if isCruel {
		actionType = "cmute"
	}
	b.logAction(ctx, actionType, reasonText, durationMinutes)

	return nil
}

// WebMuteUser mutes a user from the WebUI. durationMinutes==0 means forever.
// messageID may be 0 when not tied to a specific message.
func (b *Bot) WebMuteUser(userID, chatID int64, messageID, durationMinutes int) error {
	return b.applyMute(userID, chatID, messageID, durationMinutes, false)
}

// WebCruelMuteUser cruel-mutes a user from the WebUI. durationMinutes==0 means
// forever. messageID may be 0 when not tied to a specific message.
func (b *Bot) WebCruelMuteUser(userID, chatID int64, messageID, durationMinutes int) error {
	return b.applyMute(userID, chatID, messageID, durationMinutes, true)
}

// WebDeleteUserMessages deletes a user's recent messages from the WebUI. period
// is one of "1h", "1d" or "all". It returns the number of messages deleted.
func (b *Bot) WebDeleteUserMessages(userID, chatID int64, period string) (int, error) {
	since, ok := purgePeriodSince(period)
	if !ok {
		return 0, fmt.Errorf("invalid period %q", period)
	}
	return b.deleteUserMessagesSince(userID, chatID, since), nil
}

// WebDeleteMessage deletes a single message from the Telegram chat (and clears
// any pending-deletion record for it). The local message_info row is left
// intact so the WebUI message list keeps its history; use the separate
// "delete from DB" action to remove the record.
func (b *Bot) WebDeleteMessage(userID, chatID int64, messageID int) error {
	if messageID == 0 {
		return fmt.Errorf("message_id is required")
	}
	ctx, err := b.newWebModContext(userID, chatID, messageID)
	if err != nil {
		return err
	}

	if err := b.tg.DeleteMessage(chatID, messageID); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "message to delete not found") {
			// Already gone from Telegram - still clean up local state.
			_ = b.db.RemoveMessageFromDeletion(messageID, chatID)
			return fmt.Errorf("message already deleted in Telegram")
		}
		if strings.Contains(errMsg, "message can't be deleted") {
			return fmt.Errorf("Telegram refused to delete message (too old or insufficient rights)")
		}
		return fmt.Errorf("telegram delete failed: %w", err)
	}

	if err := b.db.RemoveMessageFromDeletion(messageID, chatID); err != nil {
		log.Printf("WebUI delete: failed to remove message %d from deletion queue: %v", messageID, err)
	}

	b.logAction(ctx, "delete", i18n.T("delete.reason"), 0)
	return nil
}

// WebUnmuteUser removes both regular and cruel mute records for the given user
// in the given chat, and lifts the Telegram restriction.
func (b *Bot) WebUnmuteUser(userID, chatID int64) error {
	ctx, err := b.newWebModContext(userID, chatID, 0)
	if err != nil {
		return err
	}

	// Prefer the stored mute record's username when available (it may carry a
	// nicer formatted handle than the live Telegram lookup).
	if mutedUsers, err := b.db.GetActiveMutedUsers(); err == nil {
		for _, u := range mutedUsers {
			if u.UserID == userID && u.ChatID == chatID && u.Username != "" {
				ctx.username = u.Username
				break
			}
		}
	}

	if err := b.db.UnmuteUser(userID, chatID); err != nil {
		return fmt.Errorf("unmute user: %w", err)
	}
	b.unrestrictUserInChats(userID, chatID)

	b.logAction(ctx, "unmute", i18n.T("unmute.reason"), 0)
	return nil
}

// WebWarnUser issues a warning for a specific message from the WebUI. The
// warning text is generated by the same AI pipeline as the inline admin
// keyboard and posted as a reply in the source chat.
func (b *Bot) WebWarnUser(userID, chatID int64, messageID int) error {
	if messageID == 0 {
		return fmt.Errorf("message_id is required")
	}
	ctx, err := b.newWebModContext(userID, chatID, messageID)
	if err != nil {
		return err
	}

	hasWarning, err := b.db.HasWarningForMessage(userID, messageID)
	if err != nil {
		return fmt.Errorf("check existing warning: %w", err)
	}
	if hasWarning {
		return fmt.Errorf("warning already exists for this message")
	}

	_, _, messageText, _ := b.getUserInfoFromMessage(chatID, messageID)

	warning := &database.Warning{
		UserID:    ctx.userID,
		Username:  ctx.username,
		ChatID:    ctx.chatID,
		WarnedBy:  ctx.adminID,
		WarnedAt:  time.Now(),
		Reason:    i18n.T("warn.reason"),
		MessageID: ctx.messageID,
	}
	if err := b.db.AddWarning(warning); err != nil {
		return fmt.Errorf("add warning: %w", err)
	}

	// Resolve mute info for the warning prompt.
	muteInfoText := ""
	if b.config.AI.WarningMute != "" {
		if muteInfo, err := b.db.GetActiveMuteInfo(userID, chatID); err == nil && muteInfo != nil {
			remaining := time.Until(muteInfo.UnmuteAt)
			var mutedFor string
			if remaining > 365*24*time.Hour {
				mutedFor = i18n.T("mute.duration_forever")
			} else {
				mutedFor = formatDuration(remaining)
			}
			muteInfoText = strings.ReplaceAll(b.config.AI.WarningMute, "{{muted_for}}", mutedFor)
		}
	}

	reputation := ""
	if profile, perr := b.db.GetUserProfile(userID); perr == nil && profile != nil {
		reputation = profile.Reputation
	}

	displayName := b.getUserDisplayName(userID)
	warningText, werr := b.generateWarningNotification(displayName, messageText, muteInfoText, reputation, ctx.chatID)
	if werr != nil {
		if strings.Contains(werr.Error(), "content_filter") && messageText != "" {
			warningText, werr = b.generateWarningNotification(displayName, "", muteInfoText, reputation, ctx.chatID)
		}
		if werr != nil {
			warningText = i18n.T("warn.fallback")
		}
	}

	sent, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           chatID,
		Text:             warningText,
		ReplyToMessageID: messageID,
	})
	if err != nil {
		log.Printf("WebUI: error sending warning message: %v", err)
	} else {
		b.storeBotMessageInfo(&sent)
		if err := b.db.AddMessageForDeletion(sent.MessageID, sent.Chat.ID); err != nil {
			log.Printf("WebUI: error queuing warning for deletion: %v", err)
		}
	}

	b.logAction(ctx, "warn", i18n.T("warn.reason"), 0)
	return nil
}

// WebRemoderateMessage re-runs the AI moderation pipeline on a previously
// stored message and triggers the same auto-moderation actions (delete / warn /
// mute / report) that the on-receive flow would have triggered.
//
// Unlike the on-receive light→full staging, re-moderation is a deliberate "try
// harder to catch it" pass: it runs the moderation prompt against every
// distinct configured model endpoint and acts as soon as ANY of them flags the
// message (see remoderateAcrossModels).
//
// Useful when a message was missed by AI initially and the admin has since
// adjusted the moderation prompts/rules - clicking "moderate again" runs the
// new configuration against the stored message without waiting for a new one.
//
// The original on-receive flow is *not* fully replayed: image/OCR
// enhancement, profile tracking, deletion-queue insertion and message_info
// storage are all skipped (the message is already recorded). Only the
// content-moderation analysis and its triggered actions are repeated.
func (b *Bot) WebRemoderateMessage(userID, chatID int64, messageID int) error {
	if messageID == 0 {
		return fmt.Errorf("message_id is required")
	}
	ctx, err := b.newWebModContext(userID, chatID, messageID)
	if err != nil {
		return err
	}

	synthetic, messageText, err := b.buildSyntheticMessageFromDB(messageID, chatID)
	if err != nil {
		return err
	}
	if messageText == "" {
		return fmt.Errorf("message has no text to moderate")
	}

	// Honour the same skip rules as the on-receive flow.
	skipAdmin := b.config.AI.ContentModeration.SkipAdminUsers && b.isUserAdmin(userID)
	if skipAdmin {
		return fmt.Errorf("user is an admin and SkipAdminUsers is enabled")
	}
	if b.isUserWhitelisted(userID) {
		return fmt.Errorf("user is whitelisted")
	}

	// Clear the dedup record so handleBadWordDetected won't short-circuit
	// when re-processing the same (chat_id, message_id) tuple within the
	// 10-minute dedup window.
	modKey := fmt.Sprintf("%d_%d", chatID, messageID)
	b.moderatedMu.Lock()
	delete(b.moderatedMsgs, modKey)
	b.moderatedMu.Unlock()

	b.logAction(ctx, "remoderate", i18n.T("remoderate.reason"), 0)

	log.Printf("WebUI: re-running moderation on message %d in chat %d (admin %d)", messageID, chatID, ctx.adminID)
	matchedRules, isContentFilter, decisionDetails := b.remoderateAcrossModels(synthetic, messageText)
	if len(matchedRules) == 0 && !isContentFilter {
		log.Printf("WebUI re-moderate: message %d in chat %d came back clean on every configured model", messageID, chatID)
		return nil
	}
	b.handleBadWordDetected(synthetic, messageText, isContentFilter, matchedRules, decisionDetails)
	return nil
}

// buildSyntheticMessageFromDB reconstructs a neutral telegram.Message from the
// stored message_info row for (messageID, chatID), including the reply-to
// context, so the AI moderation pipeline sees the same context it had on the
// original ingest. It is shared by the re-moderation entry points that act on
// an already-recorded message rather than a fresh update: the WebUI "moderate
// again" action and the in-chat reply-complaint trigger. Returns the
// reconstructed message and its text, or an error when the message isn't
// tracked in the database.
func (b *Bot) buildSyntheticMessageFromDB(messageID int, chatID int64) (*tgbotapi.Message, string, error) {
	messageInfo, err := b.db.GetMessageInfo(messageID, chatID)
	if err != nil || messageInfo == nil {
		return nil, "", fmt.Errorf("message not found in database")
	}

	synthetic := &tgbotapi.Message{
		MessageID: messageInfo.MessageID,
		// IsForum mirrors whether a (gated) topic was stored, so messageTopic()
		// reconstructs the original topic for scope checks during re-moderation.
		Chat: tgbotapi.Chat{ID: messageInfo.ChatID, IsForum: messageInfo.MessageThreadID != 0},
		From: &tgbotapi.User{
			ID:        messageInfo.UserID,
			FirstName: messageInfo.Username,
		},
		Text:            messageInfo.Text,
		Date:            int(messageInfo.Timestamp.Unix()),
		MessageThreadID: messageInfo.MessageThreadID,
	}

	// Reconstruct reply context from DB so the AI prompt sees the same
	// quoted-message context as on the original ingest.
	if messageInfo.ReplyToMessageID != nil {
		replyID := *messageInfo.ReplyToMessageID
		if replyInfo, rerr := b.db.GetMessageInfo(replyID, chatID); rerr == nil && replyInfo != nil {
			synthetic.ReplyToMessage = &tgbotapi.Message{
				MessageID: replyInfo.MessageID,
				Chat:      synthetic.Chat,
				From: &tgbotapi.User{
					ID:        replyInfo.UserID,
					FirstName: replyInfo.Username,
				},
				Text: replyInfo.Text,
			}
		} else {
			synthetic.ReplyToMessage = &tgbotapi.Message{
				MessageID: replyID,
				Chat:      synthetic.Chat,
			}
		}
	}

	return synthetic, messageInfo.Text, nil
}

// remoderateAcrossModels re-runs the content-moderation prompt against every
// distinct configured model endpoint (light + full, de-duplicated by
// provider/endpoint/deployment) and returns as soon as ANY of them flags the
// message - the manual "re-moderate" action is a deliberate "try harder to
// catch it" pass, so a single positive verdict is enough to act on. When no
// model flags the message (or only transient errors occur) it returns a clean
// verdict. Honors the same enabled/scope gating as the on-receive path.
//
// Returns the same tuple as containsBadWords: (matchedRules,
// isContentFilterViolation, decisionDetails).
func (b *Bot) remoderateAcrossModels(message *tgbotapi.Message, fullMessageText string) ([]config.ModerationRule, bool, string) {
	if !b.config.AI.ContentModeration.Enabled || message == nil ||
		!b.config.IsModerationChat(message.Chat.ID) {
		return nil, false, ""
	}
	if !b.config.IsModerationActive(message.Chat.ID, messageTopic(message)) {
		return nil, false, ""
	}

	replyToText := b.buildModerationReplyContext(message)
	userID := message.From.ID
	chatID := message.Chat.ID

	models := b.distinctModerationModels()
	if len(models) == 0 {
		return nil, false, ""
	}

	for _, m := range models {
		single := config.AIModelConfigs{Configs: []config.AIModelConfig{m.cfg}}
		matched, details, err := b.analyzeMessageContentWithModel(fullMessageText, userID, chatID, replyToText, single, "🔁", m.label)
		if err != nil {
			var cfErr *ContentFilterError
			if errors.As(err, &cfErr) {
				// Azure's safety filter firing is itself a positive verdict.
				log.Printf("⚠️ Re-moderation: content filter triggered by %s for message %d", m.label, message.MessageID)
				csRules := b.matchContentSecurityRules()
				csDetails := i18n.T("mod.content_security_details")
				if cfErr.Details != "" {
					csDetails += "\n" + cfErr.Details
				}
				b.replaceMessageContentWithPlaceholder(message.MessageID, message.Chat.ID, i18n.T("filter.content_removed"), csDetails)
				return csRules, true, csDetails
			}
			// A broken/unavailable endpoint shouldn't block the others.
			log.Printf("Re-moderation: %s failed for message %d: %v (trying next model)", m.label, message.MessageID, err)
			continue
		}
		if len(matched) > 0 {
			log.Printf("🔁 Re-moderation: %s flagged message %d with %d rule(s)", m.label, message.MessageID, len(matched))
			return matched, false, details
		}
		log.Printf("🔁 Re-moderation: %s cleared message %d", m.label, message.MessageID)
	}
	return nil, false, ""
}

// labeledModerationModel pairs a single model endpoint config with a
// human-readable label used only for re-moderation logging.
type labeledModerationModel struct {
	cfg   config.AIModelConfig
	label string
}

// distinctModerationModels returns every configured moderation model endpoint
// (light first, then full), de-duplicated by provider/endpoint/deployment so a
// model shared between the light and full slots is only tried once.
func (b *Bot) distinctModerationModels() []labeledModerationModel {
	var out []labeledModerationModel
	seen := make(map[string]bool)
	add := func(group string, cfgs []config.AIModelConfig) {
		for i, c := range cfgs {
			if strings.TrimSpace(c.Endpoint) == "" && strings.TrimSpace(c.DeploymentName) == "" {
				continue
			}
			key := c.ResolveProvider() + "|" + c.Endpoint + "|" + c.DeploymentName
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, labeledModerationModel{
				cfg:   c,
				label: fmt.Sprintf("%s model #%d (%s)", group, i+1, c.DeploymentName),
			})
		}
	}
	add("light", b.config.AI.LightModel.Configs)
	add("full", b.config.AI.FullModel.Configs)
	return out
}
