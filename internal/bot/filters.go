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
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
)

// messageLinkRegex matches private Telegram message links
// (https://t.me/c/<id>/<id>[/<id>]). Compiled once at package load.
var messageLinkRegex = regexp.MustCompile(`https://t\.me/c/(\d+)/(\d+)(?:/(\d+))?`)

// analyzeMessage records the inbound message and runs AI/bad-word moderation on
// it, returning true when the message was flagged and acted on. It is a thin
// coordinator over the recording and moderation halves, both of which operate on
// the shared MsgContext (the vision-enriched EnhancedMsg, the edit flag, the
// resolved Scope, and the pre-computed deletion-queue flags).
func (b *Bot) analyzeMessage(mc *MsgContext) bool {
	messageText, proceed := b.recordMessage(mc)
	if !proceed {
		return false
	}
	return b.moderateMessage(mc, messageText)
}

// recordMessage stores the inbound message in message_info and returns the text
// used for moderation plus whether processing should proceed. proceed is false
// only when an edited message's text was unchanged (a no-op reaction event), in
// which case nothing was written and moderation is skipped.
func (b *Bot) recordMessage(mc *MsgContext) (string, bool) {
	message := mc.EnhancedMsg

	// Message text is already enhanced with image analysis at this point.
	messageText := message.Text
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

	userID := int64(0)
	username := ""
	if message.From != nil {
		userID = message.From.ID
		username = getUserDisplayNameFromUser(message.From)
	}

	// Capture reply context if this is a reply.
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
		MessageThreadID:  mc.Scope.Topic,
		QuoteText:        quoteText,
		Timestamp:        time.Now(),
	}

	// For forwarded messages, prepend a "<forwarded from: …>" marker to the
	// *stored* text so the chat history preserves the origin. We deliberately
	// don't touch the messageText used for moderation - the moderator should
	// judge the actual content, not our metadata.
	if fwd := forwardOriginTag(message); fwd != "" {
		if messageInfo.Text == "" {
			messageInfo.Text = fwd
		} else {
			messageInfo.Text = fwd + " " + messageInfo.Text
		}
	}

	if mc.IsEdited {
		if !b.recordEditedMessage(mc, messageInfo) {
			return "", false
		}
		return messageText, true
	}

	b.recordNewMessage(mc, messageInfo)
	return messageText, true
}

// recordEditedMessage updates message_info for an edited message, preserving the
// original timestamp and recording a diff of substantive edits in extra_info. It
// returns false when the text was unchanged (a no-op reaction event) so the
// caller skips re-moderation; true otherwise (including when the original row is
// missing, in which case the edit is still moderated).
func (b *Bot) recordEditedMessage(mc *MsgContext, messageInfo *database.MessageInfo) bool {
	message := mc.EnhancedMsg

	existingMessage, err := b.db.GetMessageInfo(message.MessageID, message.Chat.ID)
	if err != nil || existingMessage == nil {
		// Message not found in DB (e.g. edit of a very old message) - skip saving but still moderate.
		log.Printf("Edited message %d not found in DB - skipping save", message.MessageID)
		return true
	}
	if existingMessage.Text == messageInfo.Text {
		// Text is the same, this is likely just a reaction event - skip re-analysis.
		log.Printf("Message %d received 'edited' event but text unchanged - skipping (likely a reaction)", message.MessageID)
		return false
	}

	// Preserve a diff of the change in extra_info so the original content isn't
	// lost when the user edits their message, while keeping the stored data
	// compact. Skip the diff for trivial edits (a couple of characters, e.g.
	// fixing a typo) - they add noise without useful context.
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

	if err := b.db.UpdateMessageInfo(messageInfo); err != nil {
		log.Printf("Error updating message info: %v", err)
	}
	return true
}

// recordNewMessage records a newly-received message: it bundles the message_info
// insert, the deletion-queue insert and general-profile tracking into the single
// RecordIncomingMessage transaction, alerts on username reuse, counts the
// moderation-funnel "received" stat, and screens the sender's public profile on
// their first message in the chat.
func (b *Bot) recordNewMessage(mc *MsgContext, messageInfo *database.MessageInfo) {
	message := mc.EnhancedMsg
	userID := int64(0)
	if message.From != nil {
		userID = message.From.ID
	}

	// Detect the user's first message in this chat *before* recording the
	// current message, so the optional new-user profile screening runs exactly
	// once per user per chat.
	firstMessageInChat := false
	if b.config.AI.ContentModeration.NewUserProfileCheckEnabled &&
		message.From != nil &&
		mc.Scope.Moderate &&
		b.botSelf.ID != message.From.ID &&
		!b.isUserWhitelisted(message.From.ID) {
		if cnt, cerr := b.db.CountUserMessagesInChat(message.From.ID, message.Chat.ID); cerr != nil {
			log.Printf("First-message check: error counting prior messages for user %d: %v", message.From.ID, cerr)
		} else if cnt == 0 {
			firstMessageInChat = true
		}
	}

	// Combine message_info write, deletion-queue insert, and general-profile
	// tracking into a single transaction to avoid 4-5 sequential DB round-trips
	// on the hot ingest path. The deletion-queue flags were pre-computed by the
	// prepare-deletion stage and carried on the context.
	opts := database.IncomingMessageOpts{
		AddToDeletion:  mc.AddToDeletion,
		DeletionPinned: mc.DeletionPinned,
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
		// First time we see this user_id (as a tracked profile) AND they have a
		// @username - check whether that handle was previously held by a
		// different user_id and alert admins if so.
		go b.notifyAdminsOnUsernameReuse(userID, opts.Username, opts.DisplayName)
	}

	// Count every message received in a moderation chat (excluding the bot
	// itself) as the top of the moderation funnel, regardless of whether AI
	// moderation is active for its specific topic.
	if b.config.IsModerationChat(message.Chat.ID) && message.From != nil && b.botSelf.ID != message.From.ID {
		b.recordModStat(database.ModStatReceived)
	}

	// On the user's first message, screen their whole public profile (name, bio,
	// photo and linked personal channel), recording profile notices so the AI
	// moderation can take them into account.
	if firstMessageInChat {
		b.checkFirstMessageUserProfile(message)
	}
}

// moderateMessage runs AI/bad-word moderation on the recorded message, returning
// true when it was flagged and acted on. It self-gates on empty text (the
// message was still recorded for reply/history context) and on the (chat, topic)
// being actively moderated.
func (b *Bot) moderateMessage(mc *MsgContext, messageText string) bool {
	if messageText == "" {
		return false
	}

	// AI/bad-word moderation only applies to actively-moderated (chat, topic)
	// pairs. A (chat, topic) excluded via moderation.excluded_topics is still
	// recorded for chat history and reply context, but must never be moderated.
	if !mc.Scope.Moderate {
		return false
	}

	message := mc.EnhancedMsg
	messageLower := strings.ToLower(messageText)
	skipAdmin := b.config.AI.ContentModeration.SkipAdminUsers && b.isUserAdmin(message.From.ID)
	if message.From != nil && !skipAdmin && !b.isUserWhitelisted(message.From.ID) {
		// Check for bad words using the processed text (including image analysis).
		matchedRules, isContentFilter, decisionDetails := b.containsBadWords(messageLower, message, messageText)
		if len(matchedRules) > 0 {
			b.handleBadWordDetected(message, messageText, isContentFilter, matchedRules, decisionDetails)
			return true // moderated - don't check other filters
		}
	}
	return false
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

// containsBadWords runs the two-stage AI moderation on a message and returns the
// verdict tuple (matchedRules, isContentFilter, decisionDetails). When
// isContentFilter is true and matchedRules is empty, the provider safety filter
// flagged the message rather than a custom rule, and it should be handled as a
// hard delete. When multiple rules match they are returned in declaration order
// so the caller can dispatch each action (e.g. warn AND report). decisionDetails
// is the LLM's explanatory text (lines after the trigger line).
//
// This is the imperative shell: it performs the AI calls, reactions, stats and
// placeholder writes, deferring every branch decision to the pure functions in
// classifier.go (decideConfirmedVerdict / decideDoubleCheckVerdict).
func (b *Bot) containsBadWords(messageText string, message *tgbotapi.Message, fullMessageText string) ([]config.ModerationRule, bool, string) {
	if !b.config.AI.ContentModeration.Enabled || message == nil || !b.config.IsModerationChat(message.Chat.ID) {
		return nil, false, ""
	}
	// Defense-in-depth: the sole caller (analyzeMessage) already returns early
	// for excluded (chat, topic) pairs, but re-check so this never moderates an
	// excluded scope regardless of caller.
	if !b.config.IsModerationActive(message.Chat.ID, messageTopic(message)) {
		return nil, false, ""
	}

	replyToText := b.buildModerationReplyContext(message)

	// Stage 1: light model.
	lightRules, lightDetails, lightErr := b.analyzeMessageContentWithLightModel(fullMessageText, message.From.ID, message.Chat.ID, replyToText)
	light := newModelOutcome(lightRules, lightDetails, lightErr)
	switch {
	case light.contentFilter:
		log.Printf("⚠️ Content filter triggered by light model for message %d - will confirm with full model", message.MessageID)
		b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.SuspiciousMessage)
	case light.transientErr:
		log.Printf("Error in AI content analysis (light model): %v", lightErr)
	}

	if light.flagged() {
		b.recordModStat(database.ModStatLightFlagged)
		if len(light.rules) > 0 {
			log.Printf("🔍 Light model flagged message %d with %d rule(s), confirming with full model...", message.MessageID, len(light.rules))
			b.setMessageReaction(message.Chat.ID, message.MessageID, b.config.Reactions.SuspiciousMessage)
		}

		// Stage 2: full-model confirmation.
		fullRules, fullDetails, fullErr := b.analyzeMessageContentWithFullModel(fullMessageText, message.From.ID, message.Chat.ID, replyToText)
		full := newModelOutcome(fullRules, fullDetails, fullErr)
		switch {
		case full.contentFilter:
			log.Printf("⚠️ Content filter triggered by full model for message %d", message.MessageID)
		case full.transientErr:
			log.Printf("Error in AI content analysis (full model): %v, treating light model result as authoritative", fullErr)
		case len(full.rules) > 0:
			log.Printf("✅ Full model confirmed bad content in message %d (%d rule(s))", message.MessageID, len(full.rules))
		}
		return b.applyVerdictDecision(message, decideConfirmedVerdict(light, full))
	}

	// New-user double-check: the light model cleared the message, but for a
	// user's first N messages run the full model too to catch subtle spam the
	// cheaper light model may have missed.
	if b.shouldFullModelDoubleCheck(message.From.ID, message.Chat.ID) {
		log.Printf("🔁 Light model cleared message %d but user is within first %d message(s) - double-checking with full model", message.MessageID, b.config.AI.ContentModeration.FullModelFirstMessages)
		fullRules, fullDetails, fullErr := b.analyzeMessageContentWithFullModel(fullMessageText, message.From.ID, message.Chat.ID, replyToText)
		full := newModelOutcome(fullRules, fullDetails, fullErr)
		switch {
		case full.contentFilter:
			log.Printf("⚠️ Content filter triggered by full model on new-user double-check for message %d", message.MessageID)
		case full.transientErr:
			log.Printf("Error in AI content analysis (full-model new-user double-check) for message %d: %v", message.MessageID, fullErr)
		case len(full.rules) > 0:
			log.Printf("✅ Full model flagged message %d on new-user double-check (%d rule(s))", message.MessageID, len(full.rules))
		default:
			log.Printf("❌ Full model also cleared message %d on new-user double-check", message.MessageID)
		}
		return b.applyVerdictDecision(message, decideDoubleCheckVerdict(full))
	}

	return nil, false, ""
}

// applyVerdictDecision materialises the terminal side effect of a pure
// verdictDecision and returns the (matchedRules, isContentFilter, decisionDetails)
// tuple in the shape containsBadWords' caller expects.
func (b *Bot) applyVerdictDecision(message *tgbotapi.Message, d verdictDecision) ([]config.ModerationRule, bool, string) {
	switch d.effect {
	case effectClearReaction:
		log.Printf("❌ Full model did NOT confirm bad content in message %d, clearing reaction", message.MessageID)
		b.clearMessageReaction(message.Chat.ID, message.MessageID)
		return nil, false, ""
	case effectPlaceholderRemoved:
		csRules := b.matchContentSecurityRules()
		csDetails := i18n.T("mod.content_security_details")
		if d.cfDetails != "" {
			csDetails += "\n" + d.cfDetails
		}
		b.replaceMessageContentWithPlaceholder(message.MessageID, message.Chat.ID, i18n.T("filter.content_removed"), csDetails)
		return csRules, true, csDetails
	case effectPlaceholderNotSaved:
		csDetails := i18n.T("mod.content_security_details")
		if d.cfDetails != "" {
			csDetails += "\n" + d.cfDetails
		}
		b.replaceMessageContentWithPlaceholder(message.MessageID, message.Chat.ID, "[The message was not saved, because it violated AI content policies.]", csDetails)
		return d.rules, d.isCF, d.details
	default: // effectNone
		return d.rules, d.isCF, d.details
	}
}

// shouldFullModelDoubleCheck reports whether a message the light model cleared
// should also be checked by the full model because the author is still within
// their first N messages in the chat (ai.content_moderation
// .full_model_first_messages). This catches subtle spam from new members that
// the cheaper light model may miss, at the cost of an extra full-model call for
// early messages only. Returns false when the feature is disabled (N <= 0) or
// the user has already posted more than N messages. The current message is
// already recorded by the time moderation runs, so a count of 1 is the user's
// first message.
func (b *Bot) shouldFullModelDoubleCheck(userID, chatID int64) bool {
	n := b.config.AI.ContentModeration.FullModelFirstMessages
	if n <= 0 || userID == 0 || b.db == nil {
		return false
	}
	cnt, err := b.db.CountUserMessagesInChat(userID, chatID)
	if err != nil {
		log.Printf("Full-model double-check: error counting messages for user %d in chat %d: %v", userID, chatID, err)
		return false
	}
	return cnt <= n
}

// recordModStat bumps a moderation funnel counter for today's local date.
// Errors are logged, not propagated, since stats are best-effort and must
// never disrupt moderation.
func (b *Bot) recordModStat(stat string) {
	if b.db == nil {
		return
	}
	day := time.Now().Format("2006-01-02")
	if err := b.db.IncrementModerationStat(stat, day, 1); err != nil {
		log.Printf("Error recording moderation stat %q: %v", stat, err)
	}
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

	// A confirmed bad message reaches this point exactly once (after dedupe).
	b.recordModStat(database.ModStatFullConfirmed)

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

	// Count a single auto-action if any step performs a destructive automatic
	// action (delete/warn/mute). A report-only verdict is left for the admin and
	// does not count as an auto-action.
	for _, step := range plan {
		if step.action == config.ModerationActionDelete || step.action == config.ModerationActionWarn || step.action == config.ModerationActionMute {
			b.recordModStat(database.ModStatAutoAction)
			break
		}
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
