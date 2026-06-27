// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"strings"
	"testing"
	"time"

	tgbotapi "gennadium/internal/telegram"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTruncateForLog(t *testing.T) {
	short := "hello"
	assert.Equal(t, short, truncateForLog(short))

	long := strings.Repeat("x", maxDebugLogLen+10)
	got := truncateForLog(long)
	assert.True(t, strings.HasPrefix(got, strings.Repeat("x", maxDebugLogLen)))
	assert.Contains(t, got, "trimmed, total")
}

func TestRedactSensitiveText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"api_key assignment", "api_key=supersecret", "api_key=[REDACTED]"},
		{"token assignment", "token=abc123&x=1", "token=[REDACTED]&x=1"},
		{"json password", `{"password":"hunter2"}`, `{"password":"[REDACTED]"}`},
		{"plain text untouched", "just a normal log line", "just a normal log line"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, redactSensitiveText(tt.in))
		})
	}
}

func TestRedactSensitiveBytes(t *testing.T) {
	// JSON path: nested sensitive keys are redacted.
	jsonIn := []byte(`{"auth_token":"x","nested":{"api_key":"y","ok":"z"},"list":[{"secret_token":"q"}]}`)
	got := redactSensitiveBytes(jsonIn)
	assert.Contains(t, got, `"auth_token":"[REDACTED]"`)
	assert.Contains(t, got, `"api_key":"[REDACTED]"`)
	assert.Contains(t, got, `"secret_token":"[REDACTED]"`)
	assert.Contains(t, got, `"ok":"z"`)

	// Empty body returns empty string.
	assert.Equal(t, "", redactSensitiveBytes(nil))

	// Non-JSON falls back to text redaction.
	assert.Equal(t, "password=[REDACTED]", redactSensitiveBytes([]byte("password=abc")))
}

func TestRedactSensitiveURL(t *testing.T) {
	// Telegram bot token in path. The URL serializer percent-encodes the
	// brackets, so we assert the secret is gone and the marker text remains.
	got := redactSensitiveURL("https://api.telegram.org/bot123456:ABCDEF/sendMessage?chat_id=1")
	assert.Contains(t, got, "REDACTED")
	assert.NotContains(t, got, "ABCDEF")

	// Sensitive query parameter.
	got = redactSensitiveURL("https://example.com/x?token=secret&keep=1")
	assert.Contains(t, got, "REDACTED")
	assert.NotContains(t, got, "secret")
	assert.Contains(t, got, "keep=1")

	// userinfo redaction.
	got = redactSensitiveURL("https://user:pass@example.com/")
	assert.Contains(t, got, "REDACTED")
	assert.NotContains(t, got, "pass")

	// Unparseable URL falls back to text redaction (brackets not encoded).
	got = redactSensitiveURL("://bad url api_key=secret")
	assert.Contains(t, got, redactedLogValue)
}

func TestIsSensitiveLogKey(t *testing.T) {
	for _, k := range []string{"token", "Authorization", "password", "cookie", "cookies", "api_key", "API-KEY", "apikey", "refresh_token", "my_secret"} {
		assert.True(t, isSensitiveLogKey(k), "expected %q sensitive", k)
	}
	for _, k := range []string{"chat_id", "name", "id", "count"} {
		assert.False(t, isSensitiveLogKey(k), "expected %q not sensitive", k)
	}
}

func TestComputeTextDiff(t *testing.T) {
	assert.Equal(t, "", computeTextDiff("same words here", "same words here"))

	diff := computeTextDiff("the quick brown fox", "the slow brown fox")
	assert.Contains(t, diff, "- quick")
	assert.Contains(t, diff, "+ slow")

	// Pure addition.
	diff = computeTextDiff("hello", "hello world")
	assert.Equal(t, "+ world", diff)

	// Pure deletion.
	diff = computeTextDiff("hello world", "hello")
	assert.Equal(t, "- world", diff)
}

func TestLevenshteinDistance(t *testing.T) {
	assert.Equal(t, 0, levenshteinDistance("", ""))
	assert.Equal(t, 3, levenshteinDistance("", "abc"))
	assert.Equal(t, 3, levenshteinDistance("abc", ""))
	assert.Equal(t, 0, levenshteinDistance("kitten", "kitten"))
	assert.Equal(t, 3, levenshteinDistance("kitten", "sitting"))
	// Multibyte runes count as one.
	assert.Equal(t, 1, levenshteinDistance("café", "cafe"))
}

func TestMediaTypeTag(t *testing.T) {
	assert.Equal(t, "", mediaTypeTag(nil))
	assert.Equal(t, "", mediaTypeTag(&tgbotapi.Message{Text: "hi"}))
	assert.Equal(t, "<photo>", mediaTypeTag(&tgbotapi.Message{Photo: []tgbotapi.PhotoSize{{}}}))
	assert.Equal(t, "<sticker 😀>", mediaTypeTag(&tgbotapi.Message{Sticker: &tgbotapi.Sticker{Emoji: "😀"}}))
	assert.Equal(t, "<sticker>", mediaTypeTag(&tgbotapi.Message{Sticker: &tgbotapi.Sticker{}}))
	assert.Equal(t, "<gif>", mediaTypeTag(&tgbotapi.Message{Animation: &tgbotapi.Animation{}}))
	assert.Equal(t, "<video>", mediaTypeTag(&tgbotapi.Message{Video: &tgbotapi.Video{}}))
	assert.Equal(t, "<voice>", mediaTypeTag(&tgbotapi.Message{Voice: &tgbotapi.Voice{}}))
	assert.Equal(t, "<document>", mediaTypeTag(&tgbotapi.Message{Document: &tgbotapi.Document{}}))
	assert.Equal(t, "<poll>", mediaTypeTag(&tgbotapi.Message{Poll: &tgbotapi.Poll{}}))
	assert.Equal(t, "<dice 🎲>", mediaTypeTag(&tgbotapi.Message{Dice: &tgbotapi.Dice{Emoji: "🎲"}}))
	assert.Equal(t, "<location>", mediaTypeTag(&tgbotapi.Message{Location: &tgbotapi.Location{}}))
	assert.Equal(t, "<contact>", mediaTypeTag(&tgbotapi.Message{Contact: &tgbotapi.Contact{}}))
}

func TestForwardOriginTag(t *testing.T) {
	assert.Equal(t, "", forwardOriginTag(nil))
	assert.Equal(t, "", forwardOriginTag(&tgbotapi.Message{Text: "x"}))

	assert.Equal(t, "<forwarded from: My Channel (@chan)>",
		forwardOriginTag(&tgbotapi.Message{ForwardFromChat: &tgbotapi.Chat{Title: "My Channel", Username: "chan"}}))
	assert.Equal(t, "<forwarded from: @chan>",
		forwardOriginTag(&tgbotapi.Message{ForwardFromChat: &tgbotapi.Chat{Username: "chan"}}))
	assert.Equal(t, "<forwarded from: My Channel>",
		forwardOriginTag(&tgbotapi.Message{ForwardFromChat: &tgbotapi.Chat{Title: "My Channel"}}))
	assert.Equal(t, "<forwarded from: channel>",
		forwardOriginTag(&tgbotapi.Message{ForwardFromChat: &tgbotapi.Chat{}}))

	assert.Equal(t, "<forwarded from: John Doe (@jdoe)>",
		forwardOriginTag(&tgbotapi.Message{ForwardFrom: &tgbotapi.User{FirstName: "John", LastName: "Doe", Username: "jdoe"}}))
	assert.Equal(t, "<forwarded from: @jdoe>",
		forwardOriginTag(&tgbotapi.Message{ForwardFrom: &tgbotapi.User{Username: "jdoe"}}))
	assert.Equal(t, "<forwarded from: John>",
		forwardOriginTag(&tgbotapi.Message{ForwardFrom: &tgbotapi.User{FirstName: "John"}}))
	assert.Equal(t, "<forwarded from: user>",
		forwardOriginTag(&tgbotapi.Message{ForwardFrom: &tgbotapi.User{}}))

	assert.Equal(t, "<forwarded from: Hidden>",
		forwardOriginTag(&tgbotapi.Message{ForwardSenderName: "Hidden"}))
}

func TestGenerateMessageURL(t *testing.T) {
	const chatID = int64(-1001234567890)
	assert.Equal(t, "https://t.me/c/1234567890/55", generateMessageURL(chatID, 55, nil))
	assert.Equal(t, "https://t.me/c/1234567890/22/55", generateTopicMessageURL(chatID, 55, 22))
}

func TestGenerateMessageURLFromMessage(t *testing.T) {
	msg := &tgbotapi.Message{
		MessageID: 55,
		Chat:      tgbotapi.Chat{ID: -1001234567890},
	}
	assert.Equal(t, "https://t.me/c/1234567890/55", generateMessageURLFromMessage(msg))

	// A message in a forum topic produces the topic-scoped link form.
	topicMsg := &tgbotapi.Message{
		MessageID:       55,
		Chat:            tgbotapi.Chat{ID: -1001234567890, IsForum: true},
		MessageThreadID: 22,
	}
	assert.Equal(t, "https://t.me/c/1234567890/22/55", generateMessageURLFromMessage(topicMsg))

	// In a non-forum chat the message_thread_id is a reply-thread root, not a
	// topic, so the plain (no-topic) link form is produced.
	nonForumReply := &tgbotapi.Message{
		MessageID:       56,
		Chat:            tgbotapi.Chat{ID: -1001234567890, IsForum: false},
		MessageThreadID: 30,
	}
	assert.Equal(t, "https://t.me/c/1234567890/56", generateMessageURLFromMessage(nonForumReply))
}

func TestParseTelegramMessageLink(t *testing.T) {
	mid, cid, topic, ok := parseTelegramMessageLink("see https://t.me/c/1234567890/55 now")
	require.True(t, ok)
	assert.Equal(t, 55, mid)
	assert.Equal(t, int64(-1001234567890), cid)
	assert.Nil(t, topic)

	mid, cid, topic, ok = parseTelegramMessageLink("https://t.me/c/1234567890/22/55")
	require.True(t, ok)
	assert.Equal(t, 55, mid)
	require.NotNil(t, topic)
	assert.Equal(t, 22, *topic)
	assert.Equal(t, int64(-1001234567890), cid)

	_, _, _, ok = parseTelegramMessageLink("not a link")
	assert.False(t, ok)
}

func TestParsePublicTelegramMessageLink(t *testing.T) {
	user, mid, topic, ok := parsePublicTelegramMessageLink("https://t.me/durov/123")
	require.True(t, ok)
	assert.Equal(t, "durov", user)
	assert.Equal(t, 123, mid)
	assert.Nil(t, topic)

	user, mid, topic, ok = parsePublicTelegramMessageLink("https://t.me/mychannel/10/20")
	require.True(t, ok)
	assert.Equal(t, "mychannel", user)
	assert.Equal(t, 20, mid)
	require.NotNil(t, topic)
	assert.Equal(t, 10, *topic)

	// Private "c" links are not public links.
	_, _, _, ok = parsePublicTelegramMessageLink("https://t.me/c/1234567890/55")
	assert.False(t, ok)

	_, _, _, ok = parsePublicTelegramMessageLink("nothing here")
	assert.False(t, ok)
}

func TestMessageTopic(t *testing.T) {
	forum := tgbotapi.Chat{ID: -100, IsForum: true}
	plain := tgbotapi.Chat{ID: -100, IsForum: false}

	// Topic membership comes from message_thread_id, but only in forum chats.
	assert.Equal(t, 0, messageTopic(nil))
	assert.Equal(t, 0, messageTopic(&tgbotapi.Message{}))

	// Forum chat: thread id is the topic, regardless of the reply graph.
	assert.Equal(t, 50, messageTopic(&tgbotapi.Message{Chat: forum, MessageThreadID: 50}))
	assert.Equal(t, 50, messageTopic(&tgbotapi.Message{Chat: forum, MessageThreadID: 50, ReplyToMessage: &tgbotapi.Message{MessageID: 100}}))
	// A plain forum post with no thread id is the General/main area (0).
	assert.Equal(t, 0, messageTopic(&tgbotapi.Message{Chat: forum}))

	// Non-forum chat: topics don't exist. Telegram sets message_thread_id to the
	// reply-thread root on replies, but that is NOT a topic - treat as main (0).
	assert.Equal(t, 0, messageTopic(&tgbotapi.Message{Chat: plain, MessageThreadID: 285, ReplyToMessage: &tgbotapi.Message{MessageID: 285}}))
	assert.Equal(t, 0, messageTopic(&tgbotapi.Message{Chat: plain, ReplyToMessage: &tgbotapi.Message{MessageID: 42}}))
	assert.Equal(t, 0, messageTopic(&tgbotapi.Message{Chat: plain}))
}

func TestFormatDuration(t *testing.T) {
	assert.Equal(t, "less than a minute", formatDuration(30*time.Second))
	assert.Equal(t, "5 minutes", formatDuration(5*time.Minute))
	assert.Equal(t, "2 hours", formatDuration(2*time.Hour))
	assert.Equal(t, "1 hours, 30 minutes", formatDuration(90*time.Minute))
}

func TestGetUserDisplayNameFromUser(t *testing.T) {
	assert.Equal(t, "?", getUserDisplayNameFromUser(nil))
	assert.Equal(t, "@jdoe (John Doe)", getUserDisplayNameFromUser(&tgbotapi.User{FirstName: "John", LastName: "Doe", Username: "jdoe"}))
	assert.Equal(t, "@jdoe", getUserDisplayNameFromUser(&tgbotapi.User{Username: "jdoe"}))
	assert.Equal(t, "John Doe", getUserDisplayNameFromUser(&tgbotapi.User{FirstName: "John", LastName: "Doe"}))
	assert.Equal(t, "John", getUserDisplayNameFromUser(&tgbotapi.User{FirstName: "John"}))
	assert.Equal(t, "#7", getUserDisplayNameFromUser(&tgbotapi.User{ID: 7}))
}

func TestParseBuildYear(t *testing.T) {
	assert.Equal(t, 2024, parseBuildYear("2024-05-01T10:00:00Z"))
	assert.Equal(t, 2023, parseBuildYear("2023-01-02 15:04:05"))
	assert.Equal(t, 2022, parseBuildYear("2022-12-31"))
	// Unparseable falls back to the current year.
	assert.Equal(t, time.Now().UTC().Year(), parseBuildYear("not-a-date"))
}

func TestCopyrightNotice(t *testing.T) {
	assert.Equal(t, "© 2025 Kirill aka Noiseonwires", CopyrightNotice("2025-01-01"))
	assert.Equal(t, "© 2025-2027 Kirill aka Noiseonwires", CopyrightNotice("2027-06-01"))
	// Build year older than start year clamps to start year only.
	assert.Equal(t, "© 2025 Kirill aka Noiseonwires", CopyrightNotice("2020-01-01"))
}

func TestDescribeUpdateType(t *testing.T) {
	assert.Equal(t, "message", describeUpdateType(tgbotapi.Update{Message: &tgbotapi.Message{}}))
	assert.Equal(t, "edited_message", describeUpdateType(tgbotapi.Update{EditedMessage: &tgbotapi.Message{}}))
	assert.Equal(t, "callback_query", describeUpdateType(tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{}}))
	assert.Equal(t, "channel_post", describeUpdateType(tgbotapi.Update{ChannelPost: &tgbotapi.Message{}}))
	assert.Equal(t, "edited_channel_post", describeUpdateType(tgbotapi.Update{EditedChannelPost: &tgbotapi.Message{}}))
	assert.Equal(t, "other", describeUpdateType(tgbotapi.Update{}))
}
