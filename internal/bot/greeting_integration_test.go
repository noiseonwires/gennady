// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComposePlainMorningGreeting(t *testing.T) {
	b, _ := newMockBot(t)

	// All sections present.
	out := b.composePlainMorningGreeting("21°C clear", "Test Holiday", "Some event")
	assert.Contains(t, out, "🎉 Test Holiday")
	assert.Contains(t, out, "📅")
	assert.Contains(t, out, "Some event")
	assert.Contains(t, out, "🌤 21°C clear")

	// Minimal (no holiday/events/weather): still has the date line.
	out = b.composePlainMorningGreeting("", "", "")
	assert.NotEmpty(t, out)
	assert.NotContains(t, out, "🎉")
	assert.NotContains(t, out, "🌤")
}

func TestSendDailySummary_Disabled(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.AI.DailySummary.Enabled = false
	b.sendDailySummary()
	assert.Empty(t, tg.SentMessages)
}

func TestSendMorningGreeting_Disabled(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.AI.MorningGreeting.Enabled = false
	b.sendMorningGreeting()
	assert.Empty(t, tg.SentMessages)
}

func TestDailySummaryAdjustedTimestamp(t *testing.T) {
	b, _ := newMockBot(t)
	// Valid time -> a timestamp in the past relative to next run.
	b.config.AI.DailySummary.Time = "21:00"
	ts := b.dailySummaryAdjustedTimestamp()
	assert.False(t, ts.IsZero())

	// Invalid time -> fallback (now - 25h), still non-zero.
	b.config.AI.DailySummary.Time = "not-a-time"
	ts = b.dailySummaryAdjustedTimestamp()
	assert.False(t, ts.IsZero())
}
