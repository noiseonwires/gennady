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

// The WebUI "debug moderation" feature renders the moderation prompt for
// previewing. It must substitute the {{new_user_rules}} placeholder just like
// the live pipeline - otherwise admins see the raw placeholder in the rendered
// prompt (the bug this guards against).

func debugBot(t *testing.T) (*Bot, *redirectTransport) {
	t.Helper()
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	})
	b.config.AI.Enabled = true
	b.config.AI.FullModel = fullModelConfigs()
	b.config.AI.ContentModeration.Enabled = true
	b.config.AI.ContentModeration.Prompt = config.PromptPair{
		System: "system rules: {{chat_rules}}",
		User:   "msg: {{message}}\nnewbie: {{new_user_rules}}",
	}
	b.config.AI.ContentModeration.NewUserRules = "BE STRICT WITH NEWCOMERS"
	return b, rt
}

func TestDebugModerationPrompt_RendersNewUserRules(t *testing.T) {
	b, _ := debugBot(t)

	_, userPrompt, _, err := b.DebugModerationPrompt("openai_full:gpt-test", "hi there")
	require.NoError(t, err)
	// The ad-hoc debugger previews the configured rules and never leaves the
	// placeholder literal.
	assert.Contains(t, userPrompt, "BE STRICT WITH NEWCOMERS")
	assert.NotContains(t, userPrompt, "{{new_user_rules}}")
}

func TestDebugModerationByMessageID_RendersNewUserRulesForNewUser(t *testing.T) {
	b, _ := debugBot(t)

	// Seed a stored message whose author's first-seen is "now" (new user).
	now := b.timeNow()
	_, err := b.db.RecordIncomingMessage(
		&database.MessageInfo{MessageID: 4242, ChatID: -100, UserID: 77, Username: "newbie", Text: "hello", Timestamp: now},
		database.IncomingMessageOpts{TrackProfile: true, Username: "newbie", DisplayName: "Newbie"},
	)
	require.NoError(t, err)

	_, userPrompt, _, info, err := b.DebugModerationByMessageID("openai_full:gpt-test", 4242, -100)
	require.NoError(t, err)
	assert.Contains(t, userPrompt, "BE STRICT WITH NEWCOMERS")
	assert.NotContains(t, userPrompt, "{{new_user_rules}}")
	assert.Equal(t, "BE STRICT WITH NEWCOMERS", info["new_user_rules"])
}

func TestDebugModerationByMessageID_NoRulesForEstablishedUser(t *testing.T) {
	b, _ := debugBot(t)

	// First-seen well outside the default 24h new-user window → not new, so the
	// placeholder renders empty (never literal).
	old := b.timeNow().Add(-72 * time.Hour)
	_, err := b.db.RecordIncomingMessage(
		&database.MessageInfo{MessageID: 4243, ChatID: -100, UserID: 88, Username: "veteran", Text: "hello", Timestamp: old},
		database.IncomingMessageOpts{TrackProfile: true, Username: "veteran", DisplayName: "Veteran"},
	)
	require.NoError(t, err)

	_, userPrompt, _, info, err := b.DebugModerationByMessageID("openai_full:gpt-test", 4243, -100)
	require.NoError(t, err)
	assert.NotContains(t, userPrompt, "BE STRICT WITH NEWCOMERS")
	assert.NotContains(t, userPrompt, "{{new_user_rules}}")
	assert.Equal(t, "", info["new_user_rules"])
}
