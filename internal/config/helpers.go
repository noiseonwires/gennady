// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"net"
	"strings"
	"time"
)

// Cross-cutting Config query helpers used throughout the bot. None of these
// touch I/O or mutate state; they're pure predicates over the parsed Config.

// HasUsableWebUIAuth reports whether the WebUI has at least one functional
// authentication method configured (password or OTP via super-admin).
// OTP requires both OTPEnabled and a super-admin user id, plus a bot token
// to deliver the code.
func (c *Config) HasUsableWebUIAuth() bool {
	if c.WebUI.Password != "" {
		return true
	}
	if c.WebUI.IsOTPEnabled() && c.Admin.SuperAdminUserID != 0 && c.BotToken != "" {
		return true
	}
	return false
}

// ServerBindIsLoopbackOnly reports whether the configured HTTP listen address
// is restricted to the loopback interface (and therefore not reachable from
// other hosts). An empty address or a wildcard (0.0.0.0 / ::) is treated as
// publicly reachable. Hostnames that aren't a literal loopback name or IP are
// conservatively treated as reachable.
func (c *Config) ServerBindIsLoopbackOnly() bool {
	addr := strings.TrimSpace(c.Server.ListenAddr)
	switch addr {
	case "", "0.0.0.0", "::", "*":
		return false
	case "localhost":
		return true
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// IsModerationChat checks if a chat ID is one of the moderation chats.
func (c *Config) IsModerationChat(chatID int64) bool {
	return c.Moderation.ChatIDs.Contains(chatID)
}

// GetModerationChatIDs returns all moderation chat IDs.
func (c *Config) GetModerationChatIDs() []int64 {
	return c.Moderation.ChatIDs.All()
}

// GetFirstModerationChatID returns the first moderation chat ID.
func (c *Config) GetFirstModerationChatID() int64 {
	return c.Moderation.ChatIDs.First()
}

// IsAdminReplyMessage checks if a message ID is in the admin reply messages list.
func (c *Config) IsAdminReplyMessage(messageID int) bool {
	for _, id := range c.Admin.ReplyMessageIDs {
		if id == messageID {
			return true
		}
	}
	return false
}

// IsModerationActive reports whether AI content analysis applies to a message
// in (chatID, topicID). The chat must be a moderation chat and the (chat,
// topic) pair must not be in moderation.excluded_topics.
func (c *Config) IsModerationActive(chatID int64, topicID int) bool {
	if !c.IsModerationChat(chatID) {
		return false
	}
	return !c.Moderation.ExcludedTopics.Matches(chatID, topicID)
}

// InScope is the unified (included, excluded) predicate used by every
// feature whose activation is scoped per (chat, topic). The chat is also
// required to be one of the moderation chats. An empty `included` list means
// "every moderation chat, any topic"; an entry with TopicAny matches every
// topic in that chat.
func (c *Config) InScope(included, excluded ChatTopicList, chatID int64, topicID int) bool {
	if !c.IsModerationChat(chatID) {
		return false
	}
	if !included.AppliesTo(chatID, topicID) {
		return false
	}
	if excluded.Matches(chatID, topicID) {
		return false
	}
	return true
}

// IsDeletionActive reports whether automatic deletion applies to (chatID, topicID).
func (c *Config) IsDeletionActive(chatID int64, topicID int) bool {
	if !c.MessageDeletion.Enabled {
		return false
	}
	return c.InScope(c.MessageDeletion.IncludedTopics, c.MessageDeletion.ExcludedTopics, chatID, topicID)
}

// IsCreativeReplyActive reports whether creative replies apply to (chatID, topicID).
func (c *Config) IsCreativeReplyActive(chatID int64, topicID int) bool {
	return c.InScope(c.AI.CreativeReplies.IncludedTopics, c.AI.CreativeReplies.ExcludedTopics, chatID, topicID)
}

// IsMessageSummaryActive reports whether message summarization applies to (chatID, topicID).
func (c *Config) IsMessageSummaryActive(chatID int64, topicID int) bool {
	return c.InScope(c.AI.MessageSummaries.IncludedTopics, c.AI.MessageSummaries.ExcludedTopics, chatID, topicID)
}

// IsLinkSummaryActive reports whether link summarization applies to (chatID, topicID).
func (c *Config) IsLinkSummaryActive(chatID int64, topicID int) bool {
	return c.InScope(c.AI.LinkSummaries.IncludedTopics, c.AI.LinkSummaries.ExcludedTopics, chatID, topicID)
}

// nightModeWeekdays maps the day tokens accepted in night_mode.days to a
// time.Weekday. Both the full name and the common abbreviation are accepted;
// lookups lower-case and trim the token first.
var nightModeWeekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "sun": time.Sunday,
	"monday": time.Monday, "mon": time.Monday,
	"tuesday": time.Tuesday, "tue": time.Tuesday, "tues": time.Tuesday,
	"wednesday": time.Wednesday, "wed": time.Wednesday,
	"thursday": time.Thursday, "thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday,
	"friday": time.Friday, "fri": time.Friday,
	"saturday": time.Saturday, "sat": time.Saturday,
}

// clockMinutes parses an "HH:MM" 24-hour string into minutes-since-midnight.
func clockMinutes(s string) (int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// IsNightModeActive reports whether the night-mode quiet window covers a message
// in (chatID, topicID) at the given time. The feature must be enabled, the
// (chat, topic) must be in scope (included_topics minus excluded_topics), and
// now must fall inside the configured time-of-day window on an enabled day.
func (c *Config) IsNightModeActive(chatID int64, topicID int, now time.Time) bool {
	if !c.NightMode.Enabled {
		return false
	}
	if !c.InScope(c.NightMode.IncludedTopics, c.NightMode.ExcludedTopics, chatID, topicID) {
		return false
	}
	return c.NightMode.inWindow(now)
}

// inWindow reports whether now falls inside the configured night-mode window,
// honoring the day filter. Windows may wrap midnight (EndTime < StartTime); a
// wrapping window is attributed to the weekday it STARTS on, so its morning
// portion (before EndTime) is gated on the previous day's membership. A window
// with equal start/end, or an unparseable time, is treated as inactive.
func (n *NightModeConfig) inWindow(now time.Time) bool {
	start, ok1 := clockMinutes(n.StartTime)
	end, ok2 := clockMinutes(n.EndTime)
	if !ok1 || !ok2 || start == end {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	if start < end {
		// Same-day window (e.g. 08:00-23:00).
		if cur < start || cur >= end {
			return false
		}
		return n.dayEnabled(now.Weekday())
	}
	// Overnight window (wraps midnight).
	if cur >= start {
		return n.dayEnabled(now.Weekday())
	}
	if cur < end {
		return n.dayEnabled(now.AddDate(0, 0, -1).Weekday())
	}
	return false
}

// dayEnabled reports whether weekday wd is covered by the Days filter. An empty
// list means every day.
func (n *NightModeConfig) dayEnabled(wd time.Weekday) bool {
	if len(n.Days) == 0 {
		return true
	}
	for _, d := range n.Days {
		if w, ok := nightModeWeekdays[strings.ToLower(strings.TrimSpace(d))]; ok && w == wd {
			return true
		}
	}
	return false
}

// NightModeWindowEnd returns the wall-clock time at which the night-mode window
// containing now ends (the next occurrence of EndTime at or after now). Callers
// should confirm the window is active first (IsNightModeActive). Returns the
// zero time when the window bounds are unparseable or equal.
func (c *Config) NightModeWindowEnd(now time.Time) time.Time {
	return c.NightMode.windowEnd(now)
}

func (n *NightModeConfig) windowEnd(now time.Time) time.Time {
	start, ok1 := clockMinutes(n.StartTime)
	end, ok2 := clockMinutes(n.EndTime)
	if !ok1 || !ok2 || start == end {
		return time.Time{}
	}
	endToday := time.Date(now.Year(), now.Month(), now.Day(), end/60, end%60, 0, 0, now.Location())
	// Same-day window, or the morning portion of an overnight window: EndTime is
	// later today. For the evening portion (now at/after StartTime) of an
	// overnight window, EndTime falls on the next day.
	if start < end {
		return endToday
	}
	cur := now.Hour()*60 + now.Minute()
	if cur >= start {
		return endToday.AddDate(0, 0, 1)
	}
	return endToday
}

// ChatRulesFor returns the rules text that should be substituted into
// {{chat_rules}} for the given chat: the shared AI.ChatRules baseline followed
// by any chat-specific override (separated by a blank line). When chatID is 0
// or no override is configured, only the baseline is returned.
func (c *Config) ChatRulesFor(chatID int64) string {
	if chatID == 0 {
		return c.AI.ChatRules
	}
	for _, ovr := range c.AI.ChatRulesOverrides {
		if ovr.Chat == chatID && ovr.Rules != "" {
			if c.AI.ChatRules == "" {
				return ovr.Rules
			}
			return c.AI.ChatRules + "\n\n" + ovr.Rules
		}
	}
	return c.AI.ChatRules
}

// EffectivePostTo resolves a PostTo field: returns the explicit refs when set,
// otherwise expands to every moderation chat with the main-area topic (0).
func (c *Config) EffectivePostTo(post ChatTopicList) []ChatTopicRef {
	if post.Count() > 0 {
		return post.All()
	}
	chats := c.Moderation.ChatIDs.All()
	out := make([]ChatTopicRef, 0, len(chats))
	for _, id := range chats {
		out = append(out, ChatTopicRef{Chat: id, Topic: TopicMain})
	}
	return out
}
