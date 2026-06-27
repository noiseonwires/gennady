// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"log"
	"strconv"

	tgbotapi "gennadium/internal/telegram"

	"gennadium/internal/telegram"
)

// modCallbackIDs is the standard tuple embedded at the tail of moderation
// callback data ("<action>[_<extra>...]_<chatID>_<messageID>_<userID>_<threadID>").
type modCallbackIDs struct {
	ChatID    int64
	MessageID int
	UserID    int64
	ThreadID  int
}

// parseModCallbackIDs reads the four positional fields starting at parts[fromIdx]:
//
//	parts[fromIdx]   chatID    (int64)
//	parts[fromIdx+1] messageID (int)
//	parts[fromIdx+2] userID    (int64)
//	parts[fromIdx+3] threadID  (int)
//
// On parse error it logs and returns ok=false so the caller can return early.
// `action` is just a label used in log messages (e.g. "warn", "mute", "delete").
func parseModCallbackIDs(parts []string, fromIdx int, action string) (modCallbackIDs, bool) {
	if len(parts) < fromIdx+4 {
		log.Printf("Invalid %s callback data: expected at least %d parts, got %d", action, fromIdx+4, len(parts))
		return modCallbackIDs{}, false
	}
	chatID, err := strconv.ParseInt(parts[fromIdx], 10, 64)
	if err != nil {
		log.Printf("Error parsing chatID for %s: %v", action, err)
		return modCallbackIDs{}, false
	}
	messageID, err := strconv.Atoi(parts[fromIdx+1])
	if err != nil {
		log.Printf("Error parsing messageID for %s: %v", action, err)
		return modCallbackIDs{}, false
	}
	userID, err := strconv.ParseInt(parts[fromIdx+2], 10, 64)
	if err != nil {
		log.Printf("Error parsing userID for %s: %v", action, err)
		return modCallbackIDs{}, false
	}
	threadID, err := strconv.Atoi(parts[fromIdx+3])
	if err != nil {
		log.Printf("Error parsing threadID for %s: %v", action, err)
		return modCallbackIDs{}, false
	}
	return modCallbackIDs{ChatID: chatID, MessageID: messageID, UserID: userID, ThreadID: threadID}, true
}

// parseIntField is a small wrapper for parsing one named field of a callback
// (used for the duration prefix in mute / cmute callbacks).
func parseIntField(parts []string, idx int, name, action string) (int, bool) {
	if idx >= len(parts) {
		log.Printf("Invalid %s callback data: missing %s at index %d", action, name, idx)
		return 0, false
	}
	v, err := strconv.Atoi(parts[idx])
	if err != nil {
		log.Printf("Error parsing %s for %s: %v", name, action, err)
		return 0, false
	}
	return v, true
}

// checkSelfActionGuard verifies the admin is not trying to act against the bot
// itself. If they are, it sends a sarcastic response to the admin chat and
// returns true; the caller should `return` immediately.
func (b *Bot) checkSelfActionGuard(query *tgbotapi.CallbackQuery, userID int64, actionName string) bool {
	if userID != b.botSelf.ID {
		return false
	}
	log.Printf("Admin %d tried to %s the bot itself - sending sarcastic response", query.From.ID, actionName)
	adminName := b.getUserDisplayName(int64(query.From.ID))
	b.sendToAdminChat(b.generateSarcasticSelfDefense(adminName, actionName))
	return true
}

// sendActionConfirmation posts a confirmation message to the chat where the
// moderation action was initiated, when that chat is different from the admin
// chat (i.e. private chat or another moderation chat - usually the case when
// the moderator is using the bot from a private chat for ergonomics).
//
// If the destination is a moderation chat, the confirmation is also queued for
// later automatic cleanup so it doesn't accumulate.
//
// `actionTag` is used only in log messages (e.g. "warn", "mute", "delete").
func (b *Bot) sendActionConfirmation(query *tgbotapi.CallbackQuery, text, actionTag string) {
	if query.Message == nil {
		return
	}
	if query.Message.Chat.Type != "private" && query.Message.Chat.ID == b.config.Admin.ChatID {
		return
	}
	sent, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID: query.Message.Chat.ID,
		Text:   text,
	})
	if err != nil {
		log.Printf("Error sending %s confirmation to private chat: %v", actionTag, err)
		return
	}
	if b.config.IsModerationChat(query.Message.Chat.ID) {
		if err := b.db.AddMessageForDeletion(sent.MessageID, sent.Chat.ID); err != nil {
			log.Printf("Error adding %s confirmation to deletion queue: %v", actionTag, err)
		}
	}
}
