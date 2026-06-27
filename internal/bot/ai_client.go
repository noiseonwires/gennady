// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/i18n"
)

// AzureOpenAIRequest represents the request structure for Azure OpenAI API.
type AzureOpenAIRequest struct {
	Model       string    `json:"model,omitempty"` // required by the standard OpenAI API; omitted for Azure (deployment is in the URL)
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

// Message represents a message in an AI conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AzureOpenAIResponse represents the response structure from Azure OpenAI API.
type AzureOpenAIResponse struct {
	Choices []Choice          `json:"choices"`
	Usage   *AzureOpenAIUsage `json:"usage,omitempty"`
	Error   *AzureOpenAIError `json:"error,omitempty"`
}

// AzureOpenAIUsage carries the token accounting returned by OpenAI/Azure.
type AzureOpenAIUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// AzureOpenAIError represents an error from Azure OpenAI API.
type AzureOpenAIError struct {
	Code       string                 `json:"code"`
	Message    string                 `json:"message"`
	InnerError *AzureOpenAIInnerError `json:"innererror,omitempty"`
}

// AzureOpenAIInnerError represents the inner error with content filter results.
type AzureOpenAIInnerError struct {
	Code                string                           `json:"code"`
	ContentFilterResult map[string]ContentFilterCategory `json:"content_filter_result,omitempty"`
}

// ContentFilterCategory represents a single content filter category result.
type ContentFilterCategory struct {
	Filtered bool   `json:"filtered"`
	Severity string `json:"severity,omitempty"`
}

// Choice represents a choice in the AI response.
type Choice struct {
	Message              Message                          `json:"message"`
	ContentFilterResults map[string]ContentFilterCategory `json:"content_filter_results,omitempty"`
}

// ContentFilterError is returned when Azure AI content filter is triggered.
// It carries the detailed category scores from the Azure response.
type ContentFilterError struct {
	Details string
}

func (e *ContentFilterError) Error() string {
	return "content_filter_triggered"
}

// RateLimitError is returned when an AI endpoint replies with HTTP 429
// (Azure: RateLimitReached / Too Many Requests). RetryAfter is parsed from
// the Retry-After header (delta-seconds or HTTP-date); zero if absent or
// unparseable. The retry loop uses it to back off accurately instead of
// hammering the endpoint with the default exponential schedule.
type RateLimitError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited (HTTP %d, retry after %s)", e.StatusCode, e.RetryAfter)
	}
	return fmt.Sprintf("rate limited (HTTP %d)", e.StatusCode)
}

// formatContentFilterDetails extracts category scores from an Azure content filter error.
func formatContentFilterDetails(azureErr *AzureOpenAIError) string {
	if azureErr == nil {
		return ""
	}
	var parts []string
	if azureErr.InnerError != nil && azureErr.InnerError.ContentFilterResult != nil {
		// Sort categories for stable output
		categories := make([]string, 0, len(azureErr.InnerError.ContentFilterResult))
		for cat := range azureErr.InnerError.ContentFilterResult {
			categories = append(categories, cat)
		}
		sort.Strings(categories)
		for _, cat := range categories {
			result := azureErr.InnerError.ContentFilterResult[cat]
			severity := result.Severity
			if severity == "" {
				severity = "n/a"
			}
			flag := "✅"
			if result.Filtered {
				flag = "🚫"
			}
			parts = append(parts, fmt.Sprintf("%s %s: %s", flag, cat, severity))
		}
	}
	if len(parts) == 0 {
		return azureErr.Message
	}
	return strings.Join(parts, " | ")
}

// applyReplacements replaces placeholders in a prompt template with actual values.
func applyReplacements(prompt string, replacements map[string]string) string {
	result := prompt
	for key, value := range replacements {
		result = strings.ReplaceAll(result, "{{"+key+"}}", value)
	}
	return result
}

// generateSarcasticSelfDefense returns a static response when someone tries to punish the bot.
func (b *Bot) generateSarcasticSelfDefense(adminName string, actionType string) string {
	return "😏"
}

// dumpPromptToLog logs the full AI prompt to stdout for debugging.
func (b *Bot) dumpPromptToLog(requestType, systemPrompt, userPrompt string) {
	if !b.config.Debug.DebugExternalAPIs {
		return
	}
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("=== AI PROMPT DUMP [%s] ===\nTimestamp: %s\nSystem: %s\nUser: %s",
		requestType, timestamp, truncateForLog(systemPrompt), truncateForLog(userPrompt))
}

// isRetryableError checks if an error is retryable (network/timeout errors,
// rate limits, or transient 5xx).
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return true
	}
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "context deadline exceeded") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "TLS handshake timeout") ||
		strings.Contains(errStr, "status 5") ||
		strings.Contains(errStr, "status 429") ||
		strings.Contains(errStr, "Too Many Requests") ||
		strings.Contains(errStr, "RateLimitReached") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "EOF")
}

// callAzureOpenAIWithRetries makes a request to Azure OpenAI API with retry logic.
// service is a short label identifying the calling feature (e.g. "content_moderation",
// "morning_greeting") used for per-service token-usage accounting.
func (b *Bot) callAzureOpenAIWithRetries(service string, prompt string, systemPrompt string, modelConfigs config.AIModelConfigs, maxTokens int, maxRetries int) (string, error) {
	return b.callAzureOpenAIWithRetriesAndBackoff(service, prompt, systemPrompt, modelConfigs, maxTokens, maxRetries, nil)
}

// callAzureOpenAIWithRetriesAndBackoff makes a request with custom backoff strategy.
func (b *Bot) callAzureOpenAIWithRetriesAndBackoff(service string, prompt string, systemPrompt string, modelConfigs config.AIModelConfigs, maxTokens int, maxRetries int, backoffFunc func(attempt int) time.Duration) (string, error) {
	var lastErr error
	// Cap any Retry-After we honor - Azure occasionally returns very long
	// hints (minutes) but moderation latency has its own budget. Past this we
	// fall back to the regular exponential schedule.
	const maxRetryAfter = 60 * time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		modelConfig := modelConfigs.Get(attempt)

		if attempt > 0 {
			var backoff time.Duration
			// Honor an upstream Retry-After hint from the previous attempt when
			// available. If we have multiple model endpoints configured we
			// switch deployment instead - quotas are per-deployment on Azure,
			// so a long wait isn't required.
			var rl *RateLimitError
			if errors.As(lastErr, &rl) && rl.RetryAfter > 0 && modelConfigs.Count() <= 1 {
				backoff = rl.RetryAfter
				if backoff > maxRetryAfter {
					backoff = maxRetryAfter
				}
			} else if backoffFunc != nil {
				backoff = backoffFunc(attempt)
			} else {
				backoff = time.Duration(1<<uint(attempt-1)) * time.Second
			}
			if modelConfigs.Count() > 1 {
				log.Printf("Retry attempt %d/%d after %v, switching to model: %s/%s", attempt+1, maxRetries, backoff, modelConfig.Endpoint, modelConfig.DeploymentName)
			} else {
				log.Printf("Retry attempt %d/%d after %v for %s/%s", attempt+1, maxRetries, backoff, modelConfig.Endpoint, modelConfig.DeploymentName)
			}
			time.Sleep(backoff)
		}

		result, err := b.callAzureOpenAIWithConfig(service, prompt, systemPrompt, modelConfig, maxTokens, attempt > 0)
		if err == nil {
			if attempt > 0 {
				log.Printf("Request succeeded on attempt %d/%d", attempt+1, maxRetries)
			}
			return result, nil
		}

		lastErr = err

		if !isRetryableError(err) {
			log.Printf("Non-retryable error, not retrying: %v", err)
			return "", err
		}

		log.Printf("Retryable error on attempt %d/%d: %v", attempt+1, maxRetries, err)
	}

	log.Printf("All %d retry attempts failed (tried %d model(s))", maxRetries, modelConfigs.Count())
	return "", fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}

// calculateTimeout calculates appropriate timeout based on prompt size and expected output.
func (b *Bot) calculateTimeout(systemPrompt, userPrompt string, maxTokens int, isFallback bool) time.Duration {
	inputChars := len(systemPrompt) + len(userPrompt)
	estimatedInputTokens := inputChars / 4

	estimatedOutputTokens := maxTokens
	if maxTokens == 0 {
		estimatedOutputTokens = 500
	}

	totalTokens := estimatedInputTokens + estimatedOutputTokens

	var baseTimeout time.Duration
	switch {
	case totalTokens < 500:
		baseTimeout = 30 * time.Second
	case totalTokens < 5000:
		baseTimeout = 60 * time.Second
	case totalTokens < 20000:
		baseTimeout = 120 * time.Second
	default:
		baseTimeout = 240 * time.Second
	}

	if estimatedInputTokens > 10000 {
		extraSeconds := (estimatedInputTokens - 10000) / 100
		baseTimeout += time.Duration(extraSeconds) * time.Second
	}

	if isFallback {
		baseTimeout = baseTimeout * 2 / 3
	}

	if baseTimeout < 20*time.Second {
		baseTimeout = 20 * time.Second
	}
	if baseTimeout > 300*time.Second {
		baseTimeout = 300 * time.Second
	}

	return baseTimeout
}

// callAzureOpenAI makes a request to Azure OpenAI API with automatic fallback.
func (b *Bot) callAzureOpenAI(service string, prompt string, systemPrompt string) (string, error) {
	if !b.config.AI.Enabled {
		return "", fmt.Errorf("Azure AI is not enabled")
	}

	result, err := b.callAzureOpenAIWithRetries(service, prompt, systemPrompt, b.config.AI.FullModel, 0, 3)

	if err != nil && b.config.AI.ContentModeration.Enabled {
		if isRetryableError(err) {
			log.Printf("Full model endpoint failed after retries (%v), trying fallback to light model: %s/%s", err, b.config.AI.LightModel.Get(0).Endpoint, b.config.AI.LightModel.Get(0).DeploymentName)
			return b.callAzureOpenAIWithRetries(service, prompt, systemPrompt, b.config.AI.LightModel, 0, 2)
		}
	}

	return result, err
}

// callAzureOpenAINoFallback makes a request to Azure OpenAI API without fallback.
func (b *Bot) callAzureOpenAINoFallback(service string, prompt string, systemPrompt string) (string, error) {
	if !b.config.AI.Enabled {
		return "", fmt.Errorf("Azure AI is not enabled")
	}

	return b.callAzureOpenAIWithRetries(service, prompt, systemPrompt, b.config.AI.FullModel, 0, 3)
}

// callAzureOpenAIWithConfig makes a request to Azure OpenAI API with specific model config.
func (b *Bot) callAzureOpenAIWithConfig(service string, prompt string, systemPrompt string, modelConfig config.AIModelConfig, maxTokens int, isFallback bool) (string, error) {
	provider := modelConfig.ResolveProvider()

	var url string
	if provider == config.AIProviderOpenAI {
		// Standard OpenAI / OpenAI-compatible gateway. The endpoint should be
		// the API base (e.g. https://api.openai.com/v1); default to the public
		// OpenAI base when none is given.
		base := strings.TrimSuffix(modelConfig.Endpoint, "/")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		url = base + "/chat/completions"
	} else {
		url = fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=2024-02-15-preview",
			strings.TrimSuffix(modelConfig.Endpoint, "/"),
			modelConfig.DeploymentName)
	}

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}

	requestBody := AzureOpenAIRequest{
		Messages: messages,
	}

	// The standard OpenAI API selects the model via the request body; Azure
	// encodes it in the URL path instead.
	if provider == config.AIProviderOpenAI {
		requestBody.Model = modelConfig.DeploymentName
	}

	if !modelConfig.OmitMaxTokens && maxTokens >= 0 {
		if maxTokens == 0 {
			requestBody.MaxTokens = 500
		} else {
			requestBody.MaxTokens = maxTokens
		}
	}

	if modelConfig.Temperature != nil {
		requestBody.Temperature = *modelConfig.Temperature
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("error marshaling request: %v", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("error creating request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if provider == config.AIProviderOpenAI {
		req.Header.Set("Authorization", "Bearer "+modelConfig.APIKey)
	} else {
		req.Header.Set("api-key", modelConfig.APIKey)
	}

	// Compute the diagnostics service label once so request, response and
	// error logging all agree (older code logged the request under "AI:<dep>"
	// but diagnostics under "openai_<type>:<dep>" - that mismatch is fixed
	// here as a side-effect of routing through doAPI).
	aiType := "light"
	for _, fc := range b.config.AI.FullModel.Configs {
		if modelConfig.DeploymentName == fc.DeploymentName && modelConfig.Endpoint == fc.Endpoint {
			aiType = "full"
			break
		}
	}
	aiService := fmt.Sprintf("openai_%s:%s", aiType, modelConfig.DeploymentName)

	timeout := b.calculateTimeout(systemPrompt, prompt, maxTokens, isFallback)
	res, err := b.doAPI(apiRequest{
		Service: aiService,
		Request: req,
		LogBody: jsonData,
		Client:  &http.Client{Timeout: timeout},
	})
	if err != nil {
		return "", fmt.Errorf("error making request: %v", err)
	}
	body := res.Body
	if !res.IsOK() {
		b.logAPIError(aiService, res.StatusCode, body, nil)
		var errorResp AzureOpenAIResponse
		if json.Unmarshal(body, &errorResp) == nil && errorResp.Error != nil && errorResp.Error.Code == "content_filter" {
			details := formatContentFilterDetails(errorResp.Error)
			return "", &ContentFilterError{Details: details}
		}
		if res.StatusCode == http.StatusTooManyRequests {
			retryAfter := parseRetryAfter(res.Header.Get("Retry-After"))
			return "", &RateLimitError{StatusCode: res.StatusCode, RetryAfter: retryAfter, Body: string(body)}
		}
		return "", fmt.Errorf("API request failed with status %d: %s", res.StatusCode, string(body))
	}

	var response AzureOpenAIResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return "", fmt.Errorf("error unmarshaling response: %v", err)
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	b.recordTokenUsage(service, modelConfig.DeploymentName, response.Usage)

	return response.Choices[0].Message.Content, nil
}

// recordTokenUsage persists the input/output token counts reported by an AI
// response into the per-service, per-model daily/total counters. It is
// best-effort: any failure is logged but never affects the caller.
func (b *Bot) recordTokenUsage(service, model string, usage *AzureOpenAIUsage) {
	if usage == nil || b.db == nil {
		return
	}
	if model == "" {
		model = "unknown"
	}
	if service == "" {
		service = "unknown"
	}
	today := b.timeNow().Format("2006-01-02")
	if err := b.db.RecordTokenUsage(model, service, today, usage.PromptTokens, usage.CompletionTokens); err != nil {
		log.Printf("Failed to record token usage for model %s (service %s): %v", model, service, err)
	}
}

// callContentModeration builds the moderation prompt with the given reply-to
// context and calls the specified model, returning the raw LLM response.
// newUserRules is injected into the {{new_user_rules}} placeholder (empty for
// established users).
func (b *Bot) callContentModeration(messageText, userProfile, userReputation, replyToText, newUserRules string, model config.AIModelConfigs, chatID int64) (string, error) {
	replacements := map[string]string{"message": messageText, "chat_rules": b.config.ChatRulesFor(chatID), "user_profile": userProfile, "user_reputation": userReputation, "reply_to": replyToText, "new_user_rules": newUserRules}
	systemPrompt := applyReplacements(b.config.AI.ContentModeration.Prompt.System, replacements)
	prompt := applyReplacements(b.config.AI.ContentModeration.Prompt.User, replacements)
	return b.callAzureOpenAIWithRetries("content_moderation", prompt, systemPrompt, model, 50, 3)
}

// callContentModerationWithReplyRetry calls the moderation model with the given
// reply-to context. If Azure's content filter fires *and* reply_to context was
// included, it retries once without the reply_to text - the quoted message may
// itself be the offending content rather than the message under moderation, so
// we don't want to penalize the replier for what they're replying to.
func (b *Bot) callContentModerationWithReplyRetry(messageText, userProfile, userReputation, replyToText, newUserRules string, model config.AIModelConfigs, modelLabel string, chatID int64) (string, error) {
	response, err := b.callContentModeration(messageText, userProfile, userReputation, replyToText, newUserRules, model, chatID)
	if err == nil || replyToText == "" {
		return response, err
	}
	var cfErr *ContentFilterError
	if !errors.As(err, &cfErr) {
		return response, err
	}
	log.Printf("⚠️ Content filter triggered by Azure AI (%s) with reply_to context - retrying without reply_to", modelLabel)
	return b.callContentModeration(messageText, userProfile, userReputation, "", newUserRules, model, chatID)
}

// analyzeMessageContentWithModel runs the content-moderation prompt against the
// given model set and returns every matching rule (in declaration order) plus
// the decision details. logEmoji/modelLabel only affect logging. It is the
// shared core behind the light/full-model wrappers and the across-models
// re-moderation path.
func (b *Bot) analyzeMessageContentWithModel(messageText string, userID int64, chatID int64, replyToText string, model config.AIModelConfigs, logEmoji, modelLabel string) ([]config.ModerationRule, string, error) {
	if !b.config.AI.ContentModeration.Enabled {
		return nil, "", fmt.Errorf("AI content moderation is not enabled")
	}

	userProfile := b.getUserProfileForModeration(userID, chatID)
	userReputation := b.getUserReputationForModeration(userID)
	newUserRules := b.newUserRulesFor(userID)
	response, err := b.callContentModerationWithReplyRetry(messageText, userProfile, userReputation, replyToText, newUserRules, model, modelLabel, chatID)
	if err != nil {
		var cfErr *ContentFilterError
		if errors.As(err, &cfErr) {
			log.Printf("⚠️ Content filter triggered by Azure AI (%s) - message content is inappropriate", modelLabel)
			return nil, "", cfErr
		}
		return nil, "", err
	}

	matched, details := b.matchModerationRules(response)
	if len(matched) > 0 {
		log.Printf("%s %s raw response (matched %d rule(s)): %q", logEmoji, modelLabel, len(matched), strings.TrimSpace(response))
	}
	return matched, details, nil
}

// analyzeMessageContentWithLightModel analyzes message content using the light model.
// Returns every moderation rule whose trigger substring is present in the first
// line of the model's response (case-insensitive), in declaration order. The
// second return value is "decision details" - any text the LLM wrote after the
// first line. The slice may be empty when nothing matched.
func (b *Bot) analyzeMessageContentWithLightModel(messageText string, userID int64, chatID int64, replyToText string) ([]config.ModerationRule, string, error) {
	return b.analyzeMessageContentWithModel(messageText, userID, chatID, replyToText, b.config.AI.LightModel, "🔍", "light model")
}

// analyzeMessageContentWithFullModel analyzes message content using the full model.
// Returns every moderation rule whose trigger substring is present in the first
// line of the model's response (case-insensitive), in declaration order.
// The second return value is "decision details".
func (b *Bot) analyzeMessageContentWithFullModel(messageText string, userID int64, chatID int64, replyToText string) ([]config.ModerationRule, string, error) {
	return b.analyzeMessageContentWithModel(messageText, userID, chatID, replyToText, b.config.AI.FullModel, "✅", "full model")
}

// matchModerationRules walks the configured moderation rules in order and
// returns every rule whose trigger substring is contained in the first line of
// the LLM response (case-insensitive). The returned slice preserves declaration
// order so callers can dispatch actions in the user's intended sequence.
// The second return value is the "decision details" - any text after the first
// line, trimmed. This allows the LLM to explain its reasoning on subsequent
// lines without interfering with trigger matching.
func (b *Bot) matchModerationRules(response string) ([]config.ModerationRule, string) {
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		return nil, ""
	}

	// Split into first line (trigger line) and the rest (decision details).
	firstLine := trimmed
	var details string
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		firstLine = trimmed[:idx]
		details = strings.TrimSpace(trimmed[idx+1:])
	}

	triggerLower := strings.ToLower(strings.TrimSpace(firstLine))
	rules := b.config.ModerationRules()
	var matched []config.ModerationRule
	for i := range rules {
		trigger := strings.ToLower(strings.TrimSpace(rules[i].Trigger))
		if trigger == "" {
			continue
		}
		if strings.Contains(triggerLower, trigger) {
			rule := rules[i]
			rule.Action = config.NormalizeModerationAction(rule.Action)
			matched = append(matched, rule)
		}
	}
	return matched, details
}

// ContentSecurityTrigger is the reserved trigger word for content-security events.
// Admins can configure rules with this trigger to customize the action taken when
// Azure's content safety filter fires (e.g. "content-security" → delete + report).
const ContentSecurityTrigger = "content-security"

// matchContentSecurityRules checks if any configured rule uses the reserved
// "content-security" trigger. Returns matched rules so that content-filter events
// can be handled via the standard rule dispatch pipeline.
func (b *Bot) matchContentSecurityRules() []config.ModerationRule {
	rules := b.config.ModerationRules()
	var matched []config.ModerationRule
	for i := range rules {
		trigger := strings.ToLower(strings.TrimSpace(rules[i].Trigger))
		if trigger == "" {
			continue
		}
		if strings.Contains(ContentSecurityTrigger, trigger) || strings.Contains(trigger, ContentSecurityTrigger) {
			rule := rules[i]
			rule.Action = config.NormalizeModerationAction(rule.Action)
			matched = append(matched, rule)
		}
	}
	return matched
}

// generateWarningNotification generates a personalized warning notification using AI.
func (b *Bot) generateWarningNotification(userDisplayName, userMessage, muteInfo, reputation string, chatID int64) (string, error) {
	userMessagePart := ""
	if userMessage != "" {
		userMessagePart = i18n.Tf("ai.warn_user_message", userMessage)
	}

	replacements := map[string]string{
		"username":     userDisplayName,
		"user_message": userMessagePart,
		"chat_rules":   b.config.ChatRulesFor(chatID),
		"mute_info":    muteInfo,
		"reputation":   reputation,
	}
	systemPrompt := applyReplacements(b.config.AI.ContentModeration.WarningPrompt.System, replacements)
	prompt := applyReplacements(b.config.AI.ContentModeration.WarningPrompt.User, replacements)

	return b.callAzureOpenAI("moderation_warning", prompt, systemPrompt)
}
