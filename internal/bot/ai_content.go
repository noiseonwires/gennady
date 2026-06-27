// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/i18n"

	tgbotapi "gennadium/internal/telegram"
)

var linkOnlyRegex = regexp.MustCompile(`(?i)^(https?://|www\.)\S+$`)

// generateMessageSummary generates a short summary for long messages in moderation chat.
func (b *Bot) generateMessageSummary(message *tgbotapi.Message) {
	if !b.config.AI.Enabled || !b.config.AI.MessageSummaries.Enabled {
		return
	}

	if !b.config.IsMessageSummaryActive(message.Chat.ID, messageTopic(message)) {
		return
	}

	if message.From != nil && message.From.ID == b.botSelf.ID {
		return
	}

	messageText := message.Text
	if messageText == "" {
		messageText = message.Caption
	}

	if messageText == "" || utf8.RuneCountInString(messageText) <= b.config.AI.MessageSummaries.MinLength {
		return
	}

	log.Printf("Generating summary for long message %d (length: %d chars)", message.MessageID, utf8.RuneCountInString(messageText))

	replacements := map[string]string{"message": messageText}
	systemPrompt := applyReplacements(b.config.AI.MessageSummaries.Prompt.System, replacements)
	prompt := applyReplacements(b.config.AI.MessageSummaries.Prompt.User, replacements)

	charCount := utf8.RuneCountInString(messageText)
	var modelConfigs config.AIModelConfigs
	var modelType string

	threshold := b.config.AI.MessageSummaries.LightModelThreshold
	if b.config.AI.MessageSummaries.UseFullModel && (threshold <= 0 || charCount <= threshold) {
		modelConfigs = b.config.AI.FullModel
		modelType = "full"
	} else {
		modelConfigs = b.config.AI.LightModel
		modelType = "light"
		if threshold > 0 && charCount > threshold {
			log.Printf("Message is very long (%d chars > threshold %d), using light model for summary", charCount, threshold)
		}
	}

	for i := 0; i < modelConfigs.Count(); i++ {
		modelConfig := modelConfigs.Get(i)
		log.Printf("Trying to generate summary with %s model %d/%d: %s", modelType, i+1, modelConfigs.Count(), modelConfig.DeploymentName)

		summary, err := b.callAzureOpenAIWithConfig("message_summary", prompt, systemPrompt, modelConfig, 300, false)
		if err != nil {
			log.Printf("Failed to generate summary with model %s: %v", modelConfig.DeploymentName, err)
			continue
		}

		log.Printf("Successfully generated summary for message %d", message.MessageID)

		summaryText := fmt.Sprintf("*TL;DR:* %s", summary)
		sentMsg, ok := b.sendMarkdownReply(message.Chat.ID, summaryText, message.MessageID)
		if !ok {
			return
		}

		err = b.db.AddMessageForDeletionWithPinnedStatus(sentMsg.MessageID, sentMsg.Chat.ID, false)
		if err != nil {
			log.Printf("Error marking summary message for deletion: %v", err)
		}

		return
	}

	log.Printf("All full models failed to generate summary for message %d", message.MessageID)
}

// generateCreativeReplyWithQuote generates a creative reply with optional quoted text context.
func (b *Bot) generateCreativeReplyWithQuote(messageText string, quotedText string, messageID int, userID int64, chatID int64, replyToText *string) (string, error) {
	context := b.buildCreativeReplyContext(messageText, messageID, userID, chatID, replyToText)

	var userPromptText string
	if quotedText != "" {
		userPromptText = i18n.Tf("ai.creative_reply_with_quote", messageText, quotedText, context)
	} else {
		userPromptText = i18n.Tf("ai.creative_reply", messageText, context)
	}

	replacements := map[string]string{
		"message":      messageText,
		"context":      context,
		"quote":        quotedText,
		"user_profile": b.getUserProfileForModeration(userID, chatID),
	}

	systemPrompt := applyReplacements(b.config.AI.CreativeReplies.Prompt.System, replacements)
	prompt := applyReplacements(b.config.AI.CreativeReplies.Prompt.User, replacements)
	if prompt == "" || prompt == b.config.AI.CreativeReplies.Prompt.User {
		prompt = userPromptText
	}

	b.dumpPromptToLog("creative_reply", systemPrompt, prompt)

	if b.config.AI.CreativeReplies.UseFullModel {
		return b.callAzureOpenAINoFallback("creative_reply", prompt, systemPrompt)
	}
	return b.callAzureOpenAIWithRetries("creative_reply", prompt, systemPrompt, b.config.AI.LightModel, 0, 3)
}

// buildCreativeReplyContext builds context for creative replies using reply chains or recent messages.
func (b *Bot) buildCreativeReplyContext(messageText string, messageID int, userID int64, chatID int64, replyToText *string) string {
	var contextBuilder strings.Builder

	var chainEntries []database.MessageInfo
	if replyToText != nil && *replyToText != "" {
		depth := b.config.AI.CreativeReplies.ReplyChainDepth
		if depth <= 0 {
			depth = 5
		}
		var replyChain []string
		replyChain, chainEntries = b.buildReplyChainFromMessageID(messageID, chatID, depth)

		if len(replyChain) > 1 {
			contextBuilder.WriteString(i18n.T("ai.context_reply_chain"))
			for i, msg := range replyChain {
				contextBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, msg))
			}
		}
	}

	if adjacent := b.buildAdjacentParticipantMessages(chainEntries, chatID); len(adjacent) > 0 {
		contextBuilder.WriteString(i18n.T("ai.context_adjacent_messages"))
		for i, msg := range adjacent {
			contextBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, msg))
		}
	}

	recentMessages, err := b.db.GetRecentMessagesWithUsernames(chatID, 3, 0)
	if err == nil && len(recentMessages) > 0 {
		contextBuilder.WriteString(i18n.T("ai.context_recent_messages"))
		start := 0
		if len(recentMessages) > 10 {
			start = len(recentMessages) - 10
		}
		for i, msg := range recentMessages[start:] {
			contextBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, msg))
		}
	} else {
		if err != nil {
			log.Printf("Error fetching recent messages for context: %v", err)
		}
	}

	userMessages, err := b.db.GetRecentUserMessages(userID, chatID, 5, 3)
	if err == nil && len(userMessages) > 1 {
		contextBuilder.WriteString(i18n.T("ai.context_user_messages"))
		for i, msg := range userMessages {
			contextBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, msg))
		}
	}

	return contextBuilder.String()
}

// buildReplyChainFromMessageID builds a chain of reply messages starting from a specific message ID.
// Returns the formatted strings (oldest to newest) and the corresponding raw message entries.
func (b *Bot) buildReplyChainFromMessageID(messageID int, chatID int64, maxMessages int) ([]string, []database.MessageInfo) {
	var chain []string
	var entries []database.MessageInfo
	currentMessageID := &messageID
	maxAgeHours := b.config.AI.CreativeReplies.ReplyChainMaxAgeHours
	if maxAgeHours <= 0 {
		maxAgeHours = 6
	}
	ageCutoff := time.Now().Add(-time.Duration(maxAgeHours) * time.Hour)
	var lastShownReplyTo *int
	botUserID := b.botSelf.ID
	botMessageCount := 0
	maxBotMessages := maxMessages / 2

	for len(chain) < maxMessages && currentMessageID != nil {
		messageInfo, err := b.db.GetMessageInfo(*currentMessageID, chatID)
		if err != nil {
			log.Printf("Message %d not in database, trying previous message %d", *currentMessageID, *currentMessageID-1)

			prevMessageID := *currentMessageID - 1
			messageInfo, err = b.db.GetMessageInfo(prevMessageID, chatID)
			if err != nil {
				log.Printf("Previous message %d also not found, stopping reply chain", prevMessageID)
				break
			}

			log.Printf("Found previous message %d as fallback", prevMessageID)
		}

		isFromBot := messageInfo.UserID == botUserID

		if messageInfo.Timestamp.Before(ageCutoff) {
			log.Printf("Message %d is older than %d hours (timestamp: %v), stopping chain", messageInfo.MessageID, maxAgeHours, messageInfo.Timestamp)
			break
		}

		if messageInfo.Text != "" || messageInfo.ExtraInfo != "" {
			log.Printf("Adding message %d to reply chain: text=%d chars, extra_info=%d chars, from_bot=%v",
				messageInfo.MessageID, len(messageInfo.Text), len(messageInfo.ExtraInfo), isFromBot)
			messageText := formatMessageForContext(*messageInfo)
			messageText += b.moderationMarksForMessage(chatID, messageInfo.MessageID)

			if messageInfo.ReplyToMessageID != nil {
				isConsecutive := *messageInfo.ReplyToMessageID == messageInfo.MessageID-1
				isSameAsLastShown := lastShownReplyTo != nil && *lastShownReplyTo == *messageInfo.ReplyToMessageID

				if !isConsecutive && !isSameAsLastShown {
					// Prefer the precise span the user highlighted when replying:
					// it's the exact fragment of the parent they responded to, so
					// it's both more accurate context and avoids a DB lookup.
					if q := strings.TrimSpace(messageInfo.QuoteText); q != "" {
						messageText = i18n.Tf("ai.reply_to", "", q, messageText)
						lastShownReplyTo = messageInfo.ReplyToMessageID
					} else if replyTarget, err := b.db.GetMessageInfo(*messageInfo.ReplyToMessageID, chatID); err == nil {
						replyContent := formatReplyContextContent(replyTarget.Text, replyTarget.ExtraInfo)
						if replyContent != "" {
							replyTargetIsOld := replyTarget.Timestamp.Before(ageCutoff)

							if replyTargetIsOld {
								messageText = i18n.Tf("ai.reply_to", replyTarget.Username, replyContent, messageText)
							} else {
								replyPreview := b.truncateTextForReplyPreview(replyContent, 50)
								messageText = i18n.Tf("ai.reply_to", replyTarget.Username, replyPreview, messageText)
							}
							lastShownReplyTo = messageInfo.ReplyToMessageID
						}
					}
				}
			}

			chain = append([]string{messageText}, chain...)
			entries = append([]database.MessageInfo{*messageInfo}, entries...)

			if isFromBot {
				botMessageCount++
				if botMessageCount >= maxBotMessages {
					log.Printf("Stopping reply chain after %d bot messages (message %d)", botMessageCount, messageInfo.MessageID)
					break
				}
			}
		} else {
			log.Printf("Message %d skipped from reply chain: empty text and extra_info (username: %s)",
				messageInfo.MessageID, messageInfo.Username)
		}

		currentMessageID = messageInfo.ReplyToMessageID
	}

	return chain, entries
}

// formatMessageForContext renders a single message as "Username: text" with link summary handling.
func formatMessageForContext(msg database.MessageInfo) string {
	var base string
	if msg.ExtraInfo != "" {
		if msg.Text != "" {
			base = msg.Username + ": " + msg.Text + i18n.Tf("ai.extra_info", msg.ExtraInfo)
		} else {
			base = msg.Username + ": " + i18n.Tf("ai.link_summary_prefix", msg.ExtraInfo)
		}
	} else {
		base = msg.Username + ": " + msg.Text
	}
	return base + formatReactionsForContext(msg.Reactions)
}

// moderationMarksForMessage returns an inline tag like " [message deleted, author
// muted]" noting moderation actions taken against a chain message: deletion of
// the message itself (auto-moderation, admin/WebUI single delete, or the
// post-mute bulk purge) and any warn/mute imposed on its author for it. Returns
// "" when nothing applies. Surfaced in creative-reply context so the model knows
// when a message was removed or its author sanctioned.
func (b *Bot) moderationMarksForMessage(chatID int64, messageID int) string {
	deleted, warned, muted, err := b.db.GetMessageModerationMarks(chatID, messageID)
	if err != nil {
		log.Printf("Error fetching moderation marks for message %d in chat %d: %v", messageID, chatID, err)
		return ""
	}

	var marks []string
	if deleted {
		marks = append(marks, i18n.T("ai.mark_deleted"))
	}
	if warned {
		marks = append(marks, i18n.T("ai.mark_warned"))
	}
	if muted {
		marks = append(marks, i18n.T("ai.mark_muted"))
	}
	if len(marks) == 0 {
		return ""
	}
	return " [" + strings.Join(marks, ", ") + "]"
}

// buildAdjacentParticipantMessages returns formatted messages from chain participants
// that lie within a configurable message-ID window around the chain entries, excluding
// the chain itself and the bot.
func (b *Bot) buildAdjacentParticipantMessages(chain []database.MessageInfo, chatID int64) []string {
	window := b.config.AI.CreativeReplies.ReplyChainAdjacentWindow
	if window <= 0 || len(chain) == 0 {
		return nil
	}

	botUserID := b.botSelf.ID
	userSet := make(map[int64]struct{})
	excludeIDs := make([]int, 0, len(chain))
	minID, maxID := chain[0].MessageID, chain[0].MessageID
	oldest := chain[0].Timestamp
	for _, m := range chain {
		if m.UserID != botUserID {
			userSet[m.UserID] = struct{}{}
		}
		excludeIDs = append(excludeIDs, m.MessageID)
		if m.MessageID < minID {
			minID = m.MessageID
		}
		if m.MessageID > maxID {
			maxID = m.MessageID
		}
		if m.Timestamp.Before(oldest) {
			oldest = m.Timestamp
		}
	}

	if len(userSet) == 0 {
		return nil
	}

	userIDs := make([]int64, 0, len(userSet))
	for uid := range userSet {
		userIDs = append(userIDs, uid)
	}

	maxAgeHours := b.config.AI.CreativeReplies.ReplyChainMaxAgeHours
	if maxAgeHours <= 0 {
		maxAgeHours = 6
	}
	since := oldest.Add(-time.Duration(maxAgeHours) * time.Hour)
	limit := len(chain)*window*2 + 1
	if limit > 50 {
		limit = 50
	}

	adjacent, err := b.db.GetMessagesByUsersInRange(chatID, userIDs, minID-window, maxID+window, excludeIDs, since, limit)
	if err != nil {
		log.Printf("Error fetching adjacent participant messages: %v", err)
		return nil
	}
	if len(adjacent) == 0 {
		return nil
	}

	result := make([]string, 0, len(adjacent))
	for _, m := range adjacent {
		result = append(result, formatMessageForContext(m))
	}
	return result
}

// truncateTextForReplyPreview truncates text to maxLen characters.
func (b *Bot) truncateTextForReplyPreview(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen]) + "..."
}

// formatReplyContextContent composes content for reply context, including link summaries.
func formatReplyContextContent(text, extraInfo string) string {
	trimmedText := strings.TrimSpace(text)
	trimmedExtra := strings.TrimSpace(extraInfo)

	if trimmedText != "" {
		if trimmedExtra != "" && isLinkOnlyText(trimmedText) {
			return trimmedExtra
		}
		return trimmedText
	}
	if trimmedExtra != "" {
		return i18n.Tf("ai.link_summary_prefix", trimmedExtra)
	}
	return ""
}

func isLinkOnlyText(text string) bool {
	return linkOnlyRegex.MatchString(text)
}

// translateWikipediaEvents translates historical events using Azure AI.
func (b *Bot) translateWikipediaEvents(events []string) ([]string, error) {
	if len(events) == 0 {
		return events, nil
	}

	if b.config.AI.ExternalData.TranslateWikipedia != nil && !*b.config.AI.ExternalData.TranslateWikipedia {
		return events, nil
	}

	// Skip translation when no translation prompt is configured. Sending an
	// empty prompt to the model yields a generic, useless reply (e.g. "Hi! What
	// would you like help with?"), so leave the events in their original
	// language instead. Clearing ai.translation_prompt is therefore also an
	// implicit opt-out of translation.
	if b.config.AI.TranslationPrompt.System == "" || b.config.AI.TranslationPrompt.User == "" {
		log.Printf("⚠️  Wikipedia events: ai.translation_prompt not configured, skipping translation (events left untranslated)")
		return events, nil
	}

	replacements := map[string]string{"text": strings.Join(events, "\n")}
	systemPrompt := applyReplacements(b.config.AI.TranslationPrompt.System, replacements)
	prompt := applyReplacements(b.config.AI.TranslationPrompt.User, replacements)

	b.dumpPromptToLog("translate_events", systemPrompt, prompt)

	translatedText, err := b.callAzureOpenAIWithConfig("events_translation", prompt, systemPrompt, b.config.AI.LightModel.Get(0), 2000, false)
	if err != nil {
		return nil, fmt.Errorf("failed to translate events: %w", err)
	}

	translatedEvents := strings.Split(strings.TrimSpace(translatedText), "\n")

	var result []string
	for _, event := range translatedEvents {
		if trimmed := strings.TrimSpace(event); trimmed != "" {
			result = append(result, trimmed)
		}
	}

	log.Printf("Translated %d Czech events to Russian", len(result))
	return result, nil
}

// translateExtractedContent translates extracted link content to the desired language.
func (b *Bot) translateExtractedContent(text string) (string, error) {
	if text == "" {
		return text, nil
	}

	// Skip translation when no translation prompt is configured (sending an
	// empty prompt produces a generic, useless reply). Return the original text
	// untranslated. Clearing ai.translation_prompt opts out of translation.
	if b.config.AI.TranslationPrompt.System == "" || b.config.AI.TranslationPrompt.User == "" {
		log.Printf("⚠️  Link content: ai.translation_prompt not configured, skipping translation (content left untranslated)")
		return text, nil
	}

	charCount := utf8.RuneCountInString(text)
	var modelConfigs config.AIModelConfigs
	var modelType string

	threshold := b.config.AI.LinkSummaries.LightModelThreshold
	if b.config.AI.LinkSummaries.UseFullModel && (threshold <= 0 || charCount <= threshold) {
		modelConfigs = b.config.AI.FullModel
		modelType = "full"
	} else {
		modelConfigs = b.config.AI.LightModel
		modelType = "light"
		if threshold > 0 && charCount > threshold {
			log.Printf("Content is very long (%d chars > threshold %d), using light model for translation", charCount, threshold)
		}
	}

	if charCount > 16384 {
		runes := []rune(text)
		text = string(runes[:16384]) + "..."
	}

	replacements := map[string]string{"text": text}
	systemPrompt := applyReplacements(b.config.AI.TranslationPrompt.System, replacements)
	prompt := applyReplacements(b.config.AI.TranslationPrompt.User, replacements)

	b.dumpPromptToLog("translate_content", systemPrompt, prompt)

	for i := 0; i < modelConfigs.Count(); i++ {
		modelConfig := modelConfigs.Get(i)
		log.Printf("Trying to translate with %s model %d/%d: %s", modelType, i+1, modelConfigs.Count(), modelConfig.DeploymentName)

		translatedText, err := b.callAzureOpenAIWithConfig("link_translation", prompt, systemPrompt, modelConfig, 4000, false)
		if err != nil {
			log.Printf("Failed to translate with model %s: %v", modelConfig.DeploymentName, err)
			continue
		}

		return strings.TrimSpace(translatedText), nil
	}

	return "", fmt.Errorf("all %s models failed to translate content", modelType)
}

// generateLinkContentSummary generates an AI summary of link content.
func (b *Bot) generateLinkContentSummary(content, title, targetURL string) (string, error) {
	charCount := utf8.RuneCountInString(content)
	var modelConfigs config.AIModelConfigs
	var modelType string
	truncatedSuffix := ""

	threshold := b.config.AI.LinkSummaries.LightModelThreshold
	if b.config.AI.LinkSummaries.UseFullModel && (threshold <= 0 || charCount <= threshold) {
		modelConfigs = b.config.AI.FullModel
		modelType = "full"
	} else {
		modelConfigs = b.config.AI.LightModel
		modelType = "light"
		if threshold > 0 && charCount > threshold {
			log.Printf("Content is very long (%d chars > threshold %d), using light model for link summary", charCount, threshold)
		}
	}

	if charCount > 16384 {
		runes := []rune(content)
		content = string(runes[:16384]) + "..."
		truncatedSuffix = i18n.T("ai.truncated_suffix")
	}

	replacements := map[string]string{
		"title":            title,
		"url":              targetURL,
		"content":          content,
		"truncated_suffix": truncatedSuffix,
	}
	systemPrompt := applyReplacements(b.config.AI.LinkSummaries.Prompt.System, replacements)
	prompt := applyReplacements(b.config.AI.LinkSummaries.Prompt.User, replacements)

	b.dumpPromptToLog("link_summary", systemPrompt, prompt)

	for i := 0; i < modelConfigs.Count(); i++ {
		modelConfig := modelConfigs.Get(i)
		log.Printf("Trying to generate link summary with %s model %d/%d: %s", modelType, i+1, modelConfigs.Count(), modelConfig.DeploymentName)

		summary, err := b.callAzureOpenAIWithConfig("link_summary", prompt, systemPrompt, modelConfig, 300, false)
		if err != nil {
			log.Printf("Failed to generate link summary with model %s: %v", modelConfig.DeploymentName, err)
			continue
		}

		return summary, nil
	}

	return "", fmt.Errorf("all %s models failed to generate link summary", modelType)
}

// processLinksAndCreativeReply extracts links content first, then processes creative follow-up replies.
func (b *Bot) processLinksAndCreativeReply(message *tgbotapi.Message, enhancedMessage *tgbotapi.Message) {
	summariesPosted := b.extractAndStoreLinksContent(message)

	if summariesPosted {
		log.Printf("Skipping creative reply for message %d - link summaries were posted", message.MessageID)
		return
	}

	isReplyToBot := message.ReplyToMessage != nil && message.ReplyToMessage.From != nil &&
		message.ReplyToMessage.From.ID == b.botSelf.ID

	isMentioned := b.isBotMentioned(message)

	if !b.config.AI.Enabled || !b.config.AI.CreativeReplies.Enabled {
		return
	}

	if b.isServiceMessage(message) {
		return
	}

	if !b.config.IsCreativeReplyActive(message.Chat.ID, messageTopic(message)) {
		log.Printf("Skipping creative reply for message %d - chat/topic not in creative-reply scope", message.MessageID)
		return
	}

	if !isReplyToBot && !isMentioned {
		return
	}

	// Skip the creative reply when the triggering message was itself flagged and
	// acted on by content moderation. analyzeMessage runs its moderation pass
	// synchronously before this goroutine is launched, so a flagged message is
	// already recorded in moderatedMsgs - we don't reward a rule-breaking message
	// with a friendly reply.
	if b.wasMessageModerated(message.Chat.ID, message.MessageID) {
		log.Printf("Skipping creative reply for message %d - message was flagged by content moderation", message.MessageID)
		return
	}

	// Determine whether this bot-mention routed the message into the reply
	// moderation/complaint path (handleBotMention -> handleReplyModerationTrigger),
	// which fires for a mention replying to a message that has text or is
	// forwarded. A "complaint" is such a mention replying to *another* user's
	// message; a mention replying to one of the bot's own messages is routed to
	// moderation too but is not a complaint.
	routedToModeration := b.config.IsModerationChat(message.Chat.ID) && isMentioned &&
		message.ReplyToMessage != nil &&
		(message.ReplyToMessage.Text != "" || message.ForwardFrom != nil)
	isComplaint := routedToModeration && message.ReplyToMessage.From != nil &&
		message.ReplyToMessage.From.ID != b.botSelf.ID

	if routedToModeration && !isComplaint {
		// e.g. a mention replying to one of the bot's own messages: the
		// moderation path already handled it, so don't also post a creative reply.
		log.Printf("Skipping creative reply for message %d - handled by the moderation path", message.MessageID)
		return
	}

	// For a complaint, handleBotMention already ran cross-model re-moderation on
	// the reported message synchronously (in handleReplyModerationTrigger) before
	// this goroutine was launched. The reply-chain context built below therefore
	// already reflects any resulting moderation marks (deleted / warned / muted),
	// so we go on to generate the creative reply with those results in context.

	b.handleCreativeFollowUp(enhancedMessage)
}

// extractAndStoreLinksContent extracts content from links, stores in extra_info, and optionally generates summaries.
func (b *Bot) extractAndStoreLinksContent(message *tgbotapi.Message) bool {
	if !b.config.AI.Enabled || !b.config.AI.LinkSummaries.Enabled {
		return false
	}

	if !b.config.IsLinkSummaryActive(message.Chat.ID, messageTopic(message)) {
		return false
	}

	if message.From != nil {
		for _, id := range b.config.AI.LinkSummaries.ExcludedUserIDs {
			if id == message.From.ID {
				log.Printf("Skipping link summary for message %d - user %d is excluded", message.MessageID, message.From.ID)
				return false
			}
		}
	}

	defer b.maybeForceGC()

	messageText := message.Text
	if messageText == "" {
		messageText = message.Caption
	}

	if messageText == "" {
		return false
	}

	var extractedParts []string
	summariesPosted := false

	telegramMatches := publicMessageLinkRegex.FindAllStringSubmatch(messageText, -1)
	for _, match := range telegramMatches {
		channelUsername := match[1]
		messageIDStr := match[2]

		// Skip private channel links of the form https://t.me/c/<internal_id>/<message_id>.
		// "c" is not a real username; such links have no public embed and cannot be fetched here.
		if channelUsername == "c" {
			log.Printf("Skipping Telegram private-channel link (%s): no public embed available", match[0])
			continue
		}

		content, err := b.fetchTelegramPostContent(channelUsername, messageIDStr)
		if err != nil {
			log.Printf("Failed to fetch Telegram post content: %v", err)
			continue
		}

		if content != "" {
			charCount := utf8.RuneCountInString(content)

			if charCount > b.config.AI.LinkSummaries.MaxExtractedContentLength {
				log.Printf("Telegram link content is too long (%d chars), generating summary", charCount)
				summary, err := b.generateLinkContentSummary(content, fmt.Sprintf("Telegram @%s/%s", channelUsername, messageIDStr), fmt.Sprintf("https://t.me/%s/%s", channelUsername, messageIDStr))
				if err != nil {
					log.Printf("Failed to summarize Telegram link content, using truncated: %v", err)
					runes := []rune(content)
					if len(runes) > b.config.AI.LinkSummaries.MaxExtractedContentLength {
						content = string(runes[:b.config.AI.LinkSummaries.MaxExtractedContentLength]) + "... [truncated]"
					}
				} else {
					summaryLen := utf8.RuneCountInString(summary)
					minLen := b.config.AI.LinkSummaries.MinSummaryLength
					if minLen > 0 && summaryLen < minLen {
						log.Printf("Telegram link summary too short (%d < %d chars), treating as extraction failure: @%s/%s", summaryLen, minLen, channelUsername, messageIDStr)
						break
					}
					content = summary
					log.Printf("Summarized Telegram link content (%d chars -> %d chars)", charCount, summaryLen)

					go b.postLinkSummary(message, fmt.Sprintf("https://t.me/%s/%s", channelUsername, messageIDStr), summary, fmt.Sprintf("Telegram @%s/%s", channelUsername, messageIDStr))
					summariesPosted = true
				}
			}

			extractedParts = append(extractedParts, fmt.Sprintf("[Telegram @%s/%s]: %s", channelUsername, messageIDStr, content))
			log.Printf("Stored Telegram link content (length: %d chars)", utf8.RuneCountInString(content))
			break
		}
	}

	urlRegex := regexp.MustCompile(`https?://[^\s<>"{}|\\^\[\]` + "`" + `]+`)
	urls := urlRegex.FindAllString(messageText, -1)

	for _, targetURL := range urls {
		if strings.Contains(targetURL, "t.me/") || strings.Contains(targetURL, "telegram.") {
			continue
		}

		domain := b.extractDomain(targetURL)
		if domain == "" {
			continue
		}

		if b.isDomainExcluded(domain) {
			log.Printf("Skipping link extraction for excluded domain: %s", domain)
			continue
		}

		if b.isExtensionExcluded(targetURL) {
			log.Printf("Skipping link extraction for excluded extension: %s", targetURL)
			continue
		}

		log.Printf("Fetching content from external link: %s", targetURL)

		b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.ExtractingLink)

		content, title, language, err := b.fetchAndExtractLinkContent(targetURL)
		if err != nil {
			log.Printf("Failed to fetch/extract link content: %v", err)
			b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.ExtractLinkFailed)
			continue
		}

		if content == "" {
			log.Printf("No content extracted from link: %s", targetURL)
			b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.ExtractLinkFailed)
			continue
		}

		desiredLang := b.config.AI.LinkSummaries.ContentLanguage
		if desiredLang == "" {
			desiredLang = "ru"
		}
		if language != "" && language != desiredLang {
			log.Printf("Content language (%s) differs from desired (%s), translating...", language, desiredLang)
			textToTranslate := content
			if title != "" {
				textToTranslate = title + "\n\n" + content
			}
			translatedText, err := b.translateExtractedContent(textToTranslate)
			if err != nil {
				log.Printf("Failed to translate content: %v, using original", err)
			} else {
				log.Printf("Successfully translated content from %s to %s (%d chars -> %d chars)", language, desiredLang, len(textToTranslate), len(translatedText))
				content = translatedText
			}
		}

		summary, err := b.generateLinkContentSummary(content, title, targetURL)
		if err != nil {
			log.Printf("Failed to summarize external link content: %v", err)
			charCount := utf8.RuneCountInString(content)
			if charCount > b.config.AI.LinkSummaries.MaxExtractedContentLength {
				runes := []rune(content)
				content = string(runes[:b.config.AI.LinkSummaries.MaxExtractedContentLength]) + "... [truncated]"
			}
			summary = content
		} else {
			summaryLen := utf8.RuneCountInString(summary)
			minLen := b.config.AI.LinkSummaries.MinSummaryLength
			if minLen > 0 && summaryLen < minLen {
				log.Printf("External link summary too short (%d < %d chars), treating as extraction failure: %s", summaryLen, minLen, targetURL)
				b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.ExtractLinkFailed)
				continue
			}
			log.Printf("Summarized external link content (length: %d chars -> %d chars, title: %s)", utf8.RuneCountInString(content), summaryLen, title)
			if b.config.AI.Enabled && b.config.AI.LinkSummaries.Enabled {
				go b.postLinkSummary(message, targetURL, summary, title)
				summariesPosted = true
			}
			b.clearMessageReaction(message.Chat.ID, message.MessageID)
		}

		extractedParts = append(extractedParts, fmt.Sprintf("[External link %s]:\nTitle: %s\nContent: %s", targetURL, title, summary))
		break
	}

	if len(extractedParts) > 0 {
		combinedContent := strings.Join(extractedParts, "\n\n")
		err := b.db.UpdateMessageExtraInfo(message.MessageID, message.Chat.ID, combinedContent)
		if err != nil {
			log.Printf("Error storing extracted links content in extra_info: %v", err)
		} else {
			log.Printf("Stored %d extracted link(s) in extra_info for message %d", len(extractedParts), message.MessageID)
		}
	}

	return summariesPosted
}

// postLinkSummary posts a pre-generated summary for a link.
func (b *Bot) postLinkSummary(message *tgbotapi.Message, targetURL, summary, title string) {
	summaryText := i18n.Tf("link.summary", summary)
	sentMsg, ok := b.sendMarkdownReply(message.Chat.ID, summaryText, message.MessageID)
	if !ok {
		return
	}

	err := b.db.AddMessageForDeletionWithPinnedStatus(sentMsg.MessageID, sentMsg.Chat.ID, false)
	if err != nil {
		log.Printf("Error marking link summary message: %v", err)
	}

	log.Printf("Successfully generated and posted link summary for message %d", message.MessageID)
}
