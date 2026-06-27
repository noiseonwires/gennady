// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"gennadium/internal/config"

	"github.com/stretchr/testify/require"
)

// redirectTransport is an http.RoundTripper that rewrites every outbound
// request to point at a single base URL (an httptest server), preserving the
// path and query. This lets integration tests intercept all external HTTP
// calls - both configurable endpoints (AI models) and hard-coded ones
// (Open-Meteo, Wikipedia, t.me) - without changing production URLs.
type redirectTransport struct {
	base *url.URL

	mu       sync.Mutex
	requests []*recordedRequest
}

type recordedRequest struct {
	Method string
	Host   string // original host
	Path   string
	Query  string
	Body   string
}

func newRedirectTransport(t *testing.T, server *httptest.Server) *redirectTransport {
	t.Helper()
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	return &redirectTransport{base: base}
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := &recordedRequest{
		Method: req.Method,
		Host:   req.URL.Host,
		Path:   req.URL.Path,
		Query:  req.URL.RawQuery,
	}
	if req.Body != nil {
		if data, err := io.ReadAll(req.Body); err == nil {
			rec.Body = string(data)
		}
	}
	rt.mu.Lock()
	rt.requests = append(rt.requests, rec)
	rt.mu.Unlock()

	// Rewrite to the test server, keeping path + query.
	out := req.Clone(req.Context())
	out.URL.Scheme = rt.base.Scheme
	out.URL.Host = rt.base.Host
	out.Host = rt.base.Host
	// The original body was consumed above to record it; supply a fresh reader.
	if req.Body != nil {
		out.Body = io.NopCloser(strings.NewReader(rec.Body))
		out.ContentLength = int64(len(rec.Body))
	}
	return http.DefaultTransport.RoundTrip(out)
}

func (rt *redirectTransport) count() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.requests)
}

func (rt *redirectTransport) last() *recordedRequest {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.requests) == 0 {
		return nil
	}
	return rt.requests[len(rt.requests)-1]
}

// newIntegrationBot builds a Bot wired to: a mock telegram client, a temp
// SQLite DB, and an httptest server whose handler is provided by the caller.
// All outbound HTTP calls are routed to that server via a redirect transport.
// A fixed clock (2026-06-09) makes date-dependent behaviour deterministic.
func newIntegrationBot(t *testing.T, handler http.HandlerFunc) (*Bot, *mockTelegram, *redirectTransport) {
	t.Helper()
	b, tg := newMockBot(t)

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	rt := newRedirectTransport(t, server)
	b.httpTransport = rt
	b.now = func() time.Time { return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) }

	return b, tg, rt
}

// fullModelConfig returns a single Azure model config whose endpoint is
// irrelevant (the transport redirects all hosts to the test server).
func fullModelConfig() config.AIModelConfig {
	return config.AIModelConfig{
		Endpoint:       "https://azure.example.com",
		APIKey:         "key",
		DeploymentName: "gpt-test",
		OmitMaxTokens:  false,
	}
}

// fullModelConfigs wraps fullModelConfig in an AIModelConfigs.
func fullModelConfigs() config.AIModelConfigs {
	return config.AIModelConfigs{Configs: []config.AIModelConfig{fullModelConfig()}}
}
