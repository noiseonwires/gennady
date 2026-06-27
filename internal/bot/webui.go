// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"
)

// recordDiagnostics records an external API call result if a diagnostics recorder is set.
func (b *Bot) recordDiagnostics(service string, start time.Time, resp *http.Response, err error, requestURL string) {
	if b.diagnostics == nil {
		return
	}
	sc := 0
	if resp != nil {
		sc = resp.StatusCode
	}
	errMsg := ""
	if err != nil {
		errMsg = redactSensitiveText(err.Error())
	}
	b.diagnostics.RecordCall(diagnosticsServiceKey(service), sc, errMsg, time.Since(start), redactSensitiveURL(requestURL))
}

// diagnosticsServiceKey normalizes internal per-endpoint API labels (used for
// logging) to the single service key the Web UI Diagnostics page tracks.
// The Cloudflare Browser Rendering integration calls two endpoints
// ("cloudflare_browser" for /markdown and "cloudflare_content" for /content),
// but the UI shows them as one "cloudflare" service.
func diagnosticsServiceKey(service string) string {
	switch service {
	case "cloudflare_browser", "cloudflare_content":
		return "cloudflare"
	default:
		return service
	}
}

// TestExternalAPI makes a lightweight test call to the specified external service.
// Returns HTTP status code, response time, and error message (empty on success).
// For AI models, supports "openai_light:deployment" or "openai_full:deployment" to test specific model.
func (b *Bot) TestExternalAPI(serviceName string) (statusCode int, responseTime time.Duration, errMsg string) {
	start := time.Now()

	// Handle per-model AI testing: "openai_light:deployment_name", "openai_full:deployment_name"
	if strings.HasPrefix(serviceName, "openai_light:") || strings.HasPrefix(serviceName, "openai_full:") {
		parts := strings.SplitN(serviceName, ":", 2)
		modelType := parts[0]
		deployment := parts[1]
		var models *config.AIModelConfigs
		if modelType == "openai_light" {
			models = &b.config.AI.LightModel
		} else {
			models = &b.config.AI.FullModel
		}
		for i := 0; i < models.Count(); i++ {
			m := models.Get(i)
			if m.DeploymentName == deployment {
				_, err := b.callAzureOpenAIWithConfig("webui_debug", "Say OK", "Reply: OK", m, 5, false)
				return testResult(start, err)
			}
		}
		return 0, 0, fmt.Sprintf("model with deployment %q not found", deployment)
	}

	switch serviceName {
	case "openai_light":
		_, err := b.callAzureOpenAIWithConfig("webui_debug", "Say OK", "Reply: OK", b.config.AI.LightModel.Get(0), 5, false)
		return testResult(start, err)
	case "openai_full":
		_, err := b.callAzureOpenAIWithConfig("webui_debug", "Say OK", "Reply: OK", b.config.AI.FullModel.Get(0), 5, false)
		return testResult(start, err)
	case "azure_vision":
		return b.testEndpointHealth(start, "azure_vision", b.config.AI.ContentModeration.VisionEndpoint)
	case "content_safety":
		return b.testEndpointHealth(start, "content_safety", b.config.AI.ContentModeration.ContentSafetyEndpoint)
	case "ocr_space":
		status, err := b.testOCRSpace()
		if err != nil {
			return status, time.Since(start), err.Error()
		}
		return status, time.Since(start), ""
	case "weather":
		_, _, err := b.fetchCurrentWeather()
		return testResult(start, err)
	case "holidays":
		_, err := b.fetchHolidays("US")
		return testResult(start, err)
	case "wikipedia":
		_, err := b.fetchWikipediaOnThisDay()
		return testResult(start, err)
	case "extractor_api":
		_, _, err := b.fetchWithExtractorAPI("https://wikipedia.org")
		return testResult(start, err)
	case "diffbot":
		_, _, _, err := b.fetchWithDiffbot("https://wikipedia.org")
		return testResult(start, err)
	case "cloudflare":
		_, _, err := b.fetchWithCloudflare("https://wikipedia.org")
		return testResult(start, err)
	default:
		return 0, 0, "unknown service: " + serviceName
	}
}

func testResult(start time.Time, err error) (int, time.Duration, string) {
	elapsed := time.Since(start)
	if err != nil {
		return 0, elapsed, err.Error()
	}
	return 200, elapsed, ""
}

func (b *Bot) testEndpointHealth(start time.Time, service string, endpoint string) (int, time.Duration, string) {
	if endpoint == "" {
		return 0, 0, "endpoint not configured"
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(endpoint)
	b.recordDiagnostics(service, start, resp, err, endpoint)
	elapsed := time.Since(start)
	if err != nil {
		return 0, elapsed, err.Error()
	}
	defer resp.Body.Close()
	return resp.StatusCode, elapsed, ""
}

// resolveModelConfig parses a diagnostics service key of the form
// "openai_light:deployment" or "openai_full:deployment" and returns the
// matching model config from the bot configuration. Returns an error when the
// key is malformed or no matching deployment is configured.
func (b *Bot) resolveModelConfig(serviceKey string) (config.AIModelConfig, error) {
	parts := strings.SplitN(serviceKey, ":", 2)
	if len(parts) != 2 || (parts[0] != "openai_light" && parts[0] != "openai_full") {
		return config.AIModelConfig{}, fmt.Errorf("unsupported service key %q (expected openai_light:<deployment> or openai_full:<deployment>)", serviceKey)
	}
	var models *config.AIModelConfigs
	if parts[0] == "openai_light" {
		models = &b.config.AI.LightModel
	} else {
		models = &b.config.AI.FullModel
	}
	for i := 0; i < models.Count(); i++ {
		m := models.Get(i)
		if m.DeploymentName == parts[1] {
			return m, nil
		}
	}
	return config.AIModelConfig{}, fmt.Errorf("model with deployment %q not found", parts[1])
}

// DebugModerationPrompt renders the moderation prompt for the supplied
// message exactly as the live pipeline would (chat rules + empty user profile
// + empty reply-to), runs the request against the specified model and returns
// the prompts and raw response. No DB writes or reactions are performed.
//
// Because there is no real author for an ad-hoc message, the {{new_user_rules}}
// placeholder is rendered with the configured rules text as-is so admins can
// preview it; the empty {{user_profile}}/{{user_reputation}}/{{reply_to}}
// context mirrors a brand-new message with no history.
func (b *Bot) DebugModerationPrompt(serviceKey, message string) (systemPrompt, userPrompt, response string, err error) {
	modelCfg, err := b.resolveModelConfig(serviceKey)
	if err != nil {
		return "", "", "", err
	}
	if !b.config.AI.ContentModeration.Enabled {
		// Still allow debugging - prompts are configured even if disabled.
	}
	replacements := map[string]string{
		"message":         message,
		"chat_rules":      b.config.ChatRulesFor(0),
		"user_profile":    "",
		"user_reputation": "",
		"reply_to":        "",
		"new_user_rules":  b.config.AI.ContentModeration.NewUserRules,
	}
	systemPrompt = applyReplacements(b.config.AI.ContentModeration.Prompt.System, replacements)
	userPrompt = applyReplacements(b.config.AI.ContentModeration.Prompt.User, replacements)
	resp, callErr := b.callAzureOpenAIWithConfig("webui_debug", userPrompt, systemPrompt, modelCfg, 200, false)
	return systemPrompt, userPrompt, resp, callErr
}

// DebugModerationByMessageID looks up a stored message by its Telegram message
// ID (optionally scoped to a chat) and renders the moderation prompt exactly as
// the live pipeline would: it rebuilds the {{user_profile}}, {{user_reputation}},
// {{reply_to}} and {{new_user_rules}} context from the database for the original
// author, runs the request against the specified model and returns the prompts,
// raw response and a metadata map describing the resolved message. No DB writes
// or reactions are performed. chatID may be 0 to search across all chats for the
// message ID.
func (b *Bot) DebugModerationByMessageID(serviceKey string, messageID int, chatID int64) (systemPrompt, userPrompt, response string, info map[string]any, err error) {
	modelCfg, err := b.resolveModelConfig(serviceKey)
	if err != nil {
		return "", "", "", nil, err
	}

	var msg *database.MessageInfo
	if chatID != 0 {
		msg, err = b.db.GetMessageInfo(messageID, chatID)
	} else {
		msg, err = b.db.FindMessageByID(messageID)
	}
	if err != nil {
		return "", "", "", nil, fmt.Errorf("message %d not found in database", messageID)
	}

	// Rebuild reply-to context from the stored reply target, mirroring the live
	// pipeline's "[Reply to <name>]: <text>" formatting.
	replyToText := ""
	if msg.ReplyToMessageID != nil {
		if replyMsg, rerr := b.db.GetMessageInfo(*msg.ReplyToMessageID, msg.ChatID); rerr == nil && replyMsg != nil {
			replyText := strings.TrimSpace(replyMsg.Text)
			if replyText != "" {
				if replyMsg.Username != "" {
					replyToText = fmt.Sprintf("[Reply to %s]: %s", replyMsg.Username, replyText)
				} else {
					replyToText = fmt.Sprintf("[Reply to message]: %s", replyText)
				}
			}
		}
	}

	userProfile := b.getUserProfileForModeration(msg.UserID, msg.ChatID)
	userReputation := b.getUserReputationForModeration(msg.UserID)
	newUserRules := b.newUserRulesFor(msg.UserID)

	replacements := map[string]string{
		"message":         msg.Text,
		"chat_rules":      b.config.ChatRulesFor(msg.ChatID),
		"user_profile":    userProfile,
		"user_reputation": userReputation,
		"reply_to":        replyToText,
		"new_user_rules":  newUserRules,
	}
	systemPrompt = applyReplacements(b.config.AI.ContentModeration.Prompt.System, replacements)
	userPrompt = applyReplacements(b.config.AI.ContentModeration.Prompt.User, replacements)

	resp, callErr := b.callAzureOpenAIWithConfig("webui_debug", userPrompt, systemPrompt, modelCfg, 200, false)

	info = map[string]any{
		"message_id":      msg.MessageID,
		"chat_id":         msg.ChatID,
		"chat_name":       b.GetChatName(msg.ChatID),
		"user_id":         msg.UserID,
		"username":        msg.Username,
		"message_text":    msg.Text,
		"reply_to":        replyToText,
		"user_profile":    userProfile,
		"user_reputation": userReputation,
		"new_user_rules":  newUserRules,
		"timestamp":       msg.Timestamp.Format(time.RFC3339),
	}
	return systemPrompt, userPrompt, resp, info, callErr
}

// DebugURLExtraction fetches a URL with the requested extractor service and
// returns the raw payload encoded as JSON so the UI can render every field.
// Supported services: "extractor_api", "diffbot", "cloudflare", "manual".
func (b *Bot) DebugURLExtraction(serviceKey, targetURL string) (string, error) {
	type result struct {
		Service  string `json:"service"`
		Content  string `json:"content,omitempty"`
		Title    string `json:"title,omitempty"`
		Language string `json:"language,omitempty"`
		Length   int    `json:"content_length"`
	}
	encode := func(r result) (string, error) {
		buf, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
	switch serviceKey {
	case "extractor_api":
		c, t, err := b.fetchWithExtractorAPI(targetURL)
		if err != nil {
			return "", err
		}
		return encode(result{Service: serviceKey, Content: c, Title: t, Length: len(c)})
	case "diffbot":
		c, t, l, err := b.fetchWithDiffbot(targetURL)
		if err != nil {
			return "", err
		}
		return encode(result{Service: serviceKey, Content: c, Title: t, Language: l, Length: len(c)})
	case "cloudflare":
		c, t, err := b.fetchWithCloudflare(targetURL)
		if err != nil {
			return "", err
		}
		return encode(result{Service: serviceKey, Content: c, Title: t, Length: len(c)})
	case "manual":
		c, t, err := b.fetchWithManualExtraction(targetURL)
		if err != nil {
			return "", err
		}
		return encode(result{Service: serviceKey, Content: c, Title: t, Length: len(c)})
	default:
		return "", fmt.Errorf("unsupported extractor service %q", serviceKey)
	}
}

// DebugOCR runs the supplied image through the chosen OCR / vision service and
// returns the extracted text (plus a little metadata) as a JSON-encoded string
// so the web UI can show exactly what the moderation pipeline would see.
// Supported services: "azure_vision", "ocr_space".
func (b *Bot) DebugOCR(serviceKey string, imageData []byte) (string, error) {
	if len(imageData) == 0 {
		return "", fmt.Errorf("no image data provided")
	}
	type result struct {
		Service              string `json:"service"`
		Text                 string `json:"text"`
		Length               int    `json:"text_length"`
		ImageBytes           int    `json:"image_bytes"`
		ContentSafetyFlagged bool   `json:"content_safety_flagged,omitempty"`
	}
	encode := func(r result) (string, error) {
		buf, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
	switch serviceKey {
	case "azure_vision":
		text, flagged, err := b.analyzeImageWithVision(imageData)
		if err != nil {
			return "", err
		}
		return encode(result{Service: serviceKey, Text: text, Length: len(text), ImageBytes: len(imageData), ContentSafetyFlagged: flagged})
	case "ocr_space":
		text, err := b.analyzeImageWithOCRSpace(imageData)
		if err != nil {
			return "", err
		}
		return encode(result{Service: serviceKey, Text: text, Length: len(text), ImageBytes: len(imageData)})
	default:
		return "", fmt.Errorf("unsupported OCR service %q", serviceKey)
	}
}
