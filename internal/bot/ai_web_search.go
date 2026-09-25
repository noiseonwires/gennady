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
	"strings"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/i18n"
)

type webSearchResponsesRequest struct {
	Model           string                  `json:"model"`
	Instructions    string                  `json:"instructions,omitempty"`
	Input           string                  `json:"input"`
	Tools           []webSearchResponseTool `json:"tools"`
	ToolChoice      string                  `json:"tool_choice"`
	MaxOutputTokens int                     `json:"max_output_tokens,omitempty"`
	Temperature     *float64                `json:"temperature,omitempty"`
	Store           bool                    `json:"store"`
}

type webSearchResponseTool struct {
	Type string `json:"type"`
}

type webSearchResponsesResponse struct {
	Output []struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Action *struct {
			Type    string   `json:"type"`
			Query   string   `json:"query"`
			Queries []string `json:"queries"`
		} `json:"action,omitempty"`
		Summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"summary,omitempty"`
		Content []struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Refusal     string `json:"refusal"`
			Annotations []struct {
				Type  string `json:"type"`
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"annotations"`
		} `json:"content"`
	} `json:"output"`
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
		TotalTokens  int64 `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *AzureOpenAIError `json:"error,omitempty"`
}

type webSearchToolCallInfo struct {
	ID         string   `json:"id,omitempty"`
	Status     string   `json:"status,omitempty"`
	ActionType string   `json:"action_type,omitempty"`
	Queries    []string `json:"queries,omitempty"`
}

type webSearchCitationInfo struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

type webSearchCallResult struct {
	Text               string                  `json:"text"`
	WebSearchUsed      bool                    `json:"web_search_used"`
	ToolCalls          []webSearchToolCallInfo `json:"tool_calls,omitempty"`
	Citations          []webSearchCitationInfo `json:"citations,omitempty"`
	ReasoningSummaries []string                `json:"reasoning_summaries,omitempty"`
	Usage              *AzureOpenAIUsage       `json:"usage,omitempty"`
}

func webSearchModels(models config.AIModelConfigs) config.AIModelConfigs {
	eligible := make([]config.AIModelConfig, 0, len(models.Configs))
	seen := make(map[string]struct{})
	for _, model := range models.Configs {
		if !model.IsEnabled() || !model.UseForWebSearch {
			continue
		}
		key := model.ResolveProvider() + "\x00" + strings.TrimSuffix(model.Endpoint, "/") + "\x00" + model.DeploymentName
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		eligible = append(eligible, model)
	}
	return config.AIModelConfigs{Configs: eligible}
}

func responsesEndpoint(model config.AIModelConfig) string {
	base := strings.TrimSuffix(model.Endpoint, "/")
	if model.ResolveProvider() == config.AIProviderOpenAI {
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		return base + "/responses"
	}
	if strings.HasSuffix(strings.ToLower(base), "/openai/v1") {
		return base + "/responses"
	}
	return base + "/openai/v1/responses"
}

func (b *Bot) callResponsesWithWebSearch(service, prompt, systemPrompt string, model config.AIModelConfig, maxOutputTokens int) (string, error) {
	result, err := b.callResponsesWithWebSearchDetailed(service, prompt, systemPrompt, model, maxOutputTokens)
	return result.Text, err
}

func (b *Bot) callResponsesWithWebSearchDetailed(service, prompt, systemPrompt string, model config.AIModelConfig, maxOutputTokens int) (webSearchCallResult, error) {
	if !model.IsEnabled() || !model.UseForWebSearch {
		return webSearchCallResult{}, fmt.Errorf("AI model %q is not enabled for web search", model.DeploymentName)
	}

	requestBody := webSearchResponsesRequest{
		Model:        model.DeploymentName,
		Instructions: systemPrompt,
		Input:        prompt,
		Tools:        []webSearchResponseTool{{Type: "web_search"}},
		ToolChoice:   "auto",
		Temperature:  model.Temperature,
		Store:        false,
	}
	if !model.OmitMaxTokens {
		if maxOutputTokens <= 0 {
			requestBody.MaxOutputTokens = 500
		} else {
			requestBody.MaxOutputTokens = maxOutputTokens
		}
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return webSearchCallResult{}, fmt.Errorf("error marshaling Responses API request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, responsesEndpoint(model), bytes.NewReader(jsonData))
	if err != nil {
		return webSearchCallResult{}, fmt.Errorf("error creating Responses API request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if model.ResolveProvider() == config.AIProviderOpenAI {
		req.Header.Set("Authorization", "Bearer "+model.APIKey)
	} else {
		req.Header.Set("api-key", model.APIKey)
	}

	timeout := b.calculateTimeout(systemPrompt, prompt, maxOutputTokens, false)
	if timeout < 120*time.Second {
		timeout = 120 * time.Second
	}
	res, err := b.doAPI(apiRequest{
		Service: "web_search:" + model.DeploymentName,
		Request: req,
		LogBody: jsonData,
		Client:  &http.Client{Timeout: timeout},
	})
	if err != nil {
		return webSearchCallResult{}, fmt.Errorf("error making Responses API request: %v", err)
	}
	if !res.IsOK() {
		b.logAPIError("web_search:"+model.DeploymentName, res.StatusCode, res.Body, nil)
		var errorResponse webSearchResponsesResponse
		if json.Unmarshal(res.Body, &errorResponse) == nil && errorResponse.Error != nil {
			if errorResponse.Error.Code == "content_filter" {
				return webSearchCallResult{}, &ContentFilterError{Details: formatContentFilterDetails(errorResponse.Error)}
			}
		}
		if res.StatusCode == http.StatusTooManyRequests {
			return webSearchCallResult{}, &RateLimitError{
				StatusCode: res.StatusCode,
				RetryAfter: parseRetryAfter(res.Header.Get("Retry-After")),
				Body:       string(res.Body),
			}
		}
		return webSearchCallResult{}, fmt.Errorf("Responses API request failed with status %d: %s", res.StatusCode, string(res.Body))
	}

	var response webSearchResponsesResponse
	if err := json.Unmarshal(res.Body, &response); err != nil {
		return webSearchCallResult{}, fmt.Errorf("error unmarshaling Responses API response: %v", err)
	}
	result, refusal := parseWebSearchResponse(response)
	if refusal != "" {
		return result, &ContentFilterError{Details: refusal}
	}
	if result.Text == "" {
		return result, fmt.Errorf("Responses API returned no output text")
	}
	if response.Usage != nil {
		result.Usage = &AzureOpenAIUsage{
			PromptTokens:     response.Usage.InputTokens,
			CompletionTokens: response.Usage.OutputTokens,
			TotalTokens:      response.Usage.TotalTokens,
		}
		b.recordTokenUsage(service, model.DeploymentName, result.Usage)
	}
	sourceURLs := make([]string, 0, len(result.Citations))
	for _, citation := range result.Citations {
		sourceURLs = append(sourceURLs, citation.URL)
	}
	result.Text = appendWebSearchSources(result.Text, sourceURLs)
	return result, nil
}

func parseWebSearchResponse(response webSearchResponsesResponse) (webSearchCallResult, string) {
	result := webSearchCallResult{}
	var textParts []string
	var refusals []string
	seenSources := make(map[string]struct{})
	for _, item := range response.Output {
		if item.Type == "web_search_call" {
			result.WebSearchUsed = true
			call := webSearchToolCallInfo{ID: item.ID, Status: item.Status}
			if item.Action != nil {
				call.ActionType = item.Action.Type
				call.Queries = append(call.Queries, item.Action.Queries...)
				if item.Action.Query != "" && len(call.Queries) == 0 {
					call.Queries = []string{item.Action.Query}
				}
			}
			result.ToolCalls = append(result.ToolCalls, call)
			continue
		}
		if item.Type == "reasoning" {
			for _, summary := range item.Summary {
				if text := strings.TrimSpace(summary.Text); text != "" {
					result.ReasoningSummaries = append(result.ReasoningSummaries, text)
				}
			}
			continue
		}
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				textParts = append(textParts, strings.TrimSpace(content.Text))
			}
			if content.Type == "refusal" && strings.TrimSpace(content.Refusal) != "" {
				refusals = append(refusals, strings.TrimSpace(content.Refusal))
			}
			for _, annotation := range content.Annotations {
				url := strings.TrimSpace(annotation.URL)
				if annotation.Type != "url_citation" || url == "" {
					continue
				}
				if _, exists := seenSources[url]; exists {
					continue
				}
				seenSources[url] = struct{}{}
				result.Citations = append(result.Citations, webSearchCitationInfo{URL: url, Title: annotation.Title})
			}
		}
	}
	result.Text = strings.Join(textParts, "\n")
	return result, strings.Join(refusals, "\n")
}

func appendWebSearchSources(text string, sources []string) string {
	missing := make([]string, 0, len(sources))
	for _, source := range sources {
		if !strings.Contains(text, source) {
			missing = append(missing, source)
		}
	}
	if len(missing) == 0 {
		return text
	}
	return text + "\n\n" + i18n.T("ai.web_search_sources") + "\n" + strings.Join(missing, "\n")
}

func (b *Bot) callCreativeReplyWithWebSearch(prompt, systemPrompt string, models config.AIModelConfigs) (string, error) {
	models = webSearchModels(models)
	if models.Count() == 0 {
		return "", fmt.Errorf("no selected AI model is enabled for web search")
	}

	var lastErr error
	for index := 0; index < models.Count(); index++ {
		model := models.Get(index)
		result, err := b.callResponsesWithWebSearch("creative_reply", prompt, systemPrompt, model, 500)
		if err == nil {
			return result, nil
		}
		lastErr = err
		var contentFilterErr *ContentFilterError
		if errors.As(err, &contentFilterErr) {
			return "", err
		}
		log.Printf("Creative-reply web search failed for %s/%s: %v", model.Endpoint, model.DeploymentName, err)
	}
	return "", fmt.Errorf("all configured creative-reply web-search models failed: %w", lastErr)
}
