// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	h := newTestHandler(t, cfg)
	rr := get(t, h, h.handleGetConfig, "/api/config")
	assert.Equal(t, http.StatusOK, rr.Code)
	var values map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &values))
	// The bot token must be redacted, never returned in plaintext.
	assert.NotEqual(t, "super-secret-token", values["bot_token"])
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
	h := newTestHandlerWithFile(t, cfg)
	rr := httptest.NewRecorder()
	// A sensitive field echoed back as the redaction sentinel is skipped,
	// preserving the existing secret.
	h.handleSaveConfig(rr, putJSON("/api/config", `{"bot_token":"`+redactedConfigValue+`"}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "keep-me", h.config.BotToken)
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
