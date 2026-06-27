// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseModCallbackIDs(t *testing.T) {
	// Layout from index 1: chatID, messageID, userID, threadID.
	parts := []string{"warn", "-1001234567890", "55", "777", "3"}
	ids, ok := parseModCallbackIDs(parts, 1, "warn")
	require.True(t, ok)
	assert.Equal(t, int64(-1001234567890), ids.ChatID)
	assert.Equal(t, 55, ids.MessageID)
	assert.Equal(t, int64(777), ids.UserID)
	assert.Equal(t, 3, ids.ThreadID)
}

func TestParseModCallbackIDs_Errors(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		from  int
	}{
		{"too short", []string{"warn", "1", "2"}, 1},
		{"bad chatID", []string{"warn", "x", "55", "777", "3"}, 1},
		{"bad messageID", []string{"warn", "-100", "x", "777", "3"}, 1},
		{"bad userID", []string{"warn", "-100", "55", "x", "3"}, 1},
		{"bad threadID", []string{"warn", "-100", "55", "777", "x"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := parseModCallbackIDs(tt.parts, tt.from, "warn")
			assert.False(t, ok)
		})
	}
}

func TestParseIntField(t *testing.T) {
	parts := []string{"mute", "30", "-100"}
	v, ok := parseIntField(parts, 1, "duration", "mute")
	require.True(t, ok)
	assert.Equal(t, 30, v)

	_, ok = parseIntField(parts, 9, "duration", "mute")
	assert.False(t, ok)

	_, ok = parseIntField([]string{"mute", "abc"}, 1, "duration", "mute")
	assert.False(t, ok)
}
