// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteReputationTool_LookupAndScores(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.CreativeReplies.ReputationToolEnabled = true
	now := time.Now()
	_, err := b.db.RecordIncomingMessage(&database.MessageInfo{
		MessageID: 1, ChatID: -100, UserID: 7, Username: "alice", Text: "hello",
		MessageThreadID: 99, Timestamp: now,
	}, database.IncomingMessageOpts{
		TrackProfile: true, Username: "alice", DisplayName: "Alice Wonderland", DayDate: now.Format("2006-01-02"),
	})
	require.NoError(t, err)
	require.NoError(t, b.db.UpsertUserProfile(&database.UserProfile{
		UserID: 7, Username: "alice", Reputation: "good", Profile: "Helpful participant",
	}))
	b.recordAIReputation(7, "good")
	require.NoError(t, b.db.UpsertReputationEvent(database.ReputationEvent{
		SourceKey: "current", UserID: 7, ChatID: -100, Kind: "warning",
		Component: database.ReputationComponentModeration, Value: 1, OccurredAt: now,
	}))
	require.NoError(t, b.db.UpsertReputationEvent(database.ReputationEvent{
		SourceKey: "other", UserID: 7, ChatID: -200, Kind: "permanent_mute",
		Component: database.ReputationComponentModeration, Value: 1, OccurredAt: now,
	}))

	for _, user := range []string{"7", "alice", "@ALICE", "Alice Wonderland", " alice wonderland "} {
		t.Run(user, func(t *testing.T) {
			arguments, err := json.Marshal(map[string]string{"user": user})
			require.NoError(t, err)
			raw := b.executeReputationTool(string(arguments), messageToolScope{ChatID: -100, TopicOnly: true, TopicID: 42})
			var output reputationToolOutput
			require.NoError(t, json.Unmarshal([]byte(raw), &output), raw)
			assert.Equal(t, int64(7), output.UserID)
			assert.Equal(t, int64(-100), output.ChatID)
			require.NotNil(t, output.AIReputation)
			assert.Equal(t, "good", output.AIReputation.Reputation)
			assert.Equal(t, "Helpful participant", output.AIReputation.Profile)
			assert.True(t, output.GeneralReputationEnabled)
			require.NotNil(t, output.GeneralReputation)
			assert.InDelta(t, -3.5, output.GeneralReputation.Score, 0.01)
			assert.Equal(t, "neutral", output.GeneralReputation.Grade)
			assert.InDelta(t, -5, output.GeneralReputation.Components["moderation"], 0.01)
			assert.InDelta(t, 1.5, output.GeneralReputation.Components["ai"], 0.01)
		})
	}

	disabled := false
	b.config.Reputation.AIProfileAffectsScore = &disabled
	raw := b.executeReputationTool(`{"user":"7"}`, messageToolScope{ChatID: -100})
	var output reputationToolOutput
	require.NoError(t, json.Unmarshal([]byte(raw), &output), raw)
	require.NotNil(t, output.AIReputation)
	assert.Equal(t, "good", output.AIReputation.Reputation)
	require.NotNil(t, output.GeneralReputation)
	assert.InDelta(t, -5, output.GeneralReputation.Score, 0.01)
}

func TestExecuteReputationTool_ErrorsAndMissingData(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	scope := messageToolScope{ChatID: -100}
	assert.Contains(t, b.executeReputationTool(`{"user":"7"}`, scope), `"error":"tool_disabled"`)
	b.config.AI.CreativeReplies.ReputationToolEnabled = true
	storeMessageToolTestMessage(t, b, 1, -100, 7, "shared", "one", 0, time.Now())
	storeMessageToolTestMessage(t, b, 2, -100, 8, "shared", "two", 0, time.Now())
	storeMessageToolTestMessage(t, b, 3, -200, 9, "outsider", "other chat", 0, time.Now())
	for _, test := range []struct {
		arguments string
		code      string
	}{
		{`{`, "invalid_arguments"},
		{`{}`, "invalid_arguments"},
		{`{"user":"  "}`, "invalid_arguments"},
		{`{"user":7}`, "invalid_arguments"},
		{`{"user":"shared"}`, "ambiguous_user"},
		{`{"user":"outsider"}`, "user_not_found"},
		{`{"user":"9"}`, "user_not_found"},
		{`{"user":"' OR 1=1 --"}`, "user_not_found"},
	} {
		assert.Contains(t, b.executeReputationTool(test.arguments, scope), `"error":"`+test.code+`"`)
	}
	raw := b.executeReputationTool(`{"user":"7"}`, scope)
	var output reputationToolOutput
	require.NoError(t, json.Unmarshal([]byte(raw), &output), raw)
	assert.Equal(t, int64(7), output.UserID, "numeric ID disambiguates a shared name")
	assert.Nil(t, output.AIReputation)
	require.NotNil(t, output.GeneralReputation)
	assert.Zero(t, output.GeneralReputation.Score)

	disabled := false
	b.config.Reputation.Enabled = &disabled
	raw = b.executeReputationTool(`{"user":"7"}`, scope)
	require.NoError(t, json.Unmarshal([]byte(raw), &output), raw)
	assert.False(t, output.GeneralReputationEnabled)
	assert.Nil(t, output.GeneralReputation, "disabled scores must not masquerade as neutral")
	assert.Contains(t, raw, `"ai_reputation":null`)
	assert.Contains(t, raw, `"general_reputation":null`)

	require.NoError(t, b.db.UpsertUserProfile(&database.UserProfile{UserID: 7, Reputation: "bad"}))
	raw = b.executeReputationTool(`{"user":"7"}`, scope)
	output = reputationToolOutput{}
	require.NoError(t, json.Unmarshal([]byte(raw), &output), raw)
	require.NotNil(t, output.AIReputation, "reputation without profile text must still be returned")
	assert.Equal(t, "bad", output.AIReputation.Reputation)
	assert.Empty(t, output.AIReputation.Profile)

	require.NoError(t, b.db.UpsertUserProfile(&database.UserProfile{
		UserID: 7, Reputation: "good", Profile: strings.Repeat("x", messageToolTextMaxRunes+1),
	}))
	raw = b.executeReputationTool(`{"user":"7"}`, scope)
	require.NoError(t, json.Unmarshal([]byte(raw), &output), raw)
	require.NotNil(t, output.AIReputation)
	assert.True(t, output.AIReputation.Truncated)
	assert.Len(t, output.AIReputation.Profile, messageToolTextMaxRunes+3)
}

func TestCreativeReputationTool_FunctionLoop(t *testing.T) {
	for _, test := range []struct {
		name        string
		history     bool
		reputation  bool
		storedTopic bool
		webSearch   bool
	}{
		{name: "reputation only", reputation: true},
		{name: "combined", history: true, reputation: true, storedTopic: true},
		{name: "unresolved topic still permits reputation", history: true, reputation: true},
		{name: "disabled reputation cannot be called", history: true, storedTopic: true},
		{name: "with web search", reputation: true, webSearch: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests int
			b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var request responsesToolsRequest
				require.NoError(t, json.Unmarshal(body, &request))
				names := make([]string, 0, len(request.Tools))
				for _, tool := range request.Tools {
					names = append(names, tool.Name)
				}
				assert.Equal(t, test.reputation, slices.Contains(names, "get_user_reputation"))
				assert.Equal(t, test.history && test.storedTopic, slices.Contains(names, "find_messages_from_user"))
				if test.webSearch {
					assert.Contains(t, string(body), `"type":"web_search"`)
				}
				requests++
				if requests == 1 {
					_, _ = w.Write([]byte(`{"output":[{"type":"function_call","call_id":"rep_1","name":"get_user_reputation","arguments":"{\"user\":\"7\"}"}]}`))
					return
				}
				assert.Contains(t, string(body), "function_call_output")
				if test.reputation {
					assert.Contains(t, string(body), `\"user_id\":7`)
					assert.Contains(t, string(body), `\"general_reputation\":`)
					assert.Contains(t, string(body), `\"ai_reputation\":`)
				} else {
					assert.Contains(t, string(body), "unknown_tool")
				}
				_, _ = w.Write([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"Reputation-aware reply."}]}]}`))
			})
			creativeConfig(b)
			b.config.AI.CreativeReplies.MessageToolsEnabled = test.history
			b.config.AI.CreativeReplies.ReputationToolEnabled = test.reputation
			b.config.AI.CreativeReplies.WebSearchEnabled = test.webSearch
			b.config.AI.FullModel.Configs[0].UseForWebSearch = test.webSearch
			storeMessageToolTestMessage(t, b, 1, -100, 7, "alice", "hello", 42, time.Now())
			messageID := 10
			if test.storedTopic {
				messageID = 1
			}
			reply, err := b.generateCreativeReplyWithQuote("How is Alice's reputation?", "", messageID, 7, -100, nil)
			require.NoError(t, err)
			assert.Equal(t, "Reputation-aware reply.", reply)
			assert.Equal(t, 2, requests)
		})
	}
}

func TestCreativeReputationTool_UnsupportedProviderFallsBack(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/responses") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"unsupported_tool","message":"no tools"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ordinary reply"}}]}`))
	})
	creativeConfig(b)
	b.config.AI.CreativeReplies.ReputationToolEnabled = true
	reply, err := b.generateCreativeReplyWithQuote("hello", "", 10, 7, -100, nil)
	require.NoError(t, err)
	assert.Equal(t, "ordinary reply", reply)
	assert.Equal(t, 2, rt.count())
}

func TestCreativeReputationTool_ContentFilterDoesNotFallback(t *testing.T) {
	for _, refusal := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "refusal"}[refusal], func(t *testing.T) {
			b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
				if refusal {
					_, _ = w.Write([]byte(`{"output":[{"type":"message","content":[{"type":"refusal","refusal":"blocked"}]}]}`))
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"content_filter","message":"blocked"}}`))
			})
			creativeConfig(b)
			b.config.AI.CreativeReplies.ReputationToolEnabled = true
			_, err := b.generateCreativeReplyWithQuote("hello", "", 10, 7, -100, nil)
			var contentFilterErr *ContentFilterError
			require.ErrorAs(t, err, &contentFilterErr)
			assert.Equal(t, 1, rt.count())
		})
	}
}
