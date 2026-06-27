// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"net/http"
)

// Web UI moderation actions: mute / cruel mute / unmute / warn.
// Delegates to the Moderator interface implemented by the bot package.

// modActionRequest is the common body shape for /api/moderation/* endpoints.
type modActionRequest struct {
	UserID    int64 `json:"user_id"`
	ChatID    int64 `json:"chat_id"`
	MessageID int   `json:"message_id,omitempty"`
	// Duration in minutes; 0 means forever (mute/cmute only).
	Duration int `json:"duration,omitempty"`
	// Cruel switches mute/unmute endpoints to the cruel variant.
	Cruel bool `json:"cruel,omitempty"`
	// Period selects the time window for the delete-messages endpoint
	// ("1h", "1d" or "all").
	Period string `json:"period,omitempty"`
}

func (h *apiHandler) decodeModRequest(w http.ResponseWriter, r *http.Request) (*modActionRequest, bool) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return nil, false
	}
	if h.moderator == nil {
		writeWebErr(w, errModerationBackendUnavailable)
		return nil, false
	}
	req, err := decodeJSON[modActionRequest](r)
	if err != nil {
		respondDecodeError(w, err)
		return nil, false
	}
	if req.UserID == 0 || req.ChatID == 0 {
		writeWebErr(w, errUserAndChatIDRequired)
		return nil, false
	}
	return &req, true
}

func (h *apiHandler) handleModerationMute(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeModRequest(w, r)
	if !ok {
		return
	}
	var err error
	if req.Cruel {
		err = h.moderator.WebCruelMuteUser(req.UserID, req.ChatID, req.MessageID, req.Duration)
	} else {
		err = h.moderator.WebMuteUser(req.UserID, req.ChatID, req.MessageID, req.Duration)
	}
	if err != nil {
		writeWebErrf(w, errModerationActionFailed, "%v", err)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *apiHandler) handleModerationUnmute(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeModRequest(w, r)
	if !ok {
		return
	}
	if err := h.moderator.WebUnmuteUser(req.UserID, req.ChatID); err != nil {
		writeWebErrf(w, errModerationActionFailed, "%v", err)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *apiHandler) handleModerationWarn(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeModRequest(w, r)
	if !ok {
		return
	}
	if req.MessageID == 0 {
		writeWebErr(w, errMessageIDRequired)
		return
	}
	if err := h.moderator.WebWarnUser(req.UserID, req.ChatID, req.MessageID); err != nil {
		writeWebErrf(w, errModerationActionFailed, "%v", err)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *apiHandler) handleModerationDeleteMessages(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeModRequest(w, r)
	if !ok {
		return
	}
	deleted, err := h.moderator.WebDeleteUserMessages(req.UserID, req.ChatID, req.Period)
	if err != nil {
		writeWebErrf(w, errModerationActionFailed, "%v", err)
		return
	}
	jsonResponse(w, map[string]int{"deleted": deleted})
}

func (h *apiHandler) handleModerationDeleteMessage(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeModRequest(w, r)
	if !ok {
		return
	}
	if req.MessageID == 0 {
		writeWebErr(w, errMessageIDRequired)
		return
	}
	if err := h.moderator.WebDeleteMessage(req.UserID, req.ChatID, req.MessageID); err != nil {
		writeWebErrf(w, errModerationActionFailed, "%v", err)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

// handleModerationRemoderate re-runs the AI moderation pipeline on an existing
// stored message. Useful when the message was missed on first pass and the
// admin has since updated the moderation prompts/rules.
func (h *apiHandler) handleModerationRemoderate(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeModRequest(w, r)
	if !ok {
		return
	}
	if req.MessageID == 0 {
		writeWebErr(w, errMessageIDRequired)
		return
	}
	if err := h.moderator.WebRemoderateMessage(req.UserID, req.ChatID, req.MessageID); err != nil {
		writeWebErrf(w, errModerationActionFailed, "%v", err)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}
