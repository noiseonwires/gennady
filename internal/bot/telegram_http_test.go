// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestTelegramDiagnosticTransportReportsEmptyResponseMetadata(t *testing.T) {
	transport := telegramDiagnosticTransport{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				Status:        "502 Bad Gateway",
				StatusCode:    http.StatusBadGateway,
				Proto:         "HTTP/2.0",
				ContentLength: 0,
				Header: http.Header{
					"Content-Type": {"application/json"},
					"Server":       {"edge"},
					"Via":          {"gateway"},
					"X-Request-Id": {"request-123"},
					"Cf-Ray":       {"ray-456"},
				},
				Body: io.NopCloser(strings.NewReader("")),
			}, nil
		}),
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/botREDACTED/deleteWebhook", nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty Telegram HTTP response")
	assert.Contains(t, err.Error(), "status=502 Bad Gateway")
	assert.Contains(t, err.Error(), "protocol=HTTP/2.0")
	assert.Contains(t, err.Error(), "content_length=0")
	assert.Contains(t, err.Error(), `content_type="application/json"`)
	assert.Contains(t, err.Error(), `server="edge"`)
	assert.Contains(t, err.Error(), `via="gateway"`)
	assert.Contains(t, err.Error(), `x_request_id="request-123"`)
	assert.Contains(t, err.Error(), `cf_ray="ray-456"`)
}

func TestTelegramDiagnosticTransportPreservesNonEmptyResponse(t *testing.T) {
	const body = `{"ok":true,"result":true}`
	transport := telegramDiagnosticTransport{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				Status:        "200 OK",
				StatusCode:    http.StatusOK,
				Proto:         "HTTP/1.1",
				ContentLength: int64(len(body)),
				Header:        make(http.Header),
				Body:          io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/botREDACTED/deleteWebhook", nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(got))
}

func TestTelegramResponseMetadataOmitsAbsentOptionalHeaders(t *testing.T) {
	resp := &http.Response{
		Status:        "200 OK",
		Proto:         "HTTP/1.1",
		ContentLength: -1,
		Header:        make(http.Header),
	}

	got := telegramResponseMetadata(resp, 1500*time.Microsecond)

	assert.Contains(t, got, "content_length=-1")
	assert.Contains(t, got, `content_type=""`)
	assert.Contains(t, got, "elapsed=2ms")
	assert.NotContains(t, got, "via=")
	assert.NotContains(t, got, "x_request_id=")
	assert.NotContains(t, got, "cf_ray=")
}
