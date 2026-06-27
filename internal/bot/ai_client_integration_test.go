// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallAzureOpenAI_Success(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","content":"hello there"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	})

	out, err := b.callAzureOpenAIWithConfig("test", "user prompt", "system prompt",
		fullModelConfig(), 100, false)
	require.NoError(t, err)
	assert.Equal(t, "hello there", out)

	// Request was routed through the transport and hit the chat-completions path.
	require.Equal(t, 1, rt.count())
	assert.Contains(t, rt.last().Path, "/chat/completions")
	assert.Contains(t, rt.last().Body, "user prompt")

	// Token usage was persisted under the bot's fixed clock date.
	stats, err := b.db.GetTokenUsageStats("2026-06-09")
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, int64(10), stats[0].DailyInput)
	assert.Equal(t, int64(5), stats[0].DailyOutput)
}

func TestCallAzureOpenAI_OpenAIProvider(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		// Standard OpenAI provider uses Bearer auth.
		assert.Equal(t, "Bearer key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	})

	model := config.AIModelConfig{
		Provider:       config.AIProviderOpenAI,
		Endpoint:       "https://api.openai.example/v1",
		APIKey:         "key",
		DeploymentName: "gpt-4o",
	}
	out, err := b.callAzureOpenAIWithConfig("test", "p", "s", model, 50, false)
	require.NoError(t, err)
	assert.Equal(t, "ok", out)
	assert.Contains(t, rt.last().Path, "/chat/completions")
}

func TestCallAzureOpenAI_ContentFilter(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"content_filter","message":"blocked",
			"innererror":{"code":"ResponsibleAIPolicyViolation",
			"content_filter_result":{"hate":{"filtered":true,"severity":"high"}}}}}`))
	})

	_, err := b.callAzureOpenAIWithConfig("test", "p", "s", fullModelConfig(), 50, false)
	require.Error(t, err)
	var cfErr *ContentFilterError
	require.ErrorAs(t, err, &cfErr)
	assert.Contains(t, cfErr.Details, "hate")
}

func TestCallAzureOpenAI_RateLimit(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"429","message":"Too Many Requests"}}`))
	})

	_, err := b.callAzureOpenAIWithConfig("test", "p", "s", fullModelConfig(), 50, false)
	require.Error(t, err)
	var rlErr *RateLimitError
	require.ErrorAs(t, err, &rlErr)
	assert.Equal(t, http.StatusTooManyRequests, rlErr.StatusCode)
	assert.Equal(t, 7, int(rlErr.RetryAfter.Seconds()))
}

func TestCallAzureOpenAI_GenericError(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	})

	_, err := b.callAzureOpenAIWithConfig("test", "p", "s", fullModelConfig(), 50, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestCallAzureOpenAI_NoChoices(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	})

	_, err := b.callAzureOpenAIWithConfig("test", "p", "s", fullModelConfig(), 50, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no choices")
}

func TestCallAzureOpenAINoFallback(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"full only"}}]}`))
	})
	b.config.AI.Enabled = true
	b.config.AI.FullModel = fullModelConfigs()

	out, err := b.callAzureOpenAINoFallback("test", "p", "s")
	require.NoError(t, err)
	assert.Equal(t, "full only", out)
}

func TestCallAzureOpenAINoFallback_Disabled(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.Enabled = false

	_, err := b.callAzureOpenAINoFallback("test", "p", "s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not enabled")
}

func TestCallAzureOpenAI_HappyPathNoRetry(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	})
	b.config.AI.Enabled = true
	b.config.AI.FullModel = fullModelConfigs()

	out, err := b.callAzureOpenAI("test", "p", "s")
	require.NoError(t, err)
	assert.Equal(t, "ok", out)
}
