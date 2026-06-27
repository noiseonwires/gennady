// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"log"
	"sync"

	"gennadium/internal/i18n"
	tgbotapi "gennadium/internal/telegram"
)

// topicDirectory caches human-readable forum-topic names keyed by
// (chat_id, thread_id).
//
// The Telegram Bot API exposes no method to look up a topic name by id or to
// list a chat's topics, so names can only be learned passively from the
// forum_topic_created / forum_topic_edited service messages that arrive in
// updates. This directory holds what the bot has observed (warmed from the DB
// on startup and kept up to date as new events arrive).
type topicDirectory struct {
	mu    sync.RWMutex
	names map[topicKey]string
}

type topicKey struct {
	chatID   int64
	threadID int
}

func newTopicDirectory() *topicDirectory {
	return &topicDirectory{names: make(map[topicKey]string)}
}

func (d *topicDirectory) get(chatID int64, threadID int) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	name, ok := d.names[topicKey{chatID, threadID}]
	return name, ok
}

func (d *topicDirectory) put(chatID int64, threadID int, name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.names[topicKey{chatID, threadID}] = name
}

// warmTopicDirectory loads all known topic names from the DB into the cache.
// Safe to call with a nil directory or DB (no-op).
func (b *Bot) warmTopicDirectory() {
	if b.topicDir == nil || b.db == nil {
		return
	}
	topics, err := b.db.ListAllForumTopics()
	if err != nil {
		log.Printf("TopicDirectory: failed to load topics: %v", err)
		return
	}
	for _, t := range topics {
		b.topicDir.put(t.ChatID, t.ThreadID, t.Name)
	}
	if len(topics) > 0 {
		log.Printf("TopicDirectory: warmed %d topic name(s) from DB", len(topics))
	}
}

// seedTopicNamesFromConfig loads operator-provided topic names from config into
// the in-memory cache, but only for (chat, topic) pairs not already known from
// the DB. Live-observed names (persisted to the DB) therefore always win over
// these static entries; config only fills gaps for topics the bot has never
// seen. Config names are intentionally not persisted to the DB so that editing
// the config later takes effect without a stale row lingering.
func (b *Bot) seedTopicNamesFromConfig() {
	if b.topicDir == nil || b.config == nil {
		return
	}
	seeded := 0
	for _, t := range b.config.Topics {
		if t.Name == "" || t.Topic <= 0 || t.Chat == 0 {
			continue
		}
		if _, ok := b.topicDir.get(t.Chat, t.Topic); ok {
			continue // DB-learned name wins
		}
		b.topicDir.put(t.Chat, t.Topic, t.Name)
		seeded++
	}
	if seeded > 0 {
		log.Printf("TopicDirectory: seeded %d topic name(s) from config", seeded)
	}
}

// harvestTopicFromMessage records a topic name when an inbound message is a
// forum_topic_created / forum_topic_edited service message. Safe to call on
// the hot update path for every message (no-op for non-topic events).
func (b *Bot) harvestTopicFromMessage(m *tgbotapi.Message) {
	if m == nil {
		return
	}
	name := ""
	switch {
	case m.ForumTopicCreated != nil:
		name = m.ForumTopicCreated.Name
	case m.ForumTopicEdited != nil:
		name = m.ForumTopicEdited.Name
	default:
		return
	}
	b.recordTopicName(m.Chat.ID, m.MessageThreadID, name)
}

// recordTopicName persists a topic name to the cache and DB. Empty names and
// the chat's main area (thread 0) are ignored.
func (b *Bot) recordTopicName(chatID int64, threadID int, name string) {
	if name == "" || threadID == 0 || chatID == 0 {
		return
	}
	if b.topicDir != nil {
		if existing, ok := b.topicDir.get(chatID, threadID); ok && existing == name {
			return // already cached with the same name; skip the DB write
		}
		b.topicDir.put(chatID, threadID, name)
	}
	if b.db != nil {
		if err := b.db.UpsertForumTopic(chatID, threadID, name); err != nil {
			log.Printf("TopicDirectory: failed to persist topic %d/%d: %v", chatID, threadID, err)
		}
	}
}

// GetTopicName is the exported wrapper around topicName, used by the web UI
// (implements web.TopicNameResolver). Returns "" when the topic is the main
// area (thread 0) or its name is unknown.
func (b *Bot) GetTopicName(chatID int64, threadID int) string {
	return b.topicName(chatID, threadID)
}

// topicName returns the cached human-readable name for a topic, or "" when
// unknown. It consults the in-memory cache first, then falls back to the DB
// (caching the result). Thread 0 (the main area) has no name.
func (b *Bot) topicName(chatID int64, threadID int) string {
	if threadID == 0 {
		return ""
	}
	if b.topicDir != nil {
		if name, ok := b.topicDir.get(chatID, threadID); ok {
			return name
		}
	}
	if b.db == nil {
		return ""
	}
	name, ok, err := b.db.GetForumTopicName(chatID, threadID)
	if err != nil {
		log.Printf("TopicDirectory: lookup failed for %d/%d: %v", chatID, threadID, err)
		return ""
	}
	if ok && b.topicDir != nil {
		b.topicDir.put(chatID, threadID, name)
	}
	return name
}

// topicContext builds the topic line appended to moderation reports. Returns
// "" for the main area (thread 0) and also when the topic name is unknown -
// an id-only line carries little value, so the field is skipped entirely
// unless a human-readable name is available.
func (b *Bot) topicContext(chatID int64, threadID int) string {
	if threadID == 0 {
		return ""
	}
	name := b.topicName(chatID, threadID)
	if name == "" {
		return ""
	}
	return i18n.Tf("mod.topic_context_named", name, threadID)
}
