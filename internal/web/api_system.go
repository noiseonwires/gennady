// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// System-level endpoints: process restart and live in-memory log buffer.

func (h *apiHandler) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	if h.restartFunc == nil {
		writeWebErr(w, errRestartUnavailable)
		return
	}

	var req struct {
		Mode string `json:"mode"`
	}
	// Body is optional; ignore parse errors and default to soft restart.
	json.NewDecoder(r.Body).Decode(&req)
	mode := req.Mode
	if mode != "hard" {
		mode = "soft"
	}

	log.Printf("WebUI: %s restart requested by admin", mode)
	jsonResponse(w, map[string]string{"status": "ok", "message": "Bot is restarting..."})
	// Flush the response, then trigger restart asynchronously
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		h.restartFunc(mode)
	}()
}

func (h *apiHandler) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	if h.logBuffer == nil {
		jsonResponse(w, []LogEntry{})
		return
	}
	jsonResponse(w, h.logBuffer.Lines())
}
