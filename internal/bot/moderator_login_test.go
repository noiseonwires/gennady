// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEffectivePublicURL(t *testing.T) {
	b := newTestBot()

	// Neither public_url nor webhook.url set → no public URL.
	assert.Equal(t, "", b.effectivePublicURL())

	// Falls back to the scheme+host of webhook.url (path stripped).
	b.config.Webhook.URL = "https://bot.example.com/webhook/abc"
	assert.Equal(t, "https://bot.example.com", b.effectivePublicURL())

	// Explicit public_url takes precedence; trailing slash is trimmed.
	b.config.WebUI.PublicURL = "https://panel.example.com/"
	assert.Equal(t, "https://panel.example.com", b.effectivePublicURL())
}

func TestModeratorWebLoginAvailable(t *testing.T) {
	b := newTestBot()
	b.config.WebUI.PathPrefix = "/admin"
	b.config.WebUI.ModeratorPathPrefix = "/mod"

	// No login generator wired → unavailable.
	assert.False(t, b.moderatorWebLoginAvailable())

	b.generateModeratorLogin = func(int64) (string, string) { return "tok", "otp" }
	// Generator present but no resolvable public URL → unavailable.
	assert.False(t, b.moderatorWebLoginAvailable())

	// Webhook URL supplies the public host → available.
	b.config.Webhook.URL = "https://bot.example.com/webhook"
	assert.True(t, b.moderatorWebLoginAvailable())

	// Explicit public_url also makes it available.
	b.config.Webhook.URL = ""
	b.config.WebUI.PublicURL = "https://bot.example.com"
	assert.True(t, b.moderatorWebLoginAvailable())

	// Moderator prefix colliding with the super-admin prefix disables it.
	b.config.WebUI.ModeratorPathPrefix = "/admin"
	assert.False(t, b.moderatorWebLoginAvailable())
}
