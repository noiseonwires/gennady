// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"net/http"
	"testing"

	"gennadium/internal/config"
	"gennadium/internal/database"

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

func TestGenerateWarningNotificationModelSelection(t *testing.T) {
	useFull, useLight := true, false
	for _, tt := range []struct {
		name     string
		useFull  *bool
		wantPath string
	}{
		{name: "omitted", wantPath: "/openai/deployments/warning-full/chat/completions"},
		{name: "full", useFull: &useFull, wantPath: "/openai/deployments/warning-full/chat/completions"},
		{name: "light", useFull: &useLight, wantPath: "/openai/deployments/warning-light/chat/completions"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"warning"}}]}`))
			})
			b.config.AI.Enabled = true
			b.config.AI.FullModel = fullModelConfigs()
			b.config.AI.FullModel.Configs[0].DeploymentName = "warning-full"
			b.config.AI.LightModel = fullModelConfigs()
			b.config.AI.LightModel.Configs[0].DeploymentName = "warning-light"
			b.config.AI.ContentModeration.WarningUseFullModel = tt.useFull

			out, err := b.generateWarningNotification("User", "message", "", 7, -100)
			require.NoError(t, err)
			assert.Equal(t, "warning", out)
			require.Equal(t, 1, rt.count())
			assert.Equal(t, tt.wantPath, rt.last().Path)

			b.config.AI.Enabled = false
			_, err = b.generateWarningNotification("User", "message", "", 7, -100)
			require.ErrorContains(t, err, "not enabled")
			assert.Equal(t, 1, rt.count(), "disabled AI must not contact either model")
		})
	}
}

func TestGenerateWarningNotificationLightModelFailover(t *testing.T) {
	calls := 0
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			assert.Contains(t, r.URL.Path, "/deployments/light-primary/")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary failure"}}`))
			return
		}
		assert.Contains(t, r.URL.Path, "/deployments/light-fallback/")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"warning"}}]}`))
	})
	b.config.AI.Enabled = true
	useFull := false
	b.config.AI.ContentModeration.WarningUseFullModel = &useFull
	primary, fallback := fullModelConfig(), fullModelConfig()
	primary.DeploymentName = "light-primary"
	fallback.DeploymentName = "light-fallback"
	b.config.AI.LightModel = config.AIModelConfigs{Configs: []config.AIModelConfig{primary, fallback}}

	out, err := b.generateWarningNotification("User", "message", "", 7, -100)
	require.NoError(t, err)
	assert.Equal(t, "warning", out)
	assert.Equal(t, 2, rt.count())
}

func TestGenerateWarningNotificationLightModelUnavailable(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "disabled"}[disabled], func(t *testing.T) {
			b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("warning generation must not fall back to the full model")
			})
			b.config.AI.Enabled = true
			b.config.AI.FullModel = fullModelConfigs()
			useFull := false
			b.config.AI.ContentModeration.WarningUseFullModel = &useFull
			if disabled {
				b.config.AI.LightModel = fullModelConfigs()
				b.config.AI.LightModel.Configs[0].Enabled = &useFull
			}

			out, err := b.generateWarningNotification("User", "message", "", 7, -100)
			require.ErrorContains(t, err, "no enabled AI model endpoint")
			assert.Empty(t, out)
			assert.Zero(t, rt.count())
		})
	}
}

func TestGenerateWarningNotificationSupportsDualReputationPlaceholders(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"warning"}}]}`))
	})
	b.config.AI.Enabled = true
	b.config.AI.FullModel = fullModelConfigs()
	b.config.AI.UserProfiles.Enabled = true
	b.config.AI.ContentModeration.WarningPrompt = config.PromptPair{
		System: "PROFILE={{user_profile}} LIGHT={{light_verdict}}",
		User:   "COMPACT={{user_reputation}} LEGACY={{reputation}} SCORE={{reputation_score}} GRADE={{reputation_grade}} FULL={{full_verdict}}",
	}
	require.NoError(t, b.db.UpsertUserProfile(&database.UserProfile{
		UserID: 42, Username: "mixed", Profile: "AI profile text", Reputation: "very_bad",
	}))

	out, err := b.generateWarningNotification("Mixed User", "message", "", 42, -100, moderationVerdicts{
		Light: "spam\nsuspicious links",
		Full:  "spam\nconfirmed scam",
	})
	require.NoError(t, err)
	assert.Equal(t, "warning", out)

	body := rt.last().Body
	assert.Contains(t, body, "Score reputation: neutral (score 0.0).")
	assert.Contains(t, body, "AI reputation: very_bad")
	assert.Contains(t, body, "AI profile text")
	assert.Contains(t, body, "LEGACY=neutral SCORE=0.0 GRADE=neutral")
	assert.Contains(t, body, `LIGHT=spam\nsuspicious links`)
	assert.Contains(t, body, `FULL=spam\nconfirmed scam`)
	assert.NotContains(t, body, "{{user_profile}}")
	assert.NotContains(t, body, "{{user_reputation}}")
}

func TestGenerateWarningNotificationModerationContext(t *testing.T) {
	tests := []struct {
		name     string
		verdicts moderationVerdicts
		want     string
	}{
		{name: "manual without verdict"},
		{name: "light only", verdicts: moderationVerdicts{Light: "spam\nlight reason"}, want: "spam\nlight reason"},
		{name: "full verdict", verdicts: moderationVerdicts{Light: "spam\nlight reason", Full: "spam\nfull reason"}, want: "spam\nfull reason"},
		{name: "decision", verdicts: moderationVerdicts{Full: "spam\nfull reason", Decision: "authoritative reason"}, want: "authoritative reason"},
		{name: "content security", verdicts: moderationVerdicts{ContentFilterDetails: "hate: high"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"warning"}}]}`))
			})
			b.config.AI.Enabled = true
			b.config.AI.FullModel = fullModelConfigs()
			b.config.AI.ContentModeration.WarningPrompt = config.PromptPair{
				System: "{{moderation_verdict}}",
				User:   "{{message}}\n{{user_message}}",
			}
			_, err := b.generateWarningNotification("User", "original message", "", 7, -100, tt.verdicts)
			require.NoError(t, err)
			var request struct{ Messages []Message }
			require.NoError(t, json.Unmarshal([]byte(rt.last().Body), &request))
			require.Len(t, request.Messages, 2)
			assert.Equal(t, tt.want, request.Messages[0].Content)
			if tt.verdicts.ContentFilterDetails != "" {
				assert.Contains(t, request.Messages[1].Content, "hate: high")
				assert.NotContains(t, rt.last().Body, "original message")
			} else {
				assert.Contains(t, request.Messages[1].Content, "original message")
			}
			assert.NotContains(t, rt.last().Body, "{{")
		})
	}
}

func TestGenerateWarningNotificationContentFilterRetry(t *testing.T) {
	for _, useFull := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "full"}[useFull], func(t *testing.T) {
			for _, blockedAgain := range []bool{false, true} {
				t.Run(map[bool]string{false: "recovers", true: "bounded retry"}[blockedAgain], func(t *testing.T) {
					calls := 0
					b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
						calls++
						if calls == 1 || blockedAgain {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"error":{"code":"content_filter","innererror":{"content_filter_result":{"hate":{"filtered":true,"severity":"high"}}}}}`))
							return
						}
						_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"warning"}}]}`))
					})
					b.config.AI.Enabled = true
					b.config.AI.FullModel = fullModelConfigs()
					b.config.AI.FullModel.Configs[0].DeploymentName = "warning-full"
					b.config.AI.LightModel = fullModelConfigs()
					b.config.AI.LightModel.Configs[0].DeploymentName = "warning-light"
					b.config.AI.ContentModeration.WarningUseFullModel = &useFull
					b.config.AI.ContentModeration.WarningPrompt = config.PromptPair{
						System: "{{moderation_verdict}}",
						User:   "{{message}}\n{{user_message}}",
					}
					_, err := b.generateWarningNotification("User", "original message", "", 7, -100, moderationVerdicts{Decision: "spam"})
					if blockedAgain {
						var cfErr *ContentFilterError
						require.ErrorAs(t, err, &cfErr)
					} else {
						require.NoError(t, err)
					}
					assert.Equal(t, 2, calls)
					assert.NotContains(t, rt.last().Body, "original message")
					assert.Contains(t, rt.last().Body, "hate: high")
					assert.Contains(t, rt.last().Body, "spam")
					modelName := "warning-light"
					if useFull {
						modelName = "warning-full"
					}
					assert.Contains(t, rt.last().Path, "/deployments/"+modelName+"/")
				})
			}
		})
	}
}
