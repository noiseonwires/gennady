// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"log"
	"unicode/utf8"

	"gennadium/internal/i18n"
)

// Direction is the control-flow signal a pipeline stage returns: Continue to
// run the next stage, or Stop to end processing for this message.
type Direction int

const (
	Continue Direction = iota
	Stop
)

// stage is one named step of the moderation ingest pipeline. Stages operate on
// the shared *MsgContext and short-circuit by returning Stop.
type stage struct {
	name string
	run  func(*MsgContext) Direction
}

// feature is one optional, independently-scoped side feature (message summary,
// link extraction, creative reply). applies is the coarse "is this feature
// switched on" gate; run performs the per-message work synchronously - the
// features stage runs all applicable features together in a single background
// goroutine. Features are data: adding one means appending to features().
type feature struct {
	name    string
	applies func(*MsgContext) bool
	run     func(*MsgContext)
}

// moderationStages returns the ordered ingest pipeline applied to every message
// in a moderation chat. The order is significant:
//
//	dump → cruel-mute → prepare-deletion → enhance → moderate
//	     → features → finalize-deletion
//
// Recording/moderation (enhance + moderate) runs synchronously before the
// features stage so that, by the time the features stage launches its single
// background goroutine, the message is recorded and the moderation outcome is
// captured on mc.Moderated. The slice is built once (cached) per Bot.
func (b *Bot) moderationStages() []stage {
	b.stagesOnce.Do(func() {
		b.stagesCache = []stage{
			{"dump", b.stageDumpModeration},
			{"cruel_mute", b.stageCruelMute},
			{"prepare_deletion", b.stagePrepareDeletion},
			{"enhance", b.stageEnhance},
			{"moderate", b.stageModerate},
			{"features", b.stageFeatures},
			{"finalize_deletion", b.stageFinalizeDeletion},
		}
	})
	return b.stagesCache
}

// runModerationPipeline runs the ordered moderation stages over mc, stopping
// early if any stage returns Stop.
func (b *Bot) runModerationPipeline(mc *MsgContext) {
	for _, s := range b.moderationStages() {
		b.debugf("moderation pipeline: stage %s for message %d", s.name, mc.Msg.MessageID)
		if s.run(mc) == Stop {
			b.debugf("moderation pipeline: stopped at stage %s for message %d", s.name, mc.Msg.MessageID)
			return
		}
	}
}

// stageDumpModeration writes the raw message to a debug dump file when
// debug.dump_moderation_messages is set.
func (b *Bot) stageDumpModeration(mc *MsgContext) Direction {
	if b.config.Debug.DumpModerationMessages {
		b.dumpMessageToFile(mc.Msg, "moderation", mc.IsEdited)
	}
	return Continue
}

// stageCruelMute enforces an active cruel mute: it silently deletes the message
// and stops all further processing. Edits are exempt.
func (b *Bot) stageCruelMute(mc *MsgContext) Direction {
	if !mc.IsEdited && b.handleCruelMuteIfActive(mc.Msg) {
		return Stop
	}
	return Continue
}

// stagePrepareDeletion pre-computes the message-deletion-queue flags so the
// insert can be bundled into analyzeMessage's single write transaction on the
// hot path. Messages from excluded users are pinned (never auto-deleted).
func (b *Bot) stagePrepareDeletion(mc *MsgContext) Direction {
	mc.AddToDeletion = b.config.MessageDeletion.Enabled && !mc.Scope.IsService && b.shouldAddToDeleteQueue(mc.Msg)
	if mc.AddToDeletion && mc.Msg.From != nil {
		for _, excludedUserID := range b.config.MessageDeletion.ExcludedUserIDs {
			if mc.Msg.From.ID == excludedUserID {
				mc.DeletionPinned = true
				log.Printf("Message %d from excluded user %d will be marked as pinned (never deleted)", mc.Msg.MessageID, excludedUserID)
				break
			}
		}
	}
	return Continue
}

// stageEnhance resolves the text fed to moderation: the raw text (or caption),
// optionally enriched with vision/OCR analysis. Vision/OCR and the
// Content-Safety image route are moderation-only and skipped when the
// (chat, topic) is excluded from moderation. It builds EnhancedMsg from the
// resulting text so later stages operate on the enriched copy.
//
// Moderation exclusion (moderation.excluded_topics, incl. topic: -1 for a whole
// chat) only turns off AI moderation; the message is still recorded and the
// other, independently-scoped features still run. Each of those self-gates on
// its own scope predicate, and the moderate stage self-gates on Scope.Moderate.
func (b *Bot) stageEnhance(mc *MsgContext) Direction {
	enhancedText := mc.Msg.Text
	if enhancedText == "" {
		enhancedText = mc.Msg.Caption
	}

	skipVision := mc.Scope.Whitelisted && !b.config.AI.DailySummary.Enabled
	if mc.Scope.Moderate && b.config.AI.Enabled && !skipVision && (b.config.AI.ContentModeration.VisionEnabled || b.config.AI.ContentModeration.OCRSpaceEnabled) {
		enhancedText, mc.Flagged = b.processMessageEnhancements(mc.Msg)
	}

	mc.Enhanced = enhancedText
	enhanced := *mc.Msg
	enhanced.Text = enhancedText
	mc.EnhancedMsg = &enhanced
	return Continue
}

// stageModerate records the message and runs moderation. A Content-Safety
// flagged image is routed straight to handleBadWordDetected; otherwise
// analyzeMessage records the message (bundling the deletion-queue insert into
// the same transaction) and self-gates its moderation step on Scope.Moderate.
func (b *Bot) stageModerate(mc *MsgContext) Direction {
	if mc.Flagged {
		// Only reachable for non-excluded (chat, topic): the vision call in the
		// enhance stage is gated on Scope.Moderate.
		log.Printf("⚠️ Content Safety flagged image in message %d, routing to moderation", mc.Msg.MessageID)
		csRules := b.matchContentSecurityRules()
		if len(csRules) > 0 {
			csDetails := i18n.T("mod.content_security_details")
			b.handleBadWordDetected(mc.EnhancedMsg, mc.Enhanced, true, csRules, csDetails)
			mc.Moderated = true
		} else {
			log.Printf("⚠️ Content Safety flagged message %d but no content-security rules configured, skipping", mc.Msg.MessageID)
		}
		return Continue
	}

	mc.Moderated = b.analyzeMessage(mc)
	if !mc.IsEdited && mc.AddToDeletion {
		mc.deletionHandled = true
	}
	return Continue
}

// stageFeatures launches the applicable side features in a SINGLE background
// goroutine (off the moderation hot path). Running them together in one
// goroutine - rather than one goroutine per feature - keeps goroutine and
// allocation churn low. Recording and moderation have already completed, so the
// features can rely on mc.Moderated and on the link feature having stored its
// extracted link content before the creative reply runs.
func (b *Bot) stageFeatures(mc *MsgContext) Direction {
	var active []feature
	for _, f := range b.features() {
		if f.applies(mc) {
			active = append(active, f)
		}
	}
	if len(active) > 0 {
		go b.runFeatures(mc, active)
	}
	return Continue
}

// runFeatures runs the active side features sequentially in their own goroutine.
// link_summary precedes creative_reply so the link feature's extracted content
// (stored on the message's extra_info) is available to the creative reply's
// reply-chain context.
func (b *Bot) runFeatures(mc *MsgContext, active []feature) {
	for _, f := range active {
		b.debugf("feature %s for message %d", f.name, mc.Msg.MessageID)
		f.run(mc)
	}
}

// features is the ordered registry of optional side features applied after a
// message has been recorded and moderated. The slice is built once (cached).
func (b *Bot) features() []feature {
	b.featOnce.Do(func() {
		b.featCache = []feature{
			{
				name:    "message_summary",
				applies: func(mc *MsgContext) bool { return !mc.IsEdited && b.config.AI.Enabled && b.config.AI.MessageSummaries.Enabled },
				run:     b.runMessageSummary,
			},
			// link_summary must precede creative_reply so the extracted link content
			// (stored on the message's extra_info by link_summary) is available to the
			// creative reply's reply-chain context.
			{
				name:    "link_summary",
				applies: func(mc *MsgContext) bool { return !mc.IsEdited && b.config.AI.Enabled && b.config.AI.LinkSummaries.Enabled },
				run:     b.runLinkSummary,
			},
			{
				name:    "creative_reply",
				applies: func(mc *MsgContext) bool { return !mc.IsEdited && b.config.AI.Enabled && b.config.AI.CreativeReplies.Enabled },
				run:     b.runCreativeReply,
			},
		}
	})
	return b.featCache
}

// runMessageSummary generates the AI message summary when the message is long
// enough and summaries are active for the (chat, topic) and user. It runs
// synchronously within the shared features goroutine.
func (b *Bot) runMessageSummary(mc *MsgContext) {
	isExcludedFromSummary := !mc.Scope.Summarize
	isExcludedUser := mc.Msg.From != nil && b.isMessageSummaryExcludedUser(mc.Msg.From.ID)
	if !mc.IsEdited && utf8.RuneCountInString(mc.Enhanced) > b.config.AI.MessageSummaries.MinLength && !isExcludedFromSummary && !isExcludedUser {
		b.generateMessageSummary(mc.EnhancedMsg)
	} else if isExcludedFromSummary {
		log.Printf("Skipping AI summary for message %d - (chat, topic) excluded from summaries", mc.Msg.MessageID)
	} else if isExcludedUser {
		log.Printf("Skipping AI summary for message %d - user %d is excluded", mc.Msg.MessageID, mc.Msg.From.ID)
	}
}

// runLinkSummary extracts and stores link content - in the message's extra_info,
// used both for moderation context and for the creative reply's reply-chain
// context - and posts link summaries when configured. It runs synchronously
// within the shared features goroutine, before the creative-reply feature, so
// the extracted content is in place when the creative reply builds its context.
func (b *Bot) runLinkSummary(mc *MsgContext) {
	b.extractAndStoreLinksContent(mc.Msg)
}

// runCreativeReply posts a creative follow-up reply. It runs after the
// link-summary feature, so any extracted link content is already on the
// message's extra_info and feeds the reply-chain context the reply is built
// from. Runs synchronously within the shared features goroutine.
func (b *Bot) runCreativeReply(mc *MsgContext) {
	b.creativeReply(mc.Msg, mc.EnhancedMsg, mc.Moderated)
}

// stageFinalizeDeletion performs the standalone deletion-queue insert when the
// moderate stage did not already bundle it, and records pinned service
// messages.
func (b *Bot) stageFinalizeDeletion(mc *MsgContext) Direction {
	if mc.AddToDeletion && !mc.deletionHandled {
		if err := b.db.AddMessageForDeletionWithPinnedStatus(mc.Msg.MessageID, mc.Msg.Chat.ID, mc.DeletionPinned); err != nil {
			log.Printf("Error adding message for deletion: %v", err)
		}
		return Continue
	}
	if mc.Scope.IsService {
		log.Printf("Message %d is a service message - not adding to deletion queue", mc.Msg.MessageID)
		if mc.Msg.PinnedMessage != nil {
			if err := b.db.MarkMessageAsPinned(mc.Msg.PinnedMessage.MessageID, mc.Msg.Chat.ID, true); err != nil {
				log.Printf("Error marking message %d as pinned: %v", mc.Msg.PinnedMessage.MessageID, err)
			} else {
				log.Printf("Marked message %d as pinned in chat %d", mc.Msg.PinnedMessage.MessageID, mc.Msg.Chat.ID)
			}
		}
	}
	return Continue
}
