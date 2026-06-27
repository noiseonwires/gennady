// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"log"
	"net/http"

	"gennadium/internal/config"
)

// handleGetChatRulesOverrides returns the per-chat rules-text overrides as a
// JSON array of {chat, rules} objects.
//
// GET /api/config/chat-rules-overrides → {"overrides": [...]}
func (h *apiHandler) handleGetChatRulesOverrides(w http.ResponseWriter, r *http.Request) {
	overrides := h.config.AI.ChatRulesOverrides
	if overrides == nil {
		overrides = []config.ChatRulesOverride{}
	}
	jsonResponse(w, map[string]interface{}{
		"overrides": overrides,
	})
}

// handleSaveChatRulesOverrides replaces the per-chat rules overrides. Validates
// that every entry has a non-zero chat id and that no chat is listed twice.
//
// PUT /api/config/chat-rules-overrides
// Body: {"overrides": [{chat, rules}, ...]}
func (h *apiHandler) handleSaveChatRulesOverrides(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	req, err := decodeJSON[struct {
		Overrides []config.ChatRulesOverride `json:"overrides"`
	}](r)
	if err != nil {
		respondDecodeError(w, err)
		return
	}

	seen := make(map[int64]bool, len(req.Overrides))
	cleaned := make([]config.ChatRulesOverride, 0, len(req.Overrides))
	for i, ovr := range req.Overrides {
		if ovr.Chat == 0 {
			writeWebErrf(w, errConfigValidation, "override #%d: chat is required", i+1)
			return
		}
		if seen[ovr.Chat] {
			writeWebErrf(w, errConfigValidation, "override #%d: chat %d appears more than once", i+1, ovr.Chat)
			return
		}
		seen[ovr.Chat] = true
		cleaned = append(cleaned, config.ChatRulesOverride{Chat: ovr.Chat, Rules: ovr.Rules})
	}

	config.Lock()
	h.config.AI.ChatRulesOverrides = cleaned
	config.Unlock()

	if err := h.persistConfig(); err != nil {
		log.Printf("⚠️  Failed to save config: %v", err)
		writeWebErrf(w, errConfigSaveFailed, "failed to save config: %v", err)
		return
	}

	log.Printf("WebUI: chat rules overrides updated (%d entries)", len(cleaned))
	jsonResponse(w, map[string]string{"status": "ok"})
}
