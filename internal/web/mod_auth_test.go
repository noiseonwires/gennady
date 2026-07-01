// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionRole(t *testing.T) {
	assert.Equal(t, RoleModerator, sessionRole(moderatorTokenPrefix+"deadbeef"))
	assert.Equal(t, RoleSuper, sessionRole("deadbeef"))            // legacy raw-hex token
	assert.Equal(t, RoleSuper, sessionRole(""))                   // no token
}

func TestModeratorLogin_SuccessIssuesModeratorSession(t *testing.T) {
	a := NewAuthManager(nil)
	token, otp := a.CreateModeratorLogin(42)
	require.NotEmpty(t, token)
	require.Len(t, otp, otpLength)

	sess, err := a.ValidateModeratorLogin(token, otp, "1.2.3.4")
	require.NoError(t, err)
	require.NotEmpty(t, sess)
	assert.True(t, strings.HasPrefix(sess, moderatorTokenPrefix), "moderator session must carry the role prefix")
	assert.Equal(t, RoleModerator, sessionRole(sess))
	assert.True(t, a.ValidateSession(sess))
}

func TestModeratorLogin_WrongOTPThenCorrectSucceeds(t *testing.T) {
	a := NewAuthManager(nil)
	token, otp := a.CreateModeratorLogin(42)

	// A wrong OTP for a valid token is rejected but does not consume the
	// challenge (until the attempt cap), so the correct OTP still works.
	_, err := a.ValidateModeratorLogin(token, "000000", "ip-a")
	require.Error(t, err)

	sess, err := a.ValidateModeratorLogin(token, otp, "ip-a")
	require.NoError(t, err)
	require.NotEmpty(t, sess)
}

func TestModeratorLogin_WrongTokenRejected(t *testing.T) {
	a := NewAuthManager(nil)
	_, otp := a.CreateModeratorLogin(42)

	_, err := a.ValidateModeratorLogin("not-a-real-token", otp, "ip-b")
	require.Error(t, err)
	var we webError
	require.ErrorAs(t, err, &we)
	assert.Equal(t, errAuthInvalidCredentials.code, we.code)
}

func TestModeratorLogin_SingleUse(t *testing.T) {
	a := NewAuthManager(nil)
	token, otp := a.CreateModeratorLogin(42)

	_, err := a.ValidateModeratorLogin(token, otp, "ip-c")
	require.NoError(t, err)

	// The challenge is consumed: a replay with the same token+OTP fails.
	_, err = a.ValidateModeratorLogin(token, otp, "ip-c")
	require.Error(t, err)
}

func TestModeratorLogin_AttemptCapDiscardsChallenge(t *testing.T) {
	a := NewAuthManager(nil)
	token, otp := a.CreateModeratorLogin(42)

	// Exhaust the per-challenge attempt budget with wrong OTPs (distinct IPs so
	// the per-IP lockout doesn't mask the per-challenge discard).
	for i := 0; i < maxFailedAttempts; i++ {
		_, err := a.ValidateModeratorLogin(token, "000000", "ip-cap")
		require.Error(t, err)
	}
	// Even the correct OTP now fails: the challenge was discarded.
	_, err := a.ValidateModeratorLogin(token, otp, "ip-cap-2")
	require.Error(t, err)
}

func TestRequireAuth_RejectsModeratorToken(t *testing.T) {
	ws := newTestServer(t)
	token, otp := ws.auth.CreateModeratorLogin(42)
	sess, err := ws.auth.ValidateModeratorLogin(token, otp, "ip")
	require.NoError(t, err)

	called := false
	h := ws.requireAuth(func(http.ResponseWriter, *http.Request) { called = true })
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sess})
	h(rr, r)

	assert.False(t, called, "moderator token must not reach a super-admin endpoint")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireModAuth_AcceptsModeratorToken(t *testing.T) {
	ws := newTestServer(t)
	token, otp := ws.auth.CreateModeratorLogin(42)
	sess, err := ws.auth.ValidateModeratorLogin(token, otp, "ip")
	require.NoError(t, err)

	called := false
	h := ws.requireModAuth(func(http.ResponseWriter, *http.Request) { called = true })
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/mod/api/x", nil)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sess})
	h(rr, r)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestModeratorRoutes_ConfigEndpointsNotMounted(t *testing.T) {
	ws := newTestServer(t)
	// Gated endpoints must not exist under the moderator prefix (404), even
	// with a valid moderator session.
	token, otp := ws.auth.CreateModeratorLogin(42)
	sess, err := ws.auth.ValidateModeratorLogin(token, otp, "ip")
	require.NoError(t, err)

	for _, path := range []string{
		"/mod/api/config",
		"/mod/api/config/meta",
		"/mod/api/logs",
		"/mod/api/restart",
		"/mod/api/files/db",
		"/mod/api/diagnostics/test/azure_vision",
	} {
		rr := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sess})
		ws.Mux.ServeHTTP(rr, r)
		assert.Equal(t, http.StatusNotFound, rr.Code, "expected 404 for gated path %s", path)
	}
}

func TestModeratorRoutes_AuthModeAdvertisesModerator(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/mod/api/auth/mode", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, true, body["moderator"])
}

func TestModeratorRoutes_ProtectedRequiresSession(t *testing.T) {
	ws := newTestServer(t)
	// Without a session: 401.
	rr := httptest.NewRecorder()
	ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/mod/api/actions", nil))
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// With a moderator session: reaches the handler (200).
	token, otp := ws.auth.CreateModeratorLogin(42)
	sess, err := ws.auth.ValidateModeratorLogin(token, otp, "ip")
	require.NoError(t, err)
	rr = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/mod/api/actions", nil)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sess})
	ws.Mux.ServeHTTP(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleModeratorLogin_EndToEnd(t *testing.T) {
	ws := newTestServer(t)
	token, otp := ws.auth.CreateModeratorLogin(42)

	body, _ := json.Marshal(map[string]string{"token": token, "code": otp})
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/mod/api/auth/mod-login", strings.NewReader(string(body)))
	ws.Mux.ServeHTTP(rr, r)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["authenticated"])

	// A session cookie scoped to the moderator prefix is set.
	var modCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == webSessionCookieName {
			modCookie = c
		}
	}
	require.NotNil(t, modCookie)
	assert.Equal(t, "/mod", modCookie.Path)
	assert.Equal(t, RoleModerator, sessionRole(modCookie.Value))
}

func TestHandleModeratorLogin_MissingFields(t *testing.T) {
	ws := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"token": "", "code": ""})
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/mod/api/auth/mod-login", strings.NewReader(string(body)))
	ws.Mux.ServeHTTP(rr, r)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleGetVersion_RoleFromToken(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.moderatorPathPrefix = "/mod"

	// Super-admin session (raw hex) → role super.
	superTok := h.auth.CreatePasswordSession()
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: superTok})
	h.handleGetVersion(rr, r)
	assert.Equal(t, RoleSuper, decodeJSONBody(t, rr)["role"])

	// Moderator session → role moderator.
	mtok, otp := h.auth.CreateModeratorLogin(7)
	modSess, err := h.auth.ValidateModeratorLogin(mtok, otp, "ip")
	require.NoError(t, err)
	rr = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/mod/api/version", nil)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: modSess})
	h.handleGetVersion(rr, r)
	assert.Equal(t, RoleModerator, decodeJSONBody(t, rr)["role"])
}
