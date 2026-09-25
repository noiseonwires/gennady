// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
)

func TestWebReputationPolicyCanExcludeAIProfile(t *testing.T) {
	policy := webReputationPolicy(config.ReputationConfig{})
	assert.InDelta(t, -3, policy.Weights["ai_very_bad"], 0.001)

	disabled := false
	policy = webReputationPolicy(config.ReputationConfig{AIProfileAffectsScore: &disabled})
	assert.InDelta(t, 0, policy.Weights["ai_very_bad"], 0.001)
	assert.InDelta(t, 0, policy.Weights["ai_good"], 0.001)
	assert.InDelta(t, -5, policy.Weights["warning"], 0.001)
}
