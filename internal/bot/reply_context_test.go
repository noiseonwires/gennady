// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"strings"
	"testing"
	"unicode/utf8"

	tgbotapi "gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The {{reply_to}} context fed to the moderation prompt must be capped at the
// configured number of UTF-8 runes (not bytes), and truncation must never split
// a multibyte codepoint.

func TestBuildModerationReplyContext_TruncatesToRuneLimit(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.ContentModeration.ReplyContextMaxChars = 10

	// 50 Cyrillic runes (2 bytes each in UTF-8) - well over the 10-rune cap.
	long := strings.Repeat("я", 50)
	msg := &tgbotapi.Message{
		ReplyToMessage: &tgbotapi.Message{
			From: &tgbotapi.User{FirstName: "Bob"},
			Text: long,
		},
	}

	out := b.buildModerationReplyContext(msg)
	require.NotEmpty(t, out)
	// The quoted fragment is exactly 10 runes followed by an ellipsis.
	assert.Contains(t, out, strings.Repeat("я", 10)+"...")
	assert.NotContains(t, out, strings.Repeat("я", 11))
	// Output stays valid UTF-8 - no codepoint was split mid-rune.
	assert.True(t, utf8.ValidString(out))
}

func TestBuildModerationReplyContext_QuoteSpanIsTruncated(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.ContentModeration.ReplyContextMaxChars = 5

	msg := &tgbotapi.Message{
		ReplyToMessage: &tgbotapi.Message{
			From: &tgbotapi.User{FirstName: "Bob"},
			Text: "the full parent message",
		},
		// The highlighted sub-quote takes precedence over the parent text and
		// is itself subject to the cap.
		Quote: &tgbotapi.TextQuote{Text: strings.Repeat("x", 40)},
	}

	out := b.buildModerationReplyContext(msg)
	assert.Contains(t, out, strings.Repeat("x", 5)+"...")
	assert.NotContains(t, out, "the full parent message")
}

func TestBuildModerationReplyContext_NoTruncationWhenUnderLimitOrDisabled(t *testing.T) {
	b, _ := newMockBot(t)

	msg := &tgbotapi.Message{
		ReplyToMessage: &tgbotapi.Message{
			From: &tgbotapi.User{FirstName: "Bob"},
			Text: "short reply",
		},
	}

	// Under the limit → verbatim, no ellipsis.
	b.config.AI.ContentModeration.ReplyContextMaxChars = 500
	out := b.buildModerationReplyContext(msg)
	assert.Contains(t, out, "short reply")
	assert.NotContains(t, out, "...")

	// maxChars <= 0 disables the cap entirely.
	b.config.AI.ContentModeration.ReplyContextMaxChars = 0
	long := strings.Repeat("a", 2000)
	msg.ReplyToMessage.Text = long
	out = b.buildModerationReplyContext(msg)
	assert.Contains(t, out, long)
}
