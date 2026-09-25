// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReputationDefaultsAndOptOut(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.NoError(t, err)
	require.NotNil(t, cfg.Reputation.Enabled)
	assert.True(t, *cfg.Reputation.Enabled)
	require.NotNil(t, cfg.Reputation.AIProfileAffectsScore)
	assert.True(t, *cfg.Reputation.AIProfileAffectsScore)
	assert.InDelta(t, -5, *cfg.Reputation.Weights.Warning, 0.001)
	assert.InDelta(t, -2, *cfg.Reputation.Weights.ProfileScreeningText, 0.001)
	assert.InDelta(t, -3, *cfg.Reputation.Weights.ProfileScreeningPhoto, 0.001)
	assert.InDelta(t, -5, *cfg.Reputation.Limits.ProfileScreeningMin, 0.001)
	assert.NotEmpty(t, cfg.Reputation.PositiveReactionEmojis)

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("reputation:\n  enabled: false\n  ai_profile_affects_score: false\n  weights:\n    warning: 0\n"), 0o600))
	cfg, err = Load(path)
	require.NoError(t, err)
	assert.False(t, *cfg.Reputation.Enabled)
	assert.False(t, *cfg.Reputation.AIProfileAffectsScore)
	assert.InDelta(t, 0, *cfg.Reputation.Weights.Warning, 0.001)
}
