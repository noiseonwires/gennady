// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidatePublicURL checks that web_ui.public_url is only a scheme and host.
// UI paths belong in path_prefix and moderator_path_prefix.
func (w WebUIConfig) ValidatePublicURL() error {
	raw := strings.TrimSpace(w.PublicURL)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("web_ui.public_url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("web_ui.public_url must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	if strings.Trim(u.EscapedPath(), "/") != "" {
		return fmt.Errorf("web_ui.public_url must contain only scheme and host (e.g. https://bot.example.com); put URL paths in web_ui.path_prefix and web_ui.moderator_path_prefix")
	}
	return nil
}

// PathPrefixes returns the absolute URL paths of the admin and moderator UIs.
func (w WebUIConfig) PathPrefixes() (admin, moderator string) {
	admin = strings.TrimSpace(w.PathPrefix)
	moderator = strings.TrimSpace(w.ModeratorPathPrefix)
	if admin == "" {
		admin = "/admin"
	}
	if moderator == "" {
		moderator = "/mod"
	}
	return strings.TrimRight(admin, "/"), strings.TrimRight(moderator, "/")
}
