// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file specifies how the BOT BEHAVES when it moderates a chat - written as
// executable scenarios ("when a user does X, the bot does Y") rather than as
// coverage of individual helpers. The goal is to pin down the observable
// contract of the moderation pipeline so a future refactor that quietly changes
// what the bot does to a flagged message, a trusted user, or a repeat offender
// fails loudly here.
//
// The unit under test is mostly handleBadWordDetected - the orchestrator that
// turns the set of rules the AI matched into a concrete plan of Telegram
// actions (delete / warn / mute / report) - plus the immunity rules in
// analyzeMessage and the end-to-end inbound → AI → action path.

// behaviorBot builds a bot wired for moderation scenarios: a mock Telegram
// client, a temp DB, an admin chat (-999) and a single moderation chat (-100).
// It also initializes the moderation-dedup map that production New() sets up but
// newMockBot does not.
func behaviorBot(t *testing.T) (*Bot, *mockTelegram) {
	t.Helper()
	b, tg := newMockBot(t)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.Admin.ChatID = -999
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	return b, tg
}

// adminCardSent reports whether any message was sent to the admin chat.
func adminCardSent(tg *mockTelegram, adminChatID int64) bool {
	for _, m := range tg.SentMessages {
		if m.ChatID == adminChatID {
			return true
		}
	}
	return false
}

// ── When the AI flags a message, the bot dispatches the configured action ──

// A "delete" rule must remove the offending message and record the action so it
// shows up in the moderation log / web UI.
func TestBehavior_FlaggedDeleteRule_RemovesMessageAndLogsAction(t *testing.T) {
	b, tg := behaviorBot(t)
	rules := []config.ModerationRule{{Trigger: "spam", Action: "delete", Description: "obvious spam"}}

	b.handleBadWordDetected(testMessage(-100, 7, 55, "buy now"), "buy now", false, rules, "")

	require.Len(t, tg.DeletedIDs, 1, "the flagged message must be deleted")
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])

	actions, err := b.db.GetRecentActions(10)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
	assert.Equal(t, "delete", actions[0].ActionType)
}

// A "warn" rule must warn the user in the chat and persist the warning so a
// repeat offender's history is tracked.
func TestBehavior_FlaggedWarnRule_WarnsUserAndRecordsWarning(t *testing.T) {
	b, tg := behaviorBot(t)
	rules := []config.ModerationRule{{Trigger: "rude", Action: "warn", Description: "rudeness"}}

	b.handleBadWordDetected(testMessage(-100, 7, 55, "you fool"), "you fool", false, rules, "")

	// A warning reply was delivered into the source chat, addressed to the message.
	var warnedInChat bool
	for _, m := range tg.SentMessages {
		if m.ChatID == -100 && m.ReplyToMessageID == 55 {
			warnedInChat = true
		}
	}
	assert.True(t, warnedInChat, "the user must be warned in the chat where they misbehaved")

	has, err := b.db.HasWarningForMessage(7, 55)
	require.NoError(t, err)
	assert.True(t, has, "the warning must be recorded for the user's history")
}

// A "mute" rule must mute the user (both a DB record and a real Telegram
// restriction) so they cannot keep posting.
func TestBehavior_FlaggedMuteRule_MutesUserInChat(t *testing.T) {
	b, tg := behaviorBot(t)
	b.config.AI.ContentModeration.DefaultMuteMinutes = 30
	rules := []config.ModerationRule{{Trigger: "slur", Action: "mute", Description: "hate speech"}}

	b.handleBadWordDetected(testMessage(-100, 7, 55, "a slur"), "a slur", false, rules, "")

	// The user is restricted in Telegram...
	require.Len(t, tg.Restrictions, 1, "the user must be restricted via Telegram")
	assert.Equal(t, int64(7), tg.Restrictions[0].UserID)
	assert.False(t, tg.Restrictions[0].Permissions.CanSendMessages)

	// ...and the mute is persisted so it survives a restart / is visible in the UI.
	muted, err := b.db.IsUserMuted(7, -100)
	require.NoError(t, err)
	assert.True(t, muted, "the mute must be persisted")
}

// Stacking rules expresses combined actions: a "warn" + "report" pair must run
// BOTH - the user is warned AND the admins get a card - in declaration order.
func TestBehavior_WarnPlusReport_DispatchesBothActions(t *testing.T) {
	b, tg := behaviorBot(t)
	rules := []config.ModerationRule{
		{Trigger: "borderline", Action: "warn", Description: "warn first"},
		{Trigger: "borderline", Action: "report", Description: "and escalate"},
	}

	b.handleBadWordDetected(testMessage(-100, 7, 55, "borderline content"), "borderline content", false, rules, "AI reasoning")

	// Warn happened (recorded).
	has, err := b.db.HasWarningForMessage(7, 55)
	require.NoError(t, err)
	assert.True(t, has, "the warn action must run")

	// Report happened (admin card).
	assert.True(t, adminCardSent(tg, -999), "the report action must also run and notify admins")
}

// Safety default: if the AI flagged a message but produced no recognizable
// action (e.g. a content-filter hit with no matching rule), the bot must NOT
// silently ignore it - it falls back to reporting to the admins.
func TestBehavior_FlaggedWithoutAction_FallsBackToReport(t *testing.T) {
	b, tg := behaviorBot(t)

	// No matched rules + content-filter flag → fallback report.
	b.handleBadWordDetected(testMessage(-100, 7, 55, "filtered"), "filtered", true, nil, "blocked by safety filter")

	assert.True(t, adminCardSent(tg, -999),
		"a flagged message with no actionable rule must still be escalated to admins")
}

// Idempotency: the same message must not be moderated twice in quick
// succession. A duplicate AI verdict (e.g. an edit event re-triggering) must be
// ignored so the user isn't warned/muted/reported repeatedly for one message.
func TestBehavior_SameMessageFlaggedTwice_IsModeratedOnce(t *testing.T) {
	b, tg := behaviorBot(t)
	rules := []config.ModerationRule{{Trigger: "spam", Action: "delete"}}
	msg := testMessage(-100, 7, 55, "buy now")

	b.handleBadWordDetected(msg, "buy now", false, rules, "")
	b.handleBadWordDetected(msg, "buy now", false, rules, "") // duplicate verdict

	assert.Len(t, tg.DeletedIDs, 1, "a message must be acted on at most once within the dedup window")
}

// Different messages from the same user are independent - dedup is per message,
// not per user, so a serial spammer is moderated on every message.
func TestBehavior_DifferentMessages_AreEachModerated(t *testing.T) {
	b, tg := behaviorBot(t)
	rules := []config.ModerationRule{{Trigger: "spam", Action: "delete"}}

	b.handleBadWordDetected(testMessage(-100, 7, 55, "buy now"), "buy now", false, rules, "")
	b.handleBadWordDetected(testMessage(-100, 7, 56, "buy more"), "buy more", false, rules, "")

	assert.Len(t, tg.DeletedIDs, 2, "each distinct message must be moderated")
}

// ── Trusted users are immune from automated moderation ──

// A whitelisted user's bad message must NOT be analyzed or acted upon - the bot
// stores it for context but never calls the AI or takes any action.
func TestBehavior_WhitelistedUser_BadMessage_IsNotModerated(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		// If this fires, the bot wrongly analyzed a trusted user's message.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"spam\nflagged"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.Admin.WhitelistUserIDs = []int64{7}

	b.analyzeMessage(testMessage(-100, 7, 55, "obvious spam"), false, incomingRecordOpts{})

	assert.Equal(t, 0, rt.count(), "a whitelisted user's message must never reach the AI")
	assert.Empty(t, tg.DeletedIDs, "a whitelisted user's message must never be deleted")
	assert.Empty(t, tg.Restrictions, "a whitelisted user must never be muted")

	// The message is still stored for reply/history context.
	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	assert.NotNil(t, stored)
}

// When SkipAdminUsers is enabled, an admin's bad message is likewise left alone.
func TestBehavior_AdminUser_BadMessage_IsSkippedWhenConfigured(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"spam\nflagged"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.AI.ContentModeration.SkipAdminUsers = true
	b.config.Admin.SuperAdminUserID = 7 // user 7 is the super-admin

	b.analyzeMessage(testMessage(-100, 7, 55, "obvious spam"), false, incomingRecordOpts{})

	assert.Equal(t, 0, rt.count(), "an admin's message must not be analyzed when SkipAdminUsers is on")
	assert.Empty(t, tg.DeletedIDs)
}

// ── The bot never moderates itself ──

// The bot's own messages are stored for reply-chain context but never run
// through moderation.
func TestBehavior_BotOwnMessage_IsStoredNotModerated(t *testing.T) {
	b, tg := behaviorBot(t)
	b.config.AI.Enabled = true
	b.config.AI.ContentModeration.Enabled = true

	own := testMessage(-100, b.botSelf.ID, 55, "anything")
	b.handleMessage(own, false)

	assert.Empty(t, tg.DeletedIDs, "the bot must not delete its own message")
	assert.Empty(t, tg.Reactions, "the bot must not react to its own message")
}

// ── End to end: an inbound message that the AI flags gets acted on ──

// The strongest behavior spec: a real inbound message in a moderation chat,
// flagged by the (mocked) AI against a "delete" rule, results in the message
// being deleted and the action logged - exercising the whole
// analyzeMessage → containsBadWords → handleBadWordDetected path.
func TestBehavior_InboundMessage_AIFlagsIt_GetsDeleted(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		// Both the light and the confirming full model return the delete trigger.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"spam\ndetected spam"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.AI.ContentModeration.Rules = []config.ModerationRule{
		{Trigger: "spam", Action: "delete", Description: "spam"},
	}

	b.analyzeMessage(testMessage(-100, 7, 55, "buy cheap stuff now"), false, incomingRecordOpts{})

	require.Len(t, tg.DeletedIDs, 1, "an AI-flagged message must be deleted end to end")
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])

	actions, err := b.db.GetRecentActions(10)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
	assert.Equal(t, "delete", actions[0].ActionType)
}

// A clean message that the AI does not flag must pass through untouched: stored
// for context, no actions, no admin noise.
func TestBehavior_InboundCleanMessage_IsLeftAlone(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`)) // "no" = clean
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)

	b.analyzeMessage(testMessage(-100, 7, 55, "hello everyone"), false, incomingRecordOpts{})

	assert.Empty(t, tg.DeletedIDs, "a clean message must not be deleted")
	assert.Empty(t, tg.Restrictions, "a clean message must not get the user muted")
	assert.False(t, adminCardSent(tg, -999), "a clean message must not bother the admins")

	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	assert.NotNil(t, stored, "the clean message is still stored for context")
}

// ── A cruel-muted user is silenced without ceremony ──

// A cruel mute means: the user is not Telegram-restricted, but every message
// they send is deleted on arrival and nothing else happens (no report, no
// warning). This is the documented "shadow silence" behavior.
func TestBehavior_CruelMutedUser_MessageIsSilentlyDeleted(t *testing.T) {
	b, tg := behaviorBot(t)
	require.NoError(t, b.db.AddMutedUser(&database.MutedUser{
		UserID: 7, ChatID: -100, MutedBy: 1, MutedAt: time.Now(),
		UnmuteAt: time.Now().Add(time.Hour), IsActive: true, IsCruel: true,
	}))

	handled := b.handleCruelMuteIfActive(testMessage(-100, 7, 55, "let me speak"))

	assert.True(t, handled, "a cruel-muted user's message must be intercepted")
	require.Len(t, tg.DeletedIDs, 1, "the message must be silently deleted")
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
	assert.False(t, adminCardSent(tg, -999), "a cruel mute must not generate admin noise")
}

// A normally-muted (non-cruel) user is NOT subject to the silent-delete path -
// their restriction is enforced by Telegram, not by the bot deleting messages.
func TestBehavior_NormallyMutedUser_MessageNotSilentlyDeleted(t *testing.T) {
	b, tg := behaviorBot(t)
	require.NoError(t, b.db.AddMutedUser(&database.MutedUser{
		UserID: 7, ChatID: -100, MutedBy: 1, MutedAt: time.Now(),
		UnmuteAt: time.Now().Add(time.Hour), IsActive: true, IsCruel: false,
	}))

	handled := b.handleCruelMuteIfActive(testMessage(-100, 7, 55, "hi"))

	assert.False(t, handled, "a regular mute must not trigger the cruel-mute delete path")
	assert.Empty(t, tg.DeletedIDs)
}
