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

	"gennadium/internal/config"
)

// newTestHandler builds an apiHandler backed by a temp DB for handler tests.
func newTestHandler(t *testing.T, cfg *config.Config) *apiHandler {
	t.Helper()
	db := newTestDB(t)
	return &apiHandler{
		config:     cfg,
		db:         db,
		auth:       NewAuthManager(db),
		pathPrefix: "/admin",
	}
}

func decodeJSONBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body
}

func TestHandleGetVersion(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.version = "1.0.0"
	h.gitCommit = "abc"
	h.buildTime = "today"
	h.botName = "Gennady"

	rr := httptest.NewRecorder()
	h.handleGetVersion(rr, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, "1.0.0", body["version"])
	assert.Equal(t, "file", body["config_source"])
	assert.Equal(t, "Gennady", body["bot_name"])
}

func TestHandleGetVersion_DBSource(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.configFromDB = true
	rr := httptest.NewRecorder()
	h.handleGetVersion(rr, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	body := decodeJSONBody(t, rr)
	assert.Equal(t, "db", body["config_source"])
}

func TestHandleAuthMode_PasswordOnly(t *testing.T) {
	cfg := newTestConfig()
	cfg.WebUI.Password = "secret"
	h := newTestHandler(t, cfg)
	rr := httptest.NewRecorder()
	h.handleAuthMode(rr, httptest.NewRequest(http.MethodGet, "/api/auth/mode", nil))
	body := decodeJSONBody(t, rr)
	assert.Equal(t, true, body["password_required"])
	assert.Equal(t, false, body["otp_available"])
}

func TestHandleAuthMode_OTPAvailable(t *testing.T) {
	cfg := newTestConfig()
	cfg.Admin.SuperAdminUserID = 42
	h := newTestHandler(t, cfg)
	h.sendOTP = func(string) error { return nil }
	rr := httptest.NewRecorder()
	h.handleAuthMode(rr, httptest.NewRequest(http.MethodGet, "/api/auth/mode", nil))
	body := decodeJSONBody(t, rr)
	assert.Equal(t, true, body["otp_available"])
}

func TestHandleLogin_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleLogin(rr, httptest.NewRequest(http.MethodGet, "/api/auth/login", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleLogin_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("not json"))
	h.handleLogin(rr, r)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleLogin_PasswordOnlySuccess(t *testing.T) {
	cfg := newTestConfig()
	cfg.WebUI.Password = "secret"
	h := newTestHandler(t, cfg)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"secret"}`))
	r.RemoteAddr = "1.2.3.4:5555"
	h.handleLogin(rr, r)

	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, true, body["authenticated"])
	// A session cookie must be set.
	require.Len(t, rr.Result().Cookies(), 1)
	assert.Equal(t, webSessionCookieName, rr.Result().Cookies()[0].Name)
}

func TestHandleLogin_PasswordWrong(t *testing.T) {
	cfg := newTestConfig()
	cfg.WebUI.Password = "secret"
	h := newTestHandler(t, cfg)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"nope"}`))
	r.RemoteAddr = "1.2.3.4:5555"
	h.handleLogin(rr, r)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errAuthInvalidCredentials.code, body["error_code"])
}

func TestHandleLogin_NoMethodConfigured(t *testing.T) {
	// No password, no OTP -> password supplied but no method.
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"x"}`))
	r.RemoteAddr = "1.2.3.4:5555"
	h.handleLogin(rr, r)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errAuthNoMethodConfigured.code, body["error_code"])
}

func TestHandleLogin_PasswordThenOTP(t *testing.T) {
	cfg := newTestConfig()
	cfg.WebUI.Password = "secret"
	cfg.Admin.SuperAdminUserID = 99
	h := newTestHandler(t, cfg)
	var sentOTP string
	h.sendOTP = func(code string) error { sentOTP = code; return nil }

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"secret"}`))
	r.RemoteAddr = "1.2.3.4:5555"
	h.handleLogin(rr, r)

	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, true, body["otp_required"])
	assert.Len(t, sentOTP, otpLength)
}

func TestHandleLogin_OTPOnlyWhenNoPassword(t *testing.T) {
	cfg := newTestConfig()
	cfg.Admin.SuperAdminUserID = 99
	h := newTestHandler(t, cfg)
	h.sendOTP = func(string) error { return nil }

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"anything"}`))
	r.RemoteAddr = "1.2.3.4:5555"
	h.handleLogin(rr, r)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, true, body["otp_required"])
}

func TestHandleLogin_OTPSendFails(t *testing.T) {
	cfg := newTestConfig()
	cfg.Admin.SuperAdminUserID = 99
	h := newTestHandler(t, cfg)
	h.sendOTP = func(string) error { return assertGenericErr() }

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"anything"}`))
	r.RemoteAddr = "1.2.3.4:5555"
	h.handleLogin(rr, r)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errAuthOTPSendFailed.code, body["error_code"])
}

func TestHandleLogin_OTPVerifySuccess(t *testing.T) {
	cfg := newTestConfig()
	cfg.Admin.SuperAdminUserID = 99
	h := newTestHandler(t, cfg)
	code := h.auth.GenerateOTP()

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"code":"`+code+`"}`))
	r.RemoteAddr = "1.2.3.4:5555"
	h.handleLogin(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, true, body["authenticated"])
}

func TestHandleLogin_OTPVerifyWrong(t *testing.T) {
	cfg := newTestConfig()
	cfg.Admin.SuperAdminUserID = 99
	h := newTestHandler(t, cfg)
	h.auth.GenerateOTP()

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"code":"000000"}`))
	r.RemoteAddr = "1.2.3.4:5555"
	h.handleLogin(rr, r)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleLogin_NoCredentials(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{}`))
	r.RemoteAddr = "1.2.3.4:5555"
	h.handleLogin(rr, r)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errAuthPasswordOrCodeRequired.code, body["error_code"])
}

func TestHandleLogout(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	token := h.auth.CreatePasswordSession()
	require.True(t, h.auth.ValidateSession(token))

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: token})
	h.handleLogout(rr, r)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.False(t, h.auth.ValidateSession(token))
	// A clearing cookie should be set.
	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].MaxAge < 0)
}

func TestHandleAuthCheck(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleAuthCheck(rr, httptest.NewRequest(http.MethodGet, "/api/auth/check", nil))
	body := decodeJSONBody(t, rr)
	assert.Equal(t, true, body["authenticated"])
}

func TestHandleGetI18n_Handler(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleGetI18n(rr, httptest.NewRequest(http.MethodGet, "/api/i18n", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	var body map[string]map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Contains(t, body, "en")
}
