// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
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

// auto_moderation.go implements the four automatic actions an AI moderation
// rule can trigger (report / warn / delete / ban). Each helper assumes the
// caller has already produced a per-message dedupe key via
// handleBadWordDetected and is responsible for:
//   • performing the Telegram-side action,
//   • logging it to the actions table (admin = the bot itself), and
//   • optionally posting a notice to the admin chat (controlled by
//     rule.NotifyAdmin).

// botActionContext returns a webModContext with the bot identified as the
// initiating admin. This is the data structure the existing logAction helper
// expects - reusing it keeps "auto" actions visible in the same actions log
// as manual ones.
func (b *Bot) botActionContext(message *tgbotapi.Message) *webModContext {
	userID := int64(0)
	username := ""
	if message.From != nil {
		userID = message.From.ID
		username = getUserDisplayNameFromUser(message.From)
	}
	if username == "" {
		username = b.getUserDisplayName(userID)
	}
	botID := b.botSelf.ID
	botName := b.botSelf.Username
	if botName == "" {
		botName = i18n.T("automod.bot_actor")
	}
	return &webModContext{
		userID:    userID,
		chatID:    message.Chat.ID,
		messageID: message.MessageID,
		username:  username,
		adminID:   botID,
		adminName: botName,
	}
}

// describeRuleTrigger renders a short "rule X (trigger=…)" suffix used in
// admin notices and log lines so admins can tell which configured rule fired.
func describeRuleTrigger(rule *config.ModerationRule) string {
	if rule == nil {
		return i18n.T("automod.trigger_unknown")
	}
	if rule.Description != "" {
		return fmt.Sprintf("%q (%s)", rule.Description, rule.Trigger)
	}
	return fmt.Sprintf("%q", rule.Trigger)
}

// notifyAutoAction posts a notice to the admin chat about an automatic action
// the bot just performed, unless the rule opted out via notify_admin: false.
func (b *Bot) notifyAutoAction(rule *config.ModerationRule, text string) {
	if rule != nil && !rule.IsNotifyAdmin() {
		return
	}
	if b.config.Admin.ChatID == 0 {
		return
	}
	b.sendToAdminChat(text)
}

// messageLinkFromMessage returns a Telegram message URL for the given message.
// The underlying tgbotapi v5 Message has no thread/topic field, so the link
// won't carry topic context; callers needing topic-aware links should use the
// parsed-link helpers in filters.go instead.
func messageLinkFromMessage(message *tgbotapi.Message) string {
	return generateMessageURL(message.Chat.ID, message.MessageID, nil)
}

// ── ACTION: report ──────────────────────────────────────────────────────────

// chainAction summarizes one step in an auto-moderation dispatch chain. It is
// passed to autoModerateReport so the admin card can describe the prior/peer
// automatic actions (e.g. "🤖 Auto-warned, then reported") and so the card's
// buttons can reflect the resulting state (no Delete if already deleted, etc.).
type chainAction struct {
	action string // "warn" | "mute" | "delete" | "content_filter"
	rule   *config.ModerationRule
}

// autoModerateReport is the legacy behavior: react on the message and send a
// manual-moderation card to the admin chat. The admin chooses what to do.
// Auto-delete is not performed here (that's the "delete" action's job).
//
// otherActions lists every other automatic action dispatched for the same
// message in this batch (both already-executed and queued-after-report).
// They are summarised in the report body and used to set the card flags so
// admins see e.g. a "Restore" button when the chain also includes a delete.
// decisionDetails contains the AI's reasoning (lines after the trigger line).
func (b *Bot) autoModerateReport(message *tgbotapi.Message, messageText string, isContentFilter bool, rule *config.ModerationRule, otherActions []chainAction, decisionDetails string) {
	// notify_admin is a no-op for the report action: the admin card IS the
	// admin notification, so it's always sent regardless of the rule's
	// notify_admin value. notify_admin only suppresses the auxiliary
	// "🤖 auto-warned X" notices emitted by warn / mute / delete actions.
	emoji := b.config.Reactions.BadMessage
	if isContentFilter {
		emoji = b.config.Reactions.ContentFilter
	}
	b.setMessageReaction(message.Chat.ID, message.MessageID, emoji)

	moderationMessage := *message
	moderationMessage.Text = messageText

	reason := "🤬 Bad message detected"
	if rule != nil && rule.Description != "" {
		reason = "🤬 " + rule.Description
	}

	// Append AI decision details if present
	if decisionDetails != "" {
		reason = reason + "\n\n🧠 " + i18n.T("mod.decision_details") + "\n" + decisionDetails
	}

	// Compose an "auto-actions performed" summary so the admin understands
	// what already happened without having to scroll the action log.
	willDelete := false
	if len(otherActions) > 0 {
		var lines []string
		for _, oa := range otherActions {
			trigger := describeRuleTrigger(oa.rule)
			switch oa.action {
			case "warn":
				lines = append(lines, i18n.Tf("mod.auto_action_warn", trigger))
			case "mute":
				lines = append(lines, i18n.Tf("mod.auto_action_mute", trigger))
			case "delete":
				willDelete = true
				lines = append(lines, i18n.Tf("mod.auto_action_delete", trigger))
			}
		}
		if len(lines) > 0 {
			reason = reason + "\n\n" + i18n.T("mod.auto_actions_header") + "\n" + strings.Join(lines, "\n")
		}
	}

	// If the message will be (or already is) deleted by another step in the
	// chain, render the card in its "auto-deleted" form so admins get the
	// Restore button and the original-text preview.
	b.sendToAdminForModeration(&moderationMessage, reason, willDelete, messageText)
}

// ── ACTION: delete ──────────────────────────────────────────────────────────

// autoModerateDelete deletes the offending message immediately and either logs
// the action (admin = bot) or, on failure, falls back to a report with a
// "restore" button so the admin can recover.
func (b *Bot) autoModerateDelete(message *tgbotapi.Message, messageText string, isContentFilter bool, rule *config.ModerationRule) {
	if err := b.deleteMessageWithRetry(message.Chat.ID, message.MessageID); err != nil {
		log.Printf("Auto-moderation (delete): failed to delete message %d in chat %d: %v",
			message.MessageID, message.Chat.ID, err)
		// Fall back to a report so the admin can take action manually.
		b.autoModerateReport(message, messageText, isContentFilter, rule, nil, "")
		return
	}
	log.Printf("Auto-moderation (delete): removed message %d in chat %d (%s)",
		message.MessageID, message.Chat.ID, describeRuleTrigger(rule))

	ctx := b.botActionContext(message)
	reason := i18n.T("automod.reason_delete")
	if rule != nil && rule.Description != "" {
		reason = rule.Description
	}
	b.logAction(ctx, "delete", reason, 0)

	b.notifyAutoAction(rule, i18n.Tf("automod.notify_delete", ctx.username, describeRuleTrigger(rule)))
}

// ── ACTION: warn ────────────────────────────────────────────────────────────

// autoModerateWarn issues a warning to the user (mirroring handleWarningAction
// but with the bot acting as the moderator). The original message is kept;
// only a warning reply is sent.
func (b *Bot) autoModerateWarn(message *tgbotapi.Message, messageText string, rule *config.ModerationRule) {
	if message.From == nil {
		log.Printf("Auto-moderation (warn): cannot warn anonymous message %d", message.MessageID)
		return
	}
	ctx := b.botActionContext(message)

	// Skip if a warning for this message already exists (e.g. admin already warned).
	if has, err := b.db.HasWarningForMessage(ctx.userID, ctx.messageID); err == nil && has {
		log.Printf("Auto-moderation (warn): warning already exists for message %d, skipping", ctx.messageID)
		return
	}

	// React on the message so chat members see something happened.
	b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.BadMessage)

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
		log.Printf("Auto-moderation (warn): error storing warning: %v", err)
		return
	}

	// Resolve mute info / reputation just like handleWarningAction does so the
	// AI warning prompt has full context.
	muteInfoText := ""
	if b.config.AI.WarningMute != "" {
		if muteInfo, err := b.db.GetActiveMuteInfo(ctx.userID, ctx.chatID); err == nil && muteInfo != nil {
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
	if profile, perr := b.db.GetUserProfile(ctx.userID); perr == nil && profile != nil {
		reputation = profile.Reputation
	}

	displayName := b.getUserDisplayName(ctx.userID)
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
		ChatID:           ctx.chatID,
		Text:             warningText,
		ReplyToMessageID: ctx.messageID,
	})
	if err != nil {
		log.Printf("Auto-moderation (warn): error sending warning message: %v", err)
	} else {
		b.storeBotMessageInfo(&sent)
		if err := b.db.AddMessageForDeletion(sent.MessageID, sent.Chat.ID); err != nil {
			log.Printf("Auto-moderation (warn): error queuing warning for deletion: %v", err)
		}
		// Record the warning reply's own message id so cancelling the warning
		// later deletes exactly this message, not some other bot reply.
		if err := b.db.UpdateWarningMessageID(ctx.userID, ctx.messageID, sent.MessageID); err != nil {
			log.Printf("Auto-moderation (warn): error recording warning message id: %v", err)
		}
	}

	b.logAction(ctx, "warn", i18n.T("warn.reason"), 0)

	link := messageLinkFromMessage(message)
	b.notifyAutoAction(rule, i18n.Tf("automod.notify_warn", displayName, describeRuleTrigger(rule), link))
}

// ── ACTION: mute ───────────────────────────────────────────

// autoModerateMute restricts the offending user for ContentModeration.DefaultMuteMinutes
// (0 = effectively forever). The original message is preserved; only the user
// is silenced. On Telegram failure we roll back the DB record and fall back to
// a report so admins can act manually.
func (b *Bot) autoModerateMute(message *tgbotapi.Message, messageText string, rule *config.ModerationRule) {
	if message.From == nil {
		log.Printf("Auto-moderation (mute): cannot mute anonymous message %d", message.MessageID)
		return
	}
	ctx := b.botActionContext(message)

	log.Printf("Auto-moderation (mute): dispatching for user %d in chat %d (rule: %s)",
		ctx.userID, ctx.chatID, describeRuleTrigger(rule))

	duration := b.config.AI.ContentModeration.DefaultMuteMinutes
	var muteUntil time.Time
	var untilUnix int64
	if duration > 0 {
		muteUntil = time.Now().Add(time.Duration(duration) * time.Minute)
		untilUnix = muteUntil.Unix()
	} else {
		// 0 → "forever" for both the DB row and the Telegram restriction call.
		muteUntil = time.Now().AddDate(10, 0, 0)
		untilUnix = 0
	}

	reason := i18n.T("automod.reason_mute")
	if rule != nil && rule.Description != "" {
		reason = rule.Description
	}

	mutedUser := &database.MutedUser{
		UserID:    ctx.userID,
		Username:  ctx.username,
		ChatID:    ctx.chatID,
		MutedBy:   ctx.adminID,
		MutedAt:   time.Now(),
		UnmuteAt:  muteUntil,
		Reason:    reason,
		IsActive:  true,
		MessageID: ctx.messageID,
	}
	if err := b.db.AddMutedUserSafely(mutedUser); err != nil {
		log.Printf("Auto-moderation (mute): error storing mute record: %v", err)
		b.autoModerateReport(message, messageText, false, rule, nil, "")
		return
	}

	if _, muteErr := b.restrictUserInChats(ctx.userID, ctx.chatID, untilUnix); muteErr != nil {
		log.Printf("Auto-moderation (mute): Telegram refused restriction: %v", muteErr)
		if unmuteErr := b.db.UnmuteUser(ctx.userID, ctx.chatID); unmuteErr != nil {
			log.Printf("Auto-moderation (mute): error rolling back mute record: %v", unmuteErr)
		}
		b.autoModerateReport(message, messageText, false, rule, nil, "")
		return
	}

	b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.UserMuted)
	b.logAction(ctx, "mute", reason, duration)

	if duration > 0 {
		log.Printf("Auto-moderation (mute): muted user %d (%s) in chat %d for %d min (%s, until %s)",
			ctx.userID, ctx.username, ctx.chatID, duration, describeRuleTrigger(rule),
			muteUntil.Format(time.RFC3339))
	} else {
		log.Printf("Auto-moderation (mute): muted user %d (%s) in chat %d forever (%s)",
			ctx.userID, ctx.username, ctx.chatID, describeRuleTrigger(rule))
	}

	link := messageLinkFromMessage(message)
	b.notifyAutoAction(rule, i18n.Tf("automod.notify_mute", ctx.username, duration, describeRuleTrigger(rule), link))
}
