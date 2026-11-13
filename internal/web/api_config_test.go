// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"gennadium/internal/config"
)

// newTestHandlerWithFile is like newTestHandler but points the config source at
// a writable temp file so save handlers can persist successfully.
func newTestHandlerWithFile(t *testing.T, cfg *config.Config) *apiHandler {
	t.Helper()
	h := newTestHandler(t, cfg)
	h.configFile = filepath.Join(t.TempDir(), "config.yaml")
	return h
}

func putJSON(target, body string) *http.Request {
	return httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
}

func TestHandleGetConfig_RedactsSecrets(t *testing.T) {
	cfg := newTestConfig()
	cfg.BotToken = "super-secret-token"
	cfg.Backup.EncryptionPassword = "secret-archive-password"
	h := newTestHandler(t, cfg)
	rr := get(t, h, h.handleGetConfig, "/api/config")
	assert.Equal(t, http.StatusOK, rr.Code)
	var values map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &values))
	// The bot token must be redacted, never returned in plaintext.
	assert.NotEqual(t, "super-secret-token", values["bot_token"])
	assert.Equal(t, redactedConfigValue, values["backup.encryption_password"])
	assert.NotContains(t, rr.Body.String(), cfg.Backup.EncryptionPassword)
}

func TestHandleGetConfigSecret(t *testing.T) {
	cfg := newTestConfig()
	secrets := config.SensitiveConfigKeys()
	for key := range secrets {
		require.Equal(t, "ok", config.SetConfigValue(cfg, key, "secret-for-"+key))
	}
	h := newTestHandler(t, cfg)
	for key := range secrets {
		t.Run(key, func(t *testing.T) {
			rr := get(t, h, h.handleGetConfigSecret, "/api/config/secret?key="+key)
			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
			assert.Equal(t, map[string]string{"value": "secret-for-" + key}, decodeErrBody(t, rr))
		})
	}
}

func TestHandleGetConfigSecret_InvalidField(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	for _, key := range []string{"", "language", "does.not.exist", "ai.light_model"} {
		t.Run(key, func(t *testing.T) {
			rr := get(t, h, h.handleGetConfigSecret, "/api/config/secret?key="+key)
			assert.Equal(t, http.StatusNotFound, rr.Code)
			assert.Equal(t, errConfigSecretNotFound.code, decodeErrBody(t, rr)["error_code"])
		})
	}
}

func TestHandleGetConfigSecret_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetConfigSecret, "/api/config/secret?key=bot_token")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "", decodeErrBody(t, rr)["value"])
}

func TestHandleGetConfigSecret_EnvOverride(t *testing.T) {
	t.Setenv("BOT_TOKEN", "environment-token")
	cfg := newTestConfig()
	cfg.BotToken = "environment-token"
	h := newTestHandler(t, cfg)
	rr := get(t, h, h.handleGetConfigSecret, "/api/config/secret?key=bot_token")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, cfg.BotToken, decodeErrBody(t, rr)["value"])
}

func TestHandleGetConfigSecret_HashedPassword(t *testing.T) {
	cfg := newTestConfig()
	var err error
	cfg.WebUI.Password, err = config.HashWebUIPasswordForStorage("admin-password")
	require.NoError(t, err)
	h := newTestHandler(t, cfg)
	rr := get(t, h, h.handleGetConfigSecret, "/api/config/secret?key=web_ui.password")
	assert.Equal(t, http.StatusConflict, rr.Code)
	assert.Equal(t, errConfigSecretHashed.code, decodeErrBody(t, rr)["error_code"])
	assert.NotContains(t, rr.Body.String(), cfg.WebUI.Password)
}

func TestHandleGetConfigSecret_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleGetConfigSecret(rr, putJSON("/api/config/secret?key=bot_token", "{}"))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleGetConfigMeta(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetConfigMeta, "/api/config/meta")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Contains(t, body, "sections")
	assert.Contains(t, body, "fields")
}

func TestHandleSaveConfig_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveConfig(rr, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleSaveConfig_WarningUseFullModel(t *testing.T) {
	for _, value := range []bool{false, true} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			cfg := newTestConfig()
			h := newTestHandlerWithFile(t, cfg)
			body, err := json.Marshal(map[string]bool{
				"ai.content_moderation.warning_use_full_model": value,
			})
			require.NoError(t, err)
			rr := httptest.NewRecorder()
			h.handleSaveConfig(rr, putJSON("/api/config", string(body)))
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			assert.Equal(t, value, cfg.AI.ContentModeration.IsWarningUseFullModel())

			saved := get(t, h, h.handleGetConfig, "/api/config")
			require.Equal(t, http.StatusOK, saved.Code)
			var values map[string]any
			require.NoError(t, json.Unmarshal(saved.Body.Bytes(), &values))
			assert.Equal(t, value, values["ai.content_moderation.warning_use_full_model"])

			data, err := os.ReadFile(h.configFile)
			require.NoError(t, err)
			var restored config.Config
			require.NoError(t, yaml.Unmarshal(data, &restored))
			assert.Equal(t, value, restored.AI.ContentModeration.IsWarningUseFullModel())
		})
	}
}

func TestHandleSaveConfig_CreativeReputationTool(t *testing.T) {
	cfg := newTestConfig()
	assert.False(t, cfg.AI.CreativeReplies.ReputationToolEnabled)
	h := newTestHandlerWithFile(t, cfg)
	meta := get(t, h, h.handleGetConfigMeta, "/api/config/meta")
	var metadata struct {
		Fields []config.FieldMeta `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(meta.Body.Bytes(), &metadata))
	assert.Contains(t, metadata.Fields, config.FieldMeta{
		Key: "ai.creative_replies.reputation_tool_enabled", Section: "ai_creative_replies", Type: "bool",
	})
	for _, value := range []bool{true, false} {
		body, err := json.Marshal(map[string]bool{"ai.creative_replies.reputation_tool_enabled": value})
		require.NoError(t, err)
		rr := httptest.NewRecorder()
		h.handleSaveConfig(rr, putJSON("/api/config", string(body)))
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		assert.Equal(t, value, cfg.AI.CreativeReplies.ReputationToolEnabled)
		assert.False(t, cfg.AI.CreativeReplies.MessageToolsEnabled)

		saved := get(t, h, h.handleGetConfig, "/api/config")
		require.Equal(t, http.StatusOK, saved.Code)
		var values map[string]any
		require.NoError(t, json.Unmarshal(saved.Body.Bytes(), &values))
		assert.Equal(t, value, values["ai.creative_replies.reputation_tool_enabled"])

		data, err := os.ReadFile(h.configFile)
		require.NoError(t, err)
		var restored config.Config
		require.NoError(t, yaml.Unmarshal(data, &restored))
		assert.Equal(t, value, restored.AI.CreativeReplies.ReputationToolEnabled)
	}
}

func TestHandleSaveConfig_CreativeAdminRateLimitBypass(t *testing.T) {
	cfg := newTestConfig()
	assert.False(t, cfg.AI.CreativeReplies.SkipRateLimitForAdmins)
	h := newTestHandlerWithFile(t, cfg)
	meta := get(t, h, h.handleGetConfigMeta, "/api/config/meta")
	var metadata struct {
		Fields []ConfigFieldUI `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(meta.Body.Bytes(), &metadata))
	var field *ConfigFieldUI
	for i := range metadata.Fields {
		if metadata.Fields[i].Key == "ai.creative_replies.skip_rate_limit_for_admins" {
			field = &metadata.Fields[i]
			break
		}
	}
	require.NotNil(t, field)
	assert.Equal(t, "ai_creative_replies", field.Section)
	assert.Equal(t, "bool", field.Type)
	assert.Equal(t, "Skip Rate Limit for Admins", field.LabelEN)
	assert.NotEmpty(t, field.DescEN)
	assert.NotEmpty(t, field.LabelRU)
	assert.NotEqual(t, field.LabelEN, field.LabelRU)
	assert.NotEmpty(t, field.DescRU)
	for _, value := range []bool{true, false} {
		body, err := json.Marshal(map[string]bool{"ai.creative_replies.skip_rate_limit_for_admins": value})
		require.NoError(t, err)
		rr := httptest.NewRecorder()
		h.handleSaveConfig(rr, putJSON("/api/config", string(body)))
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		assert.Equal(t, value, cfg.AI.CreativeReplies.SkipRateLimitForAdmins)

		saved := get(t, h, h.handleGetConfig, "/api/config")
		require.Equal(t, http.StatusOK, saved.Code)
		var values map[string]any
		require.NoError(t, json.Unmarshal(saved.Body.Bytes(), &values))
		assert.Equal(t, value, values["ai.creative_replies.skip_rate_limit_for_admins"])

		data, err := os.ReadFile(h.configFile)
		require.NoError(t, err)
		var restored config.Config
		require.NoError(t, yaml.Unmarshal(data, &restored))
		assert.Equal(t, value, restored.AI.CreativeReplies.SkipRateLimitForAdmins)
	}
}

func TestHandleSaveConfig_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveConfig(rr, putJSON("/api/config", "garbage"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleSaveConfig_NoFields(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveConfig(rr, putJSON("/api/config", "{}"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errNoFieldsToUpdate.code, body["error_code"])
}

func TestHandleSaveConfig_RedactedSensitiveSkipped(t *testing.T) {
	cfg := newTestConfig()
	cfg.BotToken = "keep-me"
	cfg.Backup.EncryptionPassword = "keep-archive-password"
	h := newTestHandlerWithFile(t, cfg)
	rr := httptest.NewRecorder()
	// A sensitive field echoed back as the redaction sentinel is skipped,
	// preserving the existing secret.
	h.handleSaveConfig(rr, putJSON("/api/config", `{"bot_token":"`+redactedConfigValue+`","backup.encryption_password":"`+redactedConfigValue+`"}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "keep-me", h.config.BotToken)
	assert.Equal(t, "keep-archive-password", h.config.Backup.EncryptionPassword)
}

func TestHandleSaveConfig_SecretsPersistAcrossRoundTrip(t *testing.T) {
	for _, source := range []string{"file", "db"} {
		for _, reveal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reveal=%t", source, reveal), func(t *testing.T) {
				cfg := newTestConfig()
				secrets := config.SensitiveConfigKeys()
				for key := range secrets {
					require.Equal(t, "ok", config.SetConfigValue(cfg, key, "secret-for-"+key))
				}
				var err error
				cfg.WebUI.Password, err = config.HashWebUIPasswordForStorage("admin-password")
				require.NoError(t, err)
				h := newTestHandlerWithFile(t, cfg)
				h.configFromDB = source == "db"
				require.NoError(t, h.persistConfig())
				original := config.ConfigToStringMap(cfg)

				rr := get(t, h, h.handleGetConfig, "/api/config")
				require.Equal(t, http.StatusOK, rr.Code)
				values := decodeJSONBody(t, rr)
				updates := map[string]any{"language": "ru"}
				for key := range secrets {
					assert.Equal(t, redactedConfigValue, values[key])
					updates[key] = values[key]
					if reveal && key != "web_ui.password" {
						rr := get(t, h, h.handleGetConfigSecret, "/api/config/secret?key="+key)
						require.Equal(t, http.StatusOK, rr.Code)
						updates[key] = decodeErrBody(t, rr)["value"]
					}
				}
				body, err := json.Marshal(updates)
				require.NoError(t, err)
				saved := httptest.NewRecorder()
				h.handleSaveConfig(saved, putJSON("/api/config", string(body)))
				require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
				assert.Equal(t, "ru", cfg.Language)

				var persisted map[string]string
				if h.configFromDB {
					persisted, err = h.db.GetAllConfigValues()
					require.NoError(t, err)
				} else {
					data, err := os.ReadFile(h.configFile)
					require.NoError(t, err)
					var restored config.Config
					require.NoError(t, yaml.Unmarshal(data, &restored))
					persisted = config.ConfigToStringMap(&restored)
				}
				assert.Equal(t, "ru", persisted["language"])
				current := config.ConfigToStringMap(cfg)
				for key := range secrets {
					assert.Equal(t, original[key], current[key], "in-memory secret %s", key)
					assert.Equal(t, original[key], persisted[key], "persisted secret %s", key)
				}
			})
		}
	}
}

func TestHandleSaveConfig_SensitiveFieldsCanBeReplacedAndCleared(t *testing.T) {
	for _, value := range []string{"replacement-secret", ""} {
		t.Run(value, func(t *testing.T) {
			cfg := newTestConfig()
			cfg.BotToken = "old-token"
			cfg.Backup.EncryptionPassword = "old-password"
			h := newTestHandlerWithFile(t, cfg)
			body, err := json.Marshal(map[string]string{
				"bot_token": value, "backup.encryption_password": value,
			})
			require.NoError(t, err)
			rr := httptest.NewRecorder()
			h.handleSaveConfig(rr, putJSON("/api/config", string(body)))
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			assert.Equal(t, value, cfg.BotToken)
			assert.Equal(t, value, cfg.Backup.EncryptionPassword)
		})
	}
}

func TestHandleSaveConfig_ValidationError(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	// A known integer field given an unparseable string fails validation.
	h.handleSaveConfig(rr, putJSON("/api/config", `{"admin.super_admin_user_id":"not-a-number"}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errConfigValidation.code, body["error_code"])
	// The response must carry the field-level specifics so the web UI can show
	// the user exactly what went wrong, not just a generic "validation failed".
	assert.Contains(t, body["detail"], "admin.super_admin_user_id")
}

func TestHandleSaveConfig_EnvOverriddenFieldSkipped(t *testing.T) {
	// A field pinned by an environment variable must not be writable via the
	// web UI: the env value wins on every reload, so accepting the edit would
	// drift the stored config from what the bot runs.
	t.Setenv("LANGUAGE", "en")
	cfg := newTestConfig()
	cfg.Language = "en"
	h := newTestHandlerWithFile(t, cfg)
	rr := httptest.NewRecorder()
	h.handleSaveConfig(rr, putJSON("/api/config", `{"language":"ru"}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	// The submitted value was ignored; the env-pinned value is preserved.
	assert.Equal(t, "en", h.config.Language)
}

func TestHandleSaveConfig_EffectiveValidationRejectsBadRef(t *testing.T) {
	// Editing a non-env field (a topic scope) so it references a chat that the
	// effective moderation.chat_id list doesn't contain must be rejected at
	// save time, and the in-memory config must be rolled back unchanged.
	cfg := newTestConfig()
	cfg.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	h := newTestHandlerWithFile(t, cfg)
	rr := httptest.NewRecorder()
	h.handleSaveConfig(rr, putJSON("/api/config", `{"ai.creative_replies.included_topics":[{"chat":-200,"topic":0}]}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errConfigValidation.code, body["error_code"])
	// Rolled back: the bad ref was not retained in memory.
	assert.Equal(t, 0, h.config.AI.CreativeReplies.IncludedTopics.Count())
}

func TestHandleGetRssFeeds_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetRssFeeds, "/api/config/rss")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "[]\n", rr.Body.String())
}

func TestHandleSaveRssFeeds(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveRssFeeds(rr, putJSON("/api/config/rss", `[]`))
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleSaveRssFeeds_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveRssFeeds(rr, httptest.NewRequest(http.MethodGet, "/api/config/rss", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleSaveRssFeeds_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveRssFeeds(rr, putJSON("/api/config/rss", "x"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleGetWatchedSites_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetWatchedSites, "/api/config/website-watch")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "[]\n", rr.Body.String())
}

func TestHandleSaveWatchedSites(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveWatchedSites(rr, putJSON("/api/config/website-watch",
		`[{"name":"Rules","url":"https://example.com/rules","enabled":true,"interval_hours":12}]`))
	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, h.config.AI.WebsiteWatch.Sites, 1)
	assert.Equal(t, "Rules", h.config.AI.WebsiteWatch.Sites[0].Name)
	assert.Equal(t, 12, h.config.AI.WebsiteWatch.Sites[0].IntervalHours)
}

func TestHandleSaveWatchedSites_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveWatchedSites(rr, httptest.NewRequest(http.MethodGet, "/api/config/website-watch", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleSaveWatchedSites_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveWatchedSites(rr, putJSON("/api/config/website-watch", "x"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleGetTopics_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetTopics, "/api/config/topics")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "[]\n", rr.Body.String())
}

func TestHandleSaveTopics(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveTopics(rr, putJSON("/api/config/topics", `[{"chat":-100,"topic":7,"name":"Support"}]`))
	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, h.config.Topics, 1)
	assert.Equal(t, int64(-100), h.config.Topics[0].Chat)
	assert.Equal(t, 7, h.config.Topics[0].Topic)
	assert.Equal(t, "Support", h.config.Topics[0].Name)
}

func TestHandleSaveTopics_DropsBlankRows(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	// The empty trailing row (all-zero / blank) must be silently dropped.
	h.handleSaveTopics(rr, putJSON("/api/config/topics",
		`[{"chat":-100,"topic":7,"name":"  Support  "},{"chat":0,"topic":0,"name":""}]`))
	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, h.config.Topics, 1)
	assert.Equal(t, "Support", h.config.Topics[0].Name, "name is trimmed")
}

func TestHandleSaveTopics_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveTopics(rr, httptest.NewRequest(http.MethodGet, "/api/config/topics", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleSaveTopics_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveTopics(rr, putJSON("/api/config/topics", "x"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleSaveTopics_Validation(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{"missing chat", `[{"chat":0,"topic":7,"name":"x"}]`, errTopicChatRequired.code},
		{"non-positive topic", `[{"chat":-100,"topic":0,"name":"x"}]`, errTopicInvalidThread.code},
		{"missing name", `[{"chat":-100,"topic":7,"name":"  "}]`, errTopicNameRequired.code},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandlerWithFile(t, newTestConfig())
			rr := httptest.NewRecorder()
			h.handleSaveTopics(rr, putJSON("/api/config/topics", tc.body))
			assert.Equal(t, http.StatusBadRequest, rr.Code)
			body := decodeErrBody(t, rr)
			assert.Equal(t, tc.code, body["error_code"])
		})
	}
}

func TestHandleGetModerationRules_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetModerationRules, "/api/config/moderation-rules")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Contains(t, body, "rules")
}

func TestHandleSaveModerationRules_Valid(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModerationRules(rr, putJSON("/api/config/moderation-rules", `{"rules":[{"trigger":"spam","action":"warn"}]}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, h.config.AI.ContentModeration.Rules, 1)
	assert.Equal(t, "spam", h.config.AI.ContentModeration.Rules[0].Trigger)
}

func TestHandleSaveModerationRules_MissingTrigger(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModerationRules(rr, putJSON("/api/config/moderation-rules", `{"rules":[{"trigger":"  ","action":"warn"}]}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errRuleTriggerRequired.code, body["error_code"])
}

func TestHandleSaveModerationRules_InvalidAction(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModerationRules(rr, putJSON("/api/config/moderation-rules", `{"rules":[{"trigger":"spam","action":"explode"}]}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errRuleInvalidAction.code, body["error_code"])
}

func TestHandleSaveModerationRules_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModerationRules(rr, httptest.NewRequest(http.MethodGet, "/api/config/moderation-rules", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleSaveModerationRules_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModerationRules(rr, putJSON("/api/config/moderation-rules", "x"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleGetModels(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetModels, "/api/config/models")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Contains(t, body, "light_model")
	assert.Contains(t, body, "full_model")
}

func TestHandleSaveModels_Valid(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModels(rr, putJSON("/api/config/models", `{"light_model":[{"endpoint":"https://x","deployment_name":"d"}]}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, h.config.AI.LightModel.Configs, 1)
}

func TestHandleSaveModels_MissingEndpoint(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModels(rr, putJSON("/api/config/models", `{"light_model":[{"endpoint":"","deployment_name":"d"}]}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errModelEndpointRequired.code, body["error_code"])
}

func TestHandleSaveModels_FullModelMissingEndpoint(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModels(rr, putJSON("/api/config/models", `{"full_model":[{"endpoint":"https://x","deployment_name":""}]}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleSaveModels_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModels(rr, httptest.NewRequest(http.MethodGet, "/api/config/models", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleSaveModels_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveModels(rr, putJSON("/api/config/models", "x"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleGetChatRulesOverrides_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetChatRulesOverrides, "/api/config/chat-rules-overrides")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Contains(t, body, "overrides")
}

func TestHandleSaveChatRulesOverrides_Valid(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveChatRulesOverrides(rr, putJSON("/api/config/chat-rules-overrides", `{"overrides":[{"chat":123,"rules":"be nice"}]}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, h.config.AI.ChatRulesOverrides, 1)
}

func TestHandleSaveChatRulesOverrides_MissingChat(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveChatRulesOverrides(rr, putJSON("/api/config/chat-rules-overrides", `{"overrides":[{"chat":0,"rules":"x"}]}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleSaveChatRulesOverrides_DuplicateChat(t *testing.T) {
	h := newTestHandlerWithFile(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveChatRulesOverrides(rr, putJSON("/api/config/chat-rules-overrides", `{"overrides":[{"chat":1,"rules":"a"},{"chat":1,"rules":"b"}]}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleSaveChatRulesOverrides_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveChatRulesOverrides(rr, httptest.NewRequest(http.MethodGet, "/api/config/chat-rules-overrides", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleSaveChatRulesOverrides_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleSaveChatRulesOverrides(rr, putJSON("/api/config/chat-rules-overrides", "x"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// Route dispatchers: exercise the GET branch which dispatches to the getter.
func TestRouteDispatchers_GET(t *testing.T) {
	ws := newTestServer(t)
	cases := []struct {
		name string
		fn   http.HandlerFunc
		path string
	}{
		{"config", ws.handleConfigRoute, "/api/config"},
		{"rss", ws.handleRssRoute, "/api/config/rss"},
		{"models", ws.handleModelsRoute, "/api/config/models"},
		{"modrules", ws.handleModerationRulesRoute, "/api/config/moderation-rules"},
		{"chatrules", ws.handleChatRulesOverridesRoute, "/api/config/chat-rules-overrides"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			c.fn(rr, httptest.NewRequest(http.MethodGet, c.path, nil))
			assert.Equal(t, http.StatusOK, rr.Code)
		})
	}
}
