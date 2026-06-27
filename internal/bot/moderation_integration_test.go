// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"testing"

	"gennadium/internal/config"

	tgbotapi "gennadium/internal/telegram"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testMessage builds a minimal inbound message in the given chat from the given
// user with the given text - enough for the moderation/auto-action helpers.
func testMessage(chatID int64, userID int64, messageID int, text string) *tgbotapi.Message {
	return &tgbotapi.Message{
		MessageID: messageID,
		Chat:      tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: userID, FirstName: "Test", Username: "tester"},
		Text:      text,
	}
}

// --- matchModerationRules (pure rule dispatch) --------------------------------

func TestMatchModerationRules(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.ContentModeration.Rules = []config.ModerationRule{
		{Trigger: "спам", Action: "report", Description: "spam"},
		{Trigger: "мат", Action: "warn", Description: "profanity"},
		{Trigger: "мат", Action: "report", Description: "profanity report"},
	}

	// First line "мат" matches both "мат" rules in declaration order.
	matched, details := b.matchModerationRules("мат\nthis is the reason")
	require.Len(t, matched, 2)
	assert.Equal(t, "warn", matched[0].Action)
	assert.Equal(t, "report", matched[1].Action)
	assert.Equal(t, "this is the reason", details)

	// "нет" matches nothing.
	matched, _ = b.matchModerationRules("нет")
	assert.Empty(t, matched)

	// Empty response -> nothing.
	matched, _ = b.matchModerationRules("   ")
	assert.Empty(t, matched)
}

func TestMatchModerationRules_LegacyBanNormalized(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.ContentModeration.Rules = []config.ModerationRule{
		{Trigger: "bad", Action: "ban"}, // legacy "ban" -> "mute"
	}
	matched, _ := b.matchModerationRules("bad")
	require.Len(t, matched, 1)
	assert.Equal(t, config.ModerationActionMute, matched[0].Action)
}

func TestMatchContentSecurityRules(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.ContentModeration.Rules = []config.ModerationRule{
		{Trigger: "content-security", Action: "report", Description: "cs"},
		{Trigger: "спам", Action: "report"},
	}
	matched := b.matchContentSecurityRules()
	require.Len(t, matched, 1)
	assert.Equal(t, "report", matched[0].Action)
}

// --- analyzeMessageContentWith{Light,Full}Model via transport -----------------

func moderationConfig(b *Bot) {
	b.config.AI.Enabled = true
	b.config.AI.ContentModeration.Enabled = true
	b.config.AI.ContentModeration.Prompt = config.PromptPair{
		System: "system {{chat_rules}} {{user_profile}} {{reply_to}}",
		User:   "analyze {{message}}",
	}
	b.config.AI.FullModel = fullModelConfigs()
	b.config.AI.LightModel = fullModelConfigs()
	b.config.AI.ContentModeration.Rules = []config.ModerationRule{
		{Trigger: "мат", Action: "warn", Description: "profanity"},
	}
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
}

func TestAnalyzeMessageContentWithLightModel_Match(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"мат\nprofanity detected"}}]}`))
	})
	moderationConfig(b)

	rules, details, err := b.analyzeMessageContentWithLightModel("bad text", 7, -100, "")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "warn", rules[0].Action)
	assert.Equal(t, "profanity detected", details)
}

func TestAnalyzeMessageContentWithFullModel_NoMatch(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)

	rules, _, err := b.analyzeMessageContentWithFullModel("clean text", 7, -100, "")
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestAnalyzeMessageContent_Disabled(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.ContentModeration.Enabled = false
	_, _, err := b.analyzeMessageContentWithLightModel("x", 7, -100, "")
	require.Error(t, err)
}

// --- auto-moderation actions (assert recorded telegram calls) -----------------

func TestAutoModerateDelete(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.ChatID = -999
	rule := &config.ModerationRule{Trigger: "мат", Action: "delete", Description: "profanity"}

	b.autoModerateDelete(testMessage(-100, 7, 55, "bad"), "bad", false, rule)

	// Message was deleted via the port.
	require.Len(t, tg.DeletedIDs, 1)
	assert.Equal(t, [2]int64{-100, 55}, tg.DeletedIDs[0])
	// Action logged.
	actions, err := b.db.GetRecentActions(10)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
	assert.Equal(t, "delete", actions[0].ActionType)
}

func TestAutoModerateWarn(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.ChatID = -999
	// No warning prompt -> falls back to static warn text.
	rule := &config.ModerationRule{Trigger: "мат", Action: "warn", Description: "profanity"}

	b.autoModerateWarn(testMessage(-100, 7, 55, "bad"), "bad", rule)

	// A warning reply was sent to the source chat.
	require.NotEmpty(t, tg.SentMessages)
	var sentToChat bool
	for _, m := range tg.SentMessages {
		if m.ChatID == -100 && m.ReplyToMessageID == 55 {
			sentToChat = true
		}
	}
	assert.True(t, sentToChat, "expected a warning reply in the source chat")

	// Warning recorded in DB.
	has, err := b.db.HasWarningForMessage(7, 55)
	require.NoError(t, err)
	assert.True(t, has)
}

func TestAutoModerateReport(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.ChatID = -999
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	rule := &config.ModerationRule{Trigger: "спам", Action: "report", Description: "spam"}

	b.autoModerateReport(testMessage(-100, 7, 55, "spammy"), "spammy", false, rule, nil, "AI reasoning")

	// A reaction was set and a report card was sent to the admin chat.
	require.NotEmpty(t, tg.Reactions)
	var sentToAdmin bool
	for _, m := range tg.SentMessages {
		if m.ChatID == -999 {
			sentToAdmin = true
		}
	}
	assert.True(t, sentToAdmin, "expected a moderation card in the admin chat")
}

func TestNotifyAutoAction_RespectsNotifyAdmin(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Admin.ChatID = -999

	off := false
	ruleOff := &config.ModerationRule{Trigger: "x", NotifyAdmin: &off}
	b.notifyAutoAction(ruleOff, "should not send")
	assert.Empty(t, tg.SentMessages)

	on := true
	ruleOn := &config.ModerationRule{Trigger: "x", NotifyAdmin: &on}
	b.notifyAutoAction(ruleOn, "should send")
	require.Len(t, tg.SentMessages, 1)
	assert.Equal(t, int64(-999), tg.SentMessages[0].ChatID)
}

func TestBotActionContext(t *testing.T) {
	b, _ := newMockBot(t)
	b.botSelf.Username = "thebot"
	ctx := b.botActionContext(testMessage(-100, 7, 55, "x"))
	assert.Equal(t, int64(7), ctx.userID)
	assert.Equal(t, int64(-100), ctx.chatID)
	assert.Equal(t, 55, ctx.messageID)
	assert.Equal(t, b.botSelf.ID, ctx.adminID)
	assert.Equal(t, "thebot", ctx.adminName)
}
