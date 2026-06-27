// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"gennadium/internal/i18n"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
)

// sendAdminMenu sends the admin menu with keyboard options.
func (b *Bot) sendAdminMenu(message *tgbotapi.Message) {
	keyboard := telegram.NewKeyboard(
		telegram.NewRow(telegram.NewButton(i18n.T("btn.view_muted"), "admin_muted_users")),
		telegram.NewRow(telegram.NewButton(i18n.T("btn.view_actions"), "admin_last_actions")),
		telegram.NewRow(telegram.NewButton(i18n.T("btn.top10"), "admin_top10_bad_users")),
		telegram.NewRow(telegram.NewButton(i18n.T("btn.punish"), "admin_punish")),
		telegram.NewRow(telegram.NewButton(i18n.T("btn.about"), "admin_about")),
	)

	menuText := i18n.Tf("menu.admin_text", BotName)

	replyTo := 0
	if message.Chat.Type != "private" && message.Chat.ID == b.config.Admin.ChatID {
		replyTo = b.firstAdminReplyID()
	}

	if _, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           message.Chat.ID,
		Text:             menuText,
		Keyboard:         &keyboard,
		ReplyToMessageID: replyTo,
	}); err != nil {
		log.Printf("Error sending admin menu: %v", err)
	}
}

// handleSuperAdminCommand handles Super admin commands for silent moderation.
func (b *Bot) handleSuperAdminCommand(message *tgbotapi.Message) bool {
	command := message.Command()
	args := strings.Fields(message.CommandArguments())
	b.debugf("Direct moderator command: admin_id=%d command=%q args=%q chat_id=%d message_id=%d", message.From.ID, command, strings.Join(args, " "), message.Chat.ID, message.MessageID)

	replyOK := func() {
		b.tg.SendMessage(telegram.SendMessageParams{
			ChatID:           message.Chat.ID,
			Text:             "OK",
			ReplyToMessageID: message.MessageID,
		})
	}

	getTargetChatID := func(argIndex int) int64 {
		if len(args) > argIndex {
			if chatID, err := strconv.ParseInt(args[argIndex], 10, 64); err == nil {
				if b.config.IsModerationChat(chatID) {
					return chatID
				}
				log.Printf("Super admin: chat_id %d is not a moderation chat, using first", chatID)
			}
		}
		return b.config.GetFirstModerationChatID()
	}

	// Helper to parse message ID or link from first argument
	parseMessageTarget := func() (messageID int, targetChatID int64, ok bool) {
		if len(args) < 1 {
			return 0, 0, false
		}
		if linkMsgID, linkChatID, _, linkOK := parseTelegramMessageLink(args[0]); linkOK {
			if !b.config.IsModerationChat(linkChatID) {
				log.Printf("Super admin: chat_id %d from link is not a moderation chat", linkChatID)
				return 0, 0, false
			}
			return linkMsgID, linkChatID, true
		}
		msgID, err := strconv.Atoi(args[0])
		if err != nil {
			log.Printf("Super admin: invalid message_id or link: %v", err)
			return 0, 0, false
		}
		return msgID, getTargetChatID(1), true
	}

	// Helper to parse user ID from message link or direct ID
	parseUserTarget := func() (userID int64, targetChatID int64, ok bool) {
		if len(args) < 1 {
			return 0, 0, false
		}
		if linkMsgID, linkChatID, _, linkOK := parseTelegramMessageLink(args[0]); linkOK {
			if !b.config.IsModerationChat(linkChatID) {
				log.Printf("Super admin: chat_id %d from link is not a moderation chat", linkChatID)
				return 0, 0, false
			}
			msgInfo, err := b.db.GetMessageInfo(linkMsgID, linkChatID)
			if err != nil {
				log.Printf("Super admin: could not find message from link in database: %v", err)
				return 0, 0, false
			}
			return msgInfo.UserID, linkChatID, true
		}
		uid, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			log.Printf("Super admin: invalid user_id or link: %v", err)
			return 0, 0, false
		}
		return uid, getTargetChatID(1), true
	}

	switch command {
	case "delete":
		messageID, targetChatID, ok := parseMessageTarget()
		if !ok {
			log.Printf("Super admin: /delete command requires message_id or message link argument")
			return true
		}
		log.Printf("Super admin %d: deleting message %d from chat %d", message.From.ID, messageID, targetChatID)
		if err := b.tg.DeleteMessage(targetChatID, messageID); err != nil {
			log.Printf("Super admin: error deleting message: %v", err)
		} else {
			replyOK()
		}
		return true

	case "forget":
		messageID, targetChatID, ok := parseMessageTarget()
		if !ok {
			log.Printf("Super admin: /forget command requires message_id or message link argument")
			return true
		}
		log.Printf("Super admin %d: forgetting message %d from chat %d (removing from message_info)", message.From.ID, messageID, targetChatID)
		if err := b.db.DeleteMessageInfo(messageID, targetChatID); err != nil {
			log.Printf("Super admin: error forgetting message: %v", err)
		} else {
			replyOK()
		}
		return true

	case "mute":
		userID, targetChatID, ok := parseUserTarget()
		if !ok {
			log.Printf("Super admin: /mute command requires user_id or message link argument")
			return true
		}
		log.Printf("Super admin %d: muting user %d in chat %d", message.From.ID, userID, targetChatID)
		b.restrictUserInChats(userID, targetChatID, 0) //nolint:errcheck // admin command, errors are logged inside
		replyOK()
		return true

	case "unmute":
		userID, targetChatID, ok := parseUserTarget()
		if !ok {
			log.Printf("Super admin: /unmute command requires user_id or message link argument")
			return true
		}
		log.Printf("Super admin %d: unmuting user %d in chat %d", message.From.ID, userID, targetChatID)
		b.unrestrictUserInChats(userID, targetChatID)
		replyOK()
		return true

	case "block":
		userID, targetChatID, ok := parseUserTarget()
		if !ok {
			log.Printf("Super admin: /block command requires user_id or message link argument")
			return true
		}
		log.Printf("Super admin %d: blocking user %d from chat %d", message.From.ID, userID, targetChatID)
		if err := b.tg.BanChatMember(targetChatID, userID); err != nil {
			log.Printf("Super admin: error blocking user: %v", err)
		} else {
			replyOK()
		}
		return true

	case "unblock":
		userID, targetChatID, ok := parseUserTarget()
		if !ok {
			log.Printf("Super admin: /unblock command requires user_id or message link argument")
			return true
		}
		log.Printf("Super admin %d: unblocking user %d from chat %d", message.From.ID, userID, targetChatID)
		if err := b.tg.UnbanChatMember(targetChatID, userID, true); err != nil {
			log.Printf("Super admin: error unblocking user: %v", err)
		} else {
			replyOK()
		}
		return true

	case "edit":
		if len(args) < 2 {
			log.Printf("Super admin: /edit command requires message_id (or link) and new_text arguments")
			return true
		}
		var messageID int
		var targetChatID int64
		rawArgs := message.CommandArguments()
		rawArgs = strings.TrimSpace(rawArgs)
		rawArgs = rawArgs[len(args[0]):]
		rawArgs = strings.TrimLeft(rawArgs, " \t")

		if linkMsgID, linkChatID, _, ok := parseTelegramMessageLink(args[0]); ok {
			messageID = linkMsgID
			targetChatID = linkChatID
			if !b.config.IsModerationChat(targetChatID) {
				log.Printf("Super admin: chat_id %d from link is not a moderation chat", targetChatID)
				return true
			}
		} else {
			msgID, err := strconv.Atoi(args[0])
			if err != nil {
				log.Printf("Super admin: invalid message_id or link: %v", err)
				return true
			}
			messageID = msgID
			if len(args) > 2 {
				if chatID, err := strconv.ParseInt(args[1], 10, 64); err == nil && b.config.IsModerationChat(chatID) {
					targetChatID = chatID
					rawArgs = rawArgs[len(args[1]):]
					rawArgs = strings.TrimLeft(rawArgs, " \t")
				} else {
					targetChatID = b.config.GetFirstModerationChatID()
				}
			} else {
				targetChatID = b.config.GetFirstModerationChatID()
			}
		}
		newText := rawArgs
		if newText == "" {
			log.Printf("Super admin: /edit command requires new_text")
			return true
		}
		log.Printf("Super admin %d: editing message %d in chat %d", message.From.ID, messageID, targetChatID)

		if _, err := b.tg.EditMessageText(telegram.EditMessageTextParams{
			ChatID:    targetChatID,
			MessageID: messageID,
			Text:      newText,
		}); err != nil {
			log.Printf("Super admin: error editing message: %v", err)
		} else {
			replyOK()
		}
		return true

	case "get":
		messageID, targetChatID, ok := parseMessageTarget()
		if !ok {
			log.Printf("Super admin: /get command requires message_id or message link argument")
			return true
		}
		log.Printf("Super admin %d: getting message %d from chat %d", message.From.ID, messageID, targetChatID)
		msgInfo, err := b.db.GetMessageInfo(messageID, targetChatID)
		if err != nil {
			log.Printf("Super admin: message not found in database: %v", err)
			b.tg.SendMessage(telegram.SendMessageParams{
				ChatID:           message.Chat.ID,
				Text:             "❌ Message not found in database.",
				ReplyToMessageID: message.MessageID,
			})
			return true
		}

		// Try to serialize full info as JSON for debugging
		infoJSON, jsonErr := json.MarshalIndent(msgInfo, "", "  ")

		replyText := msgInfo.Text
		if replyText == "" {
			replyText = "[empty message]"
		}
		if msgInfo.ExtraInfo != "" {
			replyText += "\n\n📎 Extra info:\n" + msgInfo.ExtraInfo
		}
		if jsonErr == nil {
			replyText += "\n\n📋 Full info:\n" + string(infoJSON)
		}
		b.tg.SendMessage(telegram.SendMessageParams{
			ChatID:           message.Chat.ID,
			Text:             replyText,
			ReplyToMessageID: message.MessageID,
		})
		return true

	default:
		return false
	}
}
