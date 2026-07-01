// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	tgbotapi "gennadium/internal/telegram"
)

// Scope captures, for a single inbound message, the per-(chat, topic) and
// per-user activation flags that the message-processing pipeline consults.
//
// These flags were previously re-evaluated inline at every decision point
// (handleMessage, analyzeMessage, containsBadWords), each call recomputing
// messageTopic and the config scope predicates. Resolving them once into a
// Scope removes that duplication and makes the guards explicit and shared.
//
// Every field is derived from a cheap, side-effect-free predicate. Admin status
// is intentionally NOT included: isUserAdmin performs a Telegram GetChatMember
// API call and is consulted lazily (only when
// ai.content_moderation.skip_admin_users is set), so it must stay out of this
// eagerly-computed struct.
type Scope struct {
	// Topic is messageTopic(message): the forum topic id, or 0 for non-forum
	// chats. Computed once and reused everywhere a (chat, topic) lookup is made.
	Topic int

	// InModerationChat reports whether the chat is one of the configured
	// moderation chats (config.IsModerationChat).
	InModerationChat bool

	// Moderate reports whether AI content moderation applies to this
	// (chat, topic) pair (config.IsModerationActive). A moderation chat whose
	// (chat, topic) is excluded via moderation.excluded_topics has Moderate
	// false but InModerationChat true: the message is still recorded and the
	// other, independently-scoped features still run.
	Moderate bool

	// Summarize / LinkSummary / Creative report whether the respective
	// per-(chat, topic) feature is active (IsMessageSummaryActive /
	// IsLinkSummaryActive / IsCreativeReplyActive). They are independent of
	// Moderate.
	Summarize   bool
	LinkSummary bool
	Creative    bool

	// Whitelisted reports whether the sender is in admin.whitelist_user_ids.
	// False when the message has no sender (From == nil).
	Whitelisted bool

	// IsService reports whether the message is a Telegram service message
	// (joins, pins, topic events, …) rather than user content.
	IsService bool
}

// MsgContext is the per-message value threaded through the inbound processing
// pipeline. It carries the message, whether it is an edit, the resolved Scope,
// and the working state the ordered stages populate and read so downstream
// stages share fields instead of re-deriving them.
type MsgContext struct {
	Msg      *tgbotapi.Message
	IsEdited bool
	Scope    Scope

	// Enhanced is the message text after the enhancement stage: the raw text
	// (or caption), optionally enriched with vision/OCR analysis. It is the text
	// fed to moderation, summaries and length checks.
	Enhanced string
	// EnhancedMsg is a shallow copy of Msg whose Text is Enhanced. Downstream
	// stages pass it (instead of Msg) so recording/moderation/summaries see the
	// enriched text while the original Msg stays untouched.
	EnhancedMsg *tgbotapi.Message
	// Flagged is set by the enhancement stage when the image Content-Safety
	// check flagged the message, routing it straight to moderation.
	Flagged bool
	// Moderated is set by the moderate stage when this message was flagged and
	// acted on by content moderation (an AI rule or the content-safety route).
	// The creative-reply feature reads it to avoid rewarding a rule-breaking
	// message with a friendly reply - replacing the former moderatedMsgs
	// side-channel that wasMessageModerated used to consult.
	Moderated bool

	// AddToDeletion / DeletionPinned are the pre-computed message-deletion-queue
	// flags, bundled into analyzeMessage's single write transaction on the hot
	// path. deletionHandled records whether that bundling already inserted the
	// row so the finalize stage can skip the fallback insert.
	AddToDeletion   bool
	DeletionPinned  bool
	deletionHandled bool
}

// resolveScope computes the activation flags for a message from cheap,
// side-effect-free predicates. It performs no network or DB calls.
func (b *Bot) resolveScope(m *tgbotapi.Message) Scope {
	topic := messageTopic(m)
	s := Scope{
		Topic:            topic,
		InModerationChat: b.config.IsModerationChat(m.Chat.ID),
		Moderate:         b.config.IsModerationActive(m.Chat.ID, topic),
		Summarize:        b.config.IsMessageSummaryActive(m.Chat.ID, topic),
		LinkSummary:      b.config.IsLinkSummaryActive(m.Chat.ID, topic),
		Creative:         b.config.IsCreativeReplyActive(m.Chat.ID, topic),
		IsService:        b.isServiceMessage(m),
	}
	if m.From != nil {
		s.Whitelisted = b.isUserWhitelisted(m.From.ID)
	}
	return s
}

// newInboundContext builds the MsgContext for an inbound message, resolving its
// Scope once. EnhancedMsg defaults to the original message; the enhance stage
// replaces it with the vision/OCR-enriched copy. Defaulting it here lets the
// record/moderate logic operate on mc.EnhancedMsg uniformly, including when a
// caller (e.g. a test) drives analyzeMessage without running the enhance stage.
func (b *Bot) newInboundContext(m *tgbotapi.Message, isEdited bool) *MsgContext {
	return &MsgContext{Msg: m, IsEdited: isEdited, Scope: b.resolveScope(m), EnhancedMsg: m}
}
