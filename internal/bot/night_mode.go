// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"log"
	"strings"
	"time"

	"gennadium/internal/database"
	"gennadium/internal/i18n"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
)

// nightModeKey identifies a user in a chat for night-mode flood counting.
type nightModeKey struct {
	chatID int64
	userID int64
}

// handleNightModeMessage enforces night mode for a single incoming message: it
// posts a short "quiet hours" notice as a reply, deletes the user's message, and
// schedules removal of the notice after the configured delay. When muted is set
// (the message crossed the flood threshold and the user was just muted) a
// distinct mute notice is used instead. The message is neither recorded nor
// analyzed.
func (b *Bot) handleNightModeMessage(message *tgbotapi.Message, muted bool) {
	notice := strings.TrimSpace(b.config.NightMode.Message)
	if notice == "" {
		notice = i18n.T("night_mode.default_message")
	}
	if muted {
		notice = strings.TrimSpace(b.config.NightMode.MutedMessage)
		if notice == "" {
			notice = i18n.T("night_mode.muted_message")
		}
	}

	// Reply first, while the target message still exists, so the notice threads
	// to the user's message; then delete the message to restore silence.
	sent, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           message.Chat.ID,
		Text:             notice,
		ReplyToMessageID: message.MessageID,
		MessageThreadID:  messageTopic(message),
	})

	if delErr := b.tg.DeleteMessage(message.Chat.ID, message.MessageID); delErr != nil {
		log.Printf("Night mode: failed to delete message %d in chat %d: %v", message.MessageID, message.Chat.ID, delErr)
	} else {
		log.Printf("Night mode: deleted message %d in chat %d", message.MessageID, message.Chat.ID)
	}

	if err != nil {
		log.Printf("Night mode: failed to send notice in chat %d: %v", message.Chat.ID, err)
		return
	}

	delay := time.Duration(b.nightModeReplyDeleteSeconds()) * time.Second
	b.deleteMessageAfter(message.Chat.ID, sent.MessageID, delay)
}

// registerNightModeMessage records that a user posted during the active
// night-mode window and reports whether this message pushed them over the
// night_mode.mute_after_messages threshold. When it does, the user is muted
// until the window ends and true is returned - only for the single triggering
// message. Returns false when the feature is off or the message has no sender.
func (b *Bot) registerNightModeMessage(message *tgbotapi.Message, now time.Time) bool {
	threshold := b.config.NightMode.MuteAfterMessages
	if threshold <= 0 || message.From == nil {
		return false
	}
	windowEnd := b.config.NightModeWindowEnd(now)
	if windowEnd.IsZero() {
		return false
	}

	key := nightModeKey{chatID: message.Chat.ID, userID: message.From.ID}
	b.nightMuteMu.Lock()
	// A new window (different end time) invalidates every prior count.
	if b.nightMuteCounts == nil || !b.nightMuteWindow.Equal(windowEnd) {
		b.nightMuteCounts = make(map[nightModeKey]int)
		b.nightMuteWindow = windowEnd
	}
	b.nightMuteCounts[key]++
	count := b.nightMuteCounts[key]
	b.nightMuteMu.Unlock()

	// Fire exactly once, on the message that first exceeds the threshold.
	if count != threshold+1 {
		return false
	}
	b.applyNightModeMute(message, windowEnd)
	return true
}

// applyNightModeMute mutes a user until until, recording the mute, applying the
// Telegram restriction and logging the action. It is best-effort: failures are
// logged and, on a Telegram rejection, the DB record is rolled back.
func (b *Bot) applyNightModeMute(message *tgbotapi.Message, until time.Time) {
	userID := message.From.ID
	chatID := message.Chat.ID
	username := message.From.Username
	if username == "" {
		username = b.getUserDisplayName(userID)
	}
	reason := i18n.T("night_mode.mute_reason")

	mutedUser := &database.MutedUser{
		UserID:    userID,
		Username:  username,
		ChatID:    chatID,
		MutedBy:   b.botSelf.ID,
		MutedAt:   time.Now(),
		UnmuteAt:  until,
		Reason:    reason,
		IsActive:  true,
		MessageID: message.MessageID,
	}
	if err := b.db.AddMutedUserSafely(mutedUser); err != nil {
		log.Printf("Night mode: failed to store mute for user %d in chat %d: %v", userID, chatID, err)
		return
	}
	if _, err := b.restrictUserInChats(userID, chatID, until.Unix()); err != nil {
		log.Printf("Night mode: Telegram refused to mute user %d in chat %d: %v", userID, chatID, err)
		if unErr := b.db.UnmuteUser(userID, chatID); unErr != nil {
			log.Printf("Night mode: failed to roll back mute record for user %d: %v", userID, unErr)
		}
		return
	}
	log.Printf("Night mode: muted user %d (%s) in chat %d until %s (flood threshold exceeded)",
		userID, username, chatID, until.Format(time.RFC3339))

	action := &database.Action{
		UserID:     userID,
		Username:   username,
		AdminID:    b.botSelf.ID,
		AdminName:  b.botSelf.Username,
		ActionType: "mute",
		Duration:   int(time.Until(until).Minutes()),
		Reason:     reason,
		ChatID:     chatID,
		MessageID:  message.MessageID,
		Timestamp:  time.Now(),
	}
	if err := b.db.LogAction(action); err != nil {
		log.Printf("Night mode: error logging mute action: %v", err)
	}
}

// nightModeReplyDeleteSeconds returns the configured notice lifetime, falling
// back to the default when unset or non-positive.
func (b *Bot) nightModeReplyDeleteSeconds() int {
	if s := b.config.NightMode.ReplyDeleteSeconds; s > 0 {
		return s
	}
	return DefaultNightModeReplyDeleteSeconds
}

// deleteMessageAfter removes a message after delay in a background goroutine.
func (b *Bot) deleteMessageAfter(chatID int64, messageID int, delay time.Duration) {
	if messageID == 0 {
		return
	}
	go func() {
		time.Sleep(delay)
		if err := b.deleteMessageWithRetry(chatID, messageID); err != nil {
			log.Printf("Night mode: failed to delete notice %d in chat %d: %v", messageID, chatID, err)
		}
	}()
}
