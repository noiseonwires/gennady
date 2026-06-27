// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gennadium/internal/i18n"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"
)

// chatNameCache stores cached chat names to avoid repeated API calls.
// Deprecated: superseded by chatDirectory (chat_directory.go). Kept only as a
// fallback when bot.chatDir is nil (e.g. early in test fixtures).
var chatNameCache = make(map[int64]string)
var chatNameCacheMutex sync.RWMutex

// maxDebugLogLen is the maximum number of characters to log in API debug output.
const maxDebugLogLen = 1024

const redactedLogValue = "[REDACTED]"

var sensitiveAssignmentRegex = regexp.MustCompile(`(?i)\b(api[-_]?key|apikey|auth_?token|access_?token|refresh_?token|secret_?token|token|password|authorization|cookies?)=([^&\s]+)`)
var sensitiveJSONValueRegex = regexp.MustCompile(`(?i)("(?:api[-_]?key|apikey|auth_?token|access_?token|refresh_?token|secret_?token|token|password|authorization|cookies?)"\s*:\s*)"[^"]*"`)
var telegramBotURLPathRegex = regexp.MustCompile(`(?i)/bot[^/]+`)

// timeNow returns the current time via the bot's clock seam, defaulting to
// time.Now when not overridden (older test fixtures may leave it nil).
func (b *Bot) timeNow() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// httpClient builds an *http.Client with the given timeout, routed through the
// bot's injected RoundTripper when one is configured (tests use this to redirect
// outbound calls). In production b.httpTransport is nil and the default
// transport is used.
func (b *Bot) httpClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: b.httpTransport}
}

// truncateForLog truncates a string to maxDebugLogLen, appending a notice if trimmed.
func truncateForLog(s string) string {
	if len(s) <= maxDebugLogLen {
		return s
	}
	return s[:maxDebugLogLen] + fmt.Sprintf("... [trimmed, total %d bytes]", len(s))
}

func redactSensitiveURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return redactSensitiveText(rawURL)
	}

	if parsed.User != nil {
		parsed.User = url.User(redactedLogValue)
	}
	parsed.Path = telegramBotURLPathRegex.ReplaceAllString(parsed.Path, "/bot"+redactedLogValue)

	query := parsed.Query()
	for key := range query {
		if isSensitiveLogKey(key) {
			query.Set(key, redactedLogValue)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func redactSensitiveBytes(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err == nil {
		redactJSONValue(payload)
		if redacted, err := json.Marshal(payload); err == nil {
			return string(redacted)
		}
	}
	return redactSensitiveText(string(body))
}

func redactSensitiveText(text string) string {
	if text == "" {
		return text
	}
	text = sensitiveAssignmentRegex.ReplaceAllString(text, "$1="+redactedLogValue)
	text = sensitiveJSONValueRegex.ReplaceAllString(text, `$1"`+redactedLogValue+`"`)
	return text
}

func redactJSONValue(v any) {
	switch value := v.(type) {
	case map[string]any:
		for key, child := range value {
			if isSensitiveLogKey(key) {
				value[key] = redactedLogValue
				continue
			}
			redactJSONValue(child)
		}
	case []any:
		for _, child := range value {
			redactJSONValue(child)
		}
	}
}

func isSensitiveLogKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	if normalized == "token" || normalized == "authorization" || normalized == "password" || normalized == "cookie" || normalized == "cookies" {
		return true
	}
	return strings.Contains(normalized, "api_key") || strings.Contains(normalized, "apikey") || strings.Contains(normalized, "secret") || strings.HasSuffix(normalized, "_token")
}

// computeTextDiff produces a compact, word-level diff between oldText and
// newText using the classic longest-common-subsequence algorithm. The texts are
// tokenised into whitespace-separated words; only the words that actually
// changed are reported. Each maximal run of changes emits a "- " line with the
// removed words and/or a "+ " line with the added words, so unedited text is
// not repeated. The result has no trailing newline.
func computeTextDiff(oldText, newText string) string {
	oldWords := strings.Fields(oldText)
	newWords := strings.Fields(newText)
	m, n := len(oldWords), len(newWords)

	// Build the LCS length table (lcs[i][j] = LCS length of oldWords[i:], newWords[j:]).
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldWords[i] == newWords[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var sb strings.Builder
	i, j := 0, 0
	// flush emits the accumulated removed/added words for one change run and
	// resets the buffers.
	flushRun := func(removed, added []string) {
		if len(removed) > 0 {
			sb.WriteString("- " + strings.Join(removed, " ") + "\n")
		}
		if len(added) > 0 {
			sb.WriteString("+ " + strings.Join(added, " ") + "\n")
		}
	}

	for i < m && j < n {
		if oldWords[i] == newWords[j] {
			i++
			j++
			continue
		}
		// Collect a maximal run of removed and added words between two anchors.
		var removed, added []string
		for i < m && j < n && oldWords[i] != newWords[j] {
			if lcs[i+1][j] >= lcs[i][j+1] {
				removed = append(removed, oldWords[i])
				i++
			} else {
				added = append(added, newWords[j])
				j++
			}
		}
		flushRun(removed, added)
	}
	if i < m {
		flushRun(oldWords[i:], nil)
	}
	if j < n {
		flushRun(nil, newWords[j:])
	}
	return strings.TrimRight(sb.String(), "\n")
}

// levenshteinDistance returns the character-level edit distance (insertions,
// deletions, substitutions) between two strings, operating on runes so that
// multi-byte characters count as one. Used to decide whether an edit is
// substantial enough to be worth recording.
func levenshteinDistance(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			min := del
			if ins < min {
				min = ins
			}
			if sub < min {
				min = sub
			}
			curr[j] = min
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// mediaTypeTag returns a short, human-readable placeholder tag describing the
// primary non-text payload of a Telegram message (e.g. "<sticker 😀>",
// "<gif>"). It returns "" for plain text messages with no recognised media.
// Used both for log lines and as a fallback value when storing messages
// without text in the message_info table.
func mediaTypeTag(message *tgbotapi.Message) string {
	if message == nil {
		return ""
	}
	switch {
	case len(message.Photo) > 0:
		return "<photo>"
	case message.Sticker != nil:
		if message.Sticker.Emoji != "" {
			return fmt.Sprintf("<sticker %s>", message.Sticker.Emoji)
		}
		return "<sticker>"
	case message.Animation != nil:
		return "<gif>"
	case message.Video != nil:
		return "<video>"
	case message.VideoNote != nil:
		return "<video-note>"
	case message.Voice != nil:
		return "<voice>"
	case message.Audio != nil:
		return "<audio>"
	case message.Document != nil:
		return "<document>"
	case message.Poll != nil:
		return "<poll>"
	case message.Dice != nil:
		return fmt.Sprintf("<dice %s>", message.Dice.Emoji)
	case message.Location != nil:
		return "<location>"
	case message.Contact != nil:
		return "<contact>"
	}
	return ""
}

// forwardOriginTag returns a "<forwarded from: …>" tag describing the origin of
// a forwarded message, or "" if the message isn't a forward. Used as a prefix
// when storing forwarded messages in the chat history so the source isn't lost.
func forwardOriginTag(message *tgbotapi.Message) string {
	if message == nil {
		return ""
	}
	switch {
	case message.ForwardFromChat != nil:
		c := message.ForwardFromChat
		name := strings.TrimSpace(c.Title)
		if name == "" {
			name = strings.TrimSpace(strings.TrimSpace(c.FirstName) + " " + strings.TrimSpace(c.LastName))
		}
		if c.Username != "" {
			if name != "" {
				return fmt.Sprintf("<forwarded from: %s (@%s)>", name, c.Username)
			}
			return fmt.Sprintf("<forwarded from: @%s>", c.Username)
		}
		if name != "" {
			return fmt.Sprintf("<forwarded from: %s>", name)
		}
		return "<forwarded from: channel>"
	case message.ForwardFrom != nil:
		u := message.ForwardFrom
		name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
		if u.Username != "" {
			if name != "" {
				return fmt.Sprintf("<forwarded from: %s (@%s)>", name, u.Username)
			}
			return fmt.Sprintf("<forwarded from: @%s>", u.Username)
		}
		if name != "" {
			return fmt.Sprintf("<forwarded from: %s>", name)
		}
		return "<forwarded from: user>"
	case message.ForwardSenderName != "":
		return fmt.Sprintf("<forwarded from: %s>", strings.TrimSpace(message.ForwardSenderName))
	}
	return ""
}

// logAPIRequestDebug logs an external API request when debug mode is enabled.
func (b *Bot) logAPIRequestDebug(service string, method string, url string, body []byte) {
	if !b.config.Debug.DebugExternalAPIs {
		return
	}
	safeURL := redactSensitiveURL(url)
	var msg string
	if len(body) > 0 {
		msg = fmt.Sprintf("=== API REQUEST [%s] %s %s ===\n%s", service, method, safeURL, truncateForLog(redactSensitiveBytes(body)))
	} else {
		msg = fmt.Sprintf("=== API REQUEST [%s] %s %s ===", service, method, safeURL)
	}
	log.Print(msg)
	b.sendDebugToSuperAdmin(msg)
}

// logAPIDebug logs an external API response body when debug mode is enabled.
func (b *Bot) logAPIDebug(service string, body []byte) {
	if b.config.Debug.DebugExternalAPIs {
		msg := fmt.Sprintf("=== API RESPONSE [%s] ===\n%s", service, truncateForLog(redactSensitiveBytes(body)))
		log.Print(msg)
		b.sendDebugToSuperAdmin(msg)
	}
}

// logAPIError logs a failed API request when debug_api_errors is enabled.
// It logs the service name, HTTP status code, and a trimmed response body.
// If send_to_super_admin is also enabled, it forwards the error to the super-admin.
func (b *Bot) logAPIError(service string, statusCode int, body []byte, err error) {
	if !b.config.Debug.DebugAPIErrors {
		return
	}
	var msg string
	if err != nil {
		msg = fmt.Sprintf("🔴 API ERROR [%s] %s", service, redactSensitiveText(err.Error()))
	} else {
		msg = fmt.Sprintf("🔴 API ERROR [%s] status %d: %s", service, statusCode, truncateForLog(redactSensitiveBytes(body)))
	}
	log.Print(msg)
	b.sendDebugToSuperAdmin(msg)
}

// sendDebugToSuperAdmin sends a debug log message to the super-admin user via Telegram
// when both send_to_super_admin is enabled and super_admin_user_id is configured.
func (b *Bot) sendDebugToSuperAdmin(text string) {
	if !b.config.Debug.SendToSuperAdmin || b.config.Admin.SuperAdminUserID == 0 {
		return
	}
	if _, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:                b.config.Admin.SuperAdminUserID,
		Text:                  text,
		DisableWebPagePreview: true,
	}); err != nil {
		log.Printf("Failed to send debug message to super-admin: %v", err)
	}
}

// deleteMessageWithRetry deletes a Telegram message, retrying on transient
// failures (HTTP 429 rate limiting and 5xx server errors). Telegram's
// retry_after hint is honored when present. This matters for auto-moderation:
// when a "delete" rule fires alongside other actions (e.g. mute) on a spam
// message, the burst of API calls can trip the rate limiter and a single-shot
// delete would silently downgrade to a report, leaving the offending message
// in the chat while the user is still muted. A nil return means the message was
// deleted (or was already gone). A "message to delete not found" error is
// treated as success since the end state is identical.
func (b *Bot) deleteMessageWithRetry(chatID int64, messageID int) error {
	const maxAttempts = 4
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := b.tg.DeleteMessage(chatID, messageID)
		if err == nil {
			return nil
		}
		lastErr = err

		// Already-deleted messages are a success for our purposes.
		if strings.Contains(strings.ToLower(err.Error()), "message to delete not found") {
			return nil
		}

		// Only retry transient errors. The telegram port surfaces API errors as
		// *telegram.APIError with the HTTP-equivalent code and an optional
		// retry_after hint.
		wait := time.Duration(attempt) * 500 * time.Millisecond
		var apiErr *telegram.APIError
		if errors.As(err, &apiErr) {
			if apiErr.RetryAfter > 0 {
				wait = time.Duration(apiErr.RetryAfter)*time.Second + 250*time.Millisecond
			} else if apiErr.Code != 429 && apiErr.Code < 500 {
				// Permanent error (e.g. missing permission) - don't retry.
				return err
			}
		}

		if attempt < maxAttempts {
			log.Printf("deleteMessageWithRetry: delete of message %d in chat %d failed (attempt %d/%d): %v - retrying in %s",
				messageID, chatID, attempt, maxAttempts, err, wait)
			time.Sleep(wait)
		}
	}
	return lastErr
}

// restrictUserInChats restricts (mutes) a user in one or all moderation chats.
// Returns the first error encountered (if any) along with the chat ID where it
// happened. A nil error means every targeted chat restriction succeeded.
func (b *Bot) restrictUserInChats(userID int64, sourceChatID int64, untilDate int64) (int64, error) {
	chatsToMute := []int64{sourceChatID}
	if b.config.Moderation.MuteAcrossAllChats {
		chatsToMute = b.config.GetModerationChatIDs()
	}

	var firstErr error
	var firstErrChat int64
	for _, targetChatID := range chatsToMute {
		err := b.tg.RestrictChatMember(telegram.RestrictChatMemberParams{
			ChatID:    targetChatID,
			UserID:    userID,
			UntilDate: untilDate,
			Permissions: telegram.Permissions{
				CanSendMessages: false,
			},
		})
		if err != nil {
			log.Printf("Error muting user %d in chat %d: %v", userID, targetChatID, err)
			if firstErr == nil {
				firstErr = err
				firstErrChat = targetChatID
			}
		} else {
			log.Printf("Restricted user %d in chat %d (until_date=%d)", userID, targetChatID, untilDate)
		}
	}

	if len(chatsToMute) > 1 {
		log.Printf("User %d muted across %d chats", userID, len(chatsToMute))
	}
	return firstErrChat, firstErr
}

// unrestrictUserInChats unrestricts (unmutes) a user in one or all moderation chats.
func (b *Bot) unrestrictUserInChats(userID int64, sourceChatID int64) {
	chatsToUnmute := []int64{sourceChatID}
	if b.config.Moderation.MuteAcrossAllChats {
		chatsToUnmute = b.config.GetModerationChatIDs()
	}

	for _, targetChatID := range chatsToUnmute {
		err := b.tg.RestrictChatMember(telegram.RestrictChatMemberParams{
			ChatID: targetChatID,
			UserID: userID,
			Permissions: telegram.Permissions{
				CanSendMessages:       true,
				CanSendMedia:          true,
				CanSendPolls:          true,
				CanSendOther:          true,
				CanAddWebPagePreviews: true,
			},
		})
		if err != nil {
			log.Printf("Error unmuting user %d in chat %d: %v", userID, targetChatID, err)
		}
	}

	if len(chatsToUnmute) > 1 {
		log.Printf("User %d unmuted across %d chats", userID, len(chatsToUnmute))
	}
}

// getChatName retrieves the chat title/name from Telegram API with caching.
func (b *Bot) getChatName(chatID int64) string {
	if b.chatDir != nil {
		return b.chatDisplayName(chatID)
	}

	chatNameCacheMutex.RLock()
	if name, ok := chatNameCache[chatID]; ok {
		chatNameCacheMutex.RUnlock()
		return name
	}
	chatNameCacheMutex.RUnlock()

	chat, err := b.tg.GetChat(chatID)
	if err != nil {
		log.Printf("Error getting chat info for %d: %v", chatID, err)
		return fmt.Sprintf("Chat %d", chatID)
	}

	name := chat.Title
	if name == "" {
		name = fmt.Sprintf("Chat %d", chatID)
	}

	chatNameCacheMutex.Lock()
	chatNameCache[chatID] = name
	chatNameCacheMutex.Unlock()

	return name
}

// getChatNameShort returns a short identifier for the chat.
func (b *Bot) getChatNameShort(chatID int64) string {
	name := b.getChatName(chatID)
	if len(name) > 20 {
		name = name[:20] + "..."
	}
	return fmt.Sprintf("[%s]", name)
}

// GetChatName is the exported wrapper around getChatName, used by the web UI
// (implements web.ChatNameResolver).
func (b *Bot) GetChatName(chatID int64) string {
	return b.getChatName(chatID)
}

// getChatLabel returns a human-friendly chat label combining title and numeric ID,
// suitable for inclusion in admin/moderation messages (e.g. "My Chat (-1001234567890)").
func (b *Bot) getChatLabel(chatID int64) string {
	name := b.getChatName(chatID)
	if name == "" || name == fmt.Sprintf("Chat %d", chatID) {
		return fmt.Sprintf("%d", chatID)
	}
	return fmt.Sprintf("%s (%d)", name, chatID)
}

// generateMessageURL creates a proper Telegram message URL. When
// messageThreadID is non-nil and non-zero, the topic-scoped link form
// (https://t.me/c/<chat>/<topic>/<msg>) is produced so the link opens inside
// the forum topic; otherwise the plain form is used.
func generateMessageURL(chatID int64, messageID int, messageThreadID *int) string {
	if messageThreadID != nil && *messageThreadID != 0 {
		return generateTopicMessageURL(chatID, messageID, *messageThreadID)
	}
	linkChatID := chatID*-1 - 1000000000000
	return fmt.Sprintf("https://t.me/c/%d/%d", linkChatID, messageID)
}

// generateTopicMessageURL creates a topic-specific URL.
func generateTopicMessageURL(chatID int64, messageID int, messageThreadID int) string {
	linkChatID := chatID*-1 - 1000000000000
	return fmt.Sprintf("https://t.me/c/%d/%d/%d", linkChatID, messageThreadID, messageID)
}

// generateMessageURLFromMessage creates a proper Telegram message URL from a message object.
func generateMessageURLFromMessage(message *tgbotapi.Message) string {
	var threadID *int
	if topic := messageTopic(message); topic != 0 {
		threadID = &topic
	}
	return generateMessageURL(message.Chat.ID, message.MessageID, threadID)
}

// parseTelegramMessageLink parses a Telegram private chat message link.
func parseTelegramMessageLink(text string) (messageID int, chatID int64, topicID *int, ok bool) {
	matches := messageLinkRegex.FindStringSubmatch(text)
	if len(matches) < 3 {
		return 0, 0, nil, false
	}
	linkChatID, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, 0, nil, false
	}

	if len(matches) > 3 && matches[3] != "" {
		topicIDValue, err := strconv.Atoi(matches[2])
		if err != nil {
			return 0, 0, nil, false
		}
		topicID = &topicIDValue
		messageID, err = strconv.Atoi(matches[3])
	} else {
		messageID, err = strconv.Atoi(matches[2])
	}
	if err != nil {
		return 0, 0, nil, false
	}

	chatID = linkChatID*-1 - 1000000000000
	return messageID, chatID, topicID, true
}

// parsePublicTelegramMessageLink parses a Telegram public chat message link (https://t.me/username/message_id or https://t.me/username/topic_id/message_id).
func parsePublicTelegramMessageLink(text string) (username string, messageID int, topicID *int, ok bool) {
	// Use a more specific regex that also captures optional topic ID
	re := regexp.MustCompile(`https://t\.me/([a-zA-Z][a-zA-Z0-9_]{3,})/(\d+)(?:/(\d+))?`)
	matches := re.FindStringSubmatch(text)
	if len(matches) < 3 {
		return "", 0, nil, false
	}
	username = matches[1]
	// Skip if username is "c" (private link format)
	if username == "c" {
		return "", 0, nil, false
	}

	if len(matches) > 3 && matches[3] != "" {
		topicIDValue, err := strconv.Atoi(matches[2])
		if err != nil {
			return "", 0, nil, false
		}
		topicID = &topicIDValue
		messageID, err = strconv.Atoi(matches[3])
		if err != nil {
			return "", 0, nil, false
		}
	} else {
		var err error
		messageID, err = strconv.Atoi(matches[2])
		if err != nil {
			return "", 0, nil, false
		}
	}
	return username, messageID, topicID, true
}

// resolveChatIDByUsername resolves a public chat username to a chat ID via Telegram API.
// Works for any public chat type (supergroups, channels). Regular groups cannot have usernames.
func (b *Bot) resolveChatIDByUsername(username string) (int64, error) {
	chat, err := b.tg.GetChatByUsername(username)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve username @%s: %v", username, err)
	}
	return chat.ID, nil
}

// sendErrorToAdmin sends error messages to admin chat.
func (b *Bot) sendErrorToAdmin(chatID int64, errorMsg string) {
	b.sendToAdminChat(fmt.Sprintf("❌ Ошибка: %s", errorMsg))
}

// shouldAddToDeleteQueue checks if a message should be added to the deletion queue.
func (b *Bot) shouldAddToDeleteQueue(message *tgbotapi.Message) bool {
	topic := messageTopic(message)
	if b.config.IsDeletionActive(message.Chat.ID, topic) {
		return true
	}
	// Cascade on the reply graph (independent of topic): when this message is a
	// direct reply to one that's already queued for deletion, queue this reply
	// too even if its topic isn't otherwise in the deletion scope. This keeps a
	// reply thread cleaned up together with the message it answers.
	if message.ReplyToMessage != nil {
		replyToID := message.ReplyToMessage.MessageID
		if inQueue, err := b.db.IsMessageInDeletionQueue(replyToID, message.Chat.ID); err == nil && inQueue {
			return true
		} else if err != nil {
			log.Printf("Error checking if message %d is in deletion queue: %v", replyToID, err)
		}
	}
	return false
}

// messageTopic returns the forum topic id a message belongs to. Topics are a
// forum-only concept: only chats with is_forum=true have real topics, where the
// id comes directly from Telegram's message_thread_id (0 = the General/main
// area). In non-forum chats Telegram still sets message_thread_id on replies -
// but that is a *reply-thread root*, not a topic - so we treat every non-forum
// message as the main area (0) to keep "topic: main" covering the whole chat.
// This is the canonical topic used for all per-(chat, topic) scope checks
// (moderation, deletion, summaries, creative/link replies).
//
// Note: this is distinct from the reply graph - ReplyToMessage / reply ids are
// still used for conversation threading (e.g. creative-reply chains), not for
// topic membership.
func messageTopic(m *tgbotapi.Message) int {
	if m == nil {
		return 0
	}
	if !m.Chat.IsForum {
		return 0
	}
	return m.MessageThreadID
}

// traceInboundTopic emits a TRACE line capturing the topic-relevant fields of
// an inbound message so that, after a deployment, we can verify Telegram
// populates message_thread_id / reply_to / is_forum as expected and that our
// computed topic matches. Gated behind debug.trace_topics (no-op otherwise).
func (b *Bot) traceInboundTopic(kind string, m *tgbotapi.Message) {
	if !b.config.Debug.TraceTopics || m == nil {
		return
	}
	replyToID := 0
	replyToThread := 0
	if m.ReplyToMessage != nil {
		replyToID = m.ReplyToMessage.MessageID
		replyToThread = m.ReplyToMessage.MessageThreadID
	}
	b.tracef("inbound %s: chat_id=%d is_forum=%t message_id=%d message_thread_id=%d reply_to_message_id=%d reply_to_thread_id=%d computed_topic=%d",
		kind, m.Chat.ID, m.Chat.IsForum, m.MessageID, m.MessageThreadID, replyToID, replyToThread, messageTopic(m))
}

// firstAdminReplyID returns the first configured admin reply-target message id,
// or 0 when none is configured (meaning "do not reply to a specific message").
func (b *Bot) firstAdminReplyID() int {
	if len(b.config.Admin.ReplyMessageIDs) > 0 {
		return b.config.Admin.ReplyMessageIDs[0]
	}
	return 0
}

// adminReplyIfAdminChat returns the admin reply-target message id when chatID is
// the admin chat, otherwise 0 (no specific reply target).
func (b *Bot) adminReplyIfAdminChat(chatID int64) int {
	if chatID == b.config.Admin.ChatID {
		return b.firstAdminReplyID()
	}
	return 0
}

// sendMarkdownReply sends a Markdown-formatted reply, transparently retrying as
// plain text if Telegram rejects the entities. It returns the sent message and
// ok=false when both attempts fail (errors are logged).
func (b *Bot) sendMarkdownReply(chatID int64, text string, replyToMessageID int) (telegram.Message, bool) {
	sent, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           chatID,
		Text:             text,
		ReplyToMessageID: replyToMessageID,
		ParseMode:        telegram.ParseModeMarkdown,
	})
	if err != nil {
		log.Printf("Error sending reply with Markdown, retrying as plain text: %v", err)
		sent, err = b.tg.SendMessage(telegram.SendMessageParams{
			ChatID:           chatID,
			Text:             text,
			ReplyToMessageID: replyToMessageID,
		})
		if err != nil {
			log.Printf("Error sending reply as plain text: %v", err)
			return telegram.Message{}, false
		}
	}
	return sent, true
}

// sendToAdminChat sends a message to the admin chat.
func (b *Bot) sendToAdminChat(text string) {
	if _, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           b.config.Admin.ChatID,
		Text:             text,
		ReplyToMessageID: b.firstAdminReplyID(),
	}); err != nil {
		log.Printf("Error sending message to admin chat: %v", err)
	}
}

// sendToModerationChatWithReply sends a message to a moderation chat with proper reply logic.
func (b *Bot) sendToModerationChatWithReply(chatID int64, text string, originalMessageID int) *telegram.Message {
	if chatID == 0 {
		return nil
	}

	sentMsg, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           chatID,
		Text:             text,
		ReplyToMessageID: originalMessageID,
		ParseMode:        telegram.ParseModeMarkdown,
	})
	if err != nil {
		if strings.Contains(err.Error(), "can't parse entities") {
			log.Printf("Markdown parse error, retrying without parse mode: %v", err)
			sentMsg, err = b.tg.SendMessage(telegram.SendMessageParams{
				ChatID:           chatID,
				Text:             text,
				ReplyToMessageID: originalMessageID,
			})
			if err != nil {
				log.Printf("Error sending message to moderation chat (plain text fallback): %v", err)
				return nil
			}
		} else {
			log.Printf("Error sending message to moderation chat: %v", err)
			return nil
		}
	}

	err = b.db.AddMessageForDeletion(sentMsg.MessageID, sentMsg.Chat.ID)
	if err != nil {
		log.Printf("Error adding moderation message to deletion queue: %v", err)
	}

	return &sentMsg
}

// sendToModerationChatTopic posts a (deletable) message into a moderation chat,
// targeting the given forum topic (0 = main area). Unlike
// sendToModerationChatWithReply this does not reply to a specific message - it
// is for scheduled/broadcast content (e.g. morning greeting) that belongs to a
// topic rather than a conversation.
func (b *Bot) sendToModerationChatTopic(chatID int64, text string, threadID int) *telegram.Message {
	if chatID == 0 {
		return nil
	}

	b.tracef("post_to send (deletable): chat_id=%d message_thread_id=%d text_len=%d", chatID, threadID, len(text))

	sentMsg, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:          chatID,
		Text:            text,
		MessageThreadID: threadID,
		ParseMode:       telegram.ParseModeMarkdown,
	})
	if err != nil {
		if strings.Contains(err.Error(), "can't parse entities") {
			log.Printf("Markdown parse error, retrying without parse mode: %v", err)
			sentMsg, err = b.tg.SendMessage(telegram.SendMessageParams{
				ChatID:          chatID,
				Text:            text,
				MessageThreadID: threadID,
			})
			if err != nil {
				log.Printf("Error sending topic message to moderation chat (plain text fallback): %v", err)
				return nil
			}
		} else {
			log.Printf("Error sending topic message to moderation chat: %v", err)
			return nil
		}
	}

	if err := b.db.AddMessageForDeletion(sentMsg.MessageID, sentMsg.Chat.ID); err != nil {
		log.Printf("Error adding moderation message to deletion queue: %v", err)
	}

	return &sentMsg
}
func (b *Bot) sendToModerationChatPlainText(chatID int64, text string, originalMessageID int) *telegram.Message {
	if chatID == 0 {
		return nil
	}

	sentMsg, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:           chatID,
		Text:             text,
		ReplyToMessageID: originalMessageID,
	})
	if err != nil {
		log.Printf("Error sending plain text message to moderation chat: %v", err)
		return nil
	}

	err = b.db.AddMessageForDeletion(sentMsg.MessageID, sentMsg.Chat.ID)
	if err != nil {
		log.Printf("Error adding moderation message to deletion queue: %v", err)
	}

	return &sentMsg
}

// replaceMessageContentWithPlaceholder replaces the message content in database.
// If extraInfo is non-empty it is stored in the extra_info column (typically
// the content-safety filter details), otherwise extra_info is cleared.
func (b *Bot) replaceMessageContentWithPlaceholder(messageID int, chatID int64, placeholder string, extraInfo string) {
	messageInfo, err := b.db.GetMessageInfo(messageID, chatID)
	if err != nil {
		log.Printf("Error getting message info for placeholder replacement: %v", err)
		return
	}

	messageInfo.Text = placeholder
	messageInfo.ExtraInfo = extraInfo

	err = b.db.UpdateMessageInfo(messageInfo)
	if err != nil {
		log.Printf("Error replacing message content with placeholder: %v", err)
		return
	}

	log.Printf("Replaced content of message %d with placeholder due to content filter", messageID)
}

// truncateMessage truncates text to maxLen Unicode characters.
func truncateMessage(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}

	suffix := "\n\n...(сообщение обрезано)"
	available := maxLen - utf8.RuneCountInString(suffix)
	if available <= 0 {
		return string(runes[:maxLen])
	}

	truncated := string(runes[:available])

	if lastNL := strings.LastIndex(truncated, "\n"); lastNL > 0 {
		if utf8.RuneCountInString(truncated[:lastNL]) > available*3/4 {
			truncated = truncated[:lastNL]
		}
	}

	return truncated + suffix
}

// sendToModerationChatPermanent sends a message to a moderation chat that won't be deleted.
// threadID targets a forum topic (0 = main area).
func (b *Bot) sendToModerationChatPermanent(chatID int64, text string, threadID int) *telegram.Message {
	if chatID == 0 {
		return nil
	}

	text = truncateMessage(text, MaxTelegramMessageLength)

	b.tracef("post_to send (permanent): chat_id=%d message_thread_id=%d text_len=%d", chatID, threadID, len(text))

	sentMsg, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:          chatID,
		Text:            text,
		MessageThreadID: threadID,
		ParseMode:       telegram.ParseModeMarkdown,
	})
	if err != nil {
		log.Printf("Error sending permanent message to moderation chat: %v", err)
		return nil
	}

	err = b.db.AddMessageForDeletionWithPinnedStatus(sentMsg.MessageID, sentMsg.Chat.ID, true)
	if err != nil {
		log.Printf("Error marking permanent message as pinned: %v", err)
	}

	return &sentMsg
}

// editMessageText edits a message text.
func (b *Bot) editMessageText(message *tgbotapi.Message, newText string) {
	if _, err := b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    message.Chat.ID,
		MessageID: message.MessageID,
		Text:      newText,
	}); err != nil {
		log.Printf("Error editing message: %v", err)
	}
}

// appendActionNotice appends an action notice to the report message text
// and removes buttons whose callback data starts with any of the given prefixes.
func (b *Bot) appendActionNotice(message *tgbotapi.Message, notice string, prefixesToRemove []string) {
	b.appendActionNoticeWithExtraRows(message, notice, prefixesToRemove, nil)
}

// appendActionNoticeWithExtraRows behaves like appendActionNotice but also
// appends the given extra button rows after the surviving rows. Use this when
// an action both retires some buttons and unlocks new ones on the same card.
func (b *Bot) appendActionNoticeWithExtraRows(message *tgbotapi.Message, notice string, prefixesToRemove []string, extraRows [][]telegram.InlineButton) {
	newText := message.Text + "\n" + notice
	newText = truncateMessage(newText, MaxTelegramMessageLength)

	// Rebuild the keyboard as a neutral keyboard, dropping buttons whose
	// callback data starts with any prefix to remove. The inbound markup is read
	// from the (library-typed) incoming message.
	var newRows [][]telegram.InlineButton
	if message.ReplyMarkup != nil {
		for _, row := range message.ReplyMarkup.Rows {
			var newRow []telegram.InlineButton
			for _, button := range row {
				if button.CallbackData == "" {
					newRow = append(newRow, telegram.NewURLButton(button.Text, button.URL))
					continue
				}
				remove := false
				for _, prefix := range prefixesToRemove {
					if strings.HasPrefix(button.CallbackData, prefix) {
						remove = true
						break
					}
				}
				if !remove {
					newRow = append(newRow, telegram.NewButton(button.Text, button.CallbackData))
				}
			}
			if len(newRow) > 0 {
				newRows = append(newRows, newRow)
			}
		}
	}
	newRows = append(newRows, extraRows...)

	keyboard := telegram.InlineKeyboard{Rows: newRows}
	if _, err := b.tg.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    message.Chat.ID,
		MessageID: message.MessageID,
		Text:      newText,
		Keyboard:  &keyboard,
	}); err != nil {
		log.Printf("Error appending action notice: %v", err)
	}
}

// formatDuration formats a duration into a human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		if minutes > 0 {
			return fmt.Sprintf("%d hours, %d minutes", hours, minutes)
		}
		return fmt.Sprintf("%d hours", hours)
	}

	return fmt.Sprintf("%d minutes", minutes)
}

// getUserDisplayName returns a user's display name by fetching from Telegram API.
// Tries multiple chat contexts to get the most up-to-date user information.
func (b *Bot) getUserDisplayName(userID int64) string {
	chatIDs := append([]int64{b.config.Admin.ChatID}, b.config.GetModerationChatIDs()...)

	for _, chatID := range chatIDs {
		if chatID == 0 {
			continue
		}

		chatMember, err := b.tg.GetChatMember(chatID, userID)
		if err == nil && chatMember.User != nil {
			return telegramUserDisplayName(chatMember.User)
		}
	}

	return fmt.Sprintf("User %d", userID)
}

// getUserDisplayNameByID is an alias for getUserDisplayName for backward compatibility.
func (b *Bot) getUserDisplayNameByID(userID int64, chatID int64) string {
	return b.getUserDisplayName(userID)
}

// getUserDisplayNameFromUser creates a human-friendly display name from a Telegram User object.
func getUserDisplayNameFromUser(user *tgbotapi.User) string {
	if user == nil {
		return "?"
	}

	displayName := user.FirstName
	if user.LastName != "" {
		displayName += " " + user.LastName
	}

	if user.Username != "" {
		if displayName != "" {
			return fmt.Sprintf("@%s (%s)", user.Username, displayName)
		}
		return "@" + user.Username
	}

	if displayName != "" {
		return displayName
	}

	return fmt.Sprintf("#%d", user.ID)
}

// telegramUserDisplayName builds a human-friendly display name from a neutral
// telegram.User (the port's representation), mirroring getUserDisplayNameFromUser.
func telegramUserDisplayName(user *telegram.User) string {
	if user == nil {
		return "?"
	}

	displayName := user.FirstName
	if user.LastName != "" {
		displayName += " " + user.LastName
	}

	if user.Username != "" {
		if displayName != "" {
			return fmt.Sprintf("@%s (%s)", user.Username, displayName)
		}
		return "@" + user.Username
	}

	if displayName != "" {
		return displayName
	}

	return fmt.Sprintf("#%d", user.ID)
}

// isUserAdmin checks if a user is an admin in any moderation chat or admin chat.
func (b *Bot) isUserAdmin(userID int64) bool {
	if b.config.Admin.SuperAdminUserID != 0 && userID == b.config.Admin.SuperAdminUserID {
		return true
	}

	for _, chatID := range b.config.GetModerationChatIDs() {
		if b.isUserAdminInChat(userID, chatID) {
			return true
		}
	}

	if b.config.Admin.ChatID != 0 {
		if b.isUserAdminInChat(userID, b.config.Admin.ChatID) {
			return true
		}
	}

	return false
}

// isUserWhitelisted checks if a user is in the whitelist.
func (b *Bot) isUserWhitelisted(userID int64) bool {
	for _, whitelistedID := range b.config.Admin.WhitelistUserIDs {
		if userID == whitelistedID {
			return true
		}
	}
	return false
}

// isUserAdminInChat checks if a user is an admin in a specific chat.
func (b *Bot) isUserAdminInChat(userID int64, chatID int64) bool {
	member, err := b.tg.GetChatMember(chatID, userID)
	if err != nil {
		log.Printf("Error checking admin status for user %d in chat %d: %v", userID, chatID, err)
		return false
	}

	return member.IsAdmin()
}

// getActionEmoji returns emoji for action type.
func getActionEmoji(actionType string) string {
	switch actionType {
	case "warn":
		return i18n.T("action.warn")
	case "mute":
		return i18n.T("action.mute")
	case "unmute":
		return i18n.T("action.unmute")
	case "delete":
		return i18n.T("action.delete")
	case "restore":
		return i18n.T("action.restore")
	default:
		return "❓ " + actionType
	}
}
