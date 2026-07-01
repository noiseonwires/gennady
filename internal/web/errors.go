// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
)

// Server-emitted API errors.
//
// Every error sent over the wire goes through one of the helpers below
// (writeWebErr / writeWebErrf / writeWebErrFromErr) so the response shape is
// uniform:
//
//	{"error_code": "api.method_not_allowed", "error": "method not allowed"}
//
// - `error_code` is a stable machine key. The SPA looks it up in
//   data/i18n_en.json / data/i18n_ru.json to render a localized message.
// - `error` is the English fallback. Non-localized clients (curl, scripts,
//   older frontend versions) read this directly.
//
// Adding a new error: declare it in the registry below with a unique code,
// then call writeWebErr(w, errFoo) or writeWebErrf(w, errFoo, "%v", err) from
// the handler. Don't forget to add the corresponding entries to the i18n JSON
// files so the frontend can translate.

// webError is the value passed to writeWebErr. It also satisfies the standard
// `error` interface so it can be returned from internal helpers (e.g.
// AuthManager) and unwrapped at the handler boundary by writeWebErrFromErr.
type webError struct {
	code   string // stable machine key, e.g. "api.method_not_allowed"
	msg    string // English fallback shown when no client-side translation is available
	status int    // HTTP status code
	detail string // optional formatted detail set by Format(); preserves code/status
}

// Error implements the error interface.
func (e webError) Error() string {
	if e.detail != "" {
		return e.detail
	}
	return e.msg
}

// Format returns a copy of e with msg replaced by the formatted string.
// The stable code and status are preserved so the SPA can still translate.
//
// Useful when the error category is well-defined but you want to include
// dynamic context in the English fallback (e.g. "rule #3: trigger is required").
func (e webError) Format(format string, args ...any) webError {
	out := e
	out.detail = fmt.Sprintf(format, args...)
	return out
}

// ── Error registry ────────────────────────────────────────────────────────

// Generic / cross-cutting
var (
	errMethodNotAllowed   = webError{code: "api.method_not_allowed", msg: "method not allowed", status: http.StatusMethodNotAllowed}
	errInvalidRequestBody = webError{code: "api.invalid_request_body", msg: "invalid request body", status: http.StatusBadRequest}
	errInvalidJSONBody    = webError{code: "api.invalid_json_body", msg: "invalid JSON body", status: http.StatusBadRequest}
	errFailedReadBody     = webError{code: "api.failed_read_body", msg: "failed to read request body", status: http.StatusBadRequest}
	errUnauthorized       = webError{code: "api.unauthorized", msg: "unauthorized", status: http.StatusUnauthorized}
	errInternal           = webError{code: "api.internal_error", msg: "internal server error", status: http.StatusInternalServerError}
)

// Auth (typed because they propagate as `error` from AuthManager).
// Their msg is filled in by Format() at the call site so the count of
// remaining attempts / minutes can be embedded.
var (
	errAuthLockedOut              = webError{code: "auth.locked_out", msg: "too many failed attempts", status: http.StatusUnauthorized}
	errAuthInvalidCredentials     = webError{code: "auth.invalid_credentials", msg: "invalid credentials", status: http.StatusUnauthorized}
	errAuthPasswordFirst          = webError{code: "auth.password_first", msg: "password verification required first", status: http.StatusUnauthorized}
	errAuthNoMethodConfigured     = webError{code: "auth.no_method_configured", msg: "no authentication method configured", status: http.StatusForbidden}
	errAuthPasswordOrCodeRequired = webError{code: "auth.password_or_code_required", msg: "password or code required", status: http.StatusBadRequest}
	errAuthOTPSendFailed          = webError{code: "auth.otp_send_failed", msg: "failed to send verification code", status: http.StatusInternalServerError}
	errAuthTokenAndCodeRequired   = webError{code: "auth.token_and_code_required", msg: "login token and code required", status: http.StatusBadRequest}
)

// Config save / load / validate
var (
	errNoFieldsToUpdate      = webError{code: "config.no_fields_to_update", msg: "no fields to update", status: http.StatusBadRequest}
	errConfigValidation      = webError{code: "config.validation_failed", msg: "validation failed", status: http.StatusBadRequest}
	errConfigSaveFailed      = webError{code: "config.save_failed", msg: "failed to save config", status: http.StatusInternalServerError}
	errConfigReadFileFailed  = webError{code: "config.read_file_failed", msg: "failed to read config file", status: http.StatusInternalServerError}
	errConfigMarshalFailed   = webError{code: "config.marshal_failed", msg: "failed to marshal config", status: http.StatusInternalServerError}
	errConfigYAMLInvalid     = webError{code: "config.yaml_invalid", msg: "invalid YAML", status: http.StatusBadRequest}
	errConfigSaveToDBFailed  = webError{code: "config.save_to_db_failed", msg: "failed to save config to database", status: http.StatusInternalServerError}
	errConfigCopyToDBFailed  = webError{code: "config.copy_to_db_failed", msg: "failed to copy config to database", status: http.StatusInternalServerError}
	errConfigSourceAlreadyDB = webError{code: "config.source_already_db", msg: "config source is already database", status: http.StatusBadRequest}

	errRuleTriggerRequired   = webError{code: "rules.trigger_required", msg: "rule: trigger is required", status: http.StatusBadRequest}
	errRuleInvalidAction     = webError{code: "rules.invalid_action", msg: "rule: invalid action", status: http.StatusBadRequest}
	errModelEndpointRequired = webError{code: "models.endpoint_required", msg: "model: endpoint and deployment_name are required", status: http.StatusBadRequest}

	errTopicChatRequired  = webError{code: "topics.chat_required", msg: "topic: chat is required", status: http.StatusBadRequest}
	errTopicInvalidThread = webError{code: "topics.invalid_thread", msg: "topic: topic id must be a positive forum thread id", status: http.StatusBadRequest}
	errTopicNameRequired  = webError{code: "topics.name_required", msg: "topic: name is required", status: http.StatusBadRequest}
)

// Data / DB
var (
	errGetActionsFailed    = webError{code: "data.get_actions_failed", msg: "failed to get actions", status: http.StatusInternalServerError}
	errGetMutedFailed      = webError{code: "data.get_muted_failed", msg: "failed to get muted users", status: http.StatusInternalServerError}
	errGetMessagesFailed   = webError{code: "data.get_messages_failed", msg: "failed to get messages", status: http.StatusInternalServerError}
	errGetTableStatsFailed = webError{code: "data.get_table_stats_failed", msg: "failed to get table stats", status: http.StatusInternalServerError}
	errInvalidMsgOrChatID  = webError{code: "data.invalid_msg_or_chat_id", msg: "invalid message_id or chat_id", status: http.StatusBadRequest}
	errMessageIDRequired   = webError{code: "data.message_id_required", msg: "message_id is required", status: http.StatusBadRequest}
	errDeleteMessageFailed = webError{code: "data.delete_message_failed", msg: "failed to delete message", status: http.StatusInternalServerError}
	errGetProfilesFailed   = webError{code: "data.get_profiles_failed", msg: "failed to get profiles", status: http.StatusInternalServerError}
	errUserIDRequired      = webError{code: "data.user_id_required", msg: "user_id is required", status: http.StatusBadRequest}
	errDeleteProfileFailed = webError{code: "data.delete_profile_failed", msg: "failed to delete profile", status: http.StatusInternalServerError}
)

// Moderation
var (
	errUserAndChatIDRequired        = webError{code: "mod.user_and_chat_id_required", msg: "user_id and chat_id are required", status: http.StatusBadRequest}
	errModerationBackendUnavailable = webError{code: "mod.backend_unavailable", msg: "moderation backend not available", status: http.StatusServiceUnavailable}
	errModerationActionFailed       = webError{code: "mod.action_failed", msg: "moderation action failed", status: http.StatusInternalServerError}
)

// Diagnostics
var (
	errBotTokenNotConfigured  = webError{code: "diag.bot_token_not_configured", msg: "bot token not configured", status: http.StatusServiceUnavailable}
	errFailedBuildRequest     = webError{code: "diag.failed_build_request", msg: "failed to build request", status: http.StatusInternalServerError}
	errTelegramAPIUnreachable = webError{code: "diag.telegram_api_unreachable", msg: "failed to reach Telegram API", status: http.StatusBadGateway}
	errMissingServiceName     = webError{code: "diag.missing_service_name", msg: "missing service name", status: http.StatusBadRequest}
	errAPITesterUnavailable   = webError{code: "diag.api_tester_unavailable", msg: "API tester not available", status: http.StatusServiceUnavailable}
	errMessageContentRequired = webError{code: "diag.message_required", msg: "message is required", status: http.StatusBadRequest}
	errURLRequired            = webError{code: "diag.url_required", msg: "url is required", status: http.StatusBadRequest}
	errImageRequired          = webError{code: "diag.image_required", msg: "image is required", status: http.StatusBadRequest}
	errInvalidImage           = webError{code: "diag.invalid_image", msg: "invalid image data", status: http.StatusBadRequest}
)

// File management (config upload/download, DB upload/download, env)
var (
	errReadDBFileFailed     = webError{code: "files.read_db_failed", msg: "failed to read database file", status: http.StatusInternalServerError}
	errExportDBFailed       = webError{code: "files.export_db_failed", msg: "failed to export database", status: http.StatusInternalServerError}
	errReadExportedDBFailed = webError{code: "files.read_exported_db_failed", msg: "failed to read exported database", status: http.StatusInternalServerError}
	errInvalidSQLiteFile    = webError{code: "files.invalid_sqlite_file", msg: "uploaded file is not a valid SQLite database", status: http.StatusBadRequest}
	errUploadedDBTooLarge   = webError{code: "files.uploaded_db_too_large", msg: "uploaded database is too large", status: http.StatusRequestEntityTooLarge}
	errCreateTempDirFailed  = webError{code: "files.create_temp_dir_failed", msg: "failed to create temp directory", status: http.StatusInternalServerError}
	errCreateTempFileFailed = webError{code: "files.create_temp_file_failed", msg: "failed to create temp file", status: http.StatusInternalServerError}
	errWriteTempFileFailed  = webError{code: "files.write_temp_file_failed", msg: "failed to write temp file", status: http.StatusInternalServerError}
	errImportDBFailed       = webError{code: "files.import_db_failed", msg: "failed to import database", status: http.StatusInternalServerError}

	errCloneRequiresRemote      = webError{code: "files.clone_requires_remote", msg: "clone is only available when a remote database is configured", status: http.StatusBadRequest}
	errCloneRemoteConnectFailed = webError{code: "files.clone_remote_connect_failed", msg: "failed to connect to the configured remote database", status: http.StatusBadGateway}
	errLocalDBPathNotSet        = webError{code: "files.local_db_path_not_set", msg: "local database path is not configured", status: http.StatusBadRequest}
	errLocalDBPathNotWritable   = webError{code: "files.local_db_path_not_writable", msg: "local database path is not writable", status: http.StatusInternalServerError}
	errLocalDBFileMissing       = webError{code: "files.local_db_file_missing", msg: "local database file not found", status: http.StatusBadRequest}
)

// System
var (
	errRestartUnavailable = webError{code: "system.restart_unavailable", msg: "restart not available", status: http.StatusServiceUnavailable}
)

// ── Helpers ──────────────────────────────────────────────────────────────

// writeWebErr writes a uniform JSON error response with the structured
// error_code + English fallback. The SPA translates error_code via i18n;
// non-localized clients read `error` directly.
func writeWebErr(w http.ResponseWriter, e webError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	msg := e.msg
	if e.detail != "" {
		msg = e.detail
	}
	body := map[string]string{
		"error":      msg,
		"error_code": e.code,
	}
	// Expose the dynamic detail as its own field so the SPA can render the
	// localized headline (from error_code) AND the specifics (e.g. which
	// config field failed validation). `error` stays as the full English
	// fallback for non-localized clients (curl/scripts).
	if e.detail != "" {
		body["detail"] = e.detail
	}
	_ = json.NewEncoder(w).Encode(body)
}

// writeWebErrf is sugar for writeWebErr(w, e.Format(format, args...)).
// Use when the error category (and its i18n code) is fixed but the human
// message benefits from dynamic context.
func writeWebErrf(w http.ResponseWriter, e webError, format string, args ...any) {
	writeWebErr(w, e.Format(format, args...))
}

// writeWebErrFromErr unwraps a webError from err and forwards it; falls back
// to errInternal when err is not a webError. Use at handler boundaries that
// call internal helpers returning `error` (e.g. AuthManager methods).
func writeWebErrFromErr(w http.ResponseWriter, err error) {
	var we webError
	if errors.As(err, &we) {
		writeWebErr(w, we)
		return
	}
	writeWebErr(w, errInternal.Format("%v", err))
}

// writeWebErrLogged logs the underlying error for server-side diagnosis,
// then writes the public-facing structured response. Use when err carries
// info that shouldn't leak to the client but is useful in logs.
func writeWebErrLogged(w http.ResponseWriter, e webError, err error) {
	if err != nil {
		log.Printf("API %s (%d): %v", e.code, e.status, err)
	}
	writeWebErr(w, e)
}
