// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gennadium/internal/config"
	"gennadium/internal/database"

	"gopkg.in/yaml.v3"
)

// These tests exercise the operator-facing dump/restore endpoints end to end
// (download config / upload config / download DB / upload DB / copy config to
// DB). They are written as behavior specs: a downloaded backup, when uploaded
// back, must reproduce the same data - so a future change to the file handlers
// cannot silently break backup/restore.

// newFileHandler builds an apiHandler backed by a real on-disk SQLite file so
// the DB download/upload handlers (which read/write h.config.Database.Path) work.
func newFileHandler(t *testing.T) *apiHandler {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "moderation.db")
	db, err := database.InitLocal(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := newTestConfig()
	cfg.Database.Path = dbPath
	return &apiHandler{
		config:     cfg,
		db:         db,
		auth:       NewAuthManager(db),
		pathPrefix: "/admin",
		configFile: filepath.Join(dir, "config.yaml"),
	}
}

// ── Config download / upload ──

func TestDownloadConfig_IsAFullBackupWithHashedPassword(t *testing.T) {
	h := newFileHandler(t)
	h.config.BotToken = "12345:SECRET"
	h.config.Language = "en"
	h.config.WebUI.Password = "plaintext-pw"

	rr := httptest.NewRecorder()
	h.handleDownloadConfig(rr, httptest.NewRequest(http.MethodGet, "/api/files/config", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Disposition"), "config.yaml")

	var dl config.Config
	require.NoError(t, yaml.Unmarshal(rr.Body.Bytes(), &dl))
	// A backup keeps secrets (it is the operator's own data), but the web UI
	// password must be hashed, never exported in plaintext.
	assert.Equal(t, "12345:SECRET", dl.BotToken)
	assert.Equal(t, "en", dl.Language)
	assert.True(t, config.IsHashedWebUIPassword(dl.WebUI.Password))
}

func TestConfigDownloadUpload_RoundTripPreservesValues(t *testing.T) {
	h := newFileHandler(t)
	h.config.BotToken = "round-trip-token"
	h.config.Admin.ChatID = -100999
	h.config.AI.Enabled = true

	// Download.
	dlRec := httptest.NewRecorder()
	h.handleDownloadConfig(dlRec, httptest.NewRequest(http.MethodGet, "/api/files/config", nil))
	require.Equal(t, http.StatusOK, dlRec.Code)
	backup := dlRec.Body.Bytes()

	// Mutate the live config, then restore the backup by uploading it.
	h.config.BotToken = "changed"
	h.config.Admin.ChatID = 0

	upRec := httptest.NewRecorder()
	h.handleUploadConfig(upRec, httptest.NewRequest(http.MethodPost, "/api/files/config", bytes.NewReader(backup)))
	require.Equal(t, http.StatusOK, upRec.Code)

	// The restored values must match what was downloaded.
	assert.Equal(t, "round-trip-token", h.config.BotToken)
	assert.Equal(t, int64(-100999), h.config.Admin.ChatID)
	assert.True(t, h.config.AI.Enabled)

	// File mode also writes the YAML to the config file.
	written, err := os.ReadFile(h.configFile)
	require.NoError(t, err)
	assert.Contains(t, string(written), "round-trip-token")
}

func TestUploadConfig_MethodNotAllowed(t *testing.T) {
	h := newFileHandler(t)
	rr := httptest.NewRecorder()
	h.handleUploadConfig(rr, httptest.NewRequest(http.MethodGet, "/api/files/config", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestUploadConfig_InvalidYAML(t *testing.T) {
	h := newFileHandler(t)
	rr := httptest.NewRecorder()
	h.handleUploadConfig(rr, httptest.NewRequest(http.MethodPost, "/api/files/config", strings.NewReader("bot_token: [unterminated")))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errConfigYAMLInvalid.code, body["error_code"])
}

func TestUploadConfig_DBMode_WritesToDatabase(t *testing.T) {
	h := newFileHandler(t)
	h.configFromDB = true

	yamlBody := "bot_token: db-mode-token\nlanguage: ru\n"
	rr := httptest.NewRecorder()
	h.handleUploadConfig(rr, httptest.NewRequest(http.MethodPost, "/api/files/config", strings.NewReader(yamlBody)))
	require.Equal(t, http.StatusOK, rr.Code)

	tok, err := h.db.GetConfigValue("bot_token")
	require.NoError(t, err)
	assert.Equal(t, "db-mode-token", tok)
	assert.Equal(t, "db-mode-token", h.config.BotToken)
}

func TestCopyConfigToDB(t *testing.T) {
	h := newFileHandler(t)
	h.config.BotToken = "copy-me"

	rr := httptest.NewRecorder()
	h.handleCopyConfigToDB(rr, httptest.NewRequest(http.MethodPost, "/api/config/copy-to-db", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	tok, err := h.db.GetConfigValue("bot_token")
	require.NoError(t, err)
	assert.Equal(t, "copy-me", tok)
}

func TestCopyConfigToDB_AlreadyDBSource(t *testing.T) {
	h := newFileHandler(t)
	h.configFromDB = true
	rr := httptest.NewRecorder()
	h.handleCopyConfigToDB(rr, httptest.NewRequest(http.MethodPost, "/api/config/copy-to-db", nil))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errConfigSourceAlreadyDB.code, body["error_code"])
}

func TestCopyConfigToDB_MethodNotAllowed(t *testing.T) {
	h := newFileHandler(t)
	rr := httptest.NewRecorder()
	h.handleCopyConfigToDB(rr, httptest.NewRequest(http.MethodGet, "/api/config/copy-to-db", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

// ── Env download / upload ──

func TestEnvDownloadUpload_RoundTrip(t *testing.T) {
	h := newFileHandler(t)
	h.config.BotToken = "env-token"

	dlRec := httptest.NewRecorder()
	h.handleDownloadEnv(dlRec, httptest.NewRequest(http.MethodGet, "/api/files/env", nil))
	require.Equal(t, http.StatusOK, dlRec.Code)
	assert.Contains(t, dlRec.Body.String(), "BOT_TOKEN")

	// Upload an env file that changes a value.
	upRec := httptest.NewRecorder()
	h.handleUploadEnv(upRec, httptest.NewRequest(http.MethodPost, "/api/files/env",
		strings.NewReader("# comment\nBOT_TOKEN=new-env-token\nLANGUAGE=ru\n")))
	require.Equal(t, http.StatusOK, upRec.Code)
	assert.Equal(t, "new-env-token", h.config.BotToken)
	assert.Equal(t, "ru", h.config.Language)
}

func TestUploadEnv_MethodNotAllowed(t *testing.T) {
	h := newFileHandler(t)
	rr := httptest.NewRecorder()
	h.handleUploadEnv(rr, httptest.NewRequest(http.MethodGet, "/api/files/env", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

// ── Database download / upload ──

func TestDownloadDB_Local_ServesSQLiteFile(t *testing.T) {
	h := newFileHandler(t)
	require.NoError(t, h.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 1, ChatID: -100, UserID: 1, Username: "u", Text: "x", Timestamp: timeUTC(),
	}))

	rr := httptest.NewRecorder()
	h.handleDownloadDB(rr, httptest.NewRequest(http.MethodGet, "/api/files/db", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Disposition"), "moderation.db")
	// The served bytes must be a real SQLite database file.
	assert.True(t, strings.HasPrefix(rr.Body.String(), "SQLite format 3"))
}

func TestUploadDB_RestoresData(t *testing.T) {
	h := newFileHandler(t)

	// Build a backup file from a separate, seeded database - this is exactly
	// what an operator would have previously downloaded.
	backup := makeBackupBytes(t, func(src *database.DB) {
		require.NoError(t, src.StoreMessageInfo(&database.MessageInfo{
			MessageID: 77, ChatID: -100, UserID: 5, Username: "alice", Text: "restored row", Timestamp: timeUTC(),
		}))
		require.NoError(t, src.UpsertUserProfile(&database.UserProfile{
			UserID: 5, Username: "alice", Profile: "from backup", Reputation: "good",
		}))
	})

	rr := httptest.NewRecorder()
	h.handleUploadDB(rr, httptest.NewRequest(http.MethodPost, "/api/files/db", bytes.NewReader(backup)))
	require.Equal(t, http.StatusOK, rr.Code)

	// The uploaded backup's data must now be live in the handler's DB.
	msg, err := h.db.GetMessageInfo(77, -100)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "restored row", msg.Text)

	prof, err := h.db.GetUserProfile(5)
	require.NoError(t, err)
	require.NotNil(t, prof)
	assert.Equal(t, "from backup", prof.Profile)
}

func TestUploadDB_RejectsNonSQLiteFile(t *testing.T) {
	h := newFileHandler(t)
	rr := httptest.NewRecorder()
	h.handleUploadDB(rr, httptest.NewRequest(http.MethodPost, "/api/files/db",
		strings.NewReader("this is definitely not a sqlite database file at all")))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errInvalidSQLiteFile.code, body["error_code"])
}

func TestUploadDB_MethodNotAllowed(t *testing.T) {
	h := newFileHandler(t)
	rr := httptest.NewRecorder()
	h.handleUploadDB(rr, httptest.NewRequest(http.MethodGet, "/api/files/db", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestCloneDB_RequiresRemoteDatabase(t *testing.T) {
	// newFileHandler is backed by a local SQLite DB with no remote configured;
	// both clone directions require a configured remote, so they must be refused
	// here.
	h := newFileHandler(t)
	cases := map[string]http.HandlerFunc{
		"/api/files/db/clone-to-local":  h.handleCloneRemoteToLocal,
		"/api/files/db/clone-to-remote": h.handleCloneLocalToRemote,
	}
	for path, fn := range cases {
		rr := httptest.NewRecorder()
		fn(rr, httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusBadRequest, rr.Code, path)
		body := decodeErrBody(t, rr)
		assert.Equal(t, errCloneRequiresRemote.code, body["error_code"], path)
	}
}

func TestCloneDB_RemoteConfiguredButLocalActive_ReachesRemote(t *testing.T) {
	// When the active database is local but a remote is configured (URL + auth
	// token), the clone tools are available: the handler no longer refuses with
	// clone_requires_remote and instead tries to reach the configured remote.
	// With an unreachable remote it surfaces clone_remote_connect_failed, which
	// proves the request got past the availability guard.
	h := newFileHandler(t)
	h.config.Database.URL = "libsql://nonexistent.invalid"
	h.config.Database.AuthToken = "test-token"

	cases := map[string]http.HandlerFunc{
		"/api/files/db/clone-to-local":  h.handleCloneRemoteToLocal,
		"/api/files/db/clone-to-remote": h.handleCloneLocalToRemote,
	}
	for path, fn := range cases {
		rr := httptest.NewRecorder()
		fn(rr, httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusBadGateway, rr.Code, path)
		body := decodeErrBody(t, rr)
		assert.Equal(t, errCloneRemoteConnectFailed.code, body["error_code"], path)
	}
}

func TestCloneDB_MethodNotAllowed(t *testing.T) {
	h := newFileHandler(t)
	for _, fn := range []http.HandlerFunc{h.handleCloneRemoteToLocal, h.handleCloneLocalToRemote} {
		rr := httptest.NewRecorder()
		fn(rr, httptest.NewRequest(http.MethodGet, "/api/files/db/clone", nil))
		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	}
}

func TestUploadDB_TooLarge(t *testing.T) {
	t.Setenv(maxDBUploadBytesEnv, "16")
	h := newFileHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/files/db", strings.NewReader(strings.Repeat("A", 100)))
	req.ContentLength = 100
	rr := httptest.NewRecorder()
	h.handleUploadDB(rr, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestGetMaxDBUploadBytes(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv(maxDBUploadBytesEnv, "")
		assert.Equal(t, defaultMaxDBUploadBytes, getMaxDBUploadBytes())
	})
	t.Run("valid override", func(t *testing.T) {
		t.Setenv(maxDBUploadBytesEnv, "1048576")
		assert.Equal(t, int64(1048576), getMaxDBUploadBytes())
	})
	t.Run("invalid falls back to default", func(t *testing.T) {
		t.Setenv(maxDBUploadBytesEnv, "not-a-number")
		assert.Equal(t, defaultMaxDBUploadBytes, getMaxDBUploadBytes())
	})
}

// ── Route dispatchers ──

func TestFileRoutes_MethodDispatch(t *testing.T) {
	ws := newTestServer(t)
	for _, path := range []string{"/api/files/config", "/api/files/env", "/api/files/db"} {
		rr := httptest.NewRecorder()
		var fn http.HandlerFunc
		switch path {
		case "/api/files/config":
			fn = ws.handleFileConfigRoute
		case "/api/files/env":
			fn = ws.handleFileEnvRoute
		case "/api/files/db":
			fn = ws.handleFileDBRoute
		}
		fn(rr, httptest.NewRequest(http.MethodDelete, path, nil))
		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code, path)
	}
}

// ── helpers ──

func timeUTC() time.Time { return time.Now().UTC().Truncate(time.Second) }

// makeBackupBytes seeds a fresh database, exports it to a backup file and
// returns the file's raw bytes - the same bytes an operator would download.
func makeBackupBytes(t *testing.T, seed func(*database.DB)) []byte {
	t.Helper()
	dir := t.TempDir()
	src, err := database.InitLocal(filepath.Join(dir, "src.db"))
	require.NoError(t, err)
	defer src.Close()
	if seed != nil {
		seed(src)
	}
	path, err := src.ExportToLocalFile(dir, true)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
