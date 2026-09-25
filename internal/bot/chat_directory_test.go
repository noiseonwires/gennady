// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChatNameWithoutTelegram(t *testing.T) {
	cfg := &config.Config{}
	cfg.WebUI.Enabled = true

	b, err := New(cfg, nil)
	require.NoError(t, err)
	assert.Nil(t, b.tg)
	assert.Equal(t, "Chat -100123", b.GetChatName(-100123))
}
