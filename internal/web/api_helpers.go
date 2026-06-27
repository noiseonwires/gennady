// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Generic helpers used by every handler in the package. Kept in one place so
// the wire-level conventions (Content-Type, error body shape, JSON encoding)
// stay consistent across all endpoints.

// jsonResponse writes the given value as application/json with HTTP 200.
func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// All error responses go through writeWebErr / writeWebErrf / writeWebErrFromErr
// in errors.go so they carry a stable i18n code alongside the English fallback.

// extractBearerToken parses "Authorization: Bearer xxx" and returns the token.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

const webSessionCookieName = "gennady_session"

func extractSessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(webSessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return extractBearerToken(r)
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, pathPrefix, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     webSessionCookieName,
		Value:    token,
		Path:     sessionCookiePath(pathPrefix),
		Expires:  time.Now().Add(sessionExpiry),
		MaxAge:   int(sessionExpiry.Seconds()),
		HttpOnly: true,
		Secure:   isSecureCookieRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request, pathPrefix string) {
	http.SetCookie(w, &http.Cookie{
		Name:     webSessionCookieName,
		Value:    "",
		Path:     sessionCookiePath(pathPrefix),
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecureCookieRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func sessionCookiePath(pathPrefix string) string {
	pathPrefix = strings.TrimSpace(pathPrefix)
	if pathPrefix == "" {
		return "/"
	}
	if !strings.HasPrefix(pathPrefix, "/") {
		pathPrefix = "/" + pathPrefix
	}
	pathPrefix = strings.TrimRight(pathPrefix, "/")
	if pathPrefix == "" {
		return "/"
	}
	return pathPrefix
}

func isSecureCookieRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !isTrustedForwardedHeaderProxy(net.ParseIP(host)) {
		return false
	}
	for _, proto := range strings.Split(r.Header.Get("X-Forwarded-Proto"), ",") {
		if strings.EqualFold(strings.TrimSpace(proto), "https") {
			return true
		}
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on")
}

// extractIP returns the client IP. Forwarded headers are honored only when the
// direct peer looks like a local/private reverse proxy.
func extractIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	remoteIP := net.ParseIP(host)
	if isTrustedForwardedHeaderProxy(remoteIP) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if ip := parseForwardedHeaderIP(strings.Split(xff, ",")[0]); ip != "" {
				return ip
			}
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			if ip := parseForwardedHeaderIP(xri); ip != "" {
				return ip
			}
		}
	}
	return host
}

func parseForwardedHeaderIP(value string) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func isTrustedForwardedHeaderProxy(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// addValidationError appends a "<field>: <msg>" entry to errs (no-op if nil).
func addValidationError(errs *[]string, field, msg string) {
	if errs != nil {
		*errs = append(*errs, fmt.Sprintf("%s: %s", field, msg))
	}
}

// writeFileError returns a user-friendly error message for file write failures.
func writeFileError(filePath string, err error) string {
	if os.IsPermission(err) {
		return fmt.Sprintf("Cannot write to %s: permission denied. The file or directory may be read-only. You can still download the config to save it manually.", filePath)
	}
	if os.IsNotExist(err) {
		return fmt.Sprintf("Cannot write to %s: directory does not exist.", filePath)
	}
	return fmt.Sprintf("Failed to save %s: %v. You can use the download button to save your changes manually.", filePath, err)
}
