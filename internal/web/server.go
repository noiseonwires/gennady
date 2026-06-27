// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"
)

//go:embed static
var staticFiles embed.FS

// ScheduledEventsTrigger is called to trigger missed-event checking and execution.
type ScheduledEventsTrigger interface {
	TriggerScheduledEvents()
}

// WebServer handles the admin web UI and shares the HTTP mux with the webhook handler.
type WebServer struct {
	Mux                    *http.ServeMux
	config                 *config.Config
	db                     *database.DB
	auth                   *AuthManager
	diagnostics            *DiagnosticsTracker
	handler                *apiHandler
	pathPrefix             string
	scheduledEventsTrigger ScheduledEventsTrigger
}

// NewWebServer creates a new web server with all routes registered.
// When configFromDB is true the config source is the database (no config file).
func NewWebServer(cfg *config.Config, db *database.DB, diagnostics *DiagnosticsTracker, configFile string, configFromDB bool) *WebServer {
	auth := NewAuthManager(db)
	mux := http.NewServeMux()

	prefix := cfg.WebUI.PathPrefix
	if prefix == "" {
		prefix = "/admin"
	}
	prefix = strings.TrimRight(prefix, "/")

	h := &apiHandler{
		config:       cfg,
		configFile:   configFile,
		configFromDB: configFromDB,
		db:           db,
		auth:         auth,
		pathPrefix:   prefix,
		diagnostics:  diagnostics,
		startedAt:    time.Now(),
	}

	ws := &WebServer{
		Mux:         mux,
		config:      cfg,
		db:          db,
		auth:        auth,
		diagnostics: diagnostics,
		handler:     h,
		pathPrefix:  prefix,
	}

	ws.registerRoutes()
	return ws
}

// SetAPITester sets the external API tester (typically the bot instance).
func (ws *WebServer) SetAPITester(tester ExternalAPITester) {
	ws.handler.apiTester = tester
}

// SetChatNameResolver sets the chat name resolver (typically the bot instance).
func (ws *WebServer) SetChatNameResolver(r ChatNameResolver) {
	ws.handler.chatNames = r
}

// SetChatLister sets the chat directory enumerator (typically the bot instance).
func (ws *WebServer) SetChatLister(l ChatLister) {
	ws.handler.chatLister = l
}

// SetTopicNameResolver sets the forum-topic name resolver (typically the bot instance).
func (ws *WebServer) SetTopicNameResolver(r TopicNameResolver) {
	ws.handler.topicNames = r
}

// SetModerator sets the moderation backend (typically the bot instance).
func (ws *WebServer) SetModerator(m Moderator) {
	ws.handler.moderator = m
}

// SetRestartFunc sets the function called when admin requests a bot restart.
func (ws *WebServer) SetRestartFunc(fn func(mode string)) {
	ws.handler.restartFunc = fn
}

// SetLogBuffer sets the in-memory log buffer for the web UI.
func (ws *WebServer) SetLogBuffer(lb *LogBuffer) {
	ws.handler.logBuffer = lb
}

// SetSendOTP sets the function that sends an OTP code to the super-admin via Telegram.
func (ws *WebServer) SetSendOTP(fn func(code string) error) {
	ws.handler.sendOTP = fn
}

// SetScheduledEventsTrigger sets the trigger for scheduled events webhook mode.
func (ws *WebServer) SetScheduledEventsTrigger(t ScheduledEventsTrigger) {
	ws.scheduledEventsTrigger = t
	ws.registerScheduledEventsWebhook()
}

// SetBuildInfo sets version information displayed in the web UI.
func (ws *WebServer) SetBuildInfo(version, gitCommit, buildTime, botURL string) {
	ws.handler.version = version
	ws.handler.gitCommit = gitCommit
	ws.handler.buildTime = buildTime
	ws.handler.botURL = botURL
	ws.handler.botName = "Gennady"
	ws.handler.botAuthor = "Kirill aka Noiseonwires"
	ws.handler.botLicense = "AGPL-3.0"
}

// Auth returns the auth manager (used by bot to generate OTPs).
func (ws *WebServer) Auth() *AuthManager {
	return ws.auth
}

func (ws *WebServer) registerRoutes() {
	p := ws.pathPrefix

	// Auth endpoints (no session required)
	ws.Mux.HandleFunc(p+"/api/auth/login", ws.handler.handleLogin)
	ws.Mux.HandleFunc(p+"/api/auth/mode", ws.handler.handleAuthMode)

	// i18n endpoint (no session required)
	ws.Mux.HandleFunc(p+"/api/i18n", ws.handler.handleGetI18n)

	// Protected API endpoints
	ws.Mux.HandleFunc(p+"/api/auth/check", ws.requireAuth(ws.handler.handleAuthCheck))
	ws.Mux.HandleFunc(p+"/api/auth/logout", ws.requireAuth(ws.handler.handleLogout))

	ws.Mux.HandleFunc(p+"/api/config", ws.requireAuth(ws.handleConfigRoute))
	ws.Mux.HandleFunc(p+"/api/config/meta", ws.requireAuth(ws.handler.handleGetConfigMeta))
	ws.Mux.HandleFunc(p+"/api/config/rss", ws.requireAuth(ws.handleRssRoute))
	ws.Mux.HandleFunc(p+"/api/config/topics", ws.requireAuth(ws.handleTopicsRoute))
	ws.Mux.HandleFunc(p+"/api/config/models", ws.requireAuth(ws.handleModelsRoute))
	ws.Mux.HandleFunc(p+"/api/config/moderation-rules", ws.requireAuth(ws.handleModerationRulesRoute))
	ws.Mux.HandleFunc(p+"/api/config/chat-rules-overrides", ws.requireAuth(ws.handleChatRulesOverridesRoute))

	ws.Mux.HandleFunc(p+"/api/actions", ws.requireAuth(ws.handler.handleGetActions))
	ws.Mux.HandleFunc(p+"/api/muted", ws.requireAuth(ws.handler.handleGetMutedUsers))
	ws.Mux.HandleFunc(p+"/api/stats", ws.requireAuth(ws.handler.handleGetDBStats))
	ws.Mux.HandleFunc(p+"/api/tokens", ws.requireAuth(ws.handler.handleGetTokenUsage))
	ws.Mux.HandleFunc(p+"/api/messages", ws.requireAuth(ws.handler.handleGetMessages))
	ws.Mux.HandleFunc(p+"/api/messages/delete", ws.requireAuth(ws.handler.handleDeleteMessage))
	ws.Mux.HandleFunc(p+"/api/moderation/mute", ws.requireAuth(ws.handler.handleModerationMute))
	ws.Mux.HandleFunc(p+"/api/moderation/unmute", ws.requireAuth(ws.handler.handleModerationUnmute))
	ws.Mux.HandleFunc(p+"/api/moderation/warn", ws.requireAuth(ws.handler.handleModerationWarn))
	ws.Mux.HandleFunc(p+"/api/moderation/delete-messages", ws.requireAuth(ws.handler.handleModerationDeleteMessages))
	ws.Mux.HandleFunc(p+"/api/moderation/delete-message", ws.requireAuth(ws.handler.handleModerationDeleteMessage))
	ws.Mux.HandleFunc(p+"/api/moderation/remoderate", ws.requireAuth(ws.handler.handleModerationRemoderate))
	ws.Mux.HandleFunc(p+"/api/profiles", ws.requireAuth(ws.handler.handleGetProfiles))
	ws.Mux.HandleFunc(p+"/api/profiles/delete", ws.requireAuth(ws.handler.handleDeleteProfile))
	ws.Mux.HandleFunc(p+"/api/logs", ws.requireAuth(ws.handler.handleGetLogs))
	ws.Mux.HandleFunc(p+"/api/chats", ws.requireAuth(ws.handler.handleListChats))

	ws.Mux.HandleFunc(p+"/api/diagnostics", ws.requireAuth(ws.handler.handleGetDiagnostics))
	ws.Mux.HandleFunc(p+"/api/diagnostics/webhook", ws.requireAuth(ws.handler.handleGetWebhookInfo))
	ws.Mux.HandleFunc(p+"/api/diagnostics/test/", ws.requireAuth(ws.handler.handleTestAPI))
	ws.Mux.HandleFunc(p+"/api/diagnostics/debug/moderation/", ws.requireAuth(ws.handler.handleDebugModeration))
	ws.Mux.HandleFunc(p+"/api/diagnostics/debug/moderation-by-id/", ws.requireAuth(ws.handler.handleDebugModerationByID))
	ws.Mux.HandleFunc(p+"/api/diagnostics/debug/extract/", ws.requireAuth(ws.handler.handleDebugExtract))
	ws.Mux.HandleFunc(p+"/api/diagnostics/debug/ocr/", ws.requireAuth(ws.handler.handleDebugOCR))
	ws.Mux.HandleFunc(p+"/api/restart", ws.requireAuth(ws.handler.handleRestart))
	ws.Mux.HandleFunc(p+"/api/version", ws.requireAuth(ws.handler.handleGetVersion))

	ws.Mux.HandleFunc(p+"/api/files/config", ws.requireAuth(ws.handleFileConfigRoute))
	ws.Mux.HandleFunc(p+"/api/config/copy-to-db", ws.requireAuth(ws.handler.handleCopyConfigToDB))
	ws.Mux.HandleFunc(p+"/api/files/env", ws.requireAuth(ws.handleFileEnvRoute))
	ws.Mux.HandleFunc(p+"/api/files/db", ws.requireAuth(ws.handleFileDBRoute))
	ws.Mux.HandleFunc(p+"/api/files/db/clone-to-local", ws.requireAuth(ws.handler.handleCloneRemoteToLocal))
	ws.Mux.HandleFunc(p+"/api/files/db/clone-to-remote", ws.requireAuth(ws.handler.handleCloneLocalToRemote))

	// Static files (SPA)
	staticFS, _ := fs.Sub(staticFiles, "static")
	ws.Mux.HandleFunc(p+"/", func(w http.ResponseWriter, r *http.Request) {
		// Strip the path prefix for the file server
		trimmed := strings.TrimPrefix(r.URL.Path, p)
		if trimmed == "" || trimmed == "/" {
			trimmed = "index.html"
		} else {
			trimmed = strings.TrimPrefix(trimmed, "/")
		}

		// Try to open the requested file; fall back to index.html for SPA routing
		f, err := staticFS.Open(trimmed)
		if err != nil {
			trimmed = "index.html"
			f, err = staticFS.Open(trimmed)
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		defer f.Close()

		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			// Don't serve directories - fall back to index.html
			f.Close()
			f2, err := staticFS.Open("index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f2.Close()
			stat, _ = f2.Stat()
			http.ServeContent(w, r, "index.html", stat.ModTime(), f2.(io.ReadSeeker))
			return
		}

		http.ServeContent(w, r, trimmed, stat.ModTime(), f.(io.ReadSeeker))
	})

	log.Printf("🌐 Web UI registered at %s/", p)
}

// registerScheduledEventsWebhook registers the webhook trigger endpoint for scheduled events.
func (ws *WebServer) registerScheduledEventsWebhook() {
	if !ws.config.ScheduledEvents.WebhookMode {
		return
	}

	path := ws.config.ScheduledEvents.WebhookPath
	if path == "" {
		path = "/trigger-events"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	ws.Mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			writeWebErr(w, errMethodNotAllowed)
			return
		}

		log.Printf("⏰ Scheduled events webhook triggered from %s", r.RemoteAddr)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")

		if ws.scheduledEventsTrigger != nil {
			go ws.scheduledEventsTrigger.TriggerScheduledEvents()
		}
	})

	log.Printf("⏰ Scheduled events webhook registered at %s", path)
}

// requireAuth wraps a handler with session validation.
func (ws *WebServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractSessionToken(r)
		if token == "" || !ws.auth.ValidateSession(token) {
			writeWebErr(w, errUnauthorized)
			return
		}
		next(w, r)
	}
}

// Route handlers that dispatch by HTTP method.
func (ws *WebServer) handleConfigRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.handler.handleGetConfig(w, r)
	case http.MethodPut, http.MethodPost:
		ws.handler.handleSaveConfig(w, r)
	default:
		writeWebErr(w, errMethodNotAllowed)
	}
}

func (ws *WebServer) handleRssRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.handler.handleGetRssFeeds(w, r)
	case http.MethodPut, http.MethodPost:
		ws.handler.handleSaveRssFeeds(w, r)
	default:
		writeWebErr(w, errMethodNotAllowed)
	}
}

func (ws *WebServer) handleTopicsRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.handler.handleGetTopics(w, r)
	case http.MethodPut, http.MethodPost:
		ws.handler.handleSaveTopics(w, r)
	default:
		writeWebErr(w, errMethodNotAllowed)
	}
}

func (ws *WebServer) handleModelsRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.handler.handleGetModels(w, r)
	case http.MethodPut, http.MethodPost:
		ws.handler.handleSaveModels(w, r)
	default:
		writeWebErr(w, errMethodNotAllowed)
	}
}

func (ws *WebServer) handleModerationRulesRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.handler.handleGetModerationRules(w, r)
	case http.MethodPut, http.MethodPost:
		ws.handler.handleSaveModerationRules(w, r)
	default:
		writeWebErr(w, errMethodNotAllowed)
	}
}

func (ws *WebServer) handleChatRulesOverridesRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.handler.handleGetChatRulesOverrides(w, r)
	case http.MethodPut, http.MethodPost:
		ws.handler.handleSaveChatRulesOverrides(w, r)
	default:
		writeWebErr(w, errMethodNotAllowed)
	}
}

func (ws *WebServer) handleFileConfigRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.handler.handleDownloadConfig(w, r)
	case http.MethodPost:
		ws.handler.handleUploadConfig(w, r)
	default:
		writeWebErr(w, errMethodNotAllowed)
	}
}

func (ws *WebServer) handleFileEnvRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.handler.handleDownloadEnv(w, r)
	case http.MethodPost:
		ws.handler.handleUploadEnv(w, r)
	default:
		writeWebErr(w, errMethodNotAllowed)
	}
}

func (ws *WebServer) handleFileDBRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ws.handler.handleDownloadDB(w, r)
	case http.MethodPost:
		ws.handler.handleUploadDB(w, r)
	default:
		writeWebErr(w, errMethodNotAllowed)
	}
}
