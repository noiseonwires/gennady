// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockModerator records the moderation calls made by the web handlers and lets
// each method return a configurable error.
type mockModerator struct {
	muteCalls       int
	cruelMuteCalls  int
	unmuteCalls     int
	warnCalls       int
	delMessagesArgs struct {
		userID, chatID int64
		period         string
	}
	delMessagesCount int
	delMessageCalls  int
	remoderateCalls  int
	lastActorID      int64
	lastActorName    string

	err error
}

func (m *mockModerator) actor(actorID int64, actorName string) {
	m.lastActorID = actorID
	m.lastActorName = actorName
}
func (m *mockModerator) WebMuteUser(userID, chatID int64, messageID, durationMinutes int, actorID int64, actorName string) error {
	m.muteCalls++
	m.actor(actorID, actorName)
	return m.err
}
func (m *mockModerator) WebCruelMuteUser(userID, chatID int64, messageID, durationMinutes int, actorID int64, actorName string) error {
	m.cruelMuteCalls++
	m.actor(actorID, actorName)
	return m.err
}
func (m *mockModerator) WebUnmuteUser(userID, chatID, actorID int64, actorName string) error {
	m.unmuteCalls++
	m.actor(actorID, actorName)
	return m.err
}
func (m *mockModerator) WebWarnUser(userID, chatID int64, messageID int, actorID int64, actorName string) error {
	m.warnCalls++
	m.actor(actorID, actorName)
	return m.err
}
func (m *mockModerator) WebDeleteUserMessages(userID, chatID int64, period string, actorID int64, actorName string) (int, error) {
	m.delMessagesArgs.userID = userID
	m.delMessagesArgs.chatID = chatID
	m.delMessagesArgs.period = period
	m.actor(actorID, actorName)
	return m.delMessagesCount, m.err
}
func (m *mockModerator) WebDeleteMessage(userID, chatID int64, messageID int, actorID int64, actorName string) error {
	m.delMessageCalls++
	m.actor(actorID, actorName)
	return m.err
}
func (m *mockModerator) WebRemoderateMessage(userID, chatID int64, messageID int, actorID int64, actorName string) error {
	m.remoderateCalls++
	m.actor(actorID, actorName)
	return m.err
}

func modRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/moderation/x", strings.NewReader(body))
	return r
}

func TestDecodeModRequest_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.moderator = &mockModerator{}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/moderation/mute", nil)
	_, ok := h.decodeModRequest(rr, r)
	assert.False(t, ok)
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestDecodeModRequest_NoBackend(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	_, ok := h.decodeModRequest(rr, modRequest(`{"user_id":1,"chat_id":2}`))
	assert.False(t, ok)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestDecodeModRequest_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.moderator = &mockModerator{}
	rr := httptest.NewRecorder()
	_, ok := h.decodeModRequest(rr, modRequest("garbage"))
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestDecodeModRequest_MissingIDs(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.moderator = &mockModerator{}
	rr := httptest.NewRecorder()
	_, ok := h.decodeModRequest(rr, modRequest(`{"user_id":0,"chat_id":0}`))
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errUserAndChatIDRequired.code, body["error_code"])
}

func TestHandleModerationMute(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	m := &mockModerator{}
	h.moderator = m
	rr := httptest.NewRecorder()
	h.handleModerationMute(rr, modRequest(`{"user_id":1,"chat_id":2,"duration":60}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, m.muteCalls)
	assert.Equal(t, 0, m.cruelMuteCalls)
}

func TestHandleModerationMute_Cruel(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	m := &mockModerator{}
	h.moderator = m
	rr := httptest.NewRecorder()
	h.handleModerationMute(rr, modRequest(`{"user_id":1,"chat_id":2,"cruel":true}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, m.cruelMuteCalls)
}

func TestHandleModerationMute_Error(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.moderator = &mockModerator{err: assertGenericErr()}
	rr := httptest.NewRecorder()
	h.handleModerationMute(rr, modRequest(`{"user_id":1,"chat_id":2}`))
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errModerationActionFailed.code, body["error_code"])
}

func TestHandleModerationUnmute(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	m := &mockModerator{}
	h.moderator = m
	rr := httptest.NewRecorder()
	h.handleModerationUnmute(rr, modRequest(`{"user_id":1,"chat_id":2}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, m.unmuteCalls)
}

func TestHandleModerationWarn(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	m := &mockModerator{}
	h.moderator = m
	rr := httptest.NewRecorder()
	h.handleModerationWarn(rr, modRequest(`{"user_id":1,"chat_id":2,"message_id":5}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, m.warnCalls)
}

func TestHandleModerationWarn_MissingMessageID(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.moderator = &mockModerator{}
	rr := httptest.NewRecorder()
	h.handleModerationWarn(rr, modRequest(`{"user_id":1,"chat_id":2}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errMessageIDRequired.code, body["error_code"])
}

func TestHandleModerationDeleteMessages(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	m := &mockModerator{delMessagesCount: 7}
	h.moderator = m
	rr := httptest.NewRecorder()
	h.handleModerationDeleteMessages(rr, modRequest(`{"user_id":1,"chat_id":2,"period":"1d"}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, float64(7), body["deleted"])
	assert.Equal(t, "1d", m.delMessagesArgs.period)
}

func TestHandleModerationDeleteMessages_UsesAuthenticatedModerator(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	m := &mockModerator{}
	h.moderator = m
	token, otp := h.auth.CreateModeratorLogin(42, "@admin (Admin Name)")
	sessionToken, err := h.auth.ValidateModeratorLogin(token, otp, "ip")
	require.NoError(t, err)
	r := modRequest(`{"user_id":1,"chat_id":2,"period":"all"}`)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: sessionToken})
	rr := httptest.NewRecorder()
	h.handleModerationDeleteMessages(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, int64(42), m.lastActorID)
	assert.Equal(t, "@admin (Admin Name)", m.lastActorName)
}

func TestHandleModerationDeleteMessages_LegacySessionUsesReservedActor(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	m := &mockModerator{}
	h.moderator = m
	legacyToken := moderatorTokenPrefix + "legacy"
	h.auth.sessions[legacyToken] = &session{token: legacyToken, expiresAt: time.Now().Add(time.Hour)}
	r := modRequest(`{"user_id":1,"chat_id":2,"period":"all"}`)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: legacyToken})
	rr := httptest.NewRecorder()
	h.handleModerationDeleteMessages(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, ReservedWebModeratorID, m.lastActorID)
	assert.Equal(t, ReservedWebModeratorName, m.lastActorName)
}

func TestHandleModerationDeleteMessages_Error(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.moderator = &mockModerator{err: assertGenericErr()}
	rr := httptest.NewRecorder()
	h.handleModerationDeleteMessages(rr, modRequest(`{"user_id":1,"chat_id":2,"period":"all"}`))
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHandleModerationDeleteMessage(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	m := &mockModerator{}
	h.moderator = m
	rr := httptest.NewRecorder()
	h.handleModerationDeleteMessage(rr, modRequest(`{"user_id":1,"chat_id":2,"message_id":9}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, m.delMessageCalls)
}

func TestHandleModerationDeleteMessage_MissingMessageID(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.moderator = &mockModerator{}
	rr := httptest.NewRecorder()
	h.handleModerationDeleteMessage(rr, modRequest(`{"user_id":1,"chat_id":2}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleModerationRemoderate(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	m := &mockModerator{}
	h.moderator = m
	require.NotNil(t, m)
	// remoderate may require message_id; check handler exists in api_moderation.
	rr := httptest.NewRecorder()
	h.handleModerationRemoderate(rr, modRequest(`{"user_id":1,"chat_id":2,"message_id":3}`))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, m.remoderateCalls)
}
