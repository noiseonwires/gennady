// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"log"
	"net/http"
	"strings"
)

// Web UI authentication endpoints: login (password and/or OTP), logout, auth
// status checks and the unauthenticated i18n / version / auth-mode bootstrap.

func (h *apiHandler) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	configSource := "file"
	if h.configFromDB {
		configSource = "db"
	}
	jsonResponse(w, map[string]string{
		"version":       h.version,
		"git_commit":    h.gitCommit,
		"build_time":    h.buildTime,
		"url":           h.botURL,
		"config_source": configSource,
		"bot_name":      h.botName,
		"author":        h.botAuthor,
		"license":       h.botLicense,
	})
}

func (h *apiHandler) handleAuthMode(w http.ResponseWriter, r *http.Request) {
	passwordRequired := h.config.WebUI.Password != ""
	otpAvailable := h.config.WebUI.IsOTPEnabled() && h.config.Admin.SuperAdminUserID != 0 && h.sendOTP != nil
	jsonResponse(w, map[string]interface{}{
		"password_required": passwordRequired,
		"otp_available":     otpAvailable,
	})
}

func (h *apiHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	req, err := decodeJSONLimited[struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}](r, 4<<10) // 4 KB is ample for a password + OTP code
	if err != nil {
		respondDecodeError(w, err)
		return
	}

	ip := extractIP(r)

	// Step 1: Password check (when password is provided)
	if req.Password != "" {
		expected := h.config.WebUI.Password
		otpAvailable := h.config.WebUI.IsOTPEnabled() && h.config.Admin.SuperAdminUserID != 0 && h.sendOTP != nil

		if expected == "" {
			// No password configured - fall through to OTP-only if available
			if otpAvailable {
				h.sendLoginOTP(w)
				return
			}
			writeWebErr(w, errAuthNoMethodConfigured)
			return
		}

		if err := h.auth.ValidatePassword(req.Password, expected, ip); err != nil {
			// If remote DB is configured, re-read password in case it was changed there
			if h.db.IsRemote() {
				if freshPw, dbErr := h.db.GetConfigValue("web_ui.password"); dbErr == nil && freshPw != "" && freshPw != expected {
					h.config.WebUI.Password = freshPw
					if retryErr := h.auth.ValidatePassword(req.Password, freshPw, ip); retryErr == nil {
						goto passwordOK
					}
				}
			}
			writeWebErrFromErr(w, err)
			return
		}
	passwordOK:

		if !otpAvailable {
			// No OTP needed - issue session directly
			token := h.auth.CreatePasswordSession()
			h.setLoginSession(w, r, token)
			return
		}

		h.sendLoginOTP(w)
		return
	}

	// Step 2: OTP verification
	if req.Code != "" {
		requirePassword := h.config.WebUI.Password != ""
		token, err := h.auth.ValidateOTP(strings.TrimSpace(req.Code), ip, requirePassword)
		if err != nil {
			writeWebErrFromErr(w, err)
			return
		}

		h.setLoginSession(w, r, token)
		return
	}

	writeWebErr(w, errAuthPasswordOrCodeRequired)
}

func (h *apiHandler) setLoginSession(w http.ResponseWriter, r *http.Request, token string) {
	setSessionCookie(w, r, h.pathPrefix, token)
	jsonResponse(w, map[string]interface{}{"authenticated": true})
}

// sendLoginOTP generates an OTP, sends it to the super-admin, and responds.
func (h *apiHandler) sendLoginOTP(w http.ResponseWriter) {
	otp := h.auth.GenerateOTP()
	if err := h.sendOTP(otp); err != nil {
		log.Printf("Error sending OTP to super-admin: %v", err)
		writeWebErr(w, errAuthOTPSendFailed)
		return
	}
	jsonResponse(w, map[string]interface{}{"otp_required": true})
}

func (h *apiHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := extractSessionToken(r)
	if token != "" {
		h.auth.Logout(token)
	}
	clearSessionCookie(w, r, h.pathPrefix)
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *apiHandler) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]bool{"authenticated": true})
}

func (h *apiHandler) handleGetI18n(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, GetI18n())
}
