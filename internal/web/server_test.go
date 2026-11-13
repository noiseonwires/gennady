// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T) *WebServer {
	t.Helper()
	db := newTestDB(t)
	cfg := newTestConfig()
	return NewWebServer(cfg, db, NewDiagnosticsTracker(), "config.yaml", false)
}

func TestNewWebServer_DefaultPrefix(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	cfg.WebUI.PathPrefix = ""
	ws := NewWebServer(cfg, db, NewDiagnosticsTracker(), "config.yaml", false)
	assert.Equal(t, "/admin", ws.pathPrefix)
	require.NotNil(t, ws.Mux)
	require.NotNil(t, ws.handler)
	require.NotNil(t, ws.auth)
}

func TestNewWebServer_TrimsTrailingSlash(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	cfg.WebUI.PathPrefix = "/panel/"
	ws := NewWebServer(cfg, db, NewDiagnosticsTracker(), "config.yaml", false)
	assert.Equal(t, "/panel", ws.pathPrefix)
}

func TestWebServer_AbsolutePathPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		publicURL string
		admin     string
		mod       string
		source    string
	}{
		{"nested moderator prefix", "https://panel.example.com", "/admin2", "/admin2/modentry", "config"},
		{"separate prefixes", " https://panel.example.com/ ", "/panel", "/modentry", "config"},
		{"webhook fallback", "", "/admin2", "/admin2/modentry", "webhook"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newTestConfig()
			cfg.WebUI.PublicURL = tc.publicURL
			cfg.WebUI.PathPrefix = tc.admin + "/"
			cfg.WebUI.ModeratorPathPrefix = tc.mod + "/"
			cfg.WebUI.Password = "secret"
			cfg.Webhook.URL = "https://panel.example.com/secret-webhook"
			ws := NewWebServer(cfg, newTestDB(t), NewDiagnosticsTracker(), "config.yaml", false)
			assert.Equal(t, tc.admin, ws.pathPrefix)
			assert.Equal(t, tc.mod, ws.moderatorPrefix)
			info := ws.handler.moderatorUIInfo()
			assert.Equal(t, "https://panel.example.com"+tc.mod+"/", info["url"])
			assert.Equal(t, tc.mod, info["path_prefix"])
			assert.Equal(t, tc.source, info["public_url_source"])

			for _, prefix := range []string{ws.pathPrefix, ws.moderatorPrefix} {
				for _, suffix := range []string{"/", "/js/page-logs-system.js", "/api/auth/mode"} {
					rr := httptest.NewRecorder()
					ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, prefix+suffix, nil))
					require.Equal(t, http.StatusOK, rr.Code, prefix+suffix)
				}
			}
			rr := httptest.NewRecorder()
			ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, ws.moderatorPrefix+"/api/auth/mode", nil))
			assert.JSONEq(t, `{"moderator":true}`, rr.Body.String())
			rr = httptest.NewRecorder()
			ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, ws.moderatorPrefix+"/api/config", nil))
			assert.Equal(t, http.StatusNotFound, rr.Code)

			server := httptest.NewServer(ws.Mux)
			defer server.Close()
			for _, role := range []string{"admin", "moderator"} {
				t.Run(role, func(t *testing.T) {
					jar, err := cookiejar.New(nil)
					require.NoError(t, err)
					client := server.Client()
					client.Jar = jar
					prefix := ws.pathPrefix
					loginPath := "/api/auth/login"
					body := `{"password":"secret"}`
					if role == "moderator" {
						prefix = ws.moderatorPrefix
						loginPath = "/api/auth/mod-login"
						token, otp := ws.auth.CreateModeratorLogin(42)
						body = fmt.Sprintf(`{"token":%q,"code":%q}`, token, otp)
					}
					res, err := client.Post(server.URL+prefix+loginPath, "application/json", strings.NewReader(body))
					require.NoError(t, err)
					res.Body.Close()
					require.Equal(t, http.StatusOK, res.StatusCode)
					require.Len(t, res.Cookies(), 1)
					assert.Equal(t, prefix, res.Cookies()[0].Path)

					res, err = client.Get(server.URL + prefix + "/api/auth/check")
					require.NoError(t, err)
					data, err := io.ReadAll(res.Body)
					res.Body.Close()
					require.NoError(t, err)
					assert.Equal(t, http.StatusOK, res.StatusCode)
					assert.JSONEq(t, `{"authenticated":true}`, string(data))

					res, err = client.Post(server.URL+prefix+"/api/auth/logout", "application/json", nil)
					require.NoError(t, err)
					res.Body.Close()
					require.Equal(t, http.StatusOK, res.StatusCode)
					require.Len(t, res.Cookies(), 1)
					assert.Equal(t, prefix, res.Cookies()[0].Path)
					assert.Equal(t, -1, res.Cookies()[0].MaxAge)

					res, err = client.Get(server.URL + prefix + "/api/auth/check")
					require.NoError(t, err)
					res.Body.Close()
					assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
				})
			}
		})
	}
}

func TestWebServer_PrefixCollisionDisablesModeratorUI(t *testing.T) {
	cfg := newTestConfig()
	cfg.WebUI.PublicURL = "https://panel.example.com"
	cfg.WebUI.ModeratorPathPrefix = cfg.WebUI.PathPrefix
	ws := NewWebServer(cfg, newTestDB(t), NewDiagnosticsTracker(), "config.yaml", false)
	assert.Empty(t, ws.moderatorPrefix)
	assert.Equal(t, false, ws.handler.moderatorUIInfo()["available"])
}

func TestWebServer_Auth(t *testing.T) {
	ws := newTestServer(t)
	assert.Same(t, ws.auth, ws.Auth())
}

func TestWebServer_Setters(t *testing.T) {
	ws := newTestServer(t)
	mod := &mockModerator{}
	ws.SetModerator(mod)
	assert.Same(t, mod, ws.handler.moderator.(*mockModerator))

	ws.SetRestartFunc(func(string) {})
	assert.NotNil(t, ws.handler.restartFunc)

	lb := NewLogBuffer(10)
	ws.SetLogBuffer(lb)
	assert.Same(t, lb, ws.handler.logBuffer)

	ws.SetSendOTP(func(string) error { return nil })
	assert.NotNil(t, ws.handler.sendOTP)

	ws.SetBuildInfo("v1", "commit", "time", "https://t.me/bot")
	assert.Equal(t, "v1", ws.handler.version)
	assert.Equal(t, "Gennady", ws.handler.botName)
	assert.Equal(t, "AGPL-3.0", ws.handler.botLicense)
}

func TestWebServer_SetChatResolvers(t *testing.T) {
	ws := newTestServer(t)
	r := &mockChatResolver{name: "My Chat"}
	ws.SetChatNameResolver(r)
	assert.Equal(t, "My Chat", ws.handler.resolveChatName(123))

	l := &mockChatLister{}
	ws.SetChatLister(l)
	assert.NotNil(t, ws.handler.chatLister)

	tester := &mockAPITester{}
	ws.SetAPITester(tester)
	assert.NotNil(t, ws.handler.apiTester)
}

func TestRequireAuth_NoToken(t *testing.T) {
	ws := newTestServer(t)
	called := false
	h := ws.requireAuth(func(http.ResponseWriter, *http.Request) { called = true })
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/x", nil))
	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireAuth_ValidToken(t *testing.T) {
	ws := newTestServer(t)
	token := ws.auth.CreatePasswordSession()
	called := false
	h := ws.requireAuth(func(http.ResponseWriter, *http.Request) { called = true })
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: token})
	h(rr, r)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestServeMux_StaticIndex(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "no-store, max-age=0", rr.Header().Get("Cache-Control"))
}

func TestServeMux_StaticAssetIsNotCached(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/js/page-logs-system.js", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "no-store, max-age=0", rr.Header().Get("Cache-Control"))
}

func TestServeMux_StaticSPAFallback(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	// Unknown path under prefix falls back to index.html (SPA routing).
	ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/some/route", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestServeMux_AuthEndpointUnprotected(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	// auth/mode requires no session and should return JSON.
	ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/api/auth/mode", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestServeMux_ProtectedEndpointRequiresAuth(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil))
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestServeMux_ConfigSecretRequiresSuperAdmin(t *testing.T) {
	ws := newTestServer(t)
	ws.handler.config.BotToken = "actual-token"
	adminSession := ws.auth.CreatePasswordSession()
	token, otp := ws.auth.CreateModeratorLogin(42)
	modSession, err := ws.auth.ValidateModeratorLogin(token, otp, "ip")
	require.NoError(t, err)

	for _, tc := range []struct {
		name   string
		token  string
		status int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"invalid", "invalid-session", http.StatusUnauthorized},
		{"moderator", modSession, http.StatusUnauthorized},
		{"super-admin", adminSession, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/admin/api/config/secret?key=bot_token", nil)
			if tc.token != "" {
				r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: tc.token})
			}
			ws.Mux.ServeHTTP(rr, r)
			assert.Equal(t, tc.status, rr.Code)
			if tc.status == http.StatusOK {
				assert.Equal(t, "actual-token", decodeErrBody(t, rr)["value"])
			} else {
				assert.NotContains(t, rr.Body.String(), "actual-token")
			}
		})
	}
}

func TestHandleConfigRoute_MethodDispatch(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.handleConfigRoute(rr, httptest.NewRequest(http.MethodDelete, "/api/config", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleRssRoute_MethodNotAllowed(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.handleRssRoute(rr, httptest.NewRequest(http.MethodDelete, "/api/config/rss", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleModelsRoute_MethodNotAllowed(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.handleModelsRoute(rr, httptest.NewRequest(http.MethodDelete, "/api/config/models", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleModerationRulesRoute_MethodNotAllowed(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.handleModerationRulesRoute(rr, httptest.NewRequest(http.MethodDelete, "/api/config/moderation-rules", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleChatRulesOverridesRoute_MethodNotAllowed(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.handleChatRulesOverridesRoute(rr, httptest.NewRequest(http.MethodDelete, "/api/config/chat-rules-overrides", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleFileConfigRoute_MethodNotAllowed(t *testing.T) {
	ws := newTestServer(t)
	rr := httptest.NewRecorder()
	ws.handleFileConfigRoute(rr, httptest.NewRequest(http.MethodDelete, "/api/files/config", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestScheduledEventsWebhook(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	cfg.ScheduledEvents.WebhookMode = true
	cfg.ScheduledEvents.WebhookPath = "trigger-events"
	ws := NewWebServer(cfg, db, NewDiagnosticsTracker(), "config.yaml", false)

	trig := &mockTrigger{}
	trig.wg.Add(1)
	ws.SetScheduledEventsTrigger(trig)

	rr := httptest.NewRecorder()
	ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/trigger-events", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
	trig.wait()
	assert.Equal(t, 1, trig.calls())
}

func TestScheduledEventsWebhook_MethodNotAllowed(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	cfg.ScheduledEvents.WebhookMode = true
	ws := NewWebServer(cfg, db, NewDiagnosticsTracker(), "config.yaml", false)
	ws.SetScheduledEventsTrigger(&mockTrigger{})

	rr := httptest.NewRecorder()
	ws.Mux.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/trigger-events", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

// ── mocks for the auxiliary web interfaces ──

type mockChatResolver struct{ name string }

func (m *mockChatResolver) GetChatName(int64) string { return m.name }

type mockChatLister struct{}

func (m *mockChatLister) ListChatsForUI() []any { return []any{} }

type mockAPITester struct {
	statusCode   int
	responseTime time.Duration
	errMsg       string
	debugErr     error
	lastService  string
}

func (m *mockAPITester) TestExternalAPI(service string) (int, time.Duration, string) {
	m.lastService = service
	return m.statusCode, m.responseTime, m.errMsg
}
func (m *mockAPITester) DebugModerationPrompt(serviceKey, message string) (string, string, string, error) {
	m.lastService = serviceKey
	return "sys", "usr", "resp", m.debugErr
}
func (m *mockAPITester) DebugCreativeReply(serviceKey, message string, chatID int64, topicID int) (string, string, string, map[string]any, error) {
	m.lastService = serviceKey
	return "creative sys", "creative usr", "creative resp", map[string]any{"web_search_used": true}, m.debugErr
}
func (m *mockAPITester) DebugModerationByMessageID(serviceKey string, messageID int, chatID int64) (string, string, string, map[string]any, error) {
	m.lastService = serviceKey
	return "sys", "usr", "resp", map[string]any{"chat": chatID}, m.debugErr
}
func (m *mockAPITester) DebugURLExtraction(serviceKey, targetURL string) (string, error) {
	m.lastService = serviceKey
	return "raw", m.debugErr
}
func (m *mockAPITester) DebugOCR(serviceKey string, imageData []byte) (string, error) {
	m.lastService = serviceKey
	return "text", m.debugErr
}

type mockTrigger struct {
	mu  sync.Mutex
	wg  sync.WaitGroup
	cnt int
}

func (m *mockTrigger) TriggerScheduledEvents() {
	m.mu.Lock()
	m.cnt++
	m.mu.Unlock()
	m.wg.Done()
}

func (m *mockTrigger) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cnt
}

func (m *mockTrigger) wait() { m.wg.Wait() }
