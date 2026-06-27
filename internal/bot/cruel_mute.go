// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "gennadium/internal/telegram"

	"gennadium/internal/database"
	"gennadium/internal/i18n"
	"gennadium/internal/telegram"
)

// handleCruelMuteIfActive checks whether the message author has an active
// "cruel mute" in this chat. If so, the bot deletes the message and returns
// true; otherwise it returns false and processing continues normally.
//
// A cruel mute means the user is NOT restricted via the Telegram API, but every
// message they send is auto-deleted on arrival.
func (b *Bot) handleCruelMuteIfActive(message *tgbotapi.Message) bool {
	if message == nil || message.From == nil {
		return false
	}
	info, err := b.db.GetActiveMuteInfo(message.From.ID, message.Chat.ID)
	if err != nil || info == nil || !info.IsCruel {
		return false
	}

	// Log the doomed message before it vanishes into the void (just for fun).
	log.Printf("Cruel-mute: whispering into the void - user %d in chat %d said: %s",
		message.From.ID, message.Chat.ID, describeCruelMutedMessage(message))

	// Delete the offending message.
	if err := b.tg.DeleteMessage(message.Chat.ID, message.MessageID); err != nil {
		log.Printf("Cruel-mute: failed to delete message %d from user %d in chat %d: %v",
			message.MessageID, message.From.ID, message.Chat.ID, err)
	} else {
		log.Printf("Cruel-mute: deleted message %d from user %d in chat %d",
			message.MessageID, message.From.ID, message.Chat.ID)
	}
	return true
}

func describeCruelMutedMessage(message *tgbotapi.Message) string {
	text := message.Text
	if text == "" {
		text = message.Caption
	}

	if len(text) > CruelMuteLogPreviewLen {
		text = text[:CruelMuteLogPreviewLen] + "…"
	}

	return text
}

// buildCruelMuteRow returns a single row of cruel-mute action buttons
// (callback prefix "cmute_" / "cmuteask_") for the given target.
func buildCruelMuteRow(chatID int64, messageID int, targetUserID int64, threadID int) []telegram.InlineButton {
	btns := make([]telegram.InlineButton, 0, len(cruelMuteDurations))
	for _, opt := range cruelMuteDurations {
		btns = append(btns, muteButton(opt, "cmute", "cmuteask", chatID, messageID, targetUserID, threadID))
	}
	return telegram.NewRow(btns...)
}

// applyCruelMute creates a cruel-mute record in the database (no Telegram restriction call).
// Returns the muteUntil time and the username used for the record.
func (b *Bot) applyCruelMute(query *tgbotapi.CallbackQuery, chatID int64, messageID int, userID int64, durationMinutes int) (time.Time, string, error) {
	_, username, _, err := b.getUserInfoFromMessage(chatID, messageID)
	if err != nil || username == "" {
		username = b.getUserDisplayName(userID)
	}

	var muteUntil time.Time
	var reasonText string
	if durationMinutes == 0 {
		muteUntil = time.Now().Add(ForeverMuteDuration)
		reasonText = i18n.T("cmute.reason_forever")
	} else {
		muteUntil = time.Now().Add(time.Duration(durationMinutes) * time.Minute)
		reasonText = i18n.Tf("cmute.reason_minutes", durationMinutes)
	}

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
		IsCruel:   true,
	}

	if err := b.db.AddMutedUserSafely(mutedUser); err != nil {
		return time.Time{}, username, fmt.Errorf("add cruel mute: %w", err)
	}

	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    int64(query.From.ID),
		AdminName:  b.getUserDisplayName(int64(query.From.ID)),
		ActionType: "cmute",
		Duration:   durationMinutes,
		Reason:     reasonText,
		ChatID:     chatID,
		MessageID:  messageID,
		Timestamp:  time.Now(),
	}
	if err := b.db.LogAction(action); err != nil {
		log.Printf("Error logging cruel-mute action: %v", err)
	}

	return muteUntil, username, nil
}

// handleCruelMuteAction handles "cmute_" callback (direct, non-confirmed durations).
func (b *Bot) handleCruelMuteAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 6 {
		log.Printf("Invalid cmute callback data: expected 6 parts, got %d", len(parts))
		return
	}
	duration, ok := parseIntField(parts, 1, "duration", "cmute")
	if !ok {
		return
	}
	ids, ok := parseModCallbackIDs(parts, 2, "cmute")
	if !ok {
		return
	}
	chatID, messageID, userID := ids.ChatID, ids.MessageID, ids.UserID

	if b.checkSelfActionGuard(query, userID, "mute") {
		return
	}

	_, username, err := b.applyCruelMute(query, chatID, messageID, userID, duration)
	if err != nil {
		log.Printf("Cruel-mute failed: %v", err)
		return
	}

	adminDisplayName := b.getUserDisplayName(int64(query.From.ID))
	b.sendToAdminChat(i18n.Tf("cmute.admin_notify", username, duration, adminDisplayName))
	b.promptDeleteUserMessages(query.Message, i18n.Tf("cmute.notice", duration, adminDisplayName), chatID, userID)
}

// handleCruelMuteAskConfirmation shows a confirmation dialog for long cruel-mutes (forever).
func (b *Bot) handleCruelMuteAskConfirmation(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 6 {
		log.Printf("Invalid cmuteask callback data: expected 6 parts, got %d", len(parts))
		return
	}
	duration, ok := parseIntField(parts, 1, "duration", "cmuteask")
	if !ok {
		return
	}
	ids, ok := parseModCallbackIDs(parts, 2, "cmuteask")
	if !ok {
		return
	}
	chatID, messageID, userID, threadID := ids.ChatID, ids.MessageID, ids.UserID, ids.ThreadID

	_, username, _, err := b.getUserInfoFromMessage(chatID, messageID)
	if err != nil || username == "" {
		username = b.getUserDisplayName(userID)
	}

	durationText := i18n.T("cmute.duration_forever")

	confirmKeyboard := telegram.NewKeyboard(
		telegram.NewRow(
			telegram.NewButton(i18n.T("btn.confirm_cmute")+" "+durationText,
				fmt.Sprintf("cmuteconfirm_%d_%d_%d_%d_%d", duration, chatID, messageID, userID, threadID)),
		),
		telegram.NewRow(
			telegram.NewButton(i18n.T("btn.cancel"),
				fmt.Sprintf("cmutecancel_%d_%d_%d_%d_%d", duration, chatID, messageID, userID, threadID)),
		),
	)

	confirmText := i18n.Tf("cmute.confirm_dialog", username, durationText)
	if _, err := b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    query.Message.Chat.ID,
		MessageID: query.Message.MessageID,
		Text:      confirmText,
		Keyboard:  &confirmKeyboard,
	}); err != nil {
		log.Printf("Error editing message for cruel-mute confirmation: %v", err)
	}
}

// handleCruelMuteConfirmed handles confirmed long cruel-mutes (forever).
func (b *Bot) handleCruelMuteConfirmed(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 6 {
		log.Printf("Invalid cmuteconfirm callback data: expected 6 parts, got %d", len(parts))
		return
	}
	duration, ok := parseIntField(parts, 1, "duration", "cmuteconfirm")
	if !ok {
		return
	}
	ids, ok := parseModCallbackIDs(parts, 2, "cmuteconfirm")
	if !ok {
		return
	}
	chatID, messageID, userID := ids.ChatID, ids.MessageID, ids.UserID

	if b.checkSelfActionGuard(query, userID, "mute") {
		return
	}

	_, username, err := b.applyCruelMute(query, chatID, messageID, userID, duration)
	if err != nil {
		log.Printf("Cruel-mute failed: %v", err)
		return
	}

	adminDisplayName := b.getUserDisplayName(int64(query.From.ID))
	durationText := strings.ToLower(i18n.T("cmute.duration_forever"))
	b.sendToAdminChat(i18n.Tf("cmute.admin_notify_forever", username, durationText, adminDisplayName))
	b.promptDeleteUserMessages(query.Message, i18n.Tf("cmute.notice_forever", durationText, adminDisplayName), chatID, userID)
}

// handleCruelMuteCancelled handles cancellation of the cruel-mute confirmation dialog.
func (b *Bot) handleCruelMuteCancelled(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 6 {
		return
	}
	// parts[1] is the duration field; not needed for cancel.
	ids, ok := parseModCallbackIDs(parts, 2, "cmutecancel")
	if !ok {
		return
	}
	chatID, messageID, userID, threadID := ids.ChatID, ids.MessageID, ids.UserID, ids.ThreadID

	// Restore the original moderation keyboard.
	kb := b.createModerationKeyboard(chatID, messageID, userID, threadID, false)
	if _, err := b.tg.EditMessageReplyMarkup(telegram.EditMessageReplyMarkupParams{
		ChatID:    query.Message.Chat.ID,
		MessageID: query.Message.MessageID,
		Keyboard:  &kb,
	}); err != nil {
		log.Printf("Error restoring keyboard after cruel-mute cancel: %v", err)
	}
}

// offerCruelMuteFallback sends a follow-up admin notice with a cruel-mute
// keyboard when a normal mute attempt failed (e.g. the bot lacks restrict
// rights in the target chat or the user is an admin).
func (b *Bot) offerCruelMuteFallback(query *tgbotapi.CallbackQuery, chatID int64, messageID int, userID int64, threadID int, muteErr error, username string) {
	if b.config.Admin.ChatID == 0 {
		return
	}
	kb := telegram.NewKeyboard(buildCruelMuteRow(chatID, messageID, userID, threadID))
	sent, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           b.config.Admin.ChatID,
		Text:             i18n.Tf("cmute.offer_after_failure", username, muteErr.Error()),
		Keyboard:         &kb,
		ReplyToMessageID: b.firstAdminReplyID(),
	})
	if err != nil {
		log.Printf("Error sending cruel-mute fallback offer: %v", err)
		return
	}
	if err := b.db.AddMessageForDeletion(sent.MessageID, sent.Chat.ID); err != nil {
		log.Printf("Error adding cruel-mute fallback offer to deletion queue: %v", err)
	}
}
