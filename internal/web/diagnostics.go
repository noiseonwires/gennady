// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"sync"
	"time"
)

// APICallResult stores the last result of an external API call.
type APICallResult struct {
	ServiceName    string    `json:"service_name"`
	Timestamp      time.Time `json:"timestamp"`
	StatusCode     int       `json:"status_code"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	ResponseTimeMs int64     `json:"response_time_ms"`
	RequestURL     string    `json:"request_url"`
	Success        bool      `json:"success"`
}

// TelegramStatus holds the current Telegram connection status.
type TelegramStatus struct {
	Connected      bool      `json:"connected"`
	Mode           string    `json:"mode"` // "polling" or "webhook"
	BotUsername    string    `json:"bot_username,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	ConnectedSince time.Time `json:"connected_since,omitempty"`
	LastWebhookAt  time.Time `json:"last_webhook_at,omitempty"`
}

// Service name constants for diagnostics tracking.
const (
	ServiceOpenAILight   = "openai_light"
	ServiceOpenAIFull    = "openai_full"
	ServiceAzureVision   = "azure_vision"
	ServiceContentSafety = "content_safety"
	ServiceOCRSpace      = "ocr_space"
	ServiceWeather       = "weather"
	ServiceHolidays      = "holidays"
	ServiceWikipedia     = "wikipedia"
	ServiceExtractorAPI  = "extractor_api"
	ServiceDiffbot       = "diffbot"
	ServiceCloudflare    = "cloudflare"
)

// DiagnosticsTracker records the last result of each external API call.
type DiagnosticsTracker struct {
	mu       sync.RWMutex
	results  map[string]*APICallResult
	telegram TelegramStatus
}

// NewDiagnosticsTracker creates a new diagnostics tracker.
func NewDiagnosticsTracker() *DiagnosticsTracker {
	return &DiagnosticsTracker{
		results: make(map[string]*APICallResult),
	}
}

// RecordCall records the result of an external API call.
func (t *DiagnosticsTracker) RecordCall(service string, statusCode int, errMsg string, duration time.Duration, requestURL string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.results[service] = &APICallResult{
		ServiceName:    service,
		Timestamp:      time.Now(),
		StatusCode:     statusCode,
		ErrorMessage:   errMsg,
		ResponseTimeMs: duration.Milliseconds(),
		RequestURL:     requestURL,
		Success:        statusCode >= 200 && statusCode < 300 && errMsg == "",
	}
}

// SetTelegramConnected marks Telegram as connected.
func (t *DiagnosticsTracker) SetTelegramConnected(mode, botUsername string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.telegram = TelegramStatus{
		Connected:      true,
		Mode:           mode,
		BotUsername:    botUsername,
		ConnectedSince: time.Now(),
	}
}

// SetTelegramError marks Telegram as disconnected with an error.
func (t *DiagnosticsTracker) SetTelegramError(errMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.telegram.Connected = false
	t.telegram.LastError = errMsg
}

// RecordWebhookReceived updates the last webhook callback timestamp.
func (t *DiagnosticsTracker) RecordWebhookReceived() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.telegram.LastWebhookAt = time.Now()
}

// GetTelegramStatus returns a copy of the current Telegram status.
func (t *DiagnosticsTracker) GetTelegramStatus() TelegramStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.telegram
}

// GetResults returns a copy of all recorded API call results.
func (t *DiagnosticsTracker) GetResults() map[string]*APICallResult {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make(map[string]*APICallResult, len(t.results))
	for k, v := range t.results {
		copy := *v
		out[k] = &copy
	}
	return out
}
