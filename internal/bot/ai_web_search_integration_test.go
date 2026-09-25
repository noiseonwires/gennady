// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webSearchSuccessResponse = `{
	"output":[
		{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"current events"}},
		{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"Checked current sources."}]},
		{"type":"message","content":[{"type":"output_text","text":"Current answer.",
			"annotations":[{"type":"url_citation","url":"https://example.com/source","title":"Source"}]}]}
	],
	"usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30}
}`

func TestCallResponsesWithWebSearch_OpenAI(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(webSearchSuccessResponse))
	})
	model := config.AIModelConfig{
		Provider:        config.AIProviderOpenAI,
		Endpoint:        "https://api.openai.example/v1",
		APIKey:          "key",
		DeploymentName:  "gpt-search",
		UseForWebSearch: true,
	}

	out, err := b.callResponsesWithWebSearch("creative_reply", "what happened?", "be concise", model, 500)
	require.NoError(t, err)
	assert.Contains(t, out, "Current answer.")
	assert.Contains(t, out, "https://example.com/source")
	require.Equal(t, 1, rt.count())
	assert.Equal(t, "/v1/responses", rt.last().Path)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(rt.last().Body), &body))
	assert.Equal(t, "gpt-search", body["model"])
	assert.Equal(t, "be concise", body["instructions"])
	assert.Equal(t, "what happened?", body["input"])
	assert.Equal(t, "auto", body["tool_choice"])
	assert.Equal(t, false, body["store"])
	tools := body["tools"].([]interface{})
	assert.Equal(t, "web_search", tools[0].(map[string]interface{})["type"])
}

func TestCallResponsesWithWebSearch_DetailedMetadata(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(webSearchSuccessResponse))
	})
	model := fullModelConfig()
	model.UseForWebSearch = true

	result, err := b.callResponsesWithWebSearchDetailed("creative_reply", "question", "instructions", model, 500)
	require.NoError(t, err)
	assert.True(t, result.WebSearchUsed)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "search", result.ToolCalls[0].ActionType)
	assert.Equal(t, []string{"current events"}, result.ToolCalls[0].Queries)
	require.Len(t, result.Citations, 1)
	assert.Equal(t, "Source", result.Citations[0].Title)
	assert.Equal(t, []string{"Checked current sources."}, result.ReasoningSummaries)
	require.NotNil(t, result.Usage)
	assert.Equal(t, int64(20), result.Usage.PromptTokens)
}

func TestCallResponsesWithWebSearch_AzureOpenAI(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "key", r.Header.Get("api-key"))
		assert.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(webSearchSuccessResponse))
	})
	model := fullModelConfig()
	model.UseForWebSearch = true

	_, err := b.callResponsesWithWebSearch("creative_reply", "question", "instructions", model, 500)
	require.NoError(t, err)
	require.Equal(t, 1, rt.count())
	assert.Equal(t, "/openai/v1/responses", rt.last().Path)
	assert.Contains(t, rt.last().Body, `"model":"gpt-test"`)
}

func TestGenerateCreativeReplyWithQuote_UsesWebSearch(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(webSearchSuccessResponse))
	})
	creativeConfig(b)
	b.config.AI.CreativeReplies.WebSearchEnabled = true
	b.config.AI.FullModel.Configs[0].UseForWebSearch = true

	out, err := b.generateCreativeReplyWithQuote("what happened today?", "", 10, 7, -100, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "Current answer.")
	assert.Contains(t, rt.last().Path, "/responses")
}

func TestGenerateCreativeReplyWithQuote_LightModelUsesWebSearch(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(webSearchSuccessResponse))
	})
	creativeConfig(b)
	b.config.AI.CreativeReplies.UseFullModel = false
	b.config.AI.CreativeReplies.WebSearchEnabled = true
	lightModel := fullModelConfig()
	lightModel.DeploymentName = "light-search"
	lightModel.UseForWebSearch = true
	b.config.AI.LightModel = config.AIModelConfigs{Configs: []config.AIModelConfig{lightModel}}

	_, err := b.generateCreativeReplyWithQuote("what happened today?", "", 10, 7, -100, nil)
	require.NoError(t, err)
	require.Equal(t, 1, rt.count())
	assert.Contains(t, rt.last().Path, "/responses")
	assert.Contains(t, rt.last().Body, `"model":"light-search"`)
}

func TestGenerateCreativeReplyWithQuote_WebSearchModelFailover(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"model":"first-search"`) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"server_error","message":"unavailable"}}`))
			return
		}
		_, _ = w.Write([]byte(webSearchSuccessResponse))
	})
	creativeConfig(b)
	b.config.AI.CreativeReplies.WebSearchEnabled = true
	first := fullModelConfig()
	first.DeploymentName = "first-search"
	first.UseForWebSearch = true
	second := fullModelConfig()
	second.DeploymentName = "second-search"
	second.UseForWebSearch = true
	b.config.AI.FullModel = config.AIModelConfigs{Configs: []config.AIModelConfig{first, second}}

	out, err := b.generateCreativeReplyWithQuote("what happened today?", "", 10, 7, -100, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "Current answer.")
	assert.Equal(t, 2, rt.count())
	assert.Contains(t, rt.last().Body, `"model":"second-search"`)
}

func TestGenerateCreativeReplyWithQuote_WebSearchFailureFallsBack(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/responses") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"unsupported_tool","message":"no web search"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ordinary reply"}}]}`))
	})
	creativeConfig(b)
	b.config.AI.CreativeReplies.WebSearchEnabled = true
	b.config.AI.FullModel.Configs[0].UseForWebSearch = true

	out, err := b.generateCreativeReplyWithQuote("hello", "", 10, 7, -100, nil)
	require.NoError(t, err)
	assert.Equal(t, "ordinary reply", out)
	assert.Equal(t, 2, rt.count())
	assert.Contains(t, rt.last().Path, "/chat/completions")
}

func TestGenerateCreativeReplyWithQuote_WebSearchRequiresModelOptIn(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ordinary reply"}}]}`))
	})
	creativeConfig(b)
	b.config.AI.CreativeReplies.WebSearchEnabled = true

	out, err := b.generateCreativeReplyWithQuote("hello", "", 10, 7, -100, nil)
	require.NoError(t, err)
	assert.Equal(t, "ordinary reply", out)
	require.Equal(t, 1, rt.count())
	assert.Contains(t, rt.last().Path, "/chat/completions")
}

func TestGenerateCreativeReplyWithQuote_WebSearchContentFilterDoesNotFallback(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"content_filter","message":"blocked"}}`))
	})
	creativeConfig(b)
	b.config.AI.CreativeReplies.WebSearchEnabled = true
	b.config.AI.FullModel.Configs[0].UseForWebSearch = true

	_, err := b.generateCreativeReplyWithQuote("blocked", "", 10, 7, -100, nil)
	require.Error(t, err)
	var contentFilterErr *ContentFilterError
	require.ErrorAs(t, err, &contentFilterErr)
	assert.Equal(t, 1, rt.count())
}

func TestGenerateCreativeReplyWithQuote_WebSearchRefusalDoesNotFallback(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"output":[{"type":"message","content":[{"type":"refusal","refusal":"I cannot help with that."}]}]}`))
	})
	creativeConfig(b)
	b.config.AI.CreativeReplies.WebSearchEnabled = true
	b.config.AI.FullModel.Configs[0].UseForWebSearch = true

	_, err := b.generateCreativeReplyWithQuote("blocked", "", 10, 7, -100, nil)
	require.Error(t, err)
	var contentFilterErr *ContentFilterError
	require.ErrorAs(t, err, &contentFilterErr)
	assert.Equal(t, 1, rt.count())
}
