// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONResponse(t *testing.T) {
	rr := httptest.NewRecorder()
	jsonResponse(rr, map[string]string{"hello": "world"})
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var out map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, "world", out["hello"])
}

func TestExtractBearerToken(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Equal(t, "", extractBearerToken(r))

	r.Header.Set("Authorization", "Bearer abc123")
	assert.Equal(t, "abc123", extractBearerToken(r))

	r.Header.Set("Authorization", "Basic abc123")
	assert.Equal(t, "", extractBearerToken(r))
}

func TestExtractSessionToken_CookiePreferred(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: "cookie-token"})
	r.Header.Set("Authorization", "Bearer header-token")
	assert.Equal(t, "cookie-token", extractSessionToken(r))
}

func TestExtractSessionToken_FallbackToBearer(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer header-token")
	assert.Equal(t, "header-token", extractSessionToken(r))
}

func TestExtractSessionToken_Empty(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Equal(t, "", extractSessionToken(r))
}

func TestSetAndClearSessionCookie(t *testing.T) {
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	setSessionCookie(rr, r, "/admin", "tok")
	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, webSessionCookieName, cookies[0].Name)
	assert.Equal(t, "tok", cookies[0].Value)
	assert.Equal(t, "/admin", cookies[0].Path)
	assert.True(t, cookies[0].HttpOnly)

	rr2 := httptest.NewRecorder()
	clearSessionCookie(rr2, r, "/admin")
	cleared := rr2.Result().Cookies()
	require.Len(t, cleared, 1)
	assert.Equal(t, "", cleared[0].Value)
	assert.True(t, cleared[0].MaxAge < 0)
}

func TestSessionCookiePath(t *testing.T) {
	assert.Equal(t, "/", sessionCookiePath(""))
	assert.Equal(t, "/", sessionCookiePath("   "))
	assert.Equal(t, "/admin", sessionCookiePath("/admin"))
	assert.Equal(t, "/admin", sessionCookiePath("admin"))
	assert.Equal(t, "/admin", sessionCookiePath("/admin/"))
	assert.Equal(t, "/", sessionCookiePath("/"))
}

func TestIsSecureCookieRequest_TLS(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	assert.True(t, isSecureCookieRequest(r))
}

func TestIsSecureCookieRequest_ForwardedProtoFromTrustedProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-Proto", "https")
	assert.True(t, isSecureCookieRequest(r))
}

func TestIsSecureCookieRequest_ForwardedSsl(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-Ssl", "on")
	assert.True(t, isSecureCookieRequest(r))
}

func TestIsSecureCookieRequest_UntrustedProxyIgnored(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "8.8.8.8:5555"
	r.Header.Set("X-Forwarded-Proto", "https")
	assert.False(t, isSecureCookieRequest(r))
}

func TestIsSecureCookieRequest_Plain(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	assert.False(t, isSecureCookieRequest(r))
}

func TestExtractIP_DirectPeer(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.5:1234"
	assert.Equal(t, "203.0.113.5", extractIP(r))
}

func TestExtractIP_ForwardedForFromTrustedProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	assert.Equal(t, "198.51.100.7", extractIP(r))
}

func TestExtractIP_RealIPFromTrustedProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Real-IP", "198.51.100.9")
	assert.Equal(t, "198.51.100.9", extractIP(r))
}

func TestExtractIP_ForwardedIgnoredFromUntrustedProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "8.8.8.8:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	assert.Equal(t, "8.8.8.8", extractIP(r))
}

func TestExtractIP_MalformedRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "not-an-addr"
	assert.Equal(t, "not-an-addr", extractIP(r))
}

func TestParseForwardedHeaderIP(t *testing.T) {
	assert.Equal(t, "1.2.3.4", parseForwardedHeaderIP(" 1.2.3.4 "))
	assert.Equal(t, "", parseForwardedHeaderIP("garbage"))
}

func TestAddValidationError(t *testing.T) {
	var errs []string
	addValidationError(&errs, "field", "is required")
	require.Len(t, errs, 1)
	assert.Equal(t, "field: is required", errs[0])

	// nil slice pointer is a no-op (no panic).
	assert.NotPanics(t, func() { addValidationError(nil, "x", "y") })
}

func TestWriteFileError(t *testing.T) {
	assert.Contains(t, writeFileError("/etc/x", os.ErrPermission), "permission denied")
	assert.Contains(t, writeFileError("/etc/x", os.ErrNotExist), "does not exist")
	assert.Contains(t, writeFileError("/etc/x", assertGenericErr()), "Failed to save")
}

func assertGenericErr() error { return errSentinel }

var errSentinel = &simpleErr{"boom"}

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
