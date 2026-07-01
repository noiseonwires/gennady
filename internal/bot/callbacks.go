// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"errors"
	"fmt"
	"html"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"gennadium/internal/database"
	"gennadium/internal/i18n"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
)

// handleCallbackQuery processes callback queries from inline keyboards
func (b *Bot) handleCallbackQuery(query *tgbotapi.CallbackQuery) {
	log.Printf("Received callback query: %s", query.Data)

	chatID := int64(0)
	messageID := 0
	if query.Message != nil {
		chatID = query.Message.Chat.ID
		messageID = query.Message.MessageID
	}
	b.debugf("Moderator callback action: moderator_id=%d username=%q chat_id=%d message_id=%d data=%q", query.From.ID, query.From.Username, chatID, messageID, query.Data)

	// Parse callback data
	parts := strings.Split(query.Data, "_")
	if len(parts) < 2 {
		log.Printf("Invalid callback data format: %s", query.Data)
		return
	}

	action := parts[0]
	b.debugf("Moderator callback parsed: action=%q parts=%d moderator_id=%d", action, len(parts), query.From.ID)

	// Authorization check: verify the user is an admin before processing any callback action
	if !b.isUserAdmin(query.From.ID) {
		log.Printf("SECURITY: unauthorized callback attempt from user %d (username=%q) action=%q data=%q", query.From.ID, query.From.Username, action, query.Data)
		return
	}

	// Suppress duplicate deliveries of the same callback. Telegram can deliver
	// the same callback_query more than once - most notably as a webhook retry
	// when our HTTP ACK is slow - and each delivery is handled in its own
	// goroutine. Without this guard two handlers could run concurrently and
	// both apply the action, producing duplicate notifications (this is what
	// caused the repeated "снят мут" / unmute messages). The callback_query ID
	// is stable across redeliveries, so it is a reliable dedup key.
	if query.ID != "" && b.markCallbackHandled(query.ID) {
		log.Printf("Ignoring duplicate callback query id=%s data=%q", query.ID, query.Data)
		// Still answer so the Telegram spinner clears on the client.
		if err := b.tg.AnswerCallback(query.ID, ""); err != nil {
			log.Printf("Error answering duplicate callback query: %v", err)
		}
		return
	}

	if handler, ok := callbackRegistry[action]; ok {
		handler(b, query, parts)
	} else {
		log.Printf("Unknown callback action: %q", action)
	}

	// Always answer the callback query so the spinner disappears in the Telegram UI.
	if err := b.tg.AnswerCallback(query.ID, ""); err != nil {
		log.Printf("Error answering callback query: %v", err)
	}
}

// callbackDedupTTL bounds how long a processed callback_query ID is remembered
// for duplicate suppression. Telegram webhook retries arrive within a couple of
// minutes, so a few minutes of memory is more than enough.
const callbackDedupTTL = 10 * time.Minute

// markCallbackHandled records that the callback with the given ID is being
// processed and reports whether it had already been seen recently. It is
// atomic: when Telegram delivers the same callback more than once and the
// deliveries race in separate goroutines, only the first caller gets false and
// proceeds - the rest get true and must skip the action.
func (b *Bot) markCallbackHandled(id string) (duplicate bool) {
	now := time.Now()

	b.callbackDedupMu.Lock()
	defer b.callbackDedupMu.Unlock()

	if b.callbackDedup == nil {
		b.callbackDedup = make(map[string]time.Time)
	}

	if seen, ok := b.callbackDedup[id]; ok && now.Sub(seen) < callbackDedupTTL {
		return true
	}

	b.callbackDedup[id] = now
	// Opportunistically evict stale entries to keep the map bounded.
	for k, t := range b.callbackDedup {
		if now.Sub(t) >= callbackDedupTTL {
			delete(b.callbackDedup, k)
		}
	}
	return false
}

// handleWarningAction handles warning actions
func (b *Bot) handleWarningAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 5 {
		log.Printf("Invalid warning callback data: expected 5 parts, got %d", len(parts))
		return
	}
	ids, ok := parseModCallbackIDs(parts, 1, "warn")
	if !ok {
		return
	}
	chatID, messageID, userID, threadID := ids.ChatID, ids.MessageID, ids.UserID, ids.ThreadID

	if b.checkSelfActionGuard(query, userID, "warn") {
		return
	}

	// Get username from stored message data
	_, username, messageText, err := b.getUserInfoFromMessage(chatID, messageID)
	if err != nil {
		log.Printf("Warning: Could not get message info from database: %v. Proceeding with fallback.", err)
		// Fallback: continue without message text, we can still issue warning
		username = ""
		messageText = ""
	}

	// Check if warning already exists for this message
	hasWarning, err := b.db.HasWarningForMessage(userID, messageID)
	if err != nil {
		log.Printf("Error checking existing warning: %v", err)
		return
	}

	if hasWarning {
		// Send notification to admin chat instead of editing the message
		adminDisplayName := b.getUserDisplayName(int64(query.From.ID))
		b.sendToAdminChat(i18n.Tf("warn.already_exists", adminDisplayName))
		return
	}

	// Create warning record
	warning := &database.Warning{
		UserID:    userID,
		Username:  username,
		ChatID:    chatID,
		WarnedBy:  int64(query.From.ID),
		WarnedAt:  time.Now(),
		Reason:    i18n.T("warn.reason"),
		MessageID: messageID,
	}

	// Add warning to database
	err = b.db.AddWarning(warning)
	if err != nil {
		log.Printf("Error adding warning: %v", err)
		return
	}

	// Resolve mute info for the warning prompt
	muteInfoText := ""
	if b.config.AI.WarningMute != "" {
		muteInfo, muteErr := b.db.GetActiveMuteInfo(userID, chatID)
		if muteErr != nil {
			log.Printf("Error checking mute info for warning: %v", muteErr)
		} else if muteInfo != nil {
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

	// Resolve reputation for the warning prompt
	reputation := ""
	if profile, profileErr := b.db.GetUserProfile(userID); profileErr == nil && profile != nil {
		reputation = profile.Reputation
	}

	// Send warning message to user
	displayName := b.getUserDisplayName(userID)
	warningText, err := b.generateWarningNotification(displayName, messageText, muteInfoText, reputation, chatID)
	if err != nil {
		log.Printf("Error generating AI warning: %v", err)
		// If content filter triggered and we had message content, retry without it
		if strings.Contains(err.Error(), "content_filter") && messageText != "" {
			log.Printf("Content filter triggered during warning generation, retrying without message content")
			warningText, err = b.generateWarningNotification(displayName, "", muteInfoText, reputation, chatID)
			if err != nil {
				log.Printf("Error generating AI warning without message content: %v", err)
				warningText = i18n.T("warn.fallback")
			}
		} else {
			// Fallback to static text
			warningText = i18n.T("warn.fallback")
		}
	}

	sentWarningMsg, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           chatID,
		Text:             warningText,
		ReplyToMessageID: messageID,
	})
	if err != nil {
		log.Printf("Error sending warning message: %v", err)
	} else {
		// Store warning message in message_info for reply chain tracing
		b.storeBotMessageInfo(&sentWarningMsg)
		// Add warning message to deletion queue
		err = b.db.AddMessageForDeletion(sentWarningMsg.MessageID, sentWarningMsg.Chat.ID)
		if err != nil {
			log.Printf("Error adding warning message to deletion queue: %v", err)
		}
		// Record the warning reply's own message id so cancelling the warning
		// later deletes exactly this message, not some other bot reply.
		if err := b.db.UpdateWarningMessageID(userID, messageID, sentWarningMsg.MessageID); err != nil {
			log.Printf("Error recording warning message id: %v", err)
		}
	}

	// Log action
	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    int64(query.From.ID),
		AdminName:  getUserDisplayNameFromUser(query.From),
		ActionType: "warn",
		Reason:     i18n.T("warn.reason"),
		ChatID:     chatID,
		MessageID:  messageID,
		Timestamp:  time.Now(),
	}

	err = b.db.LogAction(action)
	if err != nil {
		log.Printf("Error logging action: %v", err)
	}
	b.recordManualModStat(int64(query.From.ID), "warn")

	// Send notification to admin chat with link to original message
	var threadIDPtr *int
	if threadID > 0 {
		threadIDPtr = &threadID
	}
	messageLink := generateMessageURL(chatID, messageID, threadIDPtr)
	adminDisplayName := b.getUserDisplayName(int64(query.From.ID))
	adminNotification := i18n.Tf("warn.admin_notify", displayName, adminDisplayName, messageLink)
	b.sendToAdminChat(adminNotification)

	// Send confirmation to the private chat where the action was initiated
	b.sendActionConfirmation(query, i18n.Tf("warn.confirm", displayName, messageLink), "warn")

	// Append notice to report and remove warn button
	b.appendActionNotice(query.Message, i18n.Tf("warn.notice", adminDisplayName), []string{"warn_"})
}

// handleMuteAction handles mute actions
func (b *Bot) handleMuteAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 6 {
		log.Printf("Invalid mute callback data: expected 6 parts, got %d", len(parts))
		return
	}
	duration, ok := parseIntField(parts, 1, "duration", "mute")
	if !ok {
		return
	}
	ids, ok := parseModCallbackIDs(parts, 2, "mute")
	if !ok {
		return
	}
	chatID, messageID, userID, threadID := ids.ChatID, ids.MessageID, ids.UserID, ids.ThreadID

	if b.checkSelfActionGuard(query, userID, "mute") {
		return
	}

	// Check if user is already muted (for logging purposes)
	isMuted, err := b.db.IsUserMuted(userID, chatID)
	if err != nil {
		log.Printf("Error checking mute status: %v", err)
		return
	}

	// Get username from stored message data
	_, username, _, err := b.getUserInfoFromMessage(chatID, messageID)
	if err != nil || username == "" {
		log.Printf("Warning: Could not get message info from database: %v. Proceeding with fallback.", err)
		username = b.getUserDisplayName(userID)
	}

	if isMuted {
		log.Printf("User %d is already muted, will replace existing mute with new one", userID)
	}

	// Calculate unmute time
	muteUntil := time.Now().Add(time.Duration(duration) * time.Minute)

	// Create muted user record
	mutedUser := &database.MutedUser{
		UserID:    userID,
		Username:  username,
		ChatID:    chatID,
		MutedBy:   int64(query.From.ID),
		MutedAt:   time.Now(),
		UnmuteAt:  muteUntil,
		Reason:    i18n.Tf("mute.reason_minutes", duration),
		IsActive:  true,
		MessageID: messageID,
	}

	// Add to database using the safe method that handles duplicates
	err = b.db.AddMutedUserSafely(mutedUser)
	if err != nil {
		log.Printf("Error adding muted user: %v", err)
		return
	}

	// Restrict user in the chat(s) - uses MuteAcrossAllChats config
	if _, muteErr := b.restrictUserInChats(userID, chatID, muteUntil.Unix()); muteErr != nil {
		// Telegram refused to restrict the user (e.g. bot lacks permission,
		// target is admin). Roll back the DB mute record and offer a
		// "cruel mute" as a fallback so the admin can silence the user by
		// auto-deleting their messages instead.
		if unmuteErr := b.db.UnmuteUser(userID, chatID); unmuteErr != nil {
			log.Printf("Error rolling back mute record after Telegram failure: %v", unmuteErr)
		}
		b.offerCruelMuteFallback(query, chatID, messageID, userID, threadID, muteErr, username)
		return
	}

	// Add puke emoji reaction to the original message to indicate mute
	b.setMessageReaction(chatID, messageID, b.config.Reactions.UserMuted)

	adminDisplayName := b.getUserDisplayName(int64(query.From.ID))

	// Log action
	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    int64(query.From.ID),
		AdminName:  adminDisplayName,
		ActionType: "mute",
		Duration:   duration,
		Reason:     i18n.Tf("mute.reason_minutes", duration),
		ChatID:     chatID,
		MessageID:  messageID,
		Timestamp:  time.Now(),
	}

	err = b.db.LogAction(action)
	if err != nil {
		log.Printf("Error logging action: %v", err)
	}
	b.recordManualModStat(int64(query.From.ID), "mute")

	// Send notification to admin chat with link to original message
	var threadIDPtr *int
	if threadID > 0 {
		threadIDPtr = &threadID
	}
	messageLink := generateMessageURL(chatID, messageID, threadIDPtr)
	adminNotification := i18n.Tf("mute.admin_notify", username, duration, adminDisplayName, messageLink)
	b.sendToAdminChat(adminNotification)

	// Send confirmation to the private chat where the action was initiated
	b.sendActionConfirmation(query, i18n.Tf("mute.confirm", username, duration, messageLink), "mute")

	// Append mute notice, remove mute buttons and offer to purge the user's messages
	b.promptDeleteUserMessages(query.Message, i18n.Tf("mute.notice", duration, adminDisplayName), chatID, userID)
}

// handleMuteAskConfirmation shows a confirmation dialog for long mutes (30 days or forever)
func (b *Bot) handleMuteAskConfirmation(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 6 {
		log.Printf("Invalid muteask callback data: expected 6 parts, got %d", len(parts))
		return
	}
	duration, ok := parseIntField(parts, 1, "duration", "muteask")
	if !ok {
		return
	}
	ids, ok := parseModCallbackIDs(parts, 2, "muteask")
	if !ok {
		return
	}
	chatID, messageID, userID, threadID := ids.ChatID, ids.MessageID, ids.UserID, ids.ThreadID

	// Get username for display
	_, username, _, err := b.getUserInfoFromMessage(chatID, messageID)
	if err != nil || username == "" {
		username = b.getUserDisplayName(userID)
	}

	// Create duration text
	var durationText string
	if duration == 0 {
		durationText = i18n.T("mute.duration_forever")
	} else {
		durationText = i18n.T("mute.duration_30d")
	}

	// Create confirmation keyboard
	confirmKeyboard := telegram.NewKeyboard(
		telegram.NewRow(
			telegram.NewButton(i18n.T("btn.confirm_mute")+" "+durationText, fmt.Sprintf("muteconfirm_%d_%d_%d_%d_%d", duration, chatID, messageID, userID, threadID)),
		),
		telegram.NewRow(
			telegram.NewButton(i18n.T("btn.cancel"), fmt.Sprintf("mutecancel_%d_%d_%d_%d_%d", duration, chatID, messageID, userID, threadID)),
		),
	)

	// Edit the message to show confirmation
	confirmText := i18n.Tf("mute.confirm_dialog", username, durationText)
	if _, err = b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    query.Message.Chat.ID,
		MessageID: query.Message.MessageID,
		Text:      confirmText,
		Keyboard:  &confirmKeyboard,
	}); err != nil {
		log.Printf("Error editing message for mute confirmation: %v", err)
	}
}

// handleMuteConfirmed handles confirmed long mutes (30 days or forever)
func (b *Bot) handleMuteConfirmed(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 6 {
		log.Printf("Invalid muteconfirm callback data: expected 6 parts, got %d", len(parts))
		return
	}
	duration, ok := parseIntField(parts, 1, "duration", "muteconfirm")
	if !ok {
		return
	}
	ids, ok := parseModCallbackIDs(parts, 2, "muteconfirm")
	if !ok {
		return
	}
	chatID, messageID, userID, threadID := ids.ChatID, ids.MessageID, ids.UserID, ids.ThreadID

	if b.checkSelfActionGuard(query, userID, "mute") {
		return
	}

	// Get username from stored message data
	_, username, _, err := b.getUserInfoFromMessage(chatID, messageID)
	if err != nil || username == "" {
		log.Printf("Warning: Could not get message info from database: %v. Proceeding with fallback.", err)
		username = b.getUserDisplayName(userID)
	}

	// Calculate unmute time
	var muteUntil time.Time
	var durationText string
	var reasonText string

	if duration == 0 {
		// Forever mute - use a far future date (see ForeverMuteDuration).
		muteUntil = time.Now().Add(ForeverMuteDuration)
		durationText = strings.ToLower(i18n.T("mute.duration_forever"))
		reasonText = i18n.T("mute.reason_forever")
	} else {
		muteUntil = time.Now().Add(time.Duration(duration) * time.Minute)
		durationText = i18n.T("mute.duration_30d")
		reasonText = i18n.Tf("mute.reason_minutes", duration)
	}

	// Create muted user record
	mutedUser := &database.MutedUser{
		UserID:    userID,
		Username:  username,
		ChatID:    chatID,
		MutedBy:   int64(query.From.ID),
		MutedAt:   time.Now(),
		UnmuteAt:  muteUntil,
		Reason:    reasonText,
		IsActive:  true,
		MessageID: messageID,
	}

	// Add to database using the safe method that handles duplicates
	err = b.db.AddMutedUserSafely(mutedUser)
	if err != nil {
		log.Printf("Error adding muted user: %v", err)
		return
	}

	// Restrict user in the chat(s) - uses MuteAcrossAllChats config
	if _, muteErr := b.restrictUserInChats(userID, chatID, muteUntil.Unix()); muteErr != nil {
		if unmuteErr := b.db.UnmuteUser(userID, chatID); unmuteErr != nil {
			log.Printf("Error rolling back mute record after Telegram failure: %v", unmuteErr)
		}
		b.offerCruelMuteFallback(query, chatID, messageID, userID, threadID, muteErr, username)
		return
	}

	// Add puke emoji reaction to the original message to indicate mute
	b.setMessageReaction(chatID, messageID, b.config.Reactions.UserMuted)

	adminDisplayName := b.getUserDisplayName(int64(query.From.ID))

	// Log action
	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    int64(query.From.ID),
		AdminName:  adminDisplayName,
		ActionType: "mute",
		Duration:   duration,
		Reason:     reasonText,
		ChatID:     chatID,
		MessageID:  messageID,
		Timestamp:  time.Now(),
	}

	err = b.db.LogAction(action)
	if err != nil {
		log.Printf("Error logging action: %v", err)
	}
	b.recordManualModStat(int64(query.From.ID), "mute")

	// Send notification to admin chat with link to original message
	var threadIDPtr *int
	if threadID > 0 {
		threadIDPtr = &threadID
	}
	messageLink := generateMessageURL(chatID, messageID, threadIDPtr)
	adminNotification := i18n.Tf("mute.confirmed_admin_notify", username, durationText, adminDisplayName, messageLink)
	b.sendToAdminChat(adminNotification)

	// Append mute notice, remove confirm/cancel buttons and offer to purge the user's messages
	b.promptDeleteUserMessages(query.Message, i18n.Tf("mute.confirmed_notice", username, durationText, adminDisplayName), chatID, userID)
}

// handleMuteCancelled handles cancellation of long mute confirmation
func (b *Bot) handleMuteCancelled(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 6 {
		log.Printf("Invalid mutecancel callback data: expected 6 parts, got %d", len(parts))
		return
	}
	// parts[1] is the duration field; we don't need it for cancel.
	ids, ok := parseModCallbackIDs(parts, 2, "mutecancel")
	if !ok {
		return
	}
	chatID, messageID, userID, threadID := ids.ChatID, ids.MessageID, ids.UserID, ids.ThreadID

	// Restore the original moderation keyboard
	keyboard := b.createModerationKeyboard(chatID, messageID, userID, threadID, false)

	// Get the original message text from database for context
	messageInfo, err := b.db.GetMessageInfo(messageID, chatID)
	var originalText string
	if err == nil && messageInfo != nil {
		originalText = messageInfo.Text
	}

	// Restore original moderation message
	username := b.getUserDisplayName(userID)
	moderationText := i18n.Tf("mute.cancelled", username, b.getChatLabel(chatID), messageID)
	if originalText != "" {
		moderationText += i18n.Tf("mute.cancelled_text", truncateMessage(originalText, 500))
	}

	if _, err := b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    query.Message.Chat.ID,
		MessageID: query.Message.MessageID,
		Text:      moderationText,
		Keyboard:  &keyboard,
	}); err != nil {
		log.Printf("Error restoring moderation keyboard: %v", err)
	}
}

// handleUnmuteAction handles unmute actions
func (b *Bot) handleUnmuteAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 3 {
		return
	}

	userID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}

	chatID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return
	}

	// Get user info for notification BEFORE unmuting (since unmuting removes from muted users list)
	mutedUsers, err := b.db.GetActiveMutedUsers()
	var username string
	if err == nil {
		for _, user := range mutedUsers {
			if user.UserID == userID && user.ChatID == chatID {
				username = user.Username
				break
			}
		}
	}

	if username == "" {
		username = b.getUserDisplayName(userID)
	}

	// Unmute user in database. The returned flag reports whether THIS call
	// actually removed the active mute record. Telegram may deliver the same
	// callback more than once (e.g. a webhook retry when our ACK is slow), and
	// each delivery is handled in its own goroutine, so two handlers can run
	// concurrently. Gating on the DELETE - whose writes are serialized by the
	// database - guarantees that only the single handler that really performed
	// the unmute proceeds to notify, preventing duplicate "unmuted" messages.
	unmuted, err := b.db.UnmuteUserIfActive(userID, chatID)
	if err != nil {
		log.Printf("Error unmuting user in database: %v", err)
		return
	}
	if !unmuted {
		// The user was already unmuted by an earlier delivery of this action.
		// Refresh the list (to drop the now-stale button) and stop here without
		// sending another notification or logging a duplicate action.
		b.handleAdminMutedUsers(query)
		return
	}

	// Unrestrict user in the chat(s) - uses MuteAcrossAllChats config
	b.unrestrictUserInChats(userID, chatID)

	adminName := getUserDisplayNameFromUser(query.From)
	// Log action
	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    int64(query.From.ID),
		AdminName:  adminName,
		ActionType: "unmute",
		Reason:     i18n.T("unmute.reason"),
		ChatID:     chatID,
		Timestamp:  time.Now(),
	}

	err = b.db.LogAction(action)
	if err != nil {
		log.Printf("Error logging action: %v", err)
	}

	// Send notification to admin chat
	b.sendToAdminChat(i18n.Tf("unmute.notify", username, adminName))

	// Refresh the muted users list to show updated status
	b.handleAdminMutedUsers(query)
}

// getUserInfoFromMessage gets user information from stored message data
func (b *Bot) getUserInfoFromMessage(chatID int64, messageID int) (int64, string, string, error) {
	messageInfo, err := b.db.GetMessageInfo(messageID, chatID)
	if err != nil {
		return 0, "", "", fmt.Errorf("failed to get message info: %v", err)
	}

	return messageInfo.UserID, messageInfo.Username, messageInfo.Text, nil
}

// handleAdminAction handles admin menu callbacks
func (b *Bot) handleAdminAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) < 2 {
		log.Printf("Not enough parts for admin action: %v", parts)
		return
	}

	// Join parts after "admin" to get the sub-action
	subAction := strings.Join(parts[1:], "_")

	switch subAction {
	case "muted_users":
		log.Printf("Executing muted_users action")
		b.handleAdminMutedUsers(query)
	case "last_actions":
		log.Printf("Executing last_actions action")
		b.handleAdminLastActions(query)
	case "top10_bad_users":
		log.Printf("Executing top10_bad_users action")
		b.handleAdminTop10BadUsers(query)
	case "stats":
		log.Printf("Executing stats action")
		b.handleAdminStats(query)
	case "punish":
		log.Printf("Executing punish action")
		b.handleAdminPunish(query)
	case "menu":
		log.Printf("Executing admin menu action")
		b.handleAdminMenu(query)
	case "about":
		log.Printf("Executing about action")
		b.handleAdminAbout(query)
	case "web_otp":
		log.Printf("Executing web_otp action")
		b.handleAdminWebOTP(query)
	case "mod_web":
		log.Printf("Executing mod_web action")
		b.handleAdminModWebUI(query)
	default:
		log.Printf("Unknown admin sub-action: %s", subAction)
	}
}

// handleAdminWebOTP generates a one-time password for web UI access
func (b *Bot) handleAdminWebOTP(query *tgbotapi.CallbackQuery) {
	if b.config.Admin.SuperAdminUserID == 0 || query.From.ID != b.config.Admin.SuperAdminUserID {
		log.Printf("SECURITY: non-super-admin user %d attempted OTP generation", query.From.ID)
		return
	}

	if b.generateWebOTP == nil {
		b.tg.AnswerCallback(query.ID, i18n.T("webui.not_enabled"))
		return
	}

	otp := b.generateWebOTP()

	text := i18n.Tf("webui.otp_text", otp)

	// Send as a private message to the admin
	_, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:    query.From.ID,
		Text:      text,
		ParseMode: telegram.ParseModeMarkdown,
	})
	if err != nil {
		log.Printf("Error sending OTP to admin: %v", err)
		b.tg.AnswerCallback(query.ID, i18n.T("webui.otp_failed"))
		return
	}

	b.tg.AnswerCallback(query.ID, i18n.T("webui.otp_sent"))
}

// handleAdminModWebUI mints a one-time login link + OTP for the isolated,
// limited moderator web UI and delivers them privately to the requesting
// moderator. Available to any admin (super-admin or live chat-admin). The link
// token rides in the URL fragment so it never reaches the server's request log;
// it is useless without the separately delivered OTP.
func (b *Bot) handleAdminModWebUI(query *tgbotapi.CallbackQuery) {
	// Defence in depth: the menu is only shown to admins, but re-check before
	// issuing credentials.
	if !b.isUserAdmin(query.From.ID) {
		log.Printf("SECURITY: non-admin user %d attempted moderator web login", query.From.ID)
		b.tg.AnswerCallback(query.ID, i18n.T("webui.mod_not_allowed"))
		return
	}

	if b.generateModeratorLogin == nil {
		b.tg.AnswerCallback(query.ID, i18n.T("webui.not_enabled"))
		return
	}

	publicURL := b.effectivePublicURL()
	if publicURL == "" {
		log.Printf("Moderator web login requested but no public URL is configured (web_ui.public_url or webhook.url)")
		b.tg.AnswerCallback(query.ID, i18n.T("webui.mod_no_public_url"))
		return
	}

	modPrefix := strings.TrimRight(strings.TrimSpace(b.config.WebUI.ModeratorPathPrefix), "/")
	if modPrefix == "" {
		modPrefix = "/mod"
	}

	token, otp := b.generateModeratorLogin(query.From.ID)
	link := publicURL + modPrefix + "/#t=" + token

	// Deliver the link and the OTP as two separate Telegram messages, always to
	// the moderator's PRIVATE chat (query.From.ID), never the group the button
	// was tapped in — so a single leaked message only exposes one of the two
	// required factors and nothing sensitive lands in the shared chat. The link
	// goes first; the code lands as the most recent message, ready to paste once
	// the page prompts for it. HTML parse mode (not Markdown) is used because
	// the configured URL/prefix may legitimately contain characters such as "_"
	// that legacy Markdown would mis-parse as formatting; the dynamic values are
	// HTML-escaped to stay safe.
	if _, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:    query.From.ID,
		Text:      i18n.Tf("webui.mod_link_text", html.EscapeString(link)),
		ParseMode: telegram.ParseModeHTML,
	}); err != nil {
		b.answerModLoginSendError(query, err)
		return
	}
	if _, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:    query.From.ID,
		Text:      i18n.Tf("webui.mod_otp_text", html.EscapeString(otp)),
		ParseMode: telegram.ParseModeHTML,
	}); err != nil {
		b.answerModLoginSendError(query, err)
		return
	}

	log.Printf("🛡️  Moderator Web UI login link issued to admin %d", query.From.ID)
	b.tg.AnswerCallback(query.ID, i18n.T("webui.mod_link_sent"))
}

// answerModLoginSendError reports a failure to deliver the moderator login
// link/OTP via the callback toast. The common case when the button is tapped
// from a group chat is that the moderator has never opened a private chat with
// the bot (or blocked it), so Telegram returns 403 Forbidden; surface a clear
// instruction to start the bot privately rather than a generic error.
func (b *Bot) answerModLoginSendError(query *tgbotapi.CallbackQuery, err error) {
	log.Printf("Error sending moderator web login to admin %d: %v", query.From.ID, err)
	var apiErr *telegram.APIError
	if errors.As(err, &apiErr) && apiErr.Code == 403 {
		b.tg.AnswerCallback(query.ID, i18n.T("webui.mod_dm_required"))
		return
	}
	b.tg.AnswerCallback(query.ID, i18n.T("webui.otp_failed"))
}

// buildBunnyNetSection returns a Markdown-formatted section listing the BunnyNet
// Magic Containers environment variables that are set, or "" when none are present.
// Intended for super-admin eyes only (deployment diagnostics).
func buildBunnyNetSection() string {
	var envLines []string
	for _, key := range []string{"BUNNYNET_MC_APPID", "BUNNYNET_MC_PODID", "BUNNYNET_MC_REGION"} {
		if v := os.Getenv(key); v != "" {
			envLines = append(envLines, fmt.Sprintf("`%s`: `%s`", key, v))
		}
	}
	if len(envLines) == 0 {
		return ""
	}
	return "\n\n🐰 *BunnyNet:*\n" + strings.Join(envLines, "\n")
}

// handleAdminStats shows the moderation funnel counters (received → light →
// full → auto/manual) for today / yesterday / day before / all time. Numbers
// after "received" carry a percentage relative to received messages.
func (b *Bot) handleAdminStats(query *tgbotapi.CallbackQuery) {
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	dayBefore := now.AddDate(0, 0, -2).Format("2006-01-02")

	rows, err := b.db.GetModerationStats(today, yesterday, dayBefore)
	if err != nil {
		log.Printf("Error getting moderation stats: %v", err)
		b.editMessageText(query.Message, i18n.T("stats.empty"))
		return
	}

	byStat := make(map[string]database.ModerationStatBuckets, len(rows))
	for _, r := range rows {
		byStat[r.Stat] = r
	}
	received := byStat[database.ModStatReceived]
	if received.Today == 0 && received.Yesterday == 0 && received.DayBefore == 0 && received.AllTime == 0 {
		b.editMessageText(query.Message, i18n.T("stats.empty"))
		return
	}

	cell := func(v int64, base int64, withPct bool) string {
		if withPct && base > 0 {
			return fmt.Sprintf("%d (%.0f%%)", v, 100*float64(v)/float64(base))
		}
		return fmt.Sprintf("%d", v)
	}
	// Funnel-relative: each stage's % is of the previous stage. base buckets
	// supply the denominator per window (light→received, full→light, actions→full).
	line := func(s, base database.ModerationStatBuckets, withPct bool) string {
		return fmt.Sprintf("%s / %s / %s / %s",
			cell(s.Today, base.Today, withPct),
			cell(s.Yesterday, base.Yesterday, withPct),
			cell(s.DayBefore, base.DayBefore, withPct),
			cell(s.AllTime, base.AllTime, withPct))
	}

	light := byStat[database.ModStatLightFlagged]
	full := byStat[database.ModStatFullConfirmed]

	var text strings.Builder
	text.WriteString(i18n.T("stats.title"))
	text.WriteString("\n")
	text.WriteString(i18n.Tf("stats.received", line(received, received, false)) + "\n")
	text.WriteString(i18n.Tf("stats.light", line(light, received, true)) + "\n")
	text.WriteString(i18n.Tf("stats.full", line(full, light, true)) + "\n")
	text.WriteString(i18n.Tf("stats.auto", line(byStat[database.ModStatAutoAction], full, true)) + "\n")
	text.WriteString(i18n.Tf("stats.cleared", line(byStat[database.ModStatManualCleared], full, true)) + "\n")
	text.WriteString(i18n.Tf("stats.manual", line(byStat[database.ModStatManualAction], full, true)))

	backKeyboard := telegram.NewKeyboard(
		telegram.NewRow(
			telegram.NewButton(i18n.T("btn.back"), "admin_menu"),
		),
	)
	if _, err := b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    query.Message.Chat.ID,
		MessageID: query.Message.MessageID,
		Text:      text.String(),
		ParseMode: telegram.ParseModeMarkdown,
		Keyboard:  &backKeyboard,
	}); err != nil {
		log.Printf("Error showing moderation stats: %v", err)
	}
}

// handleAdminAbout shows bot information
func (b *Bot) handleAdminAbout(query *tgbotapi.CallbackQuery) {
	aboutText := "ℹ️ " + b.buildAboutText()

	// Append BunnyNet environment info for super admin
	if b.config.Admin.SuperAdminUserID != 0 && query.From.ID == b.config.Admin.SuperAdminUserID {
		aboutText += buildBunnyNetSection()
	}

	backKeyboard := telegram.NewKeyboard(
		telegram.NewRow(
			telegram.NewButton(i18n.T("btn.back"), "admin_menu"),
		),
	)

	if _, err := b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    query.Message.Chat.ID,
		MessageID: query.Message.MessageID,
		Text:      aboutText,
		ParseMode: telegram.ParseModeMarkdown,
		Keyboard:  &backKeyboard,
	}); err != nil {
		log.Printf("Error showing about info: %v", err)
	}
}

// handleAdminMutedUsers shows muted users for admin
func (b *Bot) handleAdminMutedUsers(query *tgbotapi.CallbackQuery) {
	mutedUsers, err := b.db.GetActiveMutedUsers()
	if err != nil {
		log.Printf("Error getting muted users: %v", err)
		b.editMessageText(query.Message, i18n.T("muted.error"))
		return
	}

	if len(mutedUsers) == 0 {
		b.editMessageText(query.Message, i18n.T("muted.empty"))
		return
	}

	var text strings.Builder
	text.WriteString(i18n.T("muted.title"))

	// Create keyboard with unmute buttons
	var keyboard [][]telegram.InlineButton

	for i, user := range mutedUsers {
		username := user.Username
		if username == "" {
			username = b.getUserDisplayName(user.MutedBy)
		}

		// Get admin name who muted this user
		adminName, err := b.db.GetAdminNameForMute(user.UserID, user.ChatID)
		if err != nil {
			adminName = b.getUserDisplayName(user.MutedBy)
		}

		timeLeft := time.Until(user.UnmuteAt)
		text.WriteString(fmt.Sprintf("%d. %s\n", i+1, username))
		text.WriteString(fmt.Sprintf("   %s\n", i18n.Tf("muted.time_left", formatDuration(timeLeft))))
		text.WriteString(fmt.Sprintf("   %s\n", i18n.Tf("muted.reason", user.Reason)))
		text.WriteString(fmt.Sprintf("   %s\n\n", i18n.Tf("muted.muted_by", adminName)))

		// Add unmute button for each user
		unmuteButton := telegram.NewButton(
			i18n.Tf("btn.unmute", username),
			fmt.Sprintf("unmute_%d_%d", user.UserID, user.ChatID), // Format: unmute_userID_chatID
		)
		keyboard = append(keyboard, []telegram.InlineButton{unmuteButton})
	}

	// Add back button
	backButton := telegram.NewButton(i18n.T("btn.back"), "admin_menu")
	keyboard = append(keyboard, []telegram.InlineButton{backButton})

	// Update message with keyboard
	kb := telegram.InlineKeyboard{Rows: keyboard}
	if _, err = b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    query.Message.Chat.ID,
		MessageID: query.Message.MessageID,
		Text:      text.String(),
		Keyboard:  &kb,
	}); err != nil {
		log.Printf("Error updating muted users message: %v", err)
	}
}

// handleAdminLastActions shows recent moderation actions
func (b *Bot) handleAdminLastActions(query *tgbotapi.CallbackQuery) {
	actions, err := b.db.GetRecentActions(10) // Get last 10 actions
	if err != nil {
		log.Printf("Error getting recent actions: %v", err)
		b.editMessageText(query.Message, i18n.T("actions.error"))
		return
	}

	if len(actions) == 0 {
		b.editMessageText(query.Message, i18n.T("actions.empty"))
		return
	}

	var text strings.Builder
	text.WriteString(i18n.T("actions.title"))

	for i, action := range actions {
		displayName := action.Username
		adminName := action.AdminName

		text.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, getActionEmoji(action.ActionType), displayName))
		text.WriteString(fmt.Sprintf("   %s\n", i18n.Tf("actions.from", adminName)))
		text.WriteString(fmt.Sprintf("   %s\n", i18n.Tf("actions.time", action.Timestamp.Format("Jan 2, 15:04"))))
		if action.Reason != "" {
			text.WriteString(fmt.Sprintf("   %s\n", i18n.Tf("actions.reason", action.Reason)))
		}
		text.WriteString("\n")
	}

	b.editMessageText(query.Message, text.String())
}

// handleAdminTop10BadUsers shows top 10 users with most violations in last 24h
func (b *Bot) handleAdminTop10BadUsers(query *tgbotapi.CallbackQuery) {
	// Get top users with most violations in last 24 hours
	topUsers, err := b.db.GetTop10BadUsers(24)
	if err != nil {
		log.Printf("Error getting top bad users: %v", err)
		b.editMessageText(query.Message, i18n.T("top10.error"))
		return
	}

	var text strings.Builder
	text.WriteString(i18n.T("top10.title"))

	if len(topUsers) == 0 {
		text.WriteString(i18n.T("top10.empty"))
	} else {
		for i, user := range topUsers {
			position := i + 1
			displayName := b.getUserDisplayName(user.UserID)
			if displayName == fmt.Sprintf("User %d", user.UserID) && user.Username != "" {
				displayName = user.Username
			}

			var medal string
			switch position {
			case 1:
				medal = "🥇"
			case 2:
				medal = "🥈"
			case 3:
				medal = "🥉"
			default:
				medal = fmt.Sprintf("%d.", position)
			}

			text.WriteString(fmt.Sprintf("%s %s\n", medal, displayName))
			text.WriteString(fmt.Sprintf("   %s\n",
				i18n.Tf("top10.stats", user.MessagesCount, user.WarningsCount, user.MutesCount)))
			text.WriteString(fmt.Sprintf("   %s\n\n", i18n.Tf("top10.score", user.TotalScore)))
		}
	}

	// Add back button
	keyboard := telegram.NewKeyboard(
		telegram.NewRow(
			telegram.NewButton(i18n.T("btn.back_to_menu"), "admin_menu"),
		),
	)

	if _, err = b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    query.Message.Chat.ID,
		MessageID: query.Message.MessageID,
		Text:      text.String(),
		Keyboard:  &keyboard,
	}); err != nil {
		log.Printf("Error editing message: %v", err)
	}
}

// handleAdminMenu shows the admin menu
func (b *Bot) handleAdminMenu(query *tgbotapi.CallbackQuery) {
	rows := [][]telegram.InlineButton{
		telegram.NewRow(telegram.NewButton(i18n.T("btn.view_muted"), "admin_muted_users")),
		telegram.NewRow(telegram.NewButton(i18n.T("btn.view_actions"), "admin_last_actions")),
		telegram.NewRow(telegram.NewButton(i18n.T("btn.stats"), "admin_stats")),
		telegram.NewRow(telegram.NewButton(i18n.T("btn.punish"), "admin_punish")),
	}
	if b.moderatorWebLoginAvailable() {
		rows = append(rows, telegram.NewRow(telegram.NewButton(i18n.T("btn.access_web_ui"), "admin_mod_web")))
	}
	rows = append(rows, telegram.NewRow(telegram.NewButton(i18n.T("btn.about"), "admin_about")))
	keyboard := telegram.NewKeyboard(rows...)

	menuText := i18n.Tf("menu.admin_text", BotName)

	if _, err := b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    query.Message.Chat.ID,
		MessageID: query.Message.MessageID,
		Text:      menuText,
		Keyboard:  &keyboard,
	}); err != nil {
		log.Printf("Error updating admin menu: %v", err)
	}
}

// handleAdminPunish creates a message for reporting
func (b *Bot) handleAdminPunish(query *tgbotapi.CallbackQuery) {
	punishText := i18n.T("punish.instructions")

	b.editMessageText(query.Message, punishText)
}

// handleDeleteAction handles delete message actions
func (b *Bot) handleDeleteAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 5 {
		log.Printf("Invalid delete callback data: expected 5 parts, got %d", len(parts))
		return
	}
	ids, ok := parseModCallbackIDs(parts, 1, "delete")
	if !ok {
		return
	}
	chatID, messageID, userID, threadID := ids.ChatID, ids.MessageID, ids.UserID, ids.ThreadID

	_, username, _, err := b.getUserInfoFromMessage(chatID, messageID)
	if err != nil {
		log.Printf("Warning: Could not get message info from database: %v. Proceeding with fallback.", err)
		username = b.getUserDisplayName(userID)
	}
	// Delete the message immediately
	err = b.tg.DeleteMessage(chatID, messageID)
	if err != nil {
		log.Printf("Error deleting message: %v", err)

		// Check if it's a common error that we can handle gracefully
		errorMsg := err.Error()
		if strings.Contains(errorMsg, "message can't be deleted") {
			b.sendToAdminChat(i18n.Tf("delete.error_cant_delete", messageID, b.getChatLabel(chatID)))
		} else if strings.Contains(errorMsg, "message to delete not found") {
			b.sendToAdminChat(i18n.Tf("delete.error_already_deleted", messageID, b.getChatLabel(chatID)))
		} else {
			b.sendToAdminChat(i18n.Tf("delete.error_generic", messageID, b.getChatLabel(chatID), err))
		}

		// Still remove from deletion queue even if deletion failed
		err = b.db.RemoveMessageFromDeletion(messageID, chatID)
		if err != nil {
			log.Printf("Error removing message from deletion queue: %v", err)
		}

		// Update the callback query to show completion
		if err := b.tg.AnswerCallback(query.ID, i18n.T("delete.failed_callback")); err != nil {
			log.Printf("Error sending callback response: %v", err)
		}
		return
	}

	// Remove from deletion queue if it exists there
	err = b.db.RemoveMessageFromDeletion(messageID, chatID)
	if err != nil {
		log.Printf("Error removing message from deletion queue: %v", err)
		// Don't return here, continue with logging
	}

	adminName := getUserDisplayNameFromUser(query.From)

	// Log the action
	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    query.From.ID,
		AdminName:  adminName,
		ActionType: "delete",
		Duration:   0,
		Reason:     i18n.T("delete.reason"),
		ChatID:     chatID,
		MessageID:  messageID,
		Timestamp:  time.Now(),
	}

	err = b.db.LogAction(action)
	if err != nil {
		log.Printf("Error logging action: %v", err)
	}
	b.recordManualModStat(int64(query.From.ID), "delete")

	// Send notification to admin chat with link reference
	var threadIDPtr *int
	if threadID > 0 {
		threadIDPtr = &threadID
	}
	messageLink := generateMessageURL(chatID, messageID, threadIDPtr)
	adminNotification := i18n.Tf("delete.admin_notify", username, adminName, messageLink)
	b.sendToAdminChat(adminNotification)

	// Send confirmation to the private chat where the action was initiated
	b.sendActionConfirmation(query, i18n.Tf("delete.confirm", username, messageLink), "delete")

	// Append delete notice and remove delete button
	b.appendActionNotice(query.Message, i18n.Tf("delete.notice", adminName), []string{"delete_"})
}

// handleRestoreAction handles restore message actions - re-posts the deleted message content
// Note: Telegram Bot API doesn't support restoring deleted messages, so we re-send the content
func (b *Bot) handleRestoreAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 5 {
		log.Printf("Invalid restore callback data: expected 5 parts, got %d", len(parts))
		return
	}
	ids, ok := parseModCallbackIDs(parts, 1, "restore")
	if !ok {
		return
	}
	chatID, messageID, userID, threadID := ids.ChatID, ids.MessageID, ids.UserID, ids.ThreadID

	// Get message info from database (contains the original text)
	messageInfo, err := b.db.GetMessageInfo(messageID, chatID)
	if err != nil {
		log.Printf("Error getting message info for restore: %v", err)
		b.sendToAdminChat(i18n.Tf("restore.error_not_found", messageID))
		return
	}

	if messageInfo.Text == "" {
		log.Printf("Cannot restore message %d: empty text in database", messageID)
		b.sendToAdminChat(i18n.Tf("restore.error_empty", messageID))
		return
	}

	// Get username for the restored message attribution
	username := messageInfo.Username
	if username == "" {
		username = b.getUserDisplayName(userID)
	}

	// Compose the restored message with attribution
	restoredText := i18n.Tf("restore.text", username, messageInfo.Text)

	// Send to the original chat. Topic targeting and reply targeting are
	// independent: post into the original forum topic (preferring the stored
	// message_thread_id, falling back to the thread id carried in the callback),
	// and reply to the original message's parent when one is known.
	postThreadID := messageInfo.MessageThreadID
	if postThreadID == 0 && threadID > 0 {
		postThreadID = threadID
	}
	replyTo := 0
	if messageInfo.ReplyToMessageID != nil {
		replyTo = *messageInfo.ReplyToMessageID
	}

	sentMsg, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           chatID,
		Text:             restoredText,
		MessageThreadID:  postThreadID,
		ReplyToMessageID: replyTo,
	})
	if err != nil {
		log.Printf("Error sending restored message: %v", err)
		b.sendToAdminChat(i18n.Tf("restore.error_send", err))
		return
	}

	// Add restored message to deletion queue (it will be cleaned up like other bot messages)
	err = b.db.AddMessageForDeletion(sentMsg.MessageID, sentMsg.Chat.ID)
	if err != nil {
		log.Printf("Error adding restored message to deletion queue: %v", err)
	}

	adminName := getUserDisplayNameFromUser(query.From)

	// Log the action
	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    query.From.ID,
		AdminName:  adminName,
		ActionType: "restore",
		Duration:   0,
		Reason:     i18n.T("restore.reason"),
		ChatID:     chatID,
		MessageID:  messageID,
		Timestamp:  time.Now(),
	}

	err = b.db.LogAction(action)
	if err != nil {
		log.Printf("Error logging restore action: %v", err)
	}

	// Send notification to admin chat
	var threadIDPtr *int
	if threadID > 0 {
		threadIDPtr = &threadID
	}
	messageLink := generateMessageURL(chatID, sentMsg.MessageID, threadIDPtr)
	adminNotification := i18n.Tf("restore.admin_notify", username, adminName, messageLink)
	b.sendToAdminChat(adminNotification)

	// Update the callback query to show completion
	if err := b.tg.AnswerCallback(query.ID, i18n.T("restore.callback")); err != nil {
		log.Printf("Error sending callback response: %v", err)
	}

	// Append restore notice and remove restore button
	b.appendActionNotice(query.Message, i18n.Tf("restore.notice", adminName), []string{"restore_"})
}

// deleteBotWarningReply removes the bot's warning reply for an offending message
// when a warning is cancelled. It prefers the exact warning message id recorded
// at warn time (warningMessageID) and only falls back to the most recent bot
// reply to the offending message for legacy warnings that predate that tracking.
// The fallback could otherwise delete an unrelated bot reply to the same message
// (e.g. a creative reply), which is the bug this targeted lookup avoids.
func (b *Bot) deleteBotWarningReply(chatID int64, userID int64, messageID int, warningMessageID int) {
	if warningMessageID > 0 {
		if delErr := b.tg.DeleteMessage(chatID, warningMessageID); delErr != nil {
			log.Printf("cancel-warn: failed to delete warning message %d: %v", warningMessageID, delErr)
		} else {
			log.Printf("cancel-warn: deleted warning message %d for offending message %d", warningMessageID, messageID)
		}
		_ = b.db.RemoveMessageFromDeletion(warningMessageID, chatID)
		return
	}

	// Legacy fallback: no recorded warning message id, so locate the bot's most
	// recent reply to the offending message.
	botUserID := b.botSelf.ID
	if replyMsg, findErr := b.db.FindBotReplyMessage(botUserID, messageID, chatID); findErr == nil && replyMsg != nil {
		if delErr := b.tg.DeleteMessage(chatID, replyMsg.MessageID); delErr != nil {
			log.Printf("cancel-warn: failed to delete bot warning reply %d: %v", replyMsg.MessageID, delErr)
		} else {
			log.Printf("cancel-warn: deleted bot warning reply %d for message %d", replyMsg.MessageID, messageID)
		}
		_ = b.db.RemoveMessageFromDeletion(replyMsg.MessageID, chatID)
	}
}

// handleNotBadAction handles "not bad" action - removes reactions and warnings
func (b *Bot) handleNotBadAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 5 {
		log.Printf("Invalid notbad callback data: expected 5 parts, got %d", len(parts))
		return
	}
	ids, ok := parseModCallbackIDs(parts, 1, "notbad")
	if !ok {
		return
	}
	chatID, messageID, userID := ids.ChatID, ids.MessageID, ids.UserID
	// notbad doesn't use threadID - keyboard is rebuilt without it.

	// Get user info for logging
	_, username, _, err := b.getUserInfoFromMessage(chatID, messageID)
	if err != nil {
		log.Printf("Warning: Could not get message info from database: %v. Proceeding with fallback.", err)
		username = b.getUserDisplayName(userID)
	}

	// Remove any reactions from the message (try to clear all reactions)
	b.clearMessageReaction(chatID, messageID)

	// Capture the recorded warning reply id before the warning row is removed.
	warningMsgID, gErr := b.db.GetWarningMessageID(userID, messageID)
	if gErr != nil {
		log.Printf("notbad: error fetching warning message id for message %d: %v", messageID, gErr)
	}

	// Remove warnings for this specific message from database
	err = b.db.RemoveWarningForMessage(userID, messageID)
	if err != nil {
		log.Printf("Error removing warning for message %d: %v", messageID, err)
	}

	// Delete the bot's warning reply message from the chat (if any)
	b.deleteBotWarningReply(chatID, userID, messageID, warningMsgID)

	adminName := getUserDisplayNameFromUser(query.From)

	// Log the action
	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    query.From.ID,
		AdminName:  adminName,
		ActionType: "cleared",
		Duration:   0,
		Reason:     i18n.T("notbad.reason"),
		ChatID:     chatID,
		MessageID:  messageID,
		Timestamp:  time.Now(),
	}

	err = b.db.LogAction(action)
	if err != nil {
		log.Printf("Error logging clear action: %v", err)
	}
	b.recordManualModStat(int64(query.From.ID), "cleared")

	// Send notification to admin chat
	adminNotification := i18n.Tf("notbad.admin_notify", username, adminName)
	b.sendToAdminChat(adminNotification)

	// Send confirmation to the private chat where the action was initiated
	b.sendActionConfirmation(query, i18n.Tf("notbad.confirm", username), "notbad")

	// Append notice and remove all action buttons (not a violation = terminal action)
	b.appendActionNotice(query.Message, i18n.Tf("notbad.notice", adminName), []string{"warn_", "mute_", "muteask_", "delete_", "restore_", "notbad_", "cmute_", "cmuteask_", "delwarn_"})
}

// handleDeleteWarningAction removes a previously issued warning (typically one
// added by the AI auto-moderator) without marking the message as "not a
// violation" and without touching mute/delete state. This lets admins undo a
// false-positive warn while still keeping the option to act differently.
func (b *Bot) handleDeleteWarningAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 5 {
		log.Printf("Invalid delwarn callback data: expected 5 parts, got %d", len(parts))
		return
	}
	ids, ok := parseModCallbackIDs(parts, 1, "delwarn")
	if !ok {
		return
	}
	chatID, messageID, userID := ids.ChatID, ids.MessageID, ids.UserID
	threadID := ids.ThreadID

	_, username, _, err := b.getUserInfoFromMessage(chatID, messageID)
	if err != nil {
		log.Printf("delwarn: could not resolve username for message %d: %v", messageID, err)
		username = b.getUserDisplayName(userID)
	}

	// Capture the recorded warning reply id before the warning row is removed.
	warningMsgID, gErr := b.db.GetWarningMessageID(userID, messageID)
	if gErr != nil {
		log.Printf("delwarn: error fetching warning message id for message %d: %v", messageID, gErr)
	}

	if err := b.db.RemoveWarningForMessage(userID, messageID); err != nil {
		log.Printf("delwarn: error removing warning for message %d: %v", messageID, err)
		return
	}

	// Delete the bot's warning reply message from the chat (if any).
	b.deleteBotWarningReply(chatID, userID, messageID, warningMsgID)

	adminName := getUserDisplayNameFromUser(query.From)

	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    int64(query.From.ID),
		AdminName:  adminName,
		ActionType: "warning_removed",
		Reason:     i18n.T("delwarn.reason"),
		ChatID:     chatID,
		MessageID:  messageID,
		Timestamp:  time.Now(),
	}
	if err := b.db.LogAction(action); err != nil {
		log.Printf("delwarn: error logging action: %v", err)
	}

	b.sendToAdminChat(i18n.Tf("delwarn.admin_notify", username, adminName))
	b.sendActionConfirmation(query, i18n.Tf("delwarn.confirm", username), "delwarn")

	// Drop the "delete warning" button, document the removal, and surface
	// "not a violation" now that the warning step is behind us so the admin
	// can complete the two-step exoneration without rebuilding the card.
	notbadRow := []telegram.InlineButton{
		telegram.NewButton(i18n.T("btn.not_violation"), fmt.Sprintf("notbad_%d_%d_%d_%d", chatID, messageID, userID, threadID)),
	}
	b.appendActionNoticeWithExtraRows(query.Message, i18n.Tf("delwarn.notice", adminName), []string{"delwarn_"}, [][]telegram.InlineButton{notbadRow})
}
