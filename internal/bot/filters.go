// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/i18n"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
)

// messageLinkRegex matches private Telegram message links
// (https://t.me/c/<id>/<id>[/<id>]). Compiled once at package load.
var messageLinkRegex = regexp.MustCompile(`https://t\.me/c/(\d+)/(\d+)(?:/(\d+))?`)

// incomingRecordOpts carries the side-effect flags for the combined
// RecordIncomingMessage write performed by analyzeMessage on non-edited
// moderation-chat messages. The handler computes them upfront so a single
// transaction can cover message_info + messages_for_deletion + general profile
// tracking instead of issuing 5 separate round-trips.
type incomingRecordOpts struct {
	AddToDeletion  bool
	DeletionPinned bool
}

// analyzeMessage analyzes a message for bad words, watchlist words, and vyimka requests
func (b *Bot) analyzeMessage(message *tgbotapi.Message, isEdited bool, recOpts incomingRecordOpts) {
	// Message text is already enhanced with image analysis at this point
	messageText := message.Text

	// If message is empty, try to get text from Caption (for forwarded media messages)
	if messageText == "" && message.Caption != "" {
		messageText = message.Caption
	}

	// For media payloads we don't process (stickers, gifs, videos, voice, …)
	// store a short descriptor tag in place of the missing text so the chat
	// history in message_info isn't a wall of blank rows.
	if messageText == "" {
		if tag := mediaTypeTag(message); tag != "" {
			messageText = tag
		}
	}

	// Store message info for potential moderation (even if text is empty, for reply context)
	userID := int64(0)
	username := ""
	if message.From != nil {
		userID = message.From.ID
		username = getUserDisplayNameFromUser(message.From)
	}

	// Capture reply context if this is a reply
	var replyToMessageID *int
	if message.ReplyToMessage != nil {
		replyID := message.ReplyToMessage.MessageID
		replyToMessageID = &replyID
	}

	// Capture the precise quoted span the sender highlighted when replying, if any.
	quoteText := ""
	if message.Quote != nil {
		quoteText = strings.TrimSpace(message.Quote.Text)
	}

	messageInfo := &database.MessageInfo{
		MessageID:        message.MessageID,
		ChatID:           message.Chat.ID,
		UserID:           userID,
		Username:         username,
		Text:             messageText,
		ReplyToMessageID: replyToMessageID,
		MessageThreadID:  messageTopic(message),
		QuoteText:        quoteText,
		Timestamp:        time.Now(),
	}

	// For forwarded messages, prepend a "<forwarded from: …>" marker to the
	// *stored* text so the chat history preserves the origin. We deliberately
	// don't touch the `messageText` variable used for bad-word / AI moderation
	// below - the moderator should judge the actual content, not our metadata.
	if fwd := forwardOriginTag(message); fwd != "" {
		if messageInfo.Text == "" {
			messageInfo.Text = fwd
		} else {
			messageInfo.Text = fwd + " " + messageInfo.Text
		}
	}

	// Use UpdateMessageInfo for edited messages to preserve original timestamp
	if isEdited {
		// Check if the message exists in DB and if the text actually changed
		existingMessage, err := b.db.GetMessageInfo(message.MessageID, message.Chat.ID)
		if err != nil || existingMessage == nil {
			// Message not found in DB (e.g. edit of a very old message) - skip saving but still moderate
			log.Printf("Edited message %d not found in DB - skipping save", message.MessageID)
		} else if existingMessage.Text == messageInfo.Text {
			// Text is the same, this is likely just a reaction event - skip re-analysis
			log.Printf("Message %d received 'edited' event but text unchanged - skipping (likely a reaction)", message.MessageID)
			return
		} else {
			// Preserve a diff of the change in extra_info so the original content
			// isn't lost when the user edits their message, while keeping the stored
			// data compact. We append a clearly marked block with the edit time and
			// a line-based diff (old vs new) to any existing extra info.
			//
			// Skip recording the diff for trivial edits (a couple of characters,
			// e.g. fixing a typo) - they add noise without useful context. The
			// message text itself is still updated below.
			const minEditDiffChars = 5
			if levenshteinDistance(existingMessage.Text, messageInfo.Text) >= minEditDiffChars {
				editTime := time.Now()
				if message.EditDate != 0 {
					editTime = time.Unix(int64(message.EditDate), 0)
				}
				editNote := fmt.Sprintf("[Edit diff at %s]\n%s",
					editTime.Format("2006-01-02 15:04:05"), computeTextDiff(existingMessage.Text, messageInfo.Text))
				if existingMessage.ExtraInfo != "" {
					messageInfo.ExtraInfo = existingMessage.ExtraInfo + "\n\n" + editNote
				} else {
					messageInfo.ExtraInfo = editNote
				}
			} else {
				// Keep any previously stored extra_info intact for minor edits.
				messageInfo.ExtraInfo = existingMessage.ExtraInfo
			}

			err = b.db.UpdateMessageInfo(messageInfo)
			if err != nil {
				log.Printf("Error updating message info: %v", err)
			}
		}
	} else {
		// Detect the user's first message in this chat *before* recording the
		// current message, so the optional new-user profile screening runs
		// exactly once per user per chat.
		firstMessageInChat := false
		if b.config.AI.ContentModeration.NewUserProfileCheckEnabled &&
			message.From != nil &&
			b.config.IsModerationChat(message.Chat.ID) &&
			b.botSelf.ID != message.From.ID &&
			!b.isUserWhitelisted(message.From.ID) {
			if cnt, cerr := b.db.CountUserMessagesInChat(message.From.ID, message.Chat.ID); cerr != nil {
				log.Printf("First-message check: error counting prior messages for user %d: %v", message.From.ID, cerr)
			} else if cnt == 0 {
				firstMessageInChat = true
			}
		}

		// Combine message_info write, deletion-queue insert, and general-profile
		// tracking into a single transaction to avoid 4-5 sequential DB
		// round-trips on the hot ingest path. The handler has pre-computed the
		// deletion-queue flags for us in recOpts.
		opts := database.IncomingMessageOpts{
			AddToDeletion:  recOpts.AddToDeletion,
			DeletionPinned: recOpts.DeletionPinned,
		}
		if b.config.UserProfiles.Enabled && message.From != nil &&
			b.config.IsModerationChat(message.Chat.ID) &&
			b.botSelf.ID != message.From.ID {
			displayName := strings.TrimSpace(strings.TrimSpace(message.From.FirstName) + " " + strings.TrimSpace(message.From.LastName))
			opts.TrackProfile = true
			opts.Username = message.From.Username
			opts.DisplayName = displayName
			opts.DayDate = messageInfo.Timestamp.Format("2006-01-02")
		}
		trackResult, err := b.db.RecordIncomingMessage(messageInfo, opts)
		if err != nil {
			log.Printf("Error recording incoming message: %v", err)
		} else if trackResult.NewUserTracked && opts.TrackProfile && opts.Username != "" {
			// First time we see this user_id (as a tracked profile) AND they
			// have a @username - check whether that handle was previously held
			// by a different user_id and alert admins if so.
			go b.notifyAdminsOnUsernameReuse(userID, opts.Username, opts.DisplayName)
		}

		// On the user's first message, screen their whole public profile (name,
		// bio, photo and linked personal channel), recording profile notices so
		// the AI moderation below can take them into account.
		if firstMessageInChat {
			b.checkFirstMessageUserProfile(message)
		}
	}

	// If no text content after processing, return (but message was still stored)
	if messageText == "" {
		return
	}

	/*
		// Check if this is a reply to a message with bot mention for moderation trigger
		if message.ReplyToMessage != nil && b.isBotMentioned(message) {
			b.handleReplyModerationTrigger(message)
			return // Reply with bot mention was processed, don't run other filters
		}
	*/

	messageLower := strings.ToLower(messageText)

	skipAdmin := b.config.AI.ContentModeration.SkipAdminUsers && b.isUserAdmin(message.From.ID)
	if message.From != nil && !skipAdmin && !b.isUserWhitelisted(message.From.ID) {
		// Check for bad words using the processed text (including image analysis)
		matchedRules, isContentFilter, decisionDetails := b.containsBadWords(messageLower, message, messageText)
		if len(matchedRules) > 0 {
			b.handleBadWordDetected(message, messageText, isContentFilter, matchedRules, decisionDetails)
			return // Don't check other filters if bad word is found
		}
	}
}

// buildModerationReplyContext builds the {{reply_to}} prompt fragment from a
// message's reply target: the parent author plus the quoted span (or the parent
// text/caption). Returns "" when the message isn't a reply or the parent has no
// usable text. Shared by the on-receive moderation path and the across-models
// re-moderation path so both feed the model identical reply context.
func (b *Bot) buildModerationReplyContext(message *tgbotapi.Message) string {
	if message == nil || message.ReplyToMessage == nil {
		return ""
	}
	replyUser := ""
	if message.ReplyToMessage.From != nil {
		replyUser = message.ReplyToMessage.From.FirstName
		if message.ReplyToMessage.From.LastName != "" {
			replyUser += " " + message.ReplyToMessage.From.LastName
		}
		if message.ReplyToMessage.From.Username != "" {
			replyUser += " (@" + message.ReplyToMessage.From.Username + ")"
		}
	}
	replyText := message.ReplyToMessage.Text
	if replyText == "" && message.ReplyToMessage.Caption != "" {
		replyText = message.ReplyToMessage.Caption
	}
	// Prefer the precise span the user highlighted when replying, so the AI
	// sees exactly the fragment they reacted to rather than the whole parent
	// message.
	if message.Quote != nil && strings.TrimSpace(message.Quote.Text) != "" {
		replyText = strings.TrimSpace(message.Quote.Text)
	}
	if replyText == "" {
		return ""
	}
	// Cap the quoted text so an oversized parent/quoted message can't bloat the
	// moderation prompt. Truncation is rune-aware (never splits a UTF-8
	// codepoint). <= 0 disables the cap.
	if maxChars := b.config.AI.ContentModeration.ReplyContextMaxChars; maxChars > 0 {
		replyText = b.truncateTextForReplyPreview(replyText, maxChars)
	}
	// Make it unambiguous that the quoted text is the EARLIER message being
	// replied to (context only), not the message under moderation. LLMs
	// otherwise tend to confuse the two and moderate the quoted text instead of
	// the new message.
	quotedFrom := replyUser
	if quotedFrom == "" {
		quotedFrom = i18n.T("mod.reply_context_unknown_user")
	}
	return i18n.Tf("mod.reply_context", quotedFrom, replyText)
}

// containsBadWords checks if message contains any bad words using AI analysis first, then traditional lookup
// Uses two-stage AI analysis: light model first, then full model confirmation
// Returns: (matchedRules, isContentFilterViolation, decisionDetails). When isContentFilter is
// true and matchedRules is empty, the message was flagged by Azure's safety
// filter rather than a custom rule and should be handled as a hard delete.
// When multiple rules match, all are returned in declaration order so the
// caller can dispatch each action (e.g. warn AND report).
// decisionDetails contains any explanatory text the LLM provided after the
// trigger line (lines 2+ of the response).
func (b *Bot) containsBadWords(messageText string, message *tgbotapi.Message, fullMessageText string) ([]config.ModerationRule, bool, string) {
	messageText = strings.ToLower(messageText)

	// First, try AI content analysis if enabled and applicable
	if b.config.AI.ContentModeration.Enabled && message != nil &&
		b.config.IsModerationChat(message.Chat.ID) {

		// Check if this message is from an excluded subchat
		isExcludedSubchat := !b.config.IsModerationActive(message.Chat.ID, messageTopic(message))

		// Only use AI analysis for non-excluded subchats and main chat
		if !isExcludedSubchat {
			// Build reply-to context for the AI prompt
			replyToText := b.buildModerationReplyContext(message)

			// Stage 1: Light model analysis
			lightRules, lightDetails, err := b.analyzeMessageContentWithLightModel(fullMessageText, message.From.ID, message.Chat.ID, replyToText)
			lightModelContentFilter := false
			var lightCFDetails string
			if err != nil {
				// Check if content filter was triggered - needs confirmation with full model
				var cfErr *ContentFilterError
				if errors.As(err, &cfErr) {
					log.Printf("⚠️ Content filter triggered by light model for message %d - will confirm with full model", message.MessageID)
					lightModelContentFilter = true
					lightCFDetails = cfErr.Details
					// Set thinking emoji to indicate processing
					b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.SuspiciousMessage)
				} else {
					log.Printf("Error in AI content analysis (light model): %v", err)
					// Continue without AI analysis on error
				}
			}
			if len(lightRules) > 0 || lightModelContentFilter {
				// Light model detected bad content or content filter triggered
				if len(lightRules) > 0 {
					log.Printf("🔍 Light model flagged message %d with %d rule(s), confirming with full model...",
						message.MessageID, len(lightRules))
					// Set thinking emoji to indicate processing
					b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.SuspiciousMessage)
				}

				// Stage 2: Full model confirmation
				fullRules, fullDetails, err := b.analyzeMessageContentWithFullModel(fullMessageText, message.From.ID, message.Chat.ID, replyToText)
				if err != nil {
					// Check if content filter was triggered - this is definitive
					var cfErr *ContentFilterError
					if errors.As(err, &cfErr) {
						log.Printf("⚠️ Content filter triggered by full model for message %d", message.MessageID)
						csRules := b.matchContentSecurityRules()
						csDetails := i18n.T("mod.content_security_details")
						if cfErr.Details != "" {
							csDetails += "\n" + cfErr.Details
						}
						b.replaceMessageContentWithPlaceholder(message.MessageID, message.Chat.ID, i18n.T("filter.content_removed"), csDetails)
						return csRules, true, csDetails
					}
					log.Printf("Error in AI content analysis (full model): %v, treating light model result as authoritative", err)
					// On full model error, trust light model result (including content filter)
					if lightModelContentFilter {
						csRules := b.matchContentSecurityRules()
						csDetails := i18n.T("mod.content_security_details")
						if lightCFDetails != "" {
							csDetails += "\n" + lightCFDetails
						}
						b.replaceMessageContentWithPlaceholder(message.MessageID, message.Chat.ID, i18n.T("filter.content_removed"), csDetails)
						return csRules, true, csDetails
					}
					return lightRules, false, lightDetails
				}

				if len(fullRules) > 0 {
					// Full model confirmed - it's bad content. Prefer the full
					// model's verdict (rule set) over the light model's.
					log.Printf("✅ Full model confirmed bad content in message %d (%d rule(s))",
						message.MessageID, len(fullRules))
					if lightModelContentFilter {
						csDetails := i18n.T("mod.content_security_details")
						if lightCFDetails != "" {
							csDetails += "\n" + lightCFDetails
						}
						b.replaceMessageContentWithPlaceholder(message.MessageID, message.Chat.ID, "[The message was not saved, because it violated AI content policies.]", csDetails)
						return fullRules, true, fullDetails
					}
					return fullRules, false, fullDetails
				} else {
					// Full model disagreed - clear the reaction and don't flag
					log.Printf("❌ Full model did NOT confirm bad content in message %d, clearing reaction", message.MessageID)
					b.clearMessageReaction(message.Chat.ID, message.MessageID)
					return nil, false, ""
				}
			}
			// Light model said it's OK - no further action needed
		}
	}

	return nil, false, ""
}

// setMessageReaction sets an emoji reaction on a message
func (b *Bot) setMessageReaction(chatID int64, messageID int, emoji string) {
	if err := b.tg.SetMessageReaction(chatID, messageID, emoji); err != nil {
		log.Printf("Error setting reaction %s: %v", emoji, err)
	}
}

// clearMessageReaction clears all reactions from a message
func (b *Bot) clearMessageReaction(chatID int64, messageID int) {
	if err := b.tg.SetMessageReaction(chatID, messageID, ""); err != nil {
		log.Printf("Error clearing reaction: %v", err)
	}
}

// handleBadWordDetected handles detection of bad words.
// isContentFilter indicates the detection came from Azure's content safety
// filter rather than a configured rule. matchedRules is the (possibly empty)
// set of rules whose triggers the LLM emitted, in declaration order. Every
// matched rule's action is dispatched in turn so that combinations like
// "warn + report" or "delete + ban" can be expressed by stacking rules.
// decisionDetails is explanatory text from the LLM (lines after the trigger line).
func (b *Bot) handleBadWordDetected(message *tgbotapi.Message, messageText string, isContentFilter bool, matchedRules []config.ModerationRule, decisionDetails string) {
	userID := int64(0)
	if message.From != nil {
		userID = message.From.ID
	}

	// Deduplication: skip if this message was already sent for moderation recently.
	// The whole set of rules is dispatched at most once per message.
	modKey := fmt.Sprintf("%d_%d", message.Chat.ID, message.MessageID)
	b.moderatedMu.Lock()
	if t, ok := b.moderatedMsgs[modKey]; ok && time.Since(t) < 10*time.Minute {
		b.moderatedMu.Unlock()
		log.Printf("Skipping duplicate moderation for message %d (already reported)", message.MessageID)
		return
	}
	b.moderatedMsgs[modKey] = time.Now()
	// Evict old entries to prevent unbounded growth
	for k, v := range b.moderatedMsgs {
		if time.Since(v) > 30*time.Minute {
			delete(b.moderatedMsgs, k)
		}
	}
	b.moderatedMu.Unlock()

	ruleDesc := "content_filter"
	if len(matchedRules) > 0 {
		parts := make([]string, 0, len(matchedRules))
		for i := range matchedRules {
			parts = append(parts, fmt.Sprintf("%q->%s", matchedRules[i].Trigger, matchedRules[i].Action))
		}
		ruleDesc = strings.Join(parts, ", ")
	}
	log.Printf("Bad message detected %d from user %d (%s, content_filter=%v)", message.MessageID, userID, ruleDesc, isContentFilter)
	if decisionDetails != "" {
		log.Printf("AI decision details for message %d: %s", message.MessageID, decisionDetails)
	}

	// Persist the AI moderation verdict's explanation onto the offending message
	// so the Web UI (chat messages list + moderation events list) can show *why*
	// it was actioned. Recorded once here - regardless of how many actions the
	// verdict triggers below - since the reason describes the message itself, not
	// any individual action. No-op when the message isn't tracked in message_info.
	if decisionDetails != "" {
		if err := b.db.UpdateMessageModerationReason(message.MessageID, message.Chat.ID, decisionDetails); err != nil {
			log.Printf("Error storing moderation reason for message %d: %v", message.MessageID, err)
		}
	}

	// Build the action plan from matched rules.
	// Every matched rule's action is dispatched in declaration order.
	// If no rule produced a recognized action, fall back to a safe report.
	type actionStep struct {
		action string
		rule   *config.ModerationRule
	}
	var plan []actionStep
	for i := range matchedRules {
		action := matchedRules[i].Action
		plan = append(plan, actionStep{action: action, rule: &matchedRules[i]})
	}
	if len(plan) == 0 {
		plan = append(plan, actionStep{action: config.ModerationActionReport, rule: nil})
	}

	// Auto-moderation actions are dispatched strictly serially: each
	// b.autoModerate* call returns before the next plan step starts. This
	// guarantees ordering (e.g. a warn rule preceding a report rule means the
	// warning is fully delivered before the admin card is composed, so the
	// card's keyboard correctly reflects the existing warning).
	for i, step := range plan {
		log.Printf("Auto-moderation: dispatch step %d/%d action=%s for message %d",
			i+1, len(plan), step.action, message.MessageID)
		switch step.action {
		case config.ModerationActionDelete:
			b.autoModerateDelete(message, messageText, isContentFilter, step.rule)
		case config.ModerationActionWarn:
			b.autoModerateWarn(message, messageText, step.rule)
		case config.ModerationActionMute:
			b.autoModerateMute(message, messageText, step.rule)
		default: // report (or unknown → safest behavior)
			// Hand the report step the list of *other* automatic actions in
			// this chain so the admin card can summarise them and toggle its
			// buttons (e.g. show Restore when delete is also queued, drop the
			// Warn button when delete-warning will be present, etc.).
			others := make([]chainAction, 0, len(plan)-1)
			for _, peer := range plan {
				if peer.action == config.ModerationActionReport {
					continue
				}
				others = append(others, chainAction{action: peer.action, rule: peer.rule})
			}
			b.autoModerateReport(message, messageText, isContentFilter, step.rule, others, decisionDetails)
		}
		log.Printf("Auto-moderation: finished step %d/%d action=%s for message %d",
			i+1, len(plan), step.action, message.MessageID)
	}
}

// wasMessageModerated reports whether the given message was recently flagged and
// acted on by content moderation. handleBadWordDetected records every actioned
// message in moderatedMsgs (keyed chatID_messageID); analyzeMessage runs that
// synchronous moderation pass before the creative-reply goroutine is launched,
// so a true result here means the message is itself a violation.
func (b *Bot) wasMessageModerated(chatID int64, messageID int) bool {
	modKey := fmt.Sprintf("%d_%d", chatID, messageID)
	b.moderatedMu.Lock()
	defer b.moderatedMu.Unlock()
	t, ok := b.moderatedMsgs[modKey]
	return ok && time.Since(t) < 10*time.Minute
}

// handleMessageLinkForModeration handles message links sent for manual moderation
func (b *Bot) handleMessageLinkForModeration(message *tgbotapi.Message) bool {
	linkMessageID, actualChatID, topicID, ok := parseTelegramMessageLink(message.Text)
	if !ok {
		// Try public link format (https://t.me/username/message_id)
		username, pubMessageID, pubTopicID, pubOk := parsePublicTelegramMessageLink(message.Text)
		if !pubOk {
			return false // No valid message link found
		}
		// Resolve username to chat ID
		resolvedChatID, err := b.resolveChatIDByUsername(username)
		if err != nil {
			log.Printf("Error resolving username @%s: %v", username, err)
			response := fmt.Sprintf("❌ Could not resolve chat @%s: %v", username, err)
			replyTo := 0
			if message.Chat.ID == b.config.Admin.ChatID {
				replyTo = b.firstAdminReplyID()
			}
			b.tg.SendMessage(telegram.SendMessageParams{ChatID: message.Chat.ID, Text: response, ReplyToMessageID: replyTo})
			return true
		}
		linkMessageID = pubMessageID
		actualChatID = resolvedChatID
		topicID = pubTopicID
	}

	// Validate that the reported message belongs to one of the moderation chats
	if !b.config.IsModerationChat(actualChatID) {
		response := fmt.Sprintf("❌ Invalid chat ID. Messages can only be reported from moderation chats. Provided chat ID: %d", actualChatID)
		replyTo := 0
		if message.Chat.ID == b.config.Admin.ChatID {
			replyTo = b.firstAdminReplyID()
		}
		b.tg.SendMessage(telegram.SendMessageParams{ChatID: message.Chat.ID, Text: response, ReplyToMessageID: replyTo})
		return true
	}

	// Try to get the message info from database
	messageInfo, err := b.db.GetMessageInfo(linkMessageID, actualChatID)
	if err != nil {
		// If not found in database, send error response
		response := "❌ Message not found in database. The bot needs to have seen this message to moderate it."
		replyTo := 0
		if message.Chat.ID == b.config.Admin.ChatID {
			replyTo = b.firstAdminReplyID()
		}
		b.tg.SendMessage(telegram.SendMessageParams{ChatID: message.Chat.ID, Text: response, ReplyToMessageID: replyTo})
		return true
	}

	// Create inline keyboard for moderation actions
	threadID := 0
	if topicID != nil {
		threadID = *topicID
	}
	keyboard := b.createModerationKeyboard(actualChatID, linkMessageID, messageInfo.UserID, threadID, false)

	// TODO: use cached username
	// Get user info for moderation text - try to get display name by ID
	username := b.getUserDisplayNameByID(messageInfo.UserID, actualChatID)

	// Build violation history text
	violationInfo := b.buildViolationInfo(messageInfo.UserID, actualChatID)

	// Handle empty message text
	messageText := messageInfo.Text
	if messageText == "" {
		messageText = i18n.T("filter.empty_message")
	}
	// Ensure message text is valid UTF-8
	if !utf8.ValidString(messageText) {
		log.Printf("DEBUG: Invalid UTF-8 in message text, cleaning...")
		messageText = strings.ToValidUTF8(messageText, "")
	}
	// Truncate long messages
	if len(messageText) > 100 {
		messageText = messageText[:100] + "..."
	}

	// Ensure username is valid UTF-8
	if !utf8.ValidString(username) {
		log.Printf("DEBUG: Invalid UTF-8 in username, cleaning...")
		username = strings.ToValidUTF8(username, "")
	}

	// Generate the original message link with proper format
	originalMessageLink := generateMessageURL(actualChatID, linkMessageID, topicID)

	// Prepare common moderation message parts
	commonModerationInfo := fmt.Sprintf("👤 Reported user: %s\n📝 Message: \"%s\"\n🔗 Link: %s\n\n%s\n\n⚡ Select action:",
		username,
		messageText,
		originalMessageLink,
		violationInfo,
	)

	// Check if the reporter is an admin
	isAdmin := message.From != nil && b.isUserAdmin(message.From.ID)
	if isAdmin {
		// Admin user: Send moderation keyboard to the current chat (admin or private)
		moderationText := "🔗 Manual moderation request\n\n" + commonModerationInfo

		// Try to clean the text by ensuring it's valid UTF-8
		if !utf8.ValidString(moderationText) {
			log.Printf("DEBUG: Text contains invalid UTF-8, cleaning...")
			moderationText = strings.ToValidUTF8(moderationText, "")
		}

		// Truncate message if needed.
		moderationText = truncateMessage(moderationText, MaxTelegramMessageLength)

		moderationMsg, err := b.tg.SendMessage(telegram.SendMessageParams{
			ChatID:           message.Chat.ID,
			Text:             moderationText,
			Keyboard:         &keyboard,
			ReplyToMessageID: b.adminReplyIfAdminChat(message.Chat.ID),
		})
		if err != nil {
			log.Printf("Error sending moderation request to admin chat: %v", err)
			return true
		}

		// Add to deletion queue if sent to moderation chat
		if b.config.IsModerationChat(message.Chat.ID) {
			err = b.db.AddMessageForDeletion(moderationMsg.MessageID, moderationMsg.Chat.ID)
			if err != nil {
				log.Printf("Error adding moderation message to deletion queue: %v", err)
			}
		}
	} else {
		// Non-admin user: Send reaction acknowledgment and moderation request to admin chat

		// Get reporter info for the admin notification
		reporterUsername := ""
		userID := int64(0)
		if message.From != nil {
			reporterUsername = message.From.Username
			userID = message.From.ID
		}

		// Add an OK emoji reaction to acknowledge the report
		b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.ReportAcknowledged)

		// Send moderation request to admin chat
		// Always use getUserDisplayNameByID as it handles all the logic properly
		reporterUsername = b.getUserDisplayNameByID(userID, message.Chat.ID)

		moderationText := fmt.Sprintf("🚨 User report from %s\n\n", reporterUsername) + commonModerationInfo

		// Truncate message if needed.
		moderationText = truncateMessage(moderationText, MaxTelegramMessageLength)

		_, err = b.tg.SendMessage(telegram.SendMessageParams{
			ChatID:           b.config.Admin.ChatID,
			Text:             moderationText,
			Keyboard:         &keyboard,
			ReplyToMessageID: b.firstAdminReplyID(),
		})
		if err != nil {
			log.Printf("Error sending moderation request to admin chat: %v", err)
		}
	}

	return true
}
