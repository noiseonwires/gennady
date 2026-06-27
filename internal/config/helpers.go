// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"net"
	"strings"
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
