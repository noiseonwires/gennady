// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storeMessageToolTestMessage(t *testing.T, b *Bot, messageID int, chatID, userID int64, username, text string, topicID int, timestamp time.Time) {
	t.Helper()
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: messageID, ChatID: chatID, UserID: userID, Username: username,
		Text: text, MessageThreadID: topicID, Timestamp: timestamp,
	}))
}

func decodeMessageToolOutput(t *testing.T, raw string) messageToolOutput {
	t.Helper()
	var output messageToolOutput
	require.NoError(t, json.Unmarshal([]byte(raw), &output), raw)
	return output
}

func TestExecuteMessageHistoryTools_AreBoundedAndScoped(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	storeMessageToolTestMessage(t, b, 1, -100, 7, "alice", "first literal 100%", 42, base)
	storeMessageToolTestMessage(t, b, 2, -100, 8, "bob", "second needle", 42, base.Add(time.Minute))
	storeMessageToolTestMessage(t, b, 3, -100, 7, "alice", "third needle", 99, base.Add(2*time.Minute))
	storeMessageToolTestMessage(t, b, 4, -200, 7, "alice", "other chat needle", 42, base.Add(3*time.Minute))
	_, err := b.db.RecordIncomingMessage(&database.MessageInfo{
		MessageID: 1, ChatID: -100, UserID: 7, Username: "@Alice", Text: "first literal 100%",
		MessageThreadID: 42, Timestamp: base,
	}, database.IncomingMessageOpts{
		TrackProfile: true, Username: "alice", DisplayName: "Alice Wonderland", DayDate: base.Format("2006-01-02"),
	})
	require.NoError(t, err)
	scope := messageToolScope{ChatID: -100, TopicID: 42, MaxLimit: 1}

	tests := []struct {
		name      string
		tool      string
		arguments string
		wantIDs   []int
		wantTotal int
		wantTopic *int
	}{
		{
			name: "user current chat", tool: "find_messages_from_user",
			arguments: `{"user":"7","limit":50,"offset":0}`,
			wantIDs:   []int{3}, wantTotal: 2,
		},
		{
			name: "username current chat", tool: "find_messages_from_user",
			arguments: `{"user":"@ALICE","limit":50,"offset":0}`,
			wantIDs:   []int{3}, wantTotal: 2,
		},
		{
			name: "display name current chat", tool: "find_messages_from_user",
			arguments: `{"user":"Alice Wonderland","limit":50,"offset":0}`,
			wantIDs:   []int{3}, wantTotal: 2,
		},
		{
			name: "current topic", tool: "list_messages_in_current_topic",
			arguments: `{"limit":50,"offset":1}`,
			wantIDs:   []int{1}, wantTotal: 2, wantTopic: &scope.TopicID,
		},
		{
			name: "literal text", tool: "find_messages_containing_text",
			arguments: `{"query":"100%","limit":50,"offset":0}`,
			wantIDs:   []int{1}, wantTotal: 1,
		},
		{
			name: "time range", tool: "find_messages_in_chat_between_times",
			arguments: `{"start_time":"2026-09-09T10:00:30Z","end_time":"2026-09-09T10:02:30Z","limit":50,"offset":0}`,
			wantIDs:   []int{3}, wantTotal: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := decodeMessageToolOutput(t, b.executeMessageHistoryTool(test.tool, test.arguments, scope))
			assert.Equal(t, -100, int(output.ChatID))
			assert.Equal(t, 1, output.Limit, "configured maximum must clamp model request")
			assert.Equal(t, test.wantTotal, output.Total)
			assert.Equal(t, test.wantTopic, output.TopicID)
			ids := make([]int, 0, len(output.Messages))
			for _, message := range output.Messages {
				ids = append(ids, message.MessageID)
				assert.Equal(t, int64(-100), scope.ChatID)
			}
			assert.Equal(t, test.wantIDs, ids)
		})
	}
}

func TestCallResponsesWithMessageTools_FunctionLoop(t *testing.T) {
	var requests int32
	var followUpBody string
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if atomic.AddInt32(&requests, 1) == 1 {
			assert.Contains(t, string(body), `"name":"list_messages_in_current_topic"`)
			assert.Contains(t, string(body), `"maximum":2`)
			_, _ = w.Write([]byte(`{
				"output":[{"type":"function_call","call_id":"call_1","name":"list_messages_in_current_topic","arguments":"{\"limit\":20,\"offset\":0}"}],
				"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}
			}`))
			return
		}
		followUpBody = string(body)
		_, _ = w.Write([]byte(`{
			"output":[{"type":"message","content":[{"type":"output_text","text":"I found the message."}]}],
			"usage":{"input_tokens":15,"output_tokens":5,"total_tokens":20}
		}`))
	})
	storeMessageToolTestMessage(t, b, 10, -100, 7, "alice", "tool result text", 42, time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC))
	b.config.AI.CreativeReplies.MessageToolsEnabled = true
	model := fullModelConfig()

	result, err := b.callResponsesWithMessageTools(
		"creative_reply", "find recent context", "be helpful", model,
		messageToolScope{ChatID: -100, TopicID: 42, MaxLimit: 2}, false,
	)
	require.NoError(t, err)
	assert.Equal(t, "I found the message.", result.Text)
	require.Len(t, result.MessageToolCalls, 1)
	assert.Equal(t, "list_messages_in_current_topic", result.MessageToolCalls[0].Name)
	assert.Contains(t, result.MessageToolCalls[0].Output, "tool result text")
	assert.Contains(t, followUpBody, `"type":"function_call_output"`)
	assert.Contains(t, followUpBody, `"call_id":"call_1"`)
	assert.Contains(t, followUpBody, "tool result text")
	assert.Equal(t, 2, rt.count())
	require.NotNil(t, result.Usage)
	assert.Equal(t, int64(25), result.Usage.PromptTokens)
}

func TestCreativeMessageToolScope_UsesStoredTopicAndConfiguredLimit(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.CreativeReplies.MessageToolsMaxLimit = 500
	enabled := true
	b.config.AI.CreativeReplies.ContextOnlyCurrentTopic = &enabled
	storeMessageToolTestMessage(t, b, 20, -100, 7, "alice", "current", 77, time.Now())

	scope := b.creativeMessageToolScope(20, -100)
	assert.Equal(t, int64(-100), scope.ChatID)
	assert.Equal(t, 77, scope.TopicID)
	assert.True(t, scope.TopicOnly)
	assert.True(t, scope.TopicResolved)
	assert.Equal(t, database.MaxAIMessageSearchLimit, scope.MaxLimit)
}

func TestExecuteMessageHistoryTool_TopicOnlyScopeFiltersEveryTool(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	storeMessageToolTestMessage(t, b, 30, -100, 7, "alice", "same topic needle", 42, base)
	storeMessageToolTestMessage(t, b, 31, -100, 7, "alice", "other topic needle", 99, base.Add(time.Second))
	scope := messageToolScope{ChatID: -100, TopicID: 42, TopicOnly: true, MaxLimit: 10}

	tests := []struct {
		name      string
		tool      string
		arguments string
	}{
		{name: "user", tool: "find_messages_from_user", arguments: `{"user":"7","limit":10,"offset":0}`},
		{name: "text", tool: "find_messages_containing_text", arguments: `{"query":"needle","limit":10,"offset":0}`},
		{name: "time range", tool: "find_messages_in_chat_between_times", arguments: `{"start_time":"2026-09-10T09:59:00Z","end_time":"2026-09-10T10:01:00Z","limit":10,"offset":0}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := decodeMessageToolOutput(t, b.executeMessageHistoryTool(test.tool, test.arguments, scope))
			require.Len(t, output.Messages, 1)
			assert.Equal(t, 30, output.Messages[0].MessageID)
			assert.Equal(t, "current_topic", output.Scope)
			require.NotNil(t, output.TopicID)
			assert.Equal(t, 42, *output.TopicID)
		})
	}
}

func TestExecuteMessageHistoryTool_RejectsInvalidTimeRange(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	raw := b.executeMessageHistoryTool(
		"find_messages_in_chat_between_times",
		`{"start_time":"2026-09-10T00:00:00Z","end_time":"2026-09-09T00:00:00Z","limit":10,"offset":0}`,
		messageToolScope{ChatID: -100, MaxLimit: 10},
	)
	assert.True(t, strings.Contains(raw, `"error":"invalid_arguments"`), raw)
}
