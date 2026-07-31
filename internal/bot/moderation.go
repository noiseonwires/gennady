// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"regexp"
	"time"

	"gennadium/internal/i18n"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
)

// createModerationKeyboard creates a standard moderation keyboard for actions.
// If isAutoDeleted is true, includes a "Restore" button instead of "Delete".
// If the target user is already muted, replaces mute buttons with an unmute button.
// If a warning for this message already exists in the database, replaces the
// "Warn" button with a "Delete warning" button so admins can undo it instead
// of being offered a no-op.
func (b *Bot) createModerationKeyboard(chatID int64, messageID int, targetUserID int64, threadID int, isAutoDeleted bool) telegram.InlineKeyboard {
	// Detect a pre-existing warning (issued either by an admin or by the
	// AI auto-moderator) so we can flip the first button to "delete warning".
	hasWarning := false
	if has, err := b.db.HasWarningForMessage(targetUserID, messageID); err == nil && has {
		hasWarning = true
	}

	var firstRow []telegram.InlineButton
	if hasWarning {
		firstRow = []telegram.InlineButton{
			telegram.NewButton(i18n.T("btn.delete_warning"), fmt.Sprintf("delwarn_%d_%d_%d_%d", chatID, messageID, targetUserID, threadID)),
		}
	} else {
		firstRow = []telegram.InlineButton{
			telegram.NewButton(i18n.T("btn.warn"), fmt.Sprintf("warn_%d_%d_%d_%d", chatID, messageID, targetUserID, threadID)),
		}
	}
	rows := [][]telegram.InlineButton{
		telegram.NewRow(firstRow...),
	}

	// Check if user is already muted - show unmute button instead of mute buttons
	muteInfo, _ := b.db.GetActiveMuteInfo(targetUserID, chatID)
	if muteInfo != nil {
		timeLeft := formatDuration(time.Until(muteInfo.UnmuteAt))
		rows = append(rows, telegram.NewRow(
			telegram.NewButton(i18n.Tf("btn.already_muted", timeLeft), fmt.Sprintf("unmute_%d_%d", targetUserID, chatID)),
		))
	} else {
		rows = append(rows, regularMuteRows(chatID, messageID, targetUserID, threadID)...)
		rows = append(rows, buildCruelMuteRow(chatID, messageID, targetUserID, threadID))
	}

	if isAutoDeleted {
		rows = append(rows, telegram.NewRow(
			telegram.NewButton(i18n.T("btn.restore_msg"), fmt.Sprintf("restore_%d_%d_%d_%d", chatID, messageID, targetUserID, threadID)),
		))
	} else {
		rows = append(rows, telegram.NewRow(
			telegram.NewButton(i18n.T("btn.delete_msg"), fmt.Sprintf("delete_%d_%d_%d_%d", chatID, messageID, targetUserID, threadID)),
		))
	}

	// While a warning is active, the admin must first remove it before the
	// message can be marked as "not a violation". This keeps the two-step
	// nature explicit and avoids ambiguous double-action cards.
	if !hasWarning {
		rows = append(rows, telegram.NewRow(
			telegram.NewButton(i18n.T("btn.not_violation"), fmt.Sprintf("notbad_%d_%d_%d_%d", chatID, messageID, targetUserID, threadID)),
		))
	}

	return telegram.NewKeyboard(rows...)
}

// sendToAdminForModeration sends a message to admin chat for moderation decision.
// If isAutoDeleted is true, uses the auto-deleted report format with a Restore button.
func (b *Bot) sendToAdminForModeration(message *tgbotapi.Message, reason string, isAutoDeleted bool, messageText string) {
	if b.config.Admin.ChatID == 0 {
		log.Printf("Admin chat ID not configured, cannot send moderation request")
		return
	}

	username := getUserDisplayNameFromUser(message.From)
	chatName := b.getChatNameShort(message.Chat.ID)

	violationInfo := b.buildViolationInfo(message.From.ID, message.Chat.ID)

	threadID := messageTopic(message)

	var moderationText string
	if isAutoDeleted {
		// Use messageText parameter (original text before deletion)
		moderationText = i18n.Tf("mod.report_auto_deleted",
			reason, username, chatName, violationInfo, messageText)
	} else {
		// Use message URL for non-deleted messages
		messageURL := generateMessageURLFromMessage(message)

		moderationText = i18n.Tf("mod.report",
			reason, username, chatName, messageURL, b.topicContext(message.Chat.ID, threadID), violationInfo, message.Text)
	}

	keyboard := b.createModerationKeyboard(message.Chat.ID, message.MessageID, message.From.ID, threadID, isAutoDeleted)

	moderationText = truncateMessage(moderationText, MaxTelegramMessageLength)

	sentMsg, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           b.config.Admin.ChatID,
		Text:             moderationText,
		Keyboard:         &keyboard,
		ReplyToMessageID: b.firstAdminReplyID(),
	})
	if err != nil {
		log.Printf("Error sending moderation request to admin: %v", err)
		return
	}

	if isAutoDeleted {
		log.Printf("Sent moderation request (auto-deleted) to admin chat: %d", sentMsg.MessageID)
	} else {
		log.Printf("Sent moderation request to admin chat: %d", sentMsg.MessageID)
	}

	// Also notify super-admin in DM if configured
	b.sendModerationToSuperAdmin(moderationText, keyboard)
}

// reportedMessage identifies the message a complaint is about, together with
// the details needed to build the manual moderation card for it.
type reportedMessage struct {
	messageID int
	chatID    int64
	userID    int64
	threadID  int
	username  string
	text      string
}

// handleReplyModerationTrigger handles moderation when bot is mentioned as reply to a message.
func (b *Bot) handleReplyModerationTrigger(message *tgbotapi.Message) {
	if message.ReplyToMessage == nil {
		return
	}

	b.handleComplaint(message, reportedMessage{
		messageID: message.ReplyToMessage.MessageID,
		chatID:    message.Chat.ID,
		userID:    message.ReplyToMessage.From.ID,
		threadID:  messageTopic(message),
		username:  getUserDisplayNameFromUser(message.ReplyToMessage.From),
		text:      message.ReplyToMessage.Text,
	})
}

// handleLinkModerationTrigger handles a complaint made by addressing the bot
// with a t.me link pointing at another message of the same chat, instead of
// replying to that message. It returns false when the message carries no such
// link or when the linked message isn't a reportable one (not recorded in the
// database, the complaint itself, or one of the bot's own messages).
func (b *Bot) handleLinkModerationTrigger(message *tgbotapi.Message) bool {
	if !b.config.IsModerationChat(message.Chat.ID) {
		return false
	}

	text := message.Text
	if text == "" {
		text = message.Caption
	}
	targetID, ok := sameChatMessageLinkTarget(text, message.Chat)
	if !ok || targetID == message.MessageID {
		return false
	}

	info, err := b.db.GetMessageInfo(targetID, message.Chat.ID)
	if err != nil || info == nil {
		log.Printf("Link complaint: message %d in chat %d is not tracked in the database - ignoring", targetID, message.Chat.ID)
		return false
	}
	if info.UserID == b.botSelf.ID {
		return false
	}

	log.Printf("Bot addressed with a link to message %d of the same chat - triggering moderation", targetID)

	if err := b.db.AddMessageForDeletion(message.MessageID, message.Chat.ID); err != nil {
		log.Printf("Error adding link complaint message to deletion queue: %v", err)
	}

	b.handleComplaint(message, reportedMessage{
		messageID: info.MessageID,
		chatID:    info.ChatID,
		userID:    info.UserID,
		threadID:  info.MessageThreadID,
		username:  b.getUserDisplayNameByID(info.UserID, info.ChatID),
		text:      info.Text,
	})
	return true
}

// handleComplaint runs the report flow for the message a user complained about:
// cross-model re-moderation first, then the manual admin card as a fallback.
func (b *Bot) handleComplaint(message *tgbotapi.Message, target reportedMessage) {
	// Before escalating to a human, re-run AI moderation across every distinct
	// configured model (mirroring the WebUI "moderate again" action). When any
	// model now flags the reported message the matching auto-moderation actions
	// are dispatched and we stop here without bothering the admins.
	if b.remoderateReportedMessage(target) {
		log.Printf("Complaint: message %d in chat %d flagged by cross-model re-moderation; acted automatically", target.messageID, target.chatID)
		return
	}

	// Every model cleared the reported message (or it couldn't be re-moderated).
	// Fall back to manual moderation unless it's disabled in config, in which
	// case the complaint ends here.
	if !b.config.AI.ContentModeration.IsComplaintManualModeration() {
		log.Printf("Complaint: message %d in chat %d cleared by all models and manual moderation is disabled; nothing to do", target.messageID, target.chatID)
		b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.ReportAcknowledged)
		return
	}

	keyboard := b.createModerationKeyboard(target.chatID, target.messageID, target.userID, target.threadID, false)

	violationInfo := b.buildViolationInfo(target.userID, target.chatID)

	responseText := i18n.Tf("mod.complaint",
		target.username, target.text, violationInfo)

	responseText = truncateMessage(responseText, MaxTelegramMessageLength)

	sentMsg, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           b.config.Admin.ChatID,
		Text:             responseText,
		Keyboard:         &keyboard,
		ReplyToMessageID: b.firstAdminReplyID(),
	})
	if err != nil {
		log.Printf("Error sending moderation keyboard: %v", err)
		return
	}

	// Also notify super-admin in DM if configured
	b.sendModerationToSuperAdmin(responseText, keyboard)

	err = b.db.AddMessageForDeletion(sentMsg.MessageID, sentMsg.Chat.ID)
	if err != nil {
		log.Printf("Error adding moderation response to deletion queue: %v", err)
	}

	if b.config.IsModerationChat(message.Chat.ID) {
		log.Printf("Report received from moderation chat, user %d reported message %d", message.From.ID, target.messageID)
	}
}

// remoderateReportedMessage re-runs AI content moderation across every distinct
// configured model for the message a user reported (the reply target of a
// "@bot" complaint, or the message its t.me link points at). When any model
// flags it, the matching auto-moderation actions are dispatched and it returns
// true. It returns false when the message can't be re-moderated (content
// moderation disabled, message not recorded in the DB, or it carries no text)
// or when every model clears it - leaving the caller to decide whether to
// escalate to manual moderation.
func (b *Bot) remoderateReportedMessage(target reportedMessage) bool {
	if !b.config.AI.ContentModeration.Enabled {
		return false
	}
	chatID := target.chatID
	replyToID := target.messageID

	synthetic, messageText, err := b.buildSyntheticMessageFromDB(replyToID, chatID)
	if err != nil || messageText == "" {
		// Not enough stored context to re-moderate (e.g. a media-only message
		// or one the bot never recorded) - let the caller fall back to manual.
		return false
	}

	// Clear the dedup record so handleBadWordDetected won't short-circuit if the
	// reported message was processed within the 10-minute dedup window.
	modKey := fmt.Sprintf("%d_%d", chatID, replyToID)
	b.moderatedMu.Lock()
	delete(b.moderatedMsgs, modKey)
	b.moderatedMu.Unlock()

	matchedRules, isContentFilter, decisionDetails := b.remoderateAcrossModels(synthetic, messageText)
	if len(matchedRules) == 0 && !isContentFilter {
		return false
	}
	b.handleBadWordDetected(synthetic, messageText, isContentFilter, matchedRules, decisionDetails)
	return true
}

// buildViolationInfo builds the violation history and user profile string for moderation messages.
// The bulk of the content (7d stats, AI profile, activity chart + first-seen) is
// produced by getUserProfileForModeration so that AI prompts and admin reports stay in sync.
func (b *Bot) buildViolationInfo(userID int64, chatID int64) string {
	return b.getUserProfileForModeration(userID, chatID)
}

// isMessageLink checks if the text contains a Telegram message link (private or public).
func (b *Bot) isMessageLink(text string) bool {
	privateLinkPattern := regexp.MustCompile(`https://t\.me/c/\d+/\d+(?:/\d+)?`)
	if privateLinkPattern.MatchString(text) {
		return true
	}
	if publicMessageLinkRegex.MatchString(text) {
		return true
	}
	return false
}

// sendModerationToSuperAdmin sends a moderation notification with keyboard to super-admin's DM
// when both notify_super_admin is enabled and super_admin_user_id is configured.
func (b *Bot) sendModerationToSuperAdmin(text string, keyboard telegram.InlineKeyboard) {
	if !b.config.Admin.NotifySuperAdmin || b.config.Admin.SuperAdminUserID == 0 {
		return
	}
	// Don't send if super-admin chat is the same as admin chat (avoid duplicate)
	if b.config.Admin.SuperAdminUserID == b.config.Admin.ChatID {
		return
	}
	if _, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:   b.config.Admin.SuperAdminUserID,
		Text:     text,
		Keyboard: &keyboard,
	}); err != nil {
		log.Printf("Error sending moderation notification to super-admin: %v", err)
	}
}
