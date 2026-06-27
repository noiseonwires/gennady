// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import "net/http"

// handleListChats returns the directory of known chats so the admin UI can
// render chat-picker dropdowns next to per-chat config fields.
//
// GET /api/chats → [{id, title, is_forum, resolved, resolved_at}]
func (h *apiHandler) handleListChats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	if h.chatLister == nil {
		jsonResponse(w, []any{})
		return
	}
	jsonResponse(w, h.chatLister.ListChatsForUI())
}
