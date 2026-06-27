// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package telegram

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInlineKeyboardBuilders(t *testing.T) {
	btn := NewButton("Label", "cb_data")
	assert.Equal(t, "Label", btn.Text)
	assert.Equal(t, "cb_data", btn.CallbackData)
	assert.Empty(t, btn.URL)

	urlBtn := NewURLButton("Open", "https://example.com")
	assert.Equal(t, "https://example.com", urlBtn.URL)
	assert.Empty(t, urlBtn.CallbackData)

	row := NewRow(btn, urlBtn)
	assert.Len(t, row, 2)

	kb := NewKeyboard(row, NewRow(btn))
	assert.Len(t, kb.Rows, 2)
	assert.Len(t, kb.Rows[0], 2)
	assert.Len(t, kb.Rows[1], 1)
}

func TestAPIError(t *testing.T) {
	err := &APIError{Code: 429, Message: "Too Many Requests", RetryAfter: 5}
	assert.Contains(t, err.Error(), "429")
	assert.Contains(t, err.Error(), "Too Many Requests")
	assert.Equal(t, 5, err.RetryAfter)
}

func TestChatMemberIsAdmin(t *testing.T) {
	assert.True(t, ChatMember{Status: StatusCreator}.IsAdmin())
	assert.True(t, ChatMember{Status: StatusAdministrator}.IsAdmin())
	assert.False(t, ChatMember{Status: StatusMember}.IsAdmin())
	assert.False(t, ChatMember{Status: StatusRestricted}.IsAdmin())
	assert.False(t, ChatMember{Status: StatusLeft}.IsAdmin())
}

func TestParseModeConstants(t *testing.T) {
	assert.Equal(t, "", ParseModeNone)
	assert.Equal(t, "Markdown", ParseModeMarkdown)
	assert.Equal(t, "MarkdownV2", ParseModeMarkdownV2)
	assert.Equal(t, "HTML", ParseModeHTML)
}
