// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestBot() *Bot {
	return &Bot{config: &config.Config{}}
}

func TestParseRetryAfter(t *testing.T) {
	assert.Equal(t, time.Duration(0), parseRetryAfter(""))
	assert.Equal(t, time.Duration(0), parseRetryAfter("   "))
	assert.Equal(t, 5*time.Second, parseRetryAfter("5"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("-3"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("garbage"))

	// HTTP-date in the past => 0.
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	assert.Equal(t, time.Duration(0), parseRetryAfter(past))

	// HTTP-date in the future => positive duration.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	assert.Greater(t, parseRetryAfter(future), time.Duration(0))
}

func TestIsTransientTransportError(t *testing.T) {
	assert.False(t, isTransientTransportError(nil))
	for _, s := range []string{
		"i/o timeout", "context deadline exceeded", "connection reset by peer",
		"connection refused", "no such host", "network is unreachable",
		"temporary failure in name resolution", "TLS handshake timeout", "unexpected EOF",
	} {
		assert.True(t, isTransientTransportError(&stringErr{s}), "expected transient: %q", s)
	}
	assert.False(t, isTransientTransportError(&stringErr{"400 bad request"}))
}

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }

func TestDoAPI_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	b := newTestBot()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	res, err := b.doAPI(apiRequest{Service: "test", Request: req})
	require.NoError(t, err)
	assert.True(t, res.IsOK())
	assert.Equal(t, "pong", string(res.Body))
}

func TestDoAPI_Validation(t *testing.T) {
	b := newTestBot()
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)

	_, err := b.doAPI(apiRequest{Service: "", Request: req})
	assert.Error(t, err)

	_, err = b.doAPI(apiRequest{Service: "test", Request: nil})
	assert.Error(t, err)
}

func TestDoAPI_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // server is now unreachable -> connection refused

	b := newTestBot()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	_, err := b.doAPI(apiRequest{Service: "test", Request: req, Client: &http.Client{Timeout: 2 * time.Second}})
	assert.Error(t, err)
}

func TestDoAPIWithRetries_SuccessFirstAttempt(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	b := newTestBot()
	res, err := b.doAPIWithRetries("test", nil, 3, func() (*http.Request, []byte, error) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		return req, nil, nil
	})
	require.NoError(t, err)
	assert.True(t, res.IsOK())
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

func TestDoAPIWithRetries_NonRetryableClientError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	b := newTestBot()
	res, err := b.doAPIWithRetries("test", nil, 3, func() (*http.Request, []byte, error) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		return req, nil, nil
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "4xx must not be retried")
}

func TestDoAPIWithRetries_BuildError(t *testing.T) {
	b := newTestBot()
	_, err := b.doAPIWithRetries("test", nil, 3, func() (*http.Request, []byte, error) {
		return nil, nil, &stringErr{"build failed"}
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build failed")
}

func TestDoAPIWithRetries_RetriesThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	b := newTestBot()
	res, err := b.doAPIWithRetries("test", nil, 2, func() (*http.Request, []byte, error) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		return req, nil, nil
	})
	require.NoError(t, err)
	assert.True(t, res.IsOK())
	assert.Equal(t, "recovered", string(res.Body))
	assert.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(2))
}

func TestApiResponseIsOK(t *testing.T) {
	assert.True(t, (&apiResponse{StatusCode: 200}).IsOK())
	assert.True(t, (&apiResponse{StatusCode: 299}).IsOK())
	assert.False(t, (&apiResponse{StatusCode: 300}).IsOK())
	assert.False(t, (&apiResponse{StatusCode: 404}).IsOK())
}

func TestDiagnosticsServiceKey(t *testing.T) {
	assert.Equal(t, "cloudflare", diagnosticsServiceKey("cloudflare_browser"))
	assert.Equal(t, "cloudflare", diagnosticsServiceKey("cloudflare_content"))
	assert.Equal(t, "azure_openai", diagnosticsServiceKey("azure_openai"))
}
