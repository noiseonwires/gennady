// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// apiRequest describes a single outbound HTTP call to a 3rd-party API. It
// captures the metadata needed for uniform logging, diagnostics recording and
// error reporting across all integrations (Azure OpenAI, Azure Vision, Open-Meteo,
// RSS, ExtractorAPI, OCR.space, etc).
type apiRequest struct {
	// Service is the short label used in logs and diagnostics ("azure_vision",
	// "extractor_api", "rss:czech_courts", "AI:gpt-4o-mini", ...).
	Service string

	// Request is the prepared *http.Request to execute. Caller is responsible
	// for setting URL, method, body, headers and any per-call options.
	Request *http.Request

	// LogBody is the request body to dump when debug logging is enabled. May
	// be nil for GET requests or when the request body is large / binary and
	// not interesting in logs.
	LogBody []byte

	// Client is the HTTP client to use. If nil, a client with a 60s timeout
	// is created on the fly.
	Client *http.Client
}

// apiResponse is what doAPI returns on a successful round-trip. The caller
// owns the body []byte; the underlying response is already closed.
type apiResponse struct {
	StatusCode int
	Body       []byte
	// Header is a snapshot of the response headers (already-closed body).
	// Callers that care about Retry-After / rate-limit metadata read it here.
	Header http.Header
}

// IsOK reports whether the response indicates success (2xx).
func (r *apiResponse) IsOK() bool {
	return r.StatusCode >= 200 && r.StatusCode < 300
}

// doAPI executes an outbound HTTP request, applying the project-wide
// conventions in one place:
//
//   - request/response debug logging (when enabled)
//   - diagnostics recording (status code, duration, request URL)
//   - request error logging (transport-level failures)
//   - response body draining + close
//
// HTTP status checking is intentionally *not* performed here: callers know
// best whether a given status code should be treated as a failure (e.g.
// extractor APIs sometimes return 204 No Content for empty pages). Use
// apiResponse.IsOK() for the common "anything other than 2xx is bad" case.
//
// On transport-level failure (err != nil from client.Do, or a body-read
// error), doAPI emits the standard "API ERROR" log line and returns the
// error to the caller. The caller is expected to wrap it with package
// context before propagating.
func (b *Bot) doAPI(req apiRequest) (*apiResponse, error) {
	if req.Service == "" {
		return nil, fmt.Errorf("doAPI: service label is required")
	}
	if req.Request == nil {
		return nil, fmt.Errorf("doAPI: request is required")
	}

	client := req.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	// Route the request through the injected transport when one is configured
	// (tests use this to redirect outbound calls to in-process servers). The
	// caller's own Transport, if any, takes precedence.
	if b.httpTransport != nil && client.Transport == nil {
		clientCopy := *client
		clientCopy.Transport = b.httpTransport
		client = &clientCopy
	}

	b.logAPIRequestDebug(req.Service, req.Request.Method, req.Request.URL.String(), req.LogBody)

	start := time.Now()
	resp, err := client.Do(req.Request)
	b.recordDiagnostics(req.Service, start, resp, err, req.Request.URL.String())

	if err != nil {
		b.logAPIError(req.Service, 0, nil, err)
		return nil, err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		b.logAPIError(req.Service, resp.StatusCode, nil, readErr)
		return nil, readErr
	}

	b.logAPIDebug(req.Service, body)

	return &apiResponse{StatusCode: resp.StatusCode, Body: body, Header: resp.Header}, nil
}

// parseRetryAfter parses an HTTP Retry-After header value, which is either a
// delta-seconds integer or an HTTP-date. Returns 0 on empty/invalid input.
func parseRetryAfter(value string) time.Duration {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// isTransientTransportError reports whether a transport-level error returned
// by doAPI (not an HTTP status) is worth retrying.
func isTransientTransportError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "timeout") ||
		strings.Contains(s, "context deadline exceeded") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "network is unreachable") ||
		strings.Contains(s, "temporary failure") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "TLS handshake timeout") ||
		strings.Contains(s, "EOF")
}

// doAPIWithRetries runs build() and retries on transient transport errors,
// HTTP 5xx, and HTTP 429. It honors Retry-After on 429 responses, capped at
// maxRetryAfter so a misbehaving upstream can't park a moderation request for
// minutes. build is called once per attempt because *http.Request bodies are
// not safely rewindable.
//
// On the final attempt the latest apiResponse is returned (possibly non-OK)
// so the caller can inspect the status code; transport-level errors after
// exhausting retries are returned as the error. Successful (IsOK) responses
// short-circuit immediately.
func (b *Bot) doAPIWithRetries(service string, client *http.Client, maxRetries int, build func() (*http.Request, []byte, error)) (*apiResponse, error) {
	const maxRetryAfter = 60 * time.Second

	var lastResp *apiResponse
	var lastErr error

	totalAttempts := maxRetries + 1
	for attempt := 0; attempt < totalAttempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			if lastResp != nil && lastResp.StatusCode == http.StatusTooManyRequests {
				if ra := parseRetryAfter(lastResp.Header.Get("Retry-After")); ra > 0 {
					backoff = ra
					if backoff > maxRetryAfter {
						backoff = maxRetryAfter
					}
				}
			}
			log.Printf("API retry %d/%d for %s after %v", attempt, maxRetries, service, backoff)
			time.Sleep(backoff)
		}

		req, logBody, err := build()
		if err != nil {
			return nil, err
		}
		res, err := b.doAPI(apiRequest{Service: service, Request: req, LogBody: logBody, Client: client})
		if err != nil {
			lastErr = err
			lastResp = nil
			if !isTransientTransportError(err) {
				return nil, err
			}
			continue
		}
		if res.IsOK() {
			return res, nil
		}
		lastResp = res
		lastErr = nil
		// Only 429 and 5xx warrant another round; everything else is a
		// definitive client error and the caller will handle it.
		if res.StatusCode != http.StatusTooManyRequests && res.StatusCode < 500 {
			return res, nil
		}
	}

	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}
