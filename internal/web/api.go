// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

// Web UI HTTP handlers.
//
// This package implements the JSON HTTP API + static asset server for the
// admin web UI. It deliberately depends only on database + config + the
// small interface set declared at the top of this file so it can be tested
// without spinning up the full bot.
//
// The handlers are split across several files for navigability:
//
//   api.go            (this file)      types, ctor helpers, config & core data endpoints
//   api_auth.go                        login, logout, OTP, auth-mode, i18n, version
//   api_diagnostics.go                 diagnostics page, debug moderation/extract, test API, webhook info, DB stats
//   api_env_apply.go                   reflection helper for applying env-file uploads onto Config
//   api_files.go                       download/upload of config / env / SQLite DB
//   api_helpers.go                     jsonResponse, extractIP, etc.
//   api_moderation.go                  mute / cmute / unmute / warn endpoints
//   api_profiles.go                    user profile list + delete with enrichment
//   api_system.go                      restart, logs
//   errors.go                          webError type, error registry, writeWebErr helpers
//   middleware.go                      requireMethod, decodeJSON, respondDecodeError

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"

	"gopkg.in/yaml.v3"
)

// redactedConfigValue is the sentinel returned in place of sensitive config
// values by handleGetConfig. handleSaveConfig treats an incoming sensitive
// field equal to this sentinel as "unchanged", so the real secret is never
// exposed to or required from the client on a round-trip.
const redactedConfigValue = "__redacted__"

// ExternalAPITester is implemented by the bot to run test calls from the web UI.
type ExternalAPITester interface {
	TestExternalAPI(serviceName string) (statusCode int, responseTime time.Duration, errMsg string)
	// DebugModerationPrompt runs the moderation pipeline for a single message
	// against the specified model (service key like "openai_light:deployment" or
	// "openai_full:deployment") and returns the rendered system prompt, user
	// prompt and raw model response.
	DebugModerationPrompt(serviceKey, message string) (systemPrompt, userPrompt, response string, err error)
	// DebugModerationByMessageID looks up a stored message by its Telegram
	// message ID (chatID may be 0 to search across all chats) and renders the
	// moderation prompt exactly as the live pipeline would - rebuilding the user
	// profile, reputation and reply-to context from the database - then runs it
	// against the specified model. The info map carries metadata about the
	// resolved message (chat, author, text, …) for display in the web UI.
	DebugModerationByMessageID(serviceKey string, messageID int, chatID int64) (systemPrompt, userPrompt, response string, info map[string]any, err error)
	// DebugURLExtraction fetches a URL with the specified extractor service
	// (e.g. "extractor_api", "diffbot") and returns the raw extracted payload
	// as a JSON-encoded string.
	DebugURLExtraction(serviceKey, targetURL string) (raw string, err error)
	// DebugOCR runs an uploaded image through the specified OCR / vision
	// service (e.g. "azure_vision", "ocr_space") and returns the
	// extracted text and metadata as a JSON-encoded string.
	DebugOCR(serviceKey string, imageData []byte) (raw string, err error)
}

// ChatNameResolver is implemented by the bot to look up a chat title by its ID.
// Used by the web UI to display human-readable chat names alongside numeric IDs.
type ChatNameResolver interface {
	GetChatName(chatID int64) string
}

// TopicNameResolver is implemented by the bot to look up a forum topic's
// human-readable name by (chat, thread) id. Returns "" when the name is
// unknown (the Bot API can't query topic names, so they're only known once
// observed or pre-seeded via config). Used by the web UI message view.
type TopicNameResolver interface {
	GetTopicName(chatID int64, threadID int) string
}

// ChatLister is implemented by the bot to enumerate known chats with their
// resolved metadata (title, is_forum flag). Surfaced via GET /api/chats so the
// admin UI can render chat-picker dropdowns next to ChatTopicList editors.
//
// The returned slice is encoded as JSON as-is; each element is expected to
// expose at least {id, title, is_forum, resolved} fields.
type ChatLister interface {
	ListChatsForUI() []any
}

// Moderator is implemented by the bot to perform moderation actions
// (mute / cruel mute / unmute / warn) initiated from the web UI.
type Moderator interface {
	WebMuteUser(userID, chatID int64, messageID int, durationMinutes int) error
	WebCruelMuteUser(userID, chatID int64, messageID int, durationMinutes int) error
	WebUnmuteUser(userID, chatID int64) error
	WebWarnUser(userID, chatID int64, messageID int) error
	WebDeleteUserMessages(userID, chatID int64, period string) (int, error)
	WebDeleteMessage(userID, chatID int64, messageID int) error
	WebRemoderateMessage(userID, chatID int64, messageID int) error
}

// apiHandler groups dependencies used by all API routes.
type apiHandler struct {
	config              *config.Config
	configFile          string
	configFromDB        bool // true when config source is DB (no config file at startup)
	db                  *database.DB
	auth                *AuthManager
	pathPrefix          string
	moderatorPathPrefix string // cookie path / link prefix for the moderator UI
	diagnostics         *DiagnosticsTracker
	apiTester           ExternalAPITester
	chatNames           ChatNameResolver
	chatLister          ChatLister
	topicNames          TopicNameResolver
	moderator           Moderator
	restartFunc         func(mode string)       // called to restart the bot process
	sendOTP             func(code string) error // sends OTP to super-admin via Telegram
	logBuffer           *LogBuffer
	startedAt           time.Time
	version             string
	gitCommit           string
	buildTime           string
	botURL              string
	botName             string
	botAuthor           string
	botLicense          string
}

// resolveChatName returns the chat title for the given chat ID, or "" if no resolver is configured.
func (h *apiHandler) resolveChatName(chatID int64) string {
	if h.chatNames == nil {
		return ""
	}
	return h.chatNames.GetChatName(chatID)
}

// resolveTopicName returns the forum topic name for (chatID, threadID), or ""
// when there is no resolver, no topic (thread 0), or the name is unknown.
func (h *apiHandler) resolveTopicName(chatID int64, threadID int) string {
	if h.topicNames == nil || threadID == 0 {
		return ""
	}
	return h.topicNames.GetTopicName(chatID, threadID)
}

// persistConfig saves the current in-memory config to the active config source
// (file or DB). Returns an error only if the write fails.
func (h *apiHandler) persistConfig() error {
	if h.configFromDB {
		kv, err := config.ConfigToDBStringMap(h.config)
		if err != nil {
			return err
		}
		if err := h.db.SetAllConfigValues(kv); err != nil {
			return err
		}
		h.config.WebUI.Password = kv["web_ui.password"]
		return nil
	}
	data, err := yaml.Marshal(h.config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return os.WriteFile(h.configFile, data, 0600)
}

// ── Config endpoints ──

func (h *apiHandler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	values := config.GetConfigValues(h.config)
	// Never expose secret values in plaintext over the API. Replace non-empty
	// sensitive fields with a sentinel; handleSaveConfig treats the sentinel as
	// "unchanged" so a round-trip save preserves the real secret.
	for key := range config.SensitiveConfigKeys() {
		if v, ok := values[key]; ok {
			if s, isStr := v.(string); isStr && s != "" {
				values[key] = redactedConfigValue
			}
		}
	}
	jsonResponse(w, values)
}

func (h *apiHandler) handleGetConfigMeta(w http.ResponseWriter, r *http.Request) {
	fields, sections := GetConfigMeta()
	jsonResponse(w, map[string]interface{}{
		"sections":      sections,
		"fields":        fields,
		"env_overrides": config.GetEnvOverrides(),
	})
}

func (h *apiHandler) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	updates, err := decodeJSON[map[string]interface{}](r)
	if err != nil {
		respondDecodeError(w, err)
		return
	}

	if len(updates) == 0 {
		writeWebErr(w, errNoFieldsToUpdate)
		return
	}

	// Fields whose value is currently supplied by an environment variable are
	// read-only here: the env var wins at every (re)load, so accepting an edit
	// would silently drift the stored config away from what the bot actually
	// runs - exactly the trap that crashes the bot on the next restart. The web
	// UI disables these inputs; we enforce it server-side as defense in depth.
	envLocked := make(map[string]bool)
	for _, key := range config.GetEnvOverrides() {
		envLocked[key] = true
	}

	// Apply updates via YAML dot-paths. Hold the config write lock so the bot's
	// concurrent reads never observe a half-applied batch. Snapshot the pre-edit
	// config first so a batch rejected by validation can be rolled back in
	// memory, never leaving the running config half-applied.
	var (
		setErrs        []string
		ignoredEnvKeys []string
		validationErr  error
	)
	sensitive := config.SensitiveConfigKeys()
	config.Lock()
	snapshot := config.ConfigToStringMap(h.config)
	for key, val := range updates {
		// Reject edits to env-pinned fields (see above).
		if envLocked[key] {
			ignoredEnvKeys = append(ignoredEnvKeys, key)
			continue
		}
		// Skip sensitive fields left at the redaction sentinel - the client is
		// echoing back the masked value, so the existing secret must be kept.
		if sensitive[key] {
			if s, ok := val.(string); ok && s == redactedConfigValue {
				continue
			}
		}
		result := config.SetConfigValue(h.config, key, val)
		if result != "" && result != "ok" {
			setErrs = append(setErrs, fmt.Sprintf("%s: %s", key, result))
		}
	}
	// Validate the whole config as it will actually run (env overrides are
	// already applied in memory). This catches cross-field problems such as a
	// post_to / included_topics ref pointing at a chat that the effective
	// moderation.chat_id list - possibly narrowed by an env var - no longer
	// contains. Prompt checks run only once the structural config is sound.
	if len(setErrs) == 0 {
		validationErr = h.config.Validate()
		if validationErr == nil && h.config.AI.Enabled {
			validationErr = h.config.AI.ValidatePrompts()
		}
	}
	// Roll the in-memory config back to its pre-edit state on any failure.
	if len(setErrs) > 0 || validationErr != nil {
		config.ApplyStringMap(h.config, snapshot)
	}
	config.Unlock()

	if len(setErrs) > 0 {
		writeWebErrf(w, errConfigValidation, "%s", strings.Join(setErrs, "\n"))
		return
	}
	if validationErr != nil {
		writeWebErrf(w, errConfigValidation, "%v", validationErr)
		return
	}

	// Persist config to the active source (file or DB)
	if err := h.persistConfig(); err != nil {
		log.Printf("⚠️  Failed to save config: %v", err)
		writeWebErrf(w, errConfigSaveFailed, "failed to save config: %v", err)
		return
	}

	if len(ignoredEnvKeys) > 0 {
		log.Printf("WebUI: ignored %d env-overridden field(s) on save (set via environment, not editable here): %s", len(ignoredEnvKeys), strings.Join(ignoredEnvKeys, ", "))
	}
	log.Printf("WebUI: config updated (%d fields changed)", len(updates)-len(ignoredEnvKeys))
	jsonResponse(w, map[string]string{"status": "ok", "message": "Configuration saved. Restart may be needed for scheduled event settings, such as enabling or disabling scheduled tasks or changing their time or period. A soft restart is enough."})
}

func (h *apiHandler) handleGetRssFeeds(w http.ResponseWriter, r *http.Request) {
	feeds := h.config.AI.Rss.Feeds
	if feeds == nil {
		feeds = []config.RssFeed{}
	}
	jsonResponse(w, feeds)
}

// handleGetTopics returns the static forum-topic name registry (config `topics`).
func (h *apiHandler) handleGetTopics(w http.ResponseWriter, r *http.Request) {
	topics := h.config.Topics
	if topics == nil {
		topics = []config.TopicNameRef{}
	}
	jsonResponse(w, topics)
}

// handleSaveTopics replaces the static forum-topic name registry. Fully-blank
// rows are dropped; the rest must carry a chat id, a positive forum thread id
// and a non-empty name. Names take effect after a restart (config is the
// source of truth for these entries; live-observed names still win).
func (h *apiHandler) handleSaveTopics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	topics, err := decodeJSON[[]config.TopicNameRef](r)
	if err != nil {
		respondDecodeError(w, err)
		return
	}

	cleaned := make([]config.TopicNameRef, 0, len(topics))
	for i, t := range topics {
		name := strings.TrimSpace(t.Name)
		// Skip a completely empty row (e.g. a freshly added, unfilled entry).
		if t.Chat == 0 && t.Topic == 0 && name == "" {
			continue
		}
		if t.Chat == 0 {
			writeWebErrf(w, errTopicChatRequired, "topic #%d: chat is required", i+1)
			return
		}
		if t.Topic <= 0 {
			writeWebErrf(w, errTopicInvalidThread, "topic #%d: topic id must be a positive forum thread id", i+1)
			return
		}
		if name == "" {
			writeWebErrf(w, errTopicNameRequired, "topic #%d: name is required", i+1)
			return
		}
		cleaned = append(cleaned, config.TopicNameRef{Chat: t.Chat, Topic: t.Topic, Name: name})
	}

	config.Lock()
	h.config.Topics = cleaned
	config.Unlock()

	if err := h.persistConfig(); err != nil {
		log.Printf("⚠️  Failed to save config: %v", err)
		writeWebErrf(w, errConfigSaveFailed, "failed to save config: %v", err)
		return
	}

	log.Printf("WebUI: topic names updated (%d topics)", len(cleaned))
	jsonResponse(w, map[string]string{"status": "ok"})
}

// handleGetModerationRules returns the list of LLM-output → action rules
// driving auto-moderation.
func (h *apiHandler) handleGetModerationRules(w http.ResponseWriter, r *http.Request) {
	rules := h.config.AI.ContentModeration.Rules
	if rules == nil {
		rules = []config.ModerationRule{}
	}
	jsonResponse(w, map[string]interface{}{
		"rules": rules,
	})
}

// handleSaveModerationRules replaces the moderation rule list. Validates each
// rule's action against the IsValidModerationAction whitelist.
func (h *apiHandler) handleSaveModerationRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	req, err := decodeJSON[struct {
		Rules []config.ModerationRule `json:"rules"`
	}](r)
	if err != nil {
		respondDecodeError(w, err)
		return
	}
	// Validate and normalize.
	cleaned := make([]config.ModerationRule, 0, len(req.Rules))
	for i, rule := range req.Rules {
		trigger := strings.TrimSpace(rule.Trigger)
		if trigger == "" {
			writeWebErrf(w, errRuleTriggerRequired, "rule #%d: trigger is required", i+1)
			return
		}
		rule.Action = config.NormalizeModerationAction(rule.Action)
		if !config.IsValidModerationAction(rule.Action) {
			writeWebErrf(w, errRuleInvalidAction, "rule #%d: invalid action %q (allowed: report, warn, mute, delete)", i+1, rule.Action)
			return
		}
		rule.Trigger = trigger
		rule.Description = strings.TrimSpace(rule.Description)
		cleaned = append(cleaned, rule)
	}

	config.Lock()
	h.config.AI.ContentModeration.Rules = cleaned
	config.Unlock()
	if err := h.persistConfig(); err != nil {
		log.Printf("⚠️  Failed to save config: %v", err)
		writeWebErrf(w, errConfigSaveFailed, "failed to save config: %v", err)
		return
	}
	log.Printf("WebUI: moderation rules updated (%d rules)", len(cleaned))
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *apiHandler) handleGetModels(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]interface{}{
		"light_model": h.config.AI.LightModel.Configs,
		"full_model":  h.config.AI.FullModel.Configs,
	})
}

func (h *apiHandler) handleSaveModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	req, err := decodeJSON[struct {
		LightModel []config.AIModelConfig `json:"light_model"`
		FullModel  []config.AIModelConfig `json:"full_model"`
	}](r)
	if err != nil {
		respondDecodeError(w, err)
		return
	}

	// When a model list is null (not provided), keep existing config values.
	if req.LightModel == nil {
		req.LightModel = h.config.AI.LightModel.Configs
	}
	if req.FullModel == nil {
		req.FullModel = h.config.AI.FullModel.Configs
	}

	// Validate only the models that were explicitly provided.
	for i, m := range req.LightModel {
		if m.Endpoint == "" || m.DeploymentName == "" {
			writeWebErrf(w, errModelEndpointRequired, "light model #%d: endpoint and deployment_name are required", i+1)
			return
		}
	}
	for i, m := range req.FullModel {
		if m.Endpoint == "" || m.DeploymentName == "" {
			writeWebErrf(w, errModelEndpointRequired, "full model #%d: endpoint and deployment_name are required", i+1)
			return
		}
	}

	config.Lock()
	h.config.AI.LightModel.Configs = req.LightModel
	h.config.AI.FullModel.Configs = req.FullModel
	config.Unlock()

	if err := h.persistConfig(); err != nil {
		log.Printf("⚠️  Failed to save config: %v", err)
		writeWebErrf(w, errConfigSaveFailed, "failed to save config: %v", err)
		return
	}

	log.Printf("WebUI: AI models updated (light=%d, full=%d)", len(req.LightModel), len(req.FullModel))
	jsonResponse(w, map[string]string{"status": "ok", "message": "Models saved. Restart required."})
}

func (h *apiHandler) handleSaveRssFeeds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	feeds, err := decodeJSON[[]config.RssFeed](r)
	if err != nil {
		respondDecodeError(w, err)
		return
	}

	config.Lock()
	h.config.AI.Rss.Feeds = feeds
	config.Unlock()

	if err := h.persistConfig(); err != nil {
		log.Printf("⚠️  Failed to save config: %v", err)
		writeWebErrf(w, errConfigSaveFailed, "failed to save config: %v", err)
		return
	}

	log.Printf("WebUI: RSS feeds updated (%d feeds)", len(feeds))
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ── Moderation data endpoints ──

func (h *apiHandler) handleGetActions(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if n, err := strconv.Atoi(offsetStr); err == nil && n > 0 {
			offset = n
		}
	}

	actions, err := h.db.GetRecentActionsEnrichedPaged(limit, offset)
	if err != nil {
		writeWebErrf(w, errGetActionsFailed, "failed to get actions: %v", err)
		return
	}
	total, err := h.db.CountActions()
	if err != nil {
		writeWebErrf(w, errGetActionsFailed, "failed to count actions: %v", err)
		return
	}

	type actionWithChat struct {
		database.ActionEnriched
		ChatName string `json:"chat_name,omitempty"`
	}
	enriched := make([]actionWithChat, len(actions))
	for i, a := range actions {
		enriched[i] = actionWithChat{ActionEnriched: a, ChatName: h.resolveChatName(a.ChatID)}
	}
	jsonResponse(w, map[string]any{"actions": enriched, "total": total})
}

func (h *apiHandler) handleGetMutedUsers(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if n, err := strconv.Atoi(offsetStr); err == nil && n > 0 {
			offset = n
		}
	}

	users, err := h.db.GetActiveMutedUsers()
	if err != nil {
		writeWebErrf(w, errGetMutedFailed, "failed to get muted users: %v", err)
		return
	}
	type mutedWithChat struct {
		database.MutedUser
		ChatName string `json:"chat_name,omitempty"`
	}
	enriched := make([]mutedWithChat, len(users))
	for i, u := range users {
		enriched[i] = mutedWithChat{MutedUser: u, ChatName: h.resolveChatName(u.ChatID)}
	}
	// Most recently muted first.
	sort.Slice(enriched, func(i, j int) bool {
		return enriched[i].MutedAt.After(enriched[j].MutedAt)
	})
	total := len(enriched)
	// Apply pagination over the in-memory (cache-derived) slice.
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := enriched[offset:end]
	jsonResponse(w, map[string]any{"users": page, "total": total})
}

// ── Messages ──

// uiMessage is a database message enriched for the Web UI message views with
// human-readable chat/topic names and formatted reactions. Reply parents are
// deliberately NOT inlined: the UI lazily loads them through the single-message
// endpoint so it can walk the reply chain recursively without bloating every
// list payload.
type uiMessage struct {
	database.MessageInfoEnriched
	ChatName      string `json:"chat_name,omitempty"`
	TopicName     string `json:"topic_name,omitempty"`
	ReactionsText string `json:"reactions_text,omitempty"`
}

// enrichUIMessage decorates a raw enriched message with the display-only fields
// the Web UI needs (chat/topic names, formatted reactions).
func (h *apiHandler) enrichUIMessage(m database.MessageInfoEnriched) uiMessage {
	return uiMessage{
		MessageInfoEnriched: m,
		ChatName:            h.resolveChatName(m.ChatID),
		TopicName:           h.resolveTopicName(m.ChatID, m.MessageThreadID),
		ReactionsText:       database.FormatReactionsDisplay(m.Reactions),
	}
}

func (h *apiHandler) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	// Single message lookup by message_id + chat_id. Returns the same enriched
	// shape as the list so the reply-chain viewer can render a fetched parent
	// with all its data.
	if msgIDStr := r.URL.Query().Get("message_id"); msgIDStr != "" {
		chatIDStr := r.URL.Query().Get("chat_id")
		msgID, err1 := strconv.Atoi(msgIDStr)
		chatID, err2 := strconv.ParseInt(chatIDStr, 10, 64)
		if err1 != nil || err2 != nil {
			writeWebErr(w, errInvalidMsgOrChatID)
			return
		}
		msg, err := h.db.GetMessageInfoEnriched(msgID, chatID)
		if err != nil || msg == nil {
			jsonResponse(w, map[string]interface{}{"messages": []interface{}{}, "total": 0})
			return
		}
		jsonResponse(w, map[string]interface{}{
			"messages": []interface{}{h.enrichUIMessage(*msg)},
			"total":    1,
		})
		return
	}

	// List filters: narrow by chat and/or author. A standalone chat_id (without
	// message_id) acts as a chat filter; user matches a user id or username
	// substring. Both are optional - empty means "all".
	var filterChatID int64
	if v := r.URL.Query().Get("chat_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			filterChatID = n
		}
	}
	userFilter := strings.TrimSpace(r.URL.Query().Get("user"))

	messages, total, err := h.db.GetRecentMessagesForUIEnriched(limit, offset, filterChatID, userFilter)
	if err != nil {
		writeWebErrf(w, errGetMessagesFailed, "failed to get messages: %v", err)
		return
	}

	enriched := make([]uiMessage, len(messages))
	for i, m := range messages {
		enriched[i] = h.enrichUIMessage(m)
	}

	jsonResponse(w, map[string]interface{}{
		"messages": enriched,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

func (h *apiHandler) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	req, err := decodeJSON[struct {
		MessageID int   `json:"message_id"`
		ChatID    int64 `json:"chat_id"`
	}](r)
	if err != nil {
		respondDecodeError(w, err)
		return
	}
	if req.MessageID == 0 {
		writeWebErr(w, errMessageIDRequired)
		return
	}

	if err := h.db.DeleteMessageInfo(req.MessageID, req.ChatID); err != nil {
		writeWebErrf(w, errDeleteMessageFailed, "failed to delete message: %v", err)
		return
	}

	jsonResponse(w, map[string]string{"status": "ok"})
}
