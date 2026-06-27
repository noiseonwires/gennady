// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"gennadium/internal/i18n"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
)

// messagePurgeDeleteDelay throttles bulk message deletions so we stay clear of
// Telegram's rate limits when purging a user's recent messages.
const messagePurgeDeleteDelay = 250 * time.Millisecond

// purgePeriodSince maps a deletion-prompt period token ("1h" / "1d" / "all")
// to the lower bound timestamp to purge from. A zero time.Time means "all".
// ok is false for unknown tokens (including "nope").
func purgePeriodSince(period string) (time.Time, bool) {
	switch period {
	case "1h":
		return time.Now().Add(-time.Hour), true
	case "1d":
		return time.Now().Add(-24 * time.Hour), true
	case "all":
		return time.Time{}, true
	default:
		return time.Time{}, false
	}
}

// deleteUserMessagesPromptKeyboard builds the "Delete this user's messages?"
// prompt keyboard. Each button carries callback data of the form
// "delmsg_<period>_<chatID>_<userID>".
func deleteUserMessagesPromptKeyboard(chatID, userID int64) telegram.InlineKeyboard {
	btn := func(labelKey, period string) telegram.InlineButton {
		return telegram.NewButton(
			i18n.T(labelKey),
			fmt.Sprintf("delmsg_%s_%d_%d", period, chatID, userID),
		)
	}
	return telegram.NewKeyboard(
		telegram.NewRow(
			btn("delmsg.btn_1h", "1h"),
			btn("delmsg.btn_1d", "1d"),
		),
		telegram.NewRow(
			btn("delmsg.btn_all", "all"),
			btn("delmsg.btn_nope", "nope"),
		),
	)
}

// promptDeleteUserMessages appends a notice to the moderation message, removes
// the (now obsolete) mute buttons, and shows the "Delete this user's messages?"
// prompt with time-window options.
func (b *Bot) promptDeleteUserMessages(message *tgbotapi.Message, notice string, chatID, userID int64) {
	if message == nil {
		return
	}
	newText := message.Text + "\n" + notice + "\n" + i18n.T("delmsg.prompt")
	newText = truncateMessage(newText, MaxTelegramMessageLength)

	keyboard := deleteUserMessagesPromptKeyboard(chatID, userID)
	if _, err := b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    message.Chat.ID,
		MessageID: message.MessageID,
		Text:      newText,
		Keyboard:  &keyboard,
	}); err != nil {
		log.Printf("Error showing delete-messages prompt: %v", err)
	}
}

// deleteUserMessagesSince deletes every message the user sent in the given chat
// at or after `since` from the chat (Telegram). The local message_info records
// are left intact (mirroring the single-message delete action); only the
// pending-deletion queue entry is cleared. Each successfully deleted message is
// logged as a "delete" action so the purge leaves an audit trail and the
// removed messages can be marked as deleted in later creative-reply context. It
// returns the number of messages successfully deleted from Telegram.
func (b *Bot) deleteUserMessagesSince(userID, chatID int64, since time.Time) int {
	ids, err := b.db.GetUserMessageIDsSince(userID, chatID, since)
	if err != nil {
		log.Printf("delmsg: error fetching messages for user %d in chat %d: %v", userID, chatID, err)
		return 0
	}

	// Reused per-message so the bulk purge leaves the same per-message "delete"
	// audit trail as single deletes; this is what later marks purged messages as
	// deleted in creative-reply context.
	actionCtx := &webModContext{
		userID:    userID,
		chatID:    chatID,
		username:  b.getUserDisplayName(userID),
		adminID:   b.config.Admin.SuperAdminUserID,
		adminName: b.getUserDisplayName(b.config.Admin.SuperAdminUserID),
	}

	deleted := 0
	for _, messageID := range ids {
		if err := b.tg.DeleteMessage(chatID, messageID); err != nil {
			log.Printf("delmsg: failed to delete message %d in chat %d: %v", messageID, chatID, err)
		} else {
			deleted++
			actionCtx.messageID = messageID
			b.logAction(actionCtx, "delete", i18n.T("delmsg.action_reason"), 0)
		}
		if err := b.db.RemoveMessageFromDeletion(messageID, chatID); err != nil {
			log.Printf("delmsg: failed to remove message %d from deletion queue: %v", messageID, chatID)
		}
		time.Sleep(messagePurgeDeleteDelay)
	}

	log.Printf("delmsg: deleted %d/%d messages from user %d in chat %d", deleted, len(ids), userID, chatID)
	return deleted
}

// handleDeleteUserMessagesAction handles the "delmsg_" callback emitted by the
// post-mute "Delete this user's messages?" prompt.
//
// Callback data: "delmsg_<period>_<chatID>_<userID>" where period is one of
// "1h", "1d", "all" or "nope".
func (b *Bot) handleDeleteUserMessagesAction(query *tgbotapi.CallbackQuery, parts []string) {
	if len(parts) != 4 {
		log.Printf("Invalid delmsg callback data: expected 4 parts, got %d", len(parts))
		return
	}
	period := parts[1]
	chatID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		log.Printf("Error parsing chatID for delmsg: %v", err)
		return
	}
	userID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		log.Printf("Error parsing userID for delmsg: %v", err)
		return
	}

	if period == "nope" {
		b.appendActionNotice(query.Message, i18n.T("delmsg.declined"), []string{"delmsg_"})
		return
	}

	since, ok := purgePeriodSince(period)
	if !ok {
		log.Printf("Invalid delmsg period: %q", period)
		return
	}

	count := b.deleteUserMessagesSince(userID, chatID, since)
	b.appendActionNotice(query.Message, i18n.Tf("delmsg.done", count), []string{"delmsg_"})
}
