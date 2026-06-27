// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMuteCallbackData(t *testing.T) {
	// Non-ask option uses the normal prefix.
	opt := muteDurationOption{Minutes: 15}
	got := muteCallbackData(opt, "mute", "muteask", -100, 22, 777, 3)
	assert.Equal(t, "mute_15_-100_22_777_3", got)

	// Ask option uses the ask prefix.
	askOpt := muteDurationOption{Minutes: 0, Ask: true}
	got = muteCallbackData(askOpt, "mute", "muteask", -100, 22, 777, 3)
	assert.Equal(t, "muteask_0_-100_22_777_3", got)
}

func TestMuteButton(t *testing.T) {
	opt := muteDurationOption{Minutes: 60, LabelKey: "btn.mute_1h"}
	btn := muteButton(opt, "mute", "muteask", -100, 22, 777, 3)
	assert.Equal(t, "mute_60_-100_22_777_3", btn.CallbackData)
	assert.NotEmpty(t, btn.Text)
}

func TestRegularMuteRows(t *testing.T) {
	rows := regularMuteRows(-100, 22, 777, 3)
	// Fixed 4-row layout (2 + 2 + 1 + 2 buttons).
	require.Len(t, rows, 4)
	assert.Len(t, rows[0], 2)
	assert.Len(t, rows[1], 2)
	assert.Len(t, rows[2], 1)
	assert.Len(t, rows[3], 2)

	// Every button carries well-formed callback data with the right tail IDs.
	for _, row := range rows {
		for _, btn := range row {
			assert.True(t, strings.HasSuffix(btn.CallbackData, "_-100_22_777_3"))
		}
	}
}

func TestCallbackRegistryWiring(t *testing.T) {
	// Every expected action prefix must be registered and non-nil.
	for _, prefix := range []string{
		"warn", "delwarn", "mute", "muteask", "muteconfirm", "mutecancel",
		"unmute", "cmute", "cmuteask", "cmuteconfirm", "cmutecancel",
		"delete", "restore", "notbad", "delmsg", "admin",
	} {
		h, ok := callbackRegistry[prefix]
		assert.True(t, ok, "missing handler for %q", prefix)
		assert.NotNil(t, h, "nil handler for %q", prefix)
	}
}
