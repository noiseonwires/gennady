// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebUIPublicURLAcceptsOrigins(t *testing.T) {
	for _, raw := range []string{"", "https://example.com", "https://example.com/", " http://localhost:8080/ "} {
		t.Run(raw, func(t *testing.T) {
			cfg := &Config{WebUI: WebUIConfig{PublicURL: raw}}
			require.NoError(t, cfg.WebUI.ValidatePublicURL())
			require.NoError(t, cfg.Validate())
		})
	}
}

func TestWebUIPublicURLRejectsInvalidURLs(t *testing.T) {
	for _, raw := range []string{
		"/admin2", "example.com", "ftp://example.com",
		"https://user:pass@example.com", "https://example.com?x=1",
		"https://example.com/?", "https://example.com/#fragment",
		"https://example.com#", "https://example.com/admin2",
		"https://example.com/admin2/", "https://example.com/%2F",
	} {
		t.Run(raw, func(t *testing.T) {
			cfg := &Config{WebUI: WebUIConfig{PublicURL: raw}}
			require.ErrorContains(t, cfg.Validate(), "web_ui.public_url")
		})
	}
}

func TestWebUIPathPrefixes(t *testing.T) {
	admin, mod := (WebUIConfig{}).PathPrefixes()
	assert.Equal(t, "/admin", admin)
	assert.Equal(t, "/mod", mod)
	admin, mod = (WebUIConfig{PathPrefix: " /panel/ ", ModeratorPathPrefix: "/panel/modentry/"}).PathPrefixes()
	assert.Equal(t, "/panel", admin)
	assert.Equal(t, "/panel/modentry", mod)
	admin, mod = (WebUIConfig{PathPrefix: "/", ModeratorPathPrefix: "/"}).PathPrefixes()
	assert.Empty(t, admin)
	assert.Empty(t, mod)
}
