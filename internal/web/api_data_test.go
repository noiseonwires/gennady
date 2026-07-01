// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gennadium/internal/database"
)

func get(t *testing.T, h *apiHandler, fn http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	fn(rr, httptest.NewRequest(http.MethodGet, target, nil))
	return rr
}

func TestHandleGetActions_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetActions, "/api/actions")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, float64(0), body["total"])
	assert.Empty(t, body["actions"])
}

func TestHandleGetActions_WithLimit(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetActions, "/api/actions?limit=10&offset=20")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Contains(t, body, "total")
}

func TestHandleGetMutedUsers_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetMutedUsers, "/api/muted")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, float64(0), body["total"])
	assert.Empty(t, body["users"])
}

func TestHandleGetMessages_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetMessages, "/api/messages?limit=5&offset=0")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, float64(0), body["total"])
}

func TestHandleGetMessages_SingleByIDNotFound(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetMessages, "/api/messages?message_id=5&chat_id=10")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, float64(0), body["total"])
}

func TestHandleGetMessages_InvalidID(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetMessages, "/api/messages?message_id=abc&chat_id=xyz")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleDeleteMessage_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleDeleteMessage(rr, httptest.NewRequest(http.MethodPost, "/api/messages/delete", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleDeleteMessage_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleDeleteMessage(rr, httptest.NewRequest(http.MethodDelete, "/api/messages/delete", strings.NewReader("x")))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleDeleteMessage_MissingID(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleDeleteMessage(rr, httptest.NewRequest(http.MethodDelete, "/api/messages/delete", strings.NewReader(`{"chat_id":1}`)))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleDeleteMessage_Success(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleDeleteMessage(rr, httptest.NewRequest(http.MethodDelete, "/api/messages/delete", strings.NewReader(`{"message_id":1,"chat_id":2}`)))
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleGetDBStats(t *testing.T) {
	cfg := newTestConfig()
	h := newTestHandler(t, cfg)
	rr := get(t, h, h.handleGetDBStats, "/api/stats")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Contains(t, body, "table_counts")
	assert.Contains(t, body, "database_provider")
}

func TestHandleGetTokenUsage(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetTokenUsage, "/api/tokens")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Contains(t, body, "rows")
	assert.Contains(t, body, "totals")
}

func TestHandleGetDiagnostics(t *testing.T) {
	cfg := newTestConfig()
	cfg.Admin.ChatID = 555
	h := newTestHandler(t, cfg)
	h.diagnostics = NewDiagnosticsTracker()
	rr := get(t, h, h.handleGetDiagnostics, "/api/diagnostics")
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Contains(t, body, "services")
	assert.Contains(t, body, "telegram")
	assert.Contains(t, body, "uptime_seconds")
}

func TestHandleGetWebhookInfo_NoToken(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleGetWebhookInfo(rr, httptest.NewRequest(http.MethodGet, "/api/diagnostics/webhook", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestHandleTestAPI_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleTestAPI(rr, httptest.NewRequest(http.MethodGet, "/api/diagnostics/test/weather", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleTestAPI_MissingService(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleTestAPI(rr, httptest.NewRequest(http.MethodPost, "/api/diagnostics/test/", nil))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleTestAPI_NoTester(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleTestAPI(rr, httptest.NewRequest(http.MethodPost, "/api/diagnostics/test/weather", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestHandleTestAPI_Success(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{statusCode: 200}
	rr := httptest.NewRecorder()
	h.handleTestAPI(rr, httptest.NewRequest(http.MethodPost, "/api/diagnostics/test/weather", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, "weather", body["service"])
	assert.Equal(t, true, body["success"])
}

func TestHandleDebugModeration(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/moderation/openai_light:m", strings.NewReader(`{"message":"hi"}`))
	h.handleDebugModeration(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, "sys", body["system_prompt"])
}

func TestHandleDebugModeration_EmptyMessage(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/moderation/svc", strings.NewReader(`{"message":"  "}`))
	h.handleDebugModeration(rr, r)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleDebugModeration_NoTester(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/moderation/svc", strings.NewReader(`{"message":"hi"}`))
	h.handleDebugModeration(rr, r)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestHandleDebugModerationByID(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/moderation-by-id/svc", strings.NewReader(`{"message_id":5,"chat_id":7}`))
	h.handleDebugModerationByID(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleDebugModerationByID_MissingID(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/moderation-by-id/svc", strings.NewReader(`{"message_id":0}`))
	h.handleDebugModerationByID(rr, r)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleDebugExtract(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/extract/diffbot", strings.NewReader(`{"url":"https://x"}`))
	h.handleDebugExtract(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleDebugExtract_MissingURL(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/extract/diffbot", strings.NewReader(`{"url":""}`))
	h.handleDebugExtract(rr, r)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleDebugOCR(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	img := base64.StdEncoding.EncodeToString([]byte("fakeimage"))
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/ocr/azure_vision", strings.NewReader(`{"image":"`+img+`"}`))
	h.handleDebugOCR(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleDebugOCR_DataURIPrefix(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	img := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("img"))
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/ocr/svc", strings.NewReader(`{"image":"`+img+`"}`))
	h.handleDebugOCR(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleDebugOCR_EmptyImage(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/ocr/svc", strings.NewReader(`{"image":""}`))
	h.handleDebugOCR(rr, r)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleDebugOCR_InvalidBase64(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.apiTester = &mockAPITester{}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/diagnostics/debug/ocr/svc", strings.NewReader(`{"image":"!!!notbase64!!!"}`))
	h.handleDebugOCR(rr, r)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleRestart_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleRestart(rr, httptest.NewRequest(http.MethodGet, "/api/restart", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleRestart_Unavailable(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleRestart(rr, httptest.NewRequest(http.MethodPost, "/api/restart", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestHandleRestart_Success(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.restartFunc = func(string) {}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/restart", strings.NewReader(`{"mode":"hard"}`))
	h.handleRestart(rr, r)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleGetLogs_NoBuffer(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetLogs, "/api/logs")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "[]\n", rr.Body.String())
}

func TestHandleGetLogs_WithBuffer(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	lb := NewLogBuffer(5)
	lb.Write([]byte("hello\n"))
	h.logBuffer = lb
	rr := get(t, h, h.handleGetLogs, "/api/logs")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "hello")
}

func TestHandleListChats_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleListChats(rr, httptest.NewRequest(http.MethodPost, "/api/chats", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleListChats_NoLister(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleListChats, "/api/chats")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "[]\n", rr.Body.String())
}

func TestHandleListChats_WithLister(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.chatLister = &mockChatLister{}
	rr := get(t, h, h.handleListChats, "/api/chats")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleGetProfiles_Empty(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := get(t, h, h.handleGetProfiles, "/api/profiles")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleGetProfiles_SortAndPagination(t *testing.T) {
	h := newTestHandler(t, newTestConfig())

	// Three users, discovered at increasing recency (user 3 is newest).
	seed := func(uid int64, name string, ago time.Duration) {
		_, err := h.db.RecordIncomingMessage(
			&database.MessageInfo{MessageID: int(uid), ChatID: -100, UserID: uid, Username: name, Timestamp: time.Now().Add(-ago)},
			database.IncomingMessageOpts{TrackProfile: true, Username: name, DisplayName: name},
		)
		require.NoError(t, err)
	}
	seed(1, "charlie", 72*time.Hour)
	seed(2, "alice", 48*time.Hour)
	seed(3, "bob", 1*time.Hour)

	userIDAt := func(profs []any, i int) float64 {
		return profs[i].(map[string]any)["user_id"].(float64)
	}

	// Default sort = discovery, newest first → bob(3), alice(2), charlie(1).
	rr := get(t, h, h.handleGetProfiles, "/api/profiles")
	require.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, float64(3), body["total"])
	profs := body["profiles"].([]any)
	require.Len(t, profs, 3)
	assert.Equal(t, float64(3), userIDAt(profs, 0))
	assert.Equal(t, float64(1), userIDAt(profs, 2))

	// Pagination: limit=1, offset=1 → the second item (alice / user 2).
	rr = get(t, h, h.handleGetProfiles, "/api/profiles?limit=1&offset=1")
	body = decodeJSONBody(t, rr)
	assert.Equal(t, float64(3), body["total"])
	profs = body["profiles"].([]any)
	require.Len(t, profs, 1)
	assert.Equal(t, float64(2), userIDAt(profs, 0))

	// Sort by name → alice(2), bob(3), charlie(1).
	rr = get(t, h, h.handleGetProfiles, "/api/profiles?sort=name")
	body = decodeJSONBody(t, rr)
	profs = body["profiles"].([]any)
	require.Len(t, profs, 3)
	assert.Equal(t, float64(2), userIDAt(profs, 0))
	assert.Equal(t, float64(3), userIDAt(profs, 1))
	assert.Equal(t, float64(1), userIDAt(profs, 2))
}

func TestHandleGetProfiles_SearchFilter(t *testing.T) {
	h := newTestHandler(t, newTestConfig())

	seed := func(uid int64, name string) {
		_, err := h.db.RecordIncomingMessage(
			&database.MessageInfo{MessageID: int(uid), ChatID: -100, UserID: uid, Username: name, Timestamp: time.Now()},
			database.IncomingMessageOpts{TrackProfile: true, Username: name, DisplayName: name},
		)
		require.NoError(t, err)
	}
	seed(1, "alice")
	seed(2, "bob")
	seed(3, "alicia")

	userIDAt := func(profs []any, i int) float64 {
		return profs[i].(map[string]any)["user_id"].(float64)
	}

	// Case-insensitive substring "ali" matches alice(1) and alicia(3) only.
	rr := get(t, h, h.handleGetProfiles, "/api/profiles?search=ALI&sort=name")
	require.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSONBody(t, rr)
	assert.Equal(t, float64(2), body["total"])
	profs := body["profiles"].([]any)
	require.Len(t, profs, 2)
	assert.Equal(t, float64(1), userIDAt(profs, 0))
	assert.Equal(t, float64(3), userIDAt(profs, 1))

	// No match → empty list, total 0.
	rr = get(t, h, h.handleGetProfiles, "/api/profiles?search=zzz")
	body = decodeJSONBody(t, rr)
	assert.Equal(t, float64(0), body["total"])
	assert.Empty(t, body["profiles"].([]any))
}

func TestHandleDeleteProfile_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleDeleteProfile(rr, httptest.NewRequest(http.MethodPost, "/api/profiles/delete", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleDeleteProfile_BadJSON(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleDeleteProfile(rr, httptest.NewRequest(http.MethodDelete, "/api/profiles/delete", strings.NewReader("x")))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleDeleteProfile_MissingUserID(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleDeleteProfile(rr, httptest.NewRequest(http.MethodDelete, "/api/profiles/delete", strings.NewReader(`{"user_id":0}`)))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleDeleteProfile_Success(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	rr := httptest.NewRecorder()
	h.handleDeleteProfile(rr, httptest.NewRequest(http.MethodDelete, "/api/profiles/delete", strings.NewReader(`{"user_id":42}`)))
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRenderActivityPlotWeb(t *testing.T) {
	out := renderActivityPlotWeb(nil)
	assert.Equal(t, "[]", out)
}

func TestLast7DaysKeysWeb(t *testing.T) {
	keys := last7DaysKeysWeb(time.Now())
	require.NotEmpty(t, keys)
	// keys are chronological (oldest first)
	assert.True(t, keys[0] <= keys[len(keys)-1])
}

// mockTopicResolver returns a fixed name only for a specific (chat, thread).
type mockTopicResolver struct {
	chatID   int64
	threadID int
	name     string
}

func (m *mockTopicResolver) GetTopicName(chatID int64, threadID int) string {
	if chatID == m.chatID && threadID == m.threadID {
		return m.name
	}
	return ""
}

func TestHandleGetMessages_EnrichesTopicReactionsQuote(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.topicNames = &mockTopicResolver{chatID: -100, threadID: 7, name: "Support"}

	// Parent message that the second message replies to.
	require.NoError(t, h.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 1, ChatID: -100, UserID: 1, Username: "alice",
		Text: "the full original parent text", Timestamp: time.Now(),
	}))
	// Reply carrying a topic, a precise quote, and reactions.
	replyTo := 1
	require.NoError(t, h.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 2, ChatID: -100, UserID: 2, Username: "bob",
		Text: "I disagree", ReplyToMessageID: &replyTo,
		MessageThreadID: 7, QuoteText: "original parent",
		Timestamp: time.Now().Add(time.Second),
	}))
	_, err := h.db.StoreMessageReactions(2, -100, `{"👍":3,"🔥":1}`)
	require.NoError(t, err)

	rr := get(t, h, h.handleGetMessages, "/api/messages?limit=10&offset=0")
	assert.Equal(t, http.StatusOK, rr.Code)

	var body struct {
		Messages []struct {
			MessageID        int    `json:"message_id"`
			TopicName        string `json:"topic_name"`
			MessageThreadID  int    `json:"message_thread_id"`
			ReactionsText    string `json:"reactions_text"`
			ReplyToMessageID *int   `json:"reply_to_message_id"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))

	byID := map[int]int{}
	for i, m := range body.Messages {
		byID[m.MessageID] = i
	}
	reply := body.Messages[byID[2]]

	// Topic name resolved from the (chat, thread) pair.
	assert.Equal(t, "Support", reply.TopicName)
	assert.Equal(t, 7, reply.MessageThreadID)
	// Reactions formatted for display (highest count first).
	assert.Equal(t, "👍 3   🔥 1", reply.ReactionsText)
	// The parent is no longer inlined - the UI lazily loads it via the
	// single-message endpoint. Only the reply id is exposed so the UI knows a
	// parent exists to fetch.
	require.NotNil(t, reply.ReplyToMessageID)
	assert.Equal(t, 1, *reply.ReplyToMessageID)
}

func TestHandleGetMessages_SingleLookupEnriched(t *testing.T) {
	h := newTestHandler(t, newTestConfig())
	h.topicNames = &mockTopicResolver{chatID: -100, threadID: 7, name: "Support"}

	require.NoError(t, h.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 5, ChatID: -100, UserID: 9, Username: "carol",
		Text: "parent in a topic", MessageThreadID: 7, Timestamp: time.Now(),
	}))
	_, err := h.db.StoreMessageReactions(5, -100, `{"🔥":2}`)
	require.NoError(t, err)

	rr := get(t, h, h.handleGetMessages, "/api/messages?message_id=5&chat_id=-100")
	assert.Equal(t, http.StatusOK, rr.Code)

	var body struct {
		Total    int `json:"total"`
		Messages []struct {
			MessageID     int    `json:"message_id"`
			Text          string `json:"text"`
			TopicName     string `json:"topic_name"`
			ReactionsText string `json:"reactions_text"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, 1, body.Total)
	require.Len(t, body.Messages, 1)
	m := body.Messages[0]
	assert.Equal(t, 5, m.MessageID)
	assert.Equal(t, "parent in a topic", m.Text)
	// Single lookups carry the same enrichment as the list (topic name +
	// formatted reactions) so the reply-chain viewer renders full data.
	assert.Equal(t, "Support", m.TopicName)
	assert.Equal(t, "🔥 2", m.ReactionsText)
}
