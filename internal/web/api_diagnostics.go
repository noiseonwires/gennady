// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"encoding/base64"

	"gennadium/internal/database"
)

// Diagnostics and debug endpoints used by the Web UI's Diagnostics page:
// table stats, service health, Telegram webhook info, live API ping and
// the moderation/extractor prompt debugger.

func (h *apiHandler) handleGetDBStats(w http.ResponseWriter, r *http.Request) {
	stats := make(map[string]interface{})

	counts, err := h.db.GetTableStats()
	if err != nil {
		writeWebErrf(w, errGetTableStatsFailed, "failed to get table stats: %v", err)
		return
	}
	stats["table_counts"] = counts

	activeMutes, err := h.db.GetActiveMuteCount()
	if err == nil {
		stats["active_mutes"] = activeMutes
	}

	if h.db.IsLocal() {
		if info, err := os.Stat(h.config.Database.Path); err == nil {
			stats["database_size_bytes"] = info.Size()
		}
	}

	stats["database_provider"] = h.db.Provider()
	// remote_configured is true when a remote database is configured (URL +
	// auth token), regardless of which provider is currently active. The Web UI
	// uses it to surface the database clone tools even when running on local.
	stats["remote_configured"] = h.remoteConfigured()

	jsonResponse(w, stats)
}

// handleGetTokenUsage returns AI token counters per (service, model): daily
// (today, resets at the end of each day) and total (cumulative), split into
// input/output. It also returns per-service subtotals and a grand total.
func (h *apiHandler) handleGetTokenUsage(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Format("2006-01-02")
	rows, err := h.db.GetTokenUsageStats(today)
	if err != nil {
		writeWebErrf(w, errGetTableStatsFailed, "failed to get token usage: %v", err)
		return
	}

	type tokenTotals struct {
		DailyInput  int64 `json:"daily_input"`
		DailyOutput int64 `json:"daily_output"`
		TotalInput  int64 `json:"total_input"`
		TotalOutput int64 `json:"total_output"`
	}
	type serviceGroup struct {
		Service string                    `json:"service"`
		Models  []database.TokenUsageStat `json:"models"`
		Totals  tokenTotals               `json:"totals"`
	}

	var grand tokenTotals
	groups := []serviceGroup{}
	idxByService := map[string]int{}

	for _, m := range rows {
		grand.DailyInput += m.DailyInput
		grand.DailyOutput += m.DailyOutput
		grand.TotalInput += m.TotalInput
		grand.TotalOutput += m.TotalOutput

		gi, ok := idxByService[m.Service]
		if !ok {
			gi = len(groups)
			idxByService[m.Service] = gi
			groups = append(groups, serviceGroup{Service: m.Service})
		}
		g := &groups[gi]
		g.Models = append(g.Models, m)
		g.Totals.DailyInput += m.DailyInput
		g.Totals.DailyOutput += m.DailyOutput
		g.Totals.TotalInput += m.TotalInput
		g.Totals.TotalOutput += m.TotalOutput
	}

	if rows == nil {
		rows = []database.TokenUsageStat{}
	}

	jsonResponse(w, map[string]interface{}{
		"day":      today,
		"rows":     rows,
		"services": groups,
		"totals":   grand,
	})
}

func (h *apiHandler) handleGetDiagnostics(w http.ResponseWriter, r *http.Request) {
	// Build model diagnostics info: service keys for each configured model
	type modelInfo struct {
		Type       string `json:"type"`
		Index      int    `json:"index"`
		Deployment string `json:"deployment"`
		Endpoint   string `json:"endpoint"`
		ServiceKey string `json:"service_key"`
	}
	var models []modelInfo
	for i, m := range h.config.AI.LightModel.Configs {
		models = append(models, modelInfo{
			Type:       "light",
			Index:      i,
			Deployment: m.DeploymentName,
			Endpoint:   m.Endpoint,
			ServiceKey: fmt.Sprintf("openai_light:%s", m.DeploymentName),
		})
	}
	for i, m := range h.config.AI.FullModel.Configs {
		models = append(models, modelInfo{
			Type:       "full",
			Index:      i,
			Deployment: m.DeploymentName,
			Endpoint:   m.Endpoint,
			ServiceKey: fmt.Sprintf("openai_full:%s", m.DeploymentName),
		})
	}

	// Scheduled events
	events, _ := h.db.GetAllScheduledEvents()

	zone, _ := time.Now().Zone()

	// Collect BunnyNet environment info
	bunnyEnv := map[string]string{}
	for _, key := range []string{"BUNNYNET_MC_APPID", "BUNNYNET_MC_PODID", "BUNNYNET_MC_REGION"} {
		if v := os.Getenv(key); v != "" {
			bunnyEnv[key] = v
		}
	}

	// Configured chats: admin + moderation, with resolved names.
	type chatInfo struct {
		Role     string `json:"role"`
		ChatID   int64  `json:"chat_id"`
		ChatName string `json:"chat_name,omitempty"`
	}
	var chats []chatInfo
	if h.config.Admin.ChatID != 0 {
		chats = append(chats, chatInfo{
			Role:     "admin",
			ChatID:   h.config.Admin.ChatID,
			ChatName: h.resolveChatName(h.config.Admin.ChatID),
		})
	}
	for _, id := range h.config.GetModerationChatIDs() {
		chats = append(chats, chatInfo{
			Role:     "moderation",
			ChatID:   id,
			ChatName: h.resolveChatName(id),
		})
	}

	jsonResponse(w, map[string]interface{}{
		"services":         h.diagnostics.GetResults(),
		"models":           models,
		"prompt_warnings":  h.config.AI.CollectPromptWarnings(),
		"scheduled_events": events,
		"server_timezone":  zone,
		"telegram":         h.diagnostics.GetTelegramStatus(),
		"bunny_env":        bunnyEnv,
		"chats":            chats,
		"uptime_seconds":   int(time.Since(h.startedAt).Seconds()),
	})
}

// handleGetWebhookInfo fetches the current webhook status from Telegram API.
func (h *apiHandler) handleGetWebhookInfo(w http.ResponseWriter, r *http.Request) {
	token := h.config.BotToken
	if token == "" {
		writeWebErr(w, errBotTokenNotConfigured)
		return
	}

	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.telegram.org/bot"+token+"/getWebhookInfo", nil)
	if err != nil {
		writeWebErr(w, errFailedBuildRequest)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeWebErrf(w, errTelegramAPIUnreachable, "failed to reach Telegram API: %v", err)
		return
	}
	defer resp.Body.Close()

	// Relay the JSON response directly to the caller.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *apiHandler) handleTestAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	// Extract service name from URL: .../api/diagnostics/test/{service}
	// The service name may contain colons (e.g. "openai_light:gpt-4o-mini")
	idx := strings.Index(r.URL.Path, "/api/diagnostics/test/")
	if idx < 0 {
		writeWebErr(w, errMissingServiceName)
		return
	}
	service := strings.TrimPrefix(r.URL.Path[idx:], "/api/diagnostics/test/")
	if service == "" {
		writeWebErr(w, errMissingServiceName)
		return
	}

	if h.apiTester == nil {
		writeWebErr(w, errAPITesterUnavailable)
		return
	}

	statusCode, responseTime, errMsg := h.apiTester.TestExternalAPI(service)
	jsonResponse(w, map[string]interface{}{
		"service":          service,
		"status_code":      statusCode,
		"response_time_ms": responseTime.Milliseconds(),
		"error":            errMsg,
		"success":          statusCode >= 200 && statusCode < 300 && errMsg == "",
	})
}

// handleDebugModeration runs the moderation pipeline against the supplied
// message and returns the rendered system / user prompts and the raw model
// response. Used by the Diagnostics page to help authors debug prompts.
func (h *apiHandler) handleDebugModeration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	const prefix = "/api/diagnostics/debug/moderation/"
	idx := strings.Index(r.URL.Path, prefix)
	if idx < 0 {
		writeWebErr(w, errMissingServiceName)
		return
	}
	service := strings.TrimPrefix(r.URL.Path[idx:], prefix)
	if service == "" {
		writeWebErr(w, errMissingServiceName)
		return
	}
	if h.apiTester == nil {
		writeWebErr(w, errAPITesterUnavailable)
		return
	}
	body, err := decodeJSONLimited[struct {
		Message string `json:"message"`
	}](r, 1<<20)
	if err != nil {
		writeWebErrf(w, errInvalidJSONBody, "%v", err)
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		writeWebErr(w, errMessageContentRequired)
		return
	}
	start := time.Now()
	sys, usr, resp, err := h.apiTester.DebugModerationPrompt(service, body.Message)
	out := map[string]interface{}{
		"service":          service,
		"system_prompt":    sys,
		"user_prompt":      usr,
		"response":         resp,
		"response_time_ms": time.Since(start).Milliseconds(),
	}
	if err != nil {
		out["error"] = err.Error()
	}
	jsonResponse(w, out)
}

// handleDebugModerationByID looks up a stored message by its Telegram message ID
// and renders the moderation prompt exactly as the live pipeline would -
// rebuilding the author's profile, reputation and reply-to context from the
// database - then runs it against the supplied model. Used by the Diagnostics
// page to debug a real message that was previously seen by the bot.
func (h *apiHandler) handleDebugModerationByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	const prefix = "/api/diagnostics/debug/moderation-by-id/"
	idx := strings.Index(r.URL.Path, prefix)
	if idx < 0 {
		writeWebErr(w, errMissingServiceName)
		return
	}
	service := strings.TrimPrefix(r.URL.Path[idx:], prefix)
	if service == "" {
		writeWebErr(w, errMissingServiceName)
		return
	}
	if h.apiTester == nil {
		writeWebErr(w, errAPITesterUnavailable)
		return
	}
	body, err := decodeJSONLimited[struct {
		MessageID int   `json:"message_id"`
		ChatID    int64 `json:"chat_id"`
	}](r, 1<<20)
	if err != nil {
		writeWebErrf(w, errInvalidJSONBody, "%v", err)
		return
	}
	if body.MessageID == 0 {
		writeWebErr(w, errMessageContentRequired)
		return
	}
	start := time.Now()
	sys, usr, resp, info, err := h.apiTester.DebugModerationByMessageID(service, body.MessageID, body.ChatID)
	out := map[string]interface{}{
		"service":          service,
		"system_prompt":    sys,
		"user_prompt":      usr,
		"response":         resp,
		"info":             info,
		"response_time_ms": time.Since(start).Milliseconds(),
	}
	if err != nil {
		out["error"] = err.Error()
	}
	jsonResponse(w, out)
}

// handleDebugExtract fetches a URL via the supplied extractor service and
// returns the raw payload (as a JSON-encoded string) for inspection.
func (h *apiHandler) handleDebugExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	const prefix = "/api/diagnostics/debug/extract/"
	idx := strings.Index(r.URL.Path, prefix)
	if idx < 0 {
		writeWebErr(w, errMissingServiceName)
		return
	}
	service := strings.TrimPrefix(r.URL.Path[idx:], prefix)
	if service == "" {
		writeWebErr(w, errMissingServiceName)
		return
	}
	if h.apiTester == nil {
		writeWebErr(w, errAPITesterUnavailable)
		return
	}
	body, err := decodeJSONLimited[struct {
		URL string `json:"url"`
	}](r, 1<<20)
	if err != nil {
		writeWebErrf(w, errInvalidJSONBody, "%v", err)
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeWebErr(w, errURLRequired)
		return
	}
	start := time.Now()
	raw, err := h.apiTester.DebugURLExtraction(service, body.URL)
	out := map[string]interface{}{
		"service":          service,
		"raw":              raw,
		"response_time_ms": time.Since(start).Milliseconds(),
	}
	if err != nil {
		out["error"] = err.Error()
	}
	jsonResponse(w, out)
}

// handleDebugOCR runs an uploaded image through the supplied OCR / vision
// service and returns the extracted text (as a JSON-encoded string) for
// inspection. The image is sent as a base64 string (optionally a data: URI).
func (h *apiHandler) handleDebugOCR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	const prefix = "/api/diagnostics/debug/ocr/"
	idx := strings.Index(r.URL.Path, prefix)
	if idx < 0 {
		writeWebErr(w, errMissingServiceName)
		return
	}
	service := strings.TrimPrefix(r.URL.Path[idx:], prefix)
	if service == "" {
		writeWebErr(w, errMissingServiceName)
		return
	}
	if h.apiTester == nil {
		writeWebErr(w, errAPITesterUnavailable)
		return
	}
	// Allow up to ~16 MB of JSON to accommodate a base64-encoded image.
	body, err := decodeJSONLimited[struct {
		Image string `json:"image"`
	}](r, 16<<20)
	if err != nil {
		writeWebErrf(w, errInvalidJSONBody, "%v", err)
		return
	}
	b64 := strings.TrimSpace(body.Image)
	// Strip an optional data URI prefix ("data:image/png;base64,...").
	if i := strings.Index(b64, ","); strings.HasPrefix(b64, "data:") && i >= 0 {
		b64 = b64[i+1:]
	}
	if b64 == "" {
		writeWebErr(w, errImageRequired)
		return
	}
	imageData, decErr := base64.StdEncoding.DecodeString(b64)
	if decErr != nil {
		writeWebErrf(w, errInvalidImage, "%v", decErr)
		return
	}
	start := time.Now()
	raw, err := h.apiTester.DebugOCR(service, imageData)
	out := map[string]interface{}{
		"service":          service,
		"raw":              raw,
		"response_time_ms": time.Since(start).Milliseconds(),
	}
	if err != nil {
		out["error"] = err.Error()
	}
	jsonResponse(w, out)
}
