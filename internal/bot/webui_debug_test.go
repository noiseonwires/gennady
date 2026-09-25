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

func TestDebugCreativeReply_ChatCompletions(t *testing.T) {
	b, rt := debugBot(t)
	b.config.AI.CreativeReplies.Prompt = config.PromptPair{
		System: "be witty",
		User:   "reply to {{message}}",
	}

	systemPrompt, userPrompt, response, details, err := b.DebugCreativeReply("openai_full:gpt-test", "hello", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, "be witty", systemPrompt)
	assert.Equal(t, "reply to hello", userPrompt)
	assert.Equal(t, "ok", response)
	assert.Equal(t, "chat_completions", details["api"])
	assert.Equal(t, false, details["web_search_attempted"])
	assert.Contains(t, rt.last().Path, "/chat/completions")
}

func TestDebugCreativeReply_WebSearchDetails(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(webSearchSuccessResponse))
	})
	b.config.AI.Enabled = true
	b.config.AI.CreativeReplies.WebSearchEnabled = true
	b.config.AI.CreativeReplies.Prompt = config.PromptPair{System: "be current", User: "answer {{message}}"}
	model := fullModelConfig()
	model.UseForWebSearch = true
	b.config.AI.FullModel = config.AIModelConfigs{Configs: []config.AIModelConfig{model}}

	_, _, response, details, err := b.DebugCreativeReply("openai_full:gpt-test", "what happened?", 0, 0)
	require.NoError(t, err)
	assert.Contains(t, response, "Current answer.")
	assert.Equal(t, "responses", details["api"])
	assert.Equal(t, true, details["web_search_attempted"])
	assert.Equal(t, true, details["web_search_used"])
	assert.NotEmpty(t, details["tool_calls"])
	assert.NotEmpty(t, details["citations"])
	assert.NotEmpty(t, details["reasoning_summaries"])
	assert.NotEmpty(t, details["usage"])
	assert.Contains(t, rt.last().Path, "/responses")
}

func TestDebugCreativeReply_MessageToolDetails(t *testing.T) {
	var calls int
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{"output":[{"type":"function_call","call_id":"call_1","name":"find_messages_from_user","arguments":"{\"user\":\"7\",\"limit\":5,\"offset\":0}"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}`))
	})
	b.config.AI.Enabled = true
	b.config.AI.CreativeReplies.MessageToolsEnabled = true
	b.config.AI.CreativeReplies.MessageToolsMaxLimit = 5
	enabled := true
	b.config.AI.CreativeReplies.ContextOnlyCurrentTopic = &enabled
	b.config.AI.CreativeReplies.Prompt = config.PromptPair{System: "be helpful", User: "answer {{message}}"}
	b.config.AI.FullModel = fullModelConfigs()
	storeMessageToolTestMessage(t, b, 1, -100, 7, "alice", "history", 42, time.Now())
	storeMessageToolTestMessage(t, b, 2, -100, 7, "alice", "other topic history", 99, time.Now())

	_, _, response, details, err := b.DebugCreativeReply("openai_full:gpt-test", "question", -100, 42)
	require.NoError(t, err)
	assert.Equal(t, "done", response)
	assert.Equal(t, true, details["message_tools_attempted"])
	toolCalls, ok := details["message_tool_calls"].([]messageToolExecutionInfo)
	require.True(t, ok)
	require.Len(t, toolCalls, 1)
	assert.Contains(t, toolCalls[0].Output, "history")
	assert.NotContains(t, toolCalls[0].Output, "other topic history")
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
