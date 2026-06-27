// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"gennadium/internal/config"
	"gennadium/internal/database"

	"gopkg.in/yaml.v3"
)

// File-management endpoints: download / upload of YAML config, env files and
// the SQLite database. Also handleCopyConfigToDB which migrates a file-based
// config into the database for DB-as-config-source mode.

const (
	defaultMaxDBUploadBytes int64 = 128 << 20
	maxDBUploadBytesEnv           = "WEB_UI_MAX_DB_UPLOAD_BYTES"
)

func (h *apiHandler) handleDownloadConfig(w http.ResponseWriter, r *http.Request) {
	exportCfg, err := config.ConfigForExport(h.config)
	if err != nil {
		writeWebErrf(w, errConfigMarshalFailed, "failed to prepare config for export: %v", err)
		return
	}
	data, err := yaml.Marshal(exportCfg)
	if err != nil {
		writeWebErrf(w, errConfigMarshalFailed, "failed to marshal config: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", "attachment; filename=config.yaml")
	w.Write(data)
}

func (h *apiHandler) handleUploadConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10 MB limit
	if err != nil {
		writeWebErrf(w, errFailedReadBody, "%v", err)
		return
	}

	// Validate YAML
	var testCfg config.Config
	if err := yaml.Unmarshal(data, &testCfg); err != nil {
		writeWebErrf(w, errConfigYAMLInvalid, "invalid YAML: %v", err)
		return
	}

	if h.configFromDB {
		// DB mode: export parsed config values to DB, no local file
		kv, err := config.ConfigToDBStringMap(&testCfg)
		if err != nil {
			writeWebErrf(w, errConfigSaveToDBFailed, "failed to prepare config for database: %v", err)
			return
		}
		if err := h.db.SetAllConfigValues(kv); err != nil {
			writeWebErrf(w, errConfigSaveToDBFailed, "failed to save config to database: %v", err)
			return
		}
		testCfg.WebUI.Password = kv["web_ui.password"]
		log.Printf("WebUI: config uploaded to database (%d values)", len(kv))
	} else {
		// File mode: replace the config file
		if err := os.WriteFile(h.configFile, data, 0600); err != nil {
			writeWebErrf(w, errConfigSaveFailed, "%s", writeFileError(h.configFile, err))
			return
		}
		log.Printf("WebUI: config file uploaded")
	}

	// Refresh in-memory config so the Web UI immediately reflects the new
	// values without requiring a process restart. Note that runtime systems
	// (scheduler, AI client, etc.) still need a restart to fully re-apply.
	// ReplaceContents holds the config write lock so the bot's concurrent reads
	// never observe a half-copied struct.
	h.config.ReplaceContents(&testCfg)

	jsonResponse(w, map[string]string{"status": "ok", "message": "Config uploaded. Restart to apply."})
}

func (h *apiHandler) handleCopyConfigToDB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	if h.configFromDB {
		writeWebErr(w, errConfigSourceAlreadyDB)
		return
	}

	kv, err := config.ConfigToDBStringMap(h.config)
	if err != nil {
		writeWebErrf(w, errConfigCopyToDBFailed, "failed to prepare config for database: %v", err)
		return
	}
	if err := h.db.SetAllConfigValues(kv); err != nil {
		writeWebErrf(w, errConfigCopyToDBFailed, "failed to copy config to database: %v", err)
		return
	}

	log.Printf("WebUI: config copied to database (%d values)", len(kv))
	jsonResponse(w, map[string]string{"status": "ok", "message": fmt.Sprintf("Config copied to database (%d values). You can now remove the config file and restart to use DB as config source.", len(kv))})
}

func (h *apiHandler) handleDownloadEnv(w http.ResponseWriter, r *http.Request) {
	exportCfg, err := config.ConfigForExport(h.config)
	if err != nil {
		writeWebErrf(w, errConfigMarshalFailed, "failed to prepare config for export: %v", err)
		return
	}
	envStr := config.ExportEnvVars(exportCfg)
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", "attachment; filename=config.env")
	w.Write([]byte(envStr))
}

func (h *apiHandler) handleUploadEnv(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		writeWebErrf(w, errFailedReadBody, "%v", err)
		return
	}

	// Parse env lines and apply to config
	updates := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		updates[line[:idx]] = line[idx+1:]
	}

	// Env upload still uses the old env-name approach via reflection
	config.Lock()
	for envName, val := range updates {
		setConfigFieldByEnv(reflect.ValueOf(h.config).Elem(), "", envName, val, nil)
	}
	config.Unlock()

	if err := h.persistConfig(); err != nil {
		writeWebErrf(w, errConfigSaveFailed, "failed to save config: %v", err)
		return
	}

	log.Printf("WebUI: env file uploaded and applied (%d variables)", len(updates))
	jsonResponse(w, map[string]string{"status": "ok", "message": "Environment applied and saved. Restart to apply fully."})
}

func (h *apiHandler) handleDownloadDB(w http.ResponseWriter, r *http.Request) {
	if h.db.IsLocal() {
		// Local: checkpoint and serve the file directly
		if err := h.db.WALCheckpoint(); err != nil {
			log.Printf("WAL checkpoint before DB download failed: %v", err)
		}
		data, err := os.ReadFile(h.config.Database.Path)
		if err != nil {
			writeWebErrf(w, errReadDBFileFailed, "%v", err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=moderation.db")
		w.Write(data)
		return
	}

	// Remote: export to a temp local SQLite file, serve it, then delete
	tmpDir := filepath.Dir(h.config.Database.Path)
	includeConfig := r.URL.Query().Get("include_config") == "1"
	tmpPath, err := h.db.ExportToLocalFile(tmpDir, includeConfig)
	if err != nil {
		writeWebErrf(w, errExportDBFailed, "failed to export database: %v", err)
		return
	}
	defer os.Remove(tmpPath)

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		writeWebErrf(w, errReadExportedDBFailed, "%v", err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=moderation.db")
	w.Write(data)
}

func (h *apiHandler) handleUploadDB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	maxUploadBytes := getMaxDBUploadBytes()
	if r.ContentLength > maxUploadBytes {
		writeWebErrf(w, errUploadedDBTooLarge, "uploaded database is too large (max %d MB)", maxUploadBytes>>20)
		return
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUploadBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeWebErrf(w, errUploadedDBTooLarge, "uploaded database is too large (max %d MB)", maxUploadBytes>>20)
			return
		}
		writeWebErrf(w, errFailedReadBody, "%v", err)
		return
	}

	// Basic SQLite validation: check magic bytes
	if len(data) < 16 || string(data[:16]) != "SQLite format 3\000" {
		writeWebErr(w, errInvalidSQLiteFile)
		return
	}

	// Write uploaded data to a temp file, then import into the live DB connection
	// so data is immediately visible without restart (works for both local and remote).
	tmpDir := filepath.Dir(h.config.Database.Path)
	if err := os.MkdirAll(tmpDir, 0700); err != nil {
		writeWebErrf(w, errCreateTempDirFailed, "%v", err)
		return
	}
	tmpFile, err := os.CreateTemp(tmpDir, "moderation_upload_*.db")
	if err != nil {
		writeWebErrf(w, errCreateTempFileFailed, "%v", err)
		return
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		writeWebErrf(w, errWriteTempFileFailed, "%v", err)
		return
	}
	tmpFile.Close()
	defer os.Remove(tmpPath)

	includeConfig := r.URL.Query().Get("include_config") == "1"
	if err := h.db.ImportFromLocalFile(tmpPath, includeConfig); err != nil {
		writeWebErrf(w, errImportDBFailed, "failed to import database: %v", err)
		return
	}
	if includeConfig {
		_, err := h.hashStoredWebUIPassword()
		if err != nil {
			writeWebErrf(w, errImportDBFailed, "failed to secure imported config: %v", err)
			return
		}
		if h.configFromDB {
			if err := h.reloadConfigFromDB(); err != nil {
				writeWebErrf(w, errImportDBFailed, "failed to reload imported config: %v", err)
				return
			}
		}
	}

	h.db.InvalidateMuteCache()
	log.Printf("WebUI: database imported from uploaded file")
	jsonResponse(w, map[string]string{"status": "ok", "message": "Database imported successfully."})
}

// remoteConfigured reports whether a remote database is configured (both a
// connection URL and an auth token are present), regardless of which provider
// is currently active. The database clone tools are available whenever this is
// true: either the remote is the live database, or it is configured but
// inactive and we open a secondary connection to it on demand.
func (h *apiHandler) remoteConfigured() bool {
	return strings.TrimSpace(h.config.Database.URL) != "" &&
		strings.TrimSpace(h.config.Database.AuthToken) != ""
}

// remoteCloneConfig builds the database.Config used to open a secondary
// connection to the configured remote database.
func (h *apiHandler) remoteCloneConfig() database.Config {
	return database.Config{
		Provider:  database.ProviderRemote,
		URL:       h.config.Database.URL,
		AuthToken: h.config.Database.AuthToken,
	}
}

// cloneTempDir returns a writable directory for the intermediate SQLite file
// used while transferring data between databases. It prefers the directory of
// the configured local database path, falling back to the OS temp dir.
func (h *apiHandler) cloneTempDir() string {
	dir := filepath.Dir(strings.TrimSpace(h.config.Database.Path))
	if dir == "" || dir == "." {
		return os.TempDir()
	}
	return dir
}

// handleCloneRemoteToLocal clones the remote database into the local database.
// Its behavior depends on which database is currently live:
//   - Active database is remote: the live remote is snapshotted into the local
//     SQLite file at the configured path, replacing the file atomically and
//     removing stale WAL sidecars so a later local-mode startup sees a clean,
//     consistent snapshot.
//   - Active database is local: the remote is configured but not live, so its
//     data is pulled down and MERGED into the live local database (the file
//     cannot be replaced under an open connection).
//
// It is available whenever a remote database is configured (URL + auth token).
func (h *apiHandler) handleCloneRemoteToLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	if !h.remoteConfigured() {
		writeWebErr(w, errCloneRequiresRemote)
		return
	}

	includeConfig := r.URL.Query().Get("include_config") == "1"

	if h.db.IsLocal() {
		h.cloneRemoteIntoActiveLocal(w, includeConfig)
		return
	}

	// Active database is remote: snapshot it into the local file.
	target := strings.TrimSpace(h.config.Database.Path)
	if target == "" {
		writeWebErr(w, errLocalDBPathNotSet)
		return
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0700); err != nil {
		writeWebErrf(w, errLocalDBPathNotWritable, "%s", writeFileError(dir, err))
		return
	}
	// Probe writability with a throwaway file so we fail early with a clear
	// error instead of part-way through the export.
	probe, err := os.CreateTemp(dir, "moderation_clone_probe_*")
	if err != nil {
		writeWebErrf(w, errLocalDBPathNotWritable, "%s", writeFileError(dir, err))
		return
	}
	probePath := probe.Name()
	probe.Close()
	os.Remove(probePath)

	tmpPath, err := h.db.ExportToLocalFile(dir, includeConfig)
	if err != nil {
		writeWebErrf(w, errExportDBFailed, "failed to export database: %v", err)
		return
	}

	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		writeWebErrf(w, errExportDBFailed, "failed to write database file: %v", err)
		return
	}
	// Drop any WAL sidecars left by a previous local database at this path; the
	// freshly exported file is a self-contained snapshot.
	os.Remove(target + "-wal")
	os.Remove(target + "-shm")

	log.Printf("WebUI: remote database cloned to local file %s (includeConfig=%v)", target, includeConfig)
	jsonResponse(w, map[string]string{"status": "ok", "message": fmt.Sprintf("Remote database cloned to local file %s.", target)})
}

// cloneRemoteIntoActiveLocal pulls data from the configured (but not live)
// remote database and merges it into the live local database. It runs the same
// post-import steps as a DB upload because the active database changed.
func (h *apiHandler) cloneRemoteIntoActiveLocal(w http.ResponseWriter, includeConfig bool) {
	remote, err := database.OpenRemote(h.remoteCloneConfig())
	if err != nil {
		writeWebErrf(w, errCloneRemoteConnectFailed, "%v", err)
		return
	}
	defer remote.Close()

	tmpPath, err := remote.ExportToLocalFile(h.cloneTempDir(), includeConfig)
	if err != nil {
		writeWebErrf(w, errExportDBFailed, "failed to export remote database: %v", err)
		return
	}
	defer os.Remove(tmpPath)

	if err := h.db.ImportFromLocalFile(tmpPath, includeConfig); err != nil {
		writeWebErrf(w, errImportDBFailed, "failed to import database: %v", err)
		return
	}
	if includeConfig {
		if _, err := h.hashStoredWebUIPassword(); err != nil {
			writeWebErrf(w, errImportDBFailed, "failed to secure imported config: %v", err)
			return
		}
		if h.configFromDB {
			if err := h.reloadConfigFromDB(); err != nil {
				writeWebErrf(w, errImportDBFailed, "failed to reload imported config: %v", err)
				return
			}
		}
	}

	h.db.InvalidateMuteCache()
	log.Printf("WebUI: configured remote database cloned into live local database (includeConfig=%v)", includeConfig)
	jsonResponse(w, map[string]string{"status": "ok", "message": "Remote database cloned into the live local database."})
}

// handleCloneLocalToRemote clones the local database into the remote database.
// Its behavior depends on which database is currently live:
//   - Active database is remote: the local SQLite file at the configured path
//     is merged into the live remote database.
//   - Active database is local: the live local database is exported and merged
//     into the configured (but not live) remote database via a secondary
//     connection.
//
// In both cases the merge strategy is the same per-table logic used by the DB
// upload endpoint. It is available whenever a remote database is configured.
func (h *apiHandler) handleCloneLocalToRemote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	if !h.remoteConfigured() {
		writeWebErr(w, errCloneRequiresRemote)
		return
	}

	includeConfig := r.URL.Query().Get("include_config") == "1"

	if h.db.IsLocal() {
		h.cloneActiveLocalIntoRemote(w, includeConfig)
		return
	}

	// Active database is remote: import the local file into the live remote.
	source := strings.TrimSpace(h.config.Database.Path)
	if source == "" {
		writeWebErr(w, errLocalDBPathNotSet)
		return
	}
	info, err := os.Stat(source)
	if err != nil || info.IsDir() {
		writeWebErr(w, errLocalDBFileMissing)
		return
	}

	if err := h.db.ImportFromLocalFile(source, includeConfig); err != nil {
		writeWebErrf(w, errImportDBFailed, "failed to import database: %v", err)
		return
	}
	if includeConfig {
		if _, err := h.hashStoredWebUIPassword(); err != nil {
			writeWebErrf(w, errImportDBFailed, "failed to secure imported config: %v", err)
			return
		}
		if h.configFromDB {
			if err := h.reloadConfigFromDB(); err != nil {
				writeWebErrf(w, errImportDBFailed, "failed to reload imported config: %v", err)
				return
			}
		}
	}

	h.db.InvalidateMuteCache()
	log.Printf("WebUI: local database file %s cloned into remote database (includeConfig=%v)", source, includeConfig)
	jsonResponse(w, map[string]string{"status": "ok", "message": "Local database file cloned into remote database."})
}

// cloneActiveLocalIntoRemote exports the live local database and merges it into
// the configured (but not live) remote database. The active local database is
// unchanged, so no mute-cache invalidation or config reload is required here.
func (h *apiHandler) cloneActiveLocalIntoRemote(w http.ResponseWriter, includeConfig bool) {
	tmpPath, err := h.db.ExportToLocalFile(h.cloneTempDir(), includeConfig)
	if err != nil {
		writeWebErrf(w, errExportDBFailed, "failed to export local database: %v", err)
		return
	}
	defer os.Remove(tmpPath)

	remote, err := database.OpenRemote(h.remoteCloneConfig())
	if err != nil {
		writeWebErrf(w, errCloneRemoteConnectFailed, "%v", err)
		return
	}
	defer remote.Close()

	if err := remote.ImportFromLocalFile(tmpPath, includeConfig); err != nil {
		writeWebErrf(w, errImportDBFailed, "failed to import database into remote: %v", err)
		return
	}
	if includeConfig {
		// Upgrade any plaintext web UI password copied into the remote so the
		// destination never stores a credential in the clear.
		if _, err := h.hashStoredWebUIPasswordOn(remote); err != nil {
			writeWebErrf(w, errImportDBFailed, "failed to secure imported config: %v", err)
			return
		}
	}

	log.Printf("WebUI: live local database cloned into configured remote database (includeConfig=%v)", includeConfig)
	jsonResponse(w, map[string]string{"status": "ok", "message": "Local database cloned into the configured remote database."})
}

func (h *apiHandler) hashStoredWebUIPassword() (bool, error) {
	return h.hashStoredWebUIPasswordOn(h.db)
}

func (h *apiHandler) hashStoredWebUIPasswordOn(db *database.DB) (bool, error) {
	password, err := db.GetConfigValue("web_ui.password")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if password == "" || config.IsHashedWebUIPassword(password) {
		return false, nil
	}
	hashed, err := config.HashWebUIPasswordForStorage(password)
	if err != nil {
		return false, err
	}
	return true, db.SetConfigValue("web_ui.password", hashed)
}

func (h *apiHandler) reloadConfigFromDB() error {
	values, err := h.db.GetAllConfigValues()
	if err != nil {
		return err
	}
	cfg, err := config.LoadFromStringMap(values)
	if err != nil {
		return err
	}
	h.config.ReplaceContents(cfg)
	return nil
}

func getMaxDBUploadBytes() int64 {
	value := strings.TrimSpace(os.Getenv(maxDBUploadBytesEnv))
	if value == "" {
		return defaultMaxDBUploadBytes
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		log.Printf("WebUI: ignoring invalid %s=%q; using default %d bytes", maxDBUploadBytesEnv, value, defaultMaxDBUploadBytes)
		return defaultMaxDBUploadBytes
	}
	return parsed
}
