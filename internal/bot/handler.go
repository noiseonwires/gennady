// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gennadium/internal/database"
	"gennadium/internal/i18n"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
)

// handleMessage processes incoming messages.
func (b *Bot) handleMessage(message *tgbotapi.Message, isEdited bool) {
	// Store bot's own messages for reply chain context
	if message.From != nil && message.From.ID == b.botSelf.ID {
		b.storeBotMessageInfo(neutralInboundMessage(message))
		return
	}

	// Handle super admin commands first
	if b.config.Admin.SuperAdminUserID != 0 && message.From != nil && message.From.ID == b.config.Admin.SuperAdminUserID {
		if message.IsCommand() {
			if b.handleSuperAdminCommand(message) {
				return
			}
		}
	}

	// Handle private messages from admins
	if message.Chat.Type == "private" {
		b.handlePrivateMessage(message)
		return
	}

	// Check if bot is mentioned
	if b.isBotMentioned(message) {
		b.handleBotMention(message)
	}

	// Handle admin chat
	if message.Chat.ID == b.config.Admin.ChatID {
		if b.config.Debug.DumpAdminMessages {
			b.dumpMessageToFile(message, "admin", isEdited)
		}

		if message.ReplyToMessage != nil && message.ReplyToMessage.From.ID == b.botSelf.ID && b.isMessageLink(message.Text) {
			b.handleMessageLinkForModeration(message)
			return
		}

		if message.ReplyToMessage != nil && b.config.IsAdminReplyMessage(message.ReplyToMessage.MessageID) {
			if message.IsCommand() {
				b.handleCommand(message)
				return
			}
		}
	}

	// Process messages in moderation chat: run the ordered ingest pipeline
	// (dump → cruel-mute → prepare-deletion → enhance → moderate → summary →
	// links-and-creative → finalize-deletion). See pipeline.go.
	if b.config.IsModerationChat(message.Chat.ID) {
		b.runModerationPipeline(b.newInboundContext(message, isEdited))
	}
}

// isBotMentioned checks if the bot is mentioned in the message.
func (b *Bot) isBotMentioned(message *tgbotapi.Message) bool {
	if message.Chat.Type == "private" {
		return false
	}

	mention := "@" + b.botSelf.Username

	if message.Entities != nil {
		for _, entity := range message.Entities {
			if entity.Type == "mention" {
				mentionText := message.Text[entity.Offset : entity.Offset+entity.Length]
				if mentionText == mention {
					return true
				}
			}
		}
	}

	// Match the @username in the message text or in a media caption. Photos,
	// videos and other media carry their text in Caption, and the inbound
	// adapter does not surface caption entities, so a substring check is the
	// only way to detect a mention written in a caption.
	if strings.Contains(message.Text, mention) || strings.Contains(message.Caption, mention) {
		return true
	}

	return false
}

// handleBotMention handles different scenarios when bot is mentioned.
func (b *Bot) handleBotMention(message *tgbotapi.Message) {
	if message.Chat.ID == b.config.Admin.ChatID && (message.Command() == "start" || message.Command() == "help") {
		b.sendAdminMenu(message)
		return
	}

	if message.Chat.ID == b.config.Admin.ChatID && message.ReplyToMessage != nil &&
		b.config.IsAdminReplyMessage(message.ReplyToMessage.MessageID) {
		b.sendAdminMenu(message)
		return
	}

	if b.config.IsModerationChat(message.Chat.ID) && message.ReplyToMessage != nil {
		// A mention replying to one of the bot's OWN messages is conversation
		// aimed at the bot (handled by the creative-reply path), not a moderation
		// complaint about another user's message. Don't route it to moderation -
		// fall through so the creative reply can answer it.
		replyTargetIsBot := message.ReplyToMessage.From != nil &&
			message.ReplyToMessage.From.ID == b.botSelf.ID
		if !replyTargetIsBot {
			if message.ReplyToMessage.Text != "" || message.ForwardFrom != nil {
				log.Printf("Bot mentioned as reply to message with text (ID: %d) - triggering moderation", message.ReplyToMessage.MessageID)

				err := b.db.AddMessageForDeletion(message.MessageID, message.Chat.ID)
				if err != nil {
					log.Printf("Error adding bot mention message to deletion queue: %v", err)
				} else {
					log.Printf("Added bot mention message %d to deletion queue", message.MessageID)
				}

				b.handleReplyModerationTrigger(message)
				return
			}
			log.Printf("🔍 Bot mentioned as reply to message without text content:")
			log.Printf("   📝 Reply to Message ID: %d", message.ReplyToMessage.MessageID)
			log.Printf("   💬 Chat ID: %d", message.ReplyToMessage.Chat.ID)
			log.Printf("   👤 Original sender: %s (ID: %d)", message.ReplyToMessage.From.Username, message.ReplyToMessage.From.ID)
			log.Printf("   📄 Message type: service message or topic root - ignoring")
			return
		}
	}

	if b.config.IsModerationChat(message.Chat.ID) && !b.isUserAdmin(message.From.ID) {
		return
	}

	log.Printf("Bot mentioned in chat %d by user %d", message.Chat.ID, message.From.ID)
}

// handlePrivateMessage handles private messages from users.
func (b *Bot) handlePrivateMessage(message *tgbotapi.Message) {
	if !b.isUserAdmin(message.From.ID) {
		aboutText := "👋 " + b.buildAboutText()
		if _, err := b.tg.SendMessage(telegram.SendMessageParams{
			ChatID:    message.Chat.ID,
			Text:      aboutText,
			ParseMode: telegram.ParseModeMarkdown,
		}); err != nil {
			log.Printf("Error sending non-admin response: %v", err)
		}
		return
	} else {
		if b.isMessageLink(message.Text) {
			b.handleMessageLinkForModeration(message)
			return
		}
	}

	if message.IsCommand() {
		switch message.Command() {
		case "start", "help":
			b.sendAdminMenu(message)
		default:
			if _, err := b.tg.SendMessage(telegram.SendMessageParams{
				ChatID: message.Chat.ID,
				Text:   i18n.T("cmd.unknown"),
			}); err != nil {
				log.Printf("Error sending unknown command response: %v", err)
			}
		}
		return
	}

	if _, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID: message.Chat.ID,
		Text:   i18n.T("cmd.default_private"),
	}); err != nil {
		log.Printf("Error sending default private response: %v", err)
	}
}

// isServiceMessage checks if a message is a service message.
func (b *Bot) isServiceMessage(message *tgbotapi.Message) bool {
	return message.NewChatMembers != nil ||
		message.LeftChatMember != nil ||
		message.NewChatTitle != "" ||
		message.NewChatPhoto ||
		message.DeleteChatPhoto ||
		message.GroupChatCreated ||
		message.SuperGroupChatCreated ||
		message.ChannelChatCreated ||
		message.MigrateToChatID != 0 ||
		message.MigrateFromChatID != 0 ||
		message.PinnedMessage != nil ||
		message.Invoice ||
		message.SuccessfulPayment ||
		message.ConnectedWebsite != "" ||
		message.PassportData ||
		message.ProximityAlertTriggered ||
		message.ForumTopicCreated != nil ||
		message.ForumTopicEdited != nil
}

// handleCommand handles bot commands.
func (b *Bot) handleCommand(message *tgbotapi.Message) {
	if message.Chat.Type == "group" || message.Chat.Type == "supergroup" {
		if !b.isBotMentioned(message) {
			log.Printf("Ignoring command '%s' in public group %d - bot not mentioned", message.Command(), message.Chat.ID)
			return
		}
	}

	switch message.Command() {
	case "help":
		b.tg.SendMessage(telegram.SendMessageParams{
			ChatID:           message.Chat.ID,
			Text:             "This is gennady. Commands will be implemented.",
			ReplyToMessageID: message.MessageID,
		})
	}
}

// handleCreativeFollowUp handles follow-up replies when user responds to bot's creative reply.
func (b *Bot) handleCreativeFollowUp(message *tgbotapi.Message) {
	log.Printf("User %d triggered creative reply (reply or mention), checking limits", message.From.ID)

	if b.config.AI.CreativeReplies.FollowUpOnlySameUser && message.ReplyToMessage != nil {
		botMsgID := message.ReplyToMessage.MessageID
		if !b.isBotReplyToUser(botMsgID, message.Chat.ID, int64(message.From.ID)) {
			log.Printf("Skipping follow-up: bot message %d was not a reply to user %d", botMsgID, message.From.ID)
			return
		}
	}

	maxMessages := b.config.AI.CreativeReplies.MaxMessages
	timeWindow := b.config.AI.CreativeReplies.TimeWindow
	recentBotMessageCount, err := b.db.GetRecentBotMessageCount(b.botSelf.ID, message.Chat.ID, timeWindow)
	if err != nil {
		log.Printf("Error checking recent bot message count: %v", err)
		return
	}

	if recentBotMessageCount >= maxMessages {
		log.Printf("Skipping follow-up reply: bot has already sent %d messages in the last %d hours (limit: %d)", recentBotMessageCount, timeWindow, maxMessages)
		b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.CreativeReplyLimit)
		log.Printf("Added yawn emoji to message %d (creative reply limit reached)", message.MessageID)
		return
	}

	log.Printf("Generating follow-up response (bot messages in last %d hours: %d/%d)", timeWindow, recentBotMessageCount, maxMessages)

	if strings.TrimSpace(message.Text) == "" {
		log.Printf("Skipping creative follow-up - message text is empty")
		return
	}

	quotedText := b.extractQuotedText(message)
	if quotedText != "" {
		log.Printf("Extracted quoted text from message %d: %s", message.MessageID, quotedText)
	}

	var replyToText *string
	if message.ReplyToMessage != nil && message.ReplyToMessage.Text != "" {
		replyToText = &message.ReplyToMessage.Text
	}

	var reaction string
	reply, err := b.generateCreativeReplyWithQuote(message.Text, quotedText, message.MessageID, int64(message.From.ID), message.Chat.ID, replyToText)
	if err != nil {
		if strings.Contains(err.Error(), "content_filter") {
			log.Printf("⚠️ Content filter triggered during follow-up reply generation - treating as inappropriate content")
			reaction = b.config.Reactions.ContentFilter
		} else {
			log.Printf("Error generating creative follow-up reply: %v", err)
			reaction = b.config.Reactions.CreativeReplyError
		}

		b.setMessageReaction(message.Chat.ID, message.MessageID, reaction)
		return
	}

	reply = strings.Trim(reply, `"`)
	reply = strings.ReplaceAll(reply, "-", "-")

	sentMsg := b.sendToModerationChatWithReply(message.Chat.ID, reply, message.MessageID)
	if sentMsg != nil {
		b.storeBotMessageInfo(sentMsg)
	}

	log.Printf("Sent creative follow-up reply and stored in database for future reply chains")
}

// extractQuotedText extracts quoted text from a message (text within blockquote entities).
func (b *Bot) extractQuotedText(message *tgbotapi.Message) string {
	if message == nil || message.Entities == nil {
		return ""
	}

	var quotedParts []string
	for _, entity := range message.Entities {
		if entity.Type == "blockquote" {
			text := message.Text
			if entity.Offset >= 0 && entity.Offset+entity.Length <= len(text) {
				quotedText := text[entity.Offset : entity.Offset+entity.Length]
				quotedParts = append(quotedParts, strings.TrimSpace(quotedText))
			}
		}
	}

	if len(quotedParts) > 0 {
		return strings.Join(quotedParts, "\n")
	}
	return ""
}

// processMessageEnhancements processes images for immediate analysis.
func (b *Bot) processMessageEnhancements(message *tgbotapi.Message) (string, bool) {
	messageText := message.Text

	if messageText == "" && message.Caption != "" {
		messageText = message.Caption
	}

	visionEnabled := b.config.AI.ContentModeration.VisionEnabled
	ocrSpaceEnabled := b.config.AI.ContentModeration.OCRSpaceEnabled
	contentSafetyFlagged := false
	if (visionEnabled || ocrSpaceEnabled) && message.Photo != nil && len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]

		log.Printf("Processing image in message %d (file_id: %s)", message.MessageID, photo.FileID)

		imageData, err := b.downloadImage(photo.FileID)
		if err != nil {
			log.Printf("Error downloading image: %v", err)
		} else {
			var imageAnalysis string
			var analysisErr error

			if visionEnabled {
				imageAnalysis, contentSafetyFlagged, analysisErr = b.analyzeImageWithVision(imageData)
				if analysisErr != nil {
					log.Printf("Error analyzing image with Vision API: %v", analysisErr)
				}
			}

			// Fallback order: Azure Vision → OCR.space.
			if (analysisErr != nil || !visionEnabled) && ocrSpaceEnabled && imageAnalysis == "" {
				log.Printf("Falling back to OCR.space for image analysis")
				imageAnalysis, analysisErr = b.analyzeImageWithOCRSpace(imageData)
				if analysisErr != nil {
					log.Printf("Error analyzing image with OCR.space: %v", analysisErr)
				}
			}

			if analysisErr == nil && imageAnalysis != "" {
				if messageText != "" {
					messageText = i18n.Tf("image.with_text", messageText, imageAnalysis)
				} else {
					messageText = i18n.Tf("image.only", imageAnalysis)
				}
			}
		}
	}

	return messageText, contentSafetyFlagged
}

// isMessageSummaryExcludedUser checks if a user ID is excluded from message summaries.
func (b *Bot) isMessageSummaryExcludedUser(userID int64) bool {
	for _, id := range b.config.AI.MessageSummaries.ExcludedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// isBotReplyToUser checks if a bot message was a reply to a message from the given user.
func (b *Bot) isBotReplyToUser(botMessageID int, chatID int64, userID int64) bool {
	botMsg, err := b.db.GetMessageInfo(botMessageID, chatID)
	if err != nil {
		prevID := botMessageID - 1
		log.Printf("Bot message %d not found in DB, trying previous message %d", botMessageID, prevID)
		botMsg, err = b.db.GetMessageInfo(prevID, chatID)
		if err != nil {
			log.Printf("Previous message %d also not found, allowing follow-up", prevID)
			return true
		}
	}

	if botMsg.ReplyToMessageID == nil {
		log.Printf("Bot message %d has no reply-to, allowing follow-up", botMsg.MessageID)
		return true
	}

	origMsg, err := b.db.GetMessageInfo(*botMsg.ReplyToMessageID, chatID)
	if err != nil {
		prevOrigID := *botMsg.ReplyToMessageID - 1
		log.Printf("Original message %d not found in DB, trying previous message %d", *botMsg.ReplyToMessageID, prevOrigID)
		origMsg, err = b.db.GetMessageInfo(prevOrigID, chatID)
		if err != nil {
			log.Printf("Previous message %d also not found, allowing follow-up", prevOrigID)
			return true
		}
	}

	return origMsg.UserID == userID
}

// storeBotMessageInfo stores bot's own messages in the database for reply chain context.
func (b *Bot) storeBotMessageInfo(message *telegram.Message) {
	var replyToMessageID *int
	if message.ReplyToMessageID != 0 {
		replyID := message.ReplyToMessageID
		replyToMessageID = &replyID
	}

	messageInfo := &database.MessageInfo{
		MessageID:        message.MessageID,
		ChatID:           message.Chat.ID,
		UserID:           b.botSelf.ID,
		Username:         b.botSelf.Username,
		Text:             message.Text,
		ReplyToMessageID: replyToMessageID,
		MessageThreadID:  messageTopic(message),
		Timestamp:        time.Now(),
	}

	err := b.db.StoreMessageInfo(messageInfo)
	if err != nil {
		log.Printf("Error storing bot message info: %v", err)
	}
}

// neutralInboundMessage converts an inbound library message into the neutral
// telegram.Message used by storeBotMessageInfo. It captures only the fields
// that helper needs (id, chat id, text, reply target).
func neutralInboundMessage(m *tgbotapi.Message) *telegram.Message {
	if m == nil {
		return &telegram.Message{}
	}
	out := &telegram.Message{
		MessageID: m.MessageID,
		Text:      m.Text,
		Chat:      telegram.Chat{ID: m.Chat.ID},
	}
	if m.ReplyToMessage != nil {
		out.ReplyToMessageID = m.ReplyToMessage.MessageID
	}
	return out
}

// dumpMessageToFile dumps incoming messages to a file for debugging.
func (b *Bot) dumpMessageToFile(message *tgbotapi.Message, chatType string, isEdited bool) {
	if err := os.MkdirAll(b.config.Debug.MessageDumpPath, 0755); err != nil {
		log.Printf("Failed to create message dump directory %s: %v", b.config.Debug.MessageDumpPath, err)
		return
	}

	date := time.Now().Format("2006-01-02")
	filename := fmt.Sprintf("%s_messages_%s.log", chatType, date)
	filePath := filepath.Join(b.config.Debug.MessageDumpPath, filename)

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	username := "Unknown"
	if message.From != nil {
		if message.From.Username != "" {
			username = "@" + message.From.Username
		} else {
			username = fmt.Sprintf("%s %s", message.From.FirstName, message.From.LastName)
		}
	}

	editedFlag := ""
	if isEdited {
		editedFlag = " [EDITED]"
	}

	replyInfo := ""
	if message.ReplyToMessage != nil {
		replyInfo = fmt.Sprintf(" [Reply to: %d]", message.ReplyToMessage.MessageID)
	}

	messageContent := message.Text
	if messageContent == "" && message.Caption != "" {
		messageContent = fmt.Sprintf("[Media with caption: %s]", message.Caption)
	} else if messageContent == "" {
		messageContent = "[Non-text message]"
	}

	logEntry := fmt.Sprintf("[%s]%s %s (ID: %d, MsgID: %d)%s: %s\n",
		timestamp, editedFlag, username, message.From.ID, message.MessageID, replyInfo, messageContent)

	b.sendDebugToSuperAdmin(fmt.Sprintf("📝 %s dump: %s", chatType, strings.TrimSpace(logEntry)))

	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open message dump file %s: %v", filePath, err)
		return
	}
	defer file.Close()

	if _, err := file.WriteString(logEntry); err != nil {
		log.Printf("Failed to write to message dump file %s: %v", filePath, err)
	}
}
