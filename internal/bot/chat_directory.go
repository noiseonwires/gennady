// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"sync"
	"time"

	"gennadium/internal/telegram"
)

// ChatInfo is the cached metadata for a single chat surfaced to the web UI
// and used internally for human-friendly logging.
//
// IsForum is not populated from the Telegram Bot API (the SDK pinned in this
// project predates forum topics on the Chat struct); the field is kept as a
// stable JSON shape for future enrichment.
type ChatInfo struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	IsForum    bool      `json:"is_forum"`
	Resolved   bool      `json:"resolved"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
}

type chatDirectory struct {
	mu      sync.RWMutex
	entries map[int64]ChatInfo
}

func newChatDirectory() *chatDirectory {
	return &chatDirectory{entries: make(map[int64]ChatInfo)}
}

func (d *chatDirectory) get(chatID int64) (ChatInfo, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	info, ok := d.entries[chatID]
	return info, ok
}

func (d *chatDirectory) put(info ChatInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries[info.ID] = info
}

func (d *chatDirectory) listResolved() []ChatInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]ChatInfo, 0, len(d.entries))
	for _, info := range d.entries {
		out = append(out, info)
	}
	return out
}

// resolveChatInfo fetches chat metadata via getChat, caches it, and returns
// the entry. Returns an unresolved placeholder on error so the caller can
// still render something.
func (b *Bot) resolveChatInfo(chatID int64) ChatInfo {
	if existing, ok := b.chatDir.get(chatID); ok && existing.Resolved {
		return existing
	}
	if b.tgbot == nil {
		info := ChatInfo{ID: chatID}
		b.chatDir.put(info)
		return info
	}
	chat, err := b.tg.GetChat(chatID)
	if err != nil {
		log.Printf("ChatDirectory: failed to resolve chat %d: %v", chatID, err)
		info := ChatInfo{ID: chatID}
		b.chatDir.put(info)
		return info
	}
	info := ChatInfo{
		ID:         chatID,
		Title:      chat.Title,
		Resolved:   true,
		ResolvedAt: time.Now(),
	}
	b.chatDir.put(info)
	return info
}

// RefreshChatFromUpdate updates the cached chat info using fresh data from an
// incoming update's chat. Safe to call from the hot update path.
func (b *Bot) RefreshChatFromUpdate(chat telegram.Chat) {
	if chat.ID == 0 {
		return
	}
	title := chat.Title
	if title == "" {
		// Private chats use the user's display name, which isn't useful for
		// the moderation-chat directory; only persist non-empty titles.
		return
	}
	if existing, ok := b.chatDir.get(chat.ID); ok && existing.Resolved && existing.Title == title {
		return
	}
	b.chatDir.put(ChatInfo{
		ID:         chat.ID,
		Title:      title,
		Resolved:   true,
		ResolvedAt: time.Now(),
	})
}

// resolveModerationChatsAsync kicks off a background goroutine that pre-warms
// the directory cache for every moderation chat plus the admin chat.
func (b *Bot) resolveModerationChatsAsync() {
	if b.tgbot == nil {
		return
	}
	chats := make([]int64, 0, b.config.Moderation.ChatIDs.Count()+1)
	chats = append(chats, b.config.Moderation.ChatIDs.All()...)
	if b.config.Admin.ChatID != 0 {
		chats = append(chats, b.config.Admin.ChatID)
	}
	go func() {
		for _, id := range chats {
			b.resolveChatInfo(id)
		}
	}()
}

// ListChats returns a snapshot of all known chats - moderation chats first
// (always included, even when not yet resolved), then any other chats the bot
// has seen. Used by the web UI's chat-picker dropdowns.
func (b *Bot) ListChats() []ChatInfo {
	want := make(map[int64]bool, b.config.Moderation.ChatIDs.Count()+1)
	for _, id := range b.config.Moderation.ChatIDs.All() {
		want[id] = true
	}
	if b.config.Admin.ChatID != 0 {
		want[b.config.Admin.ChatID] = true
	}

	resolved := b.chatDir.listResolved()
	out := make([]ChatInfo, 0, len(want)+len(resolved))
	seen := make(map[int64]bool, len(want)+len(resolved))

	// Emit moderation/admin chats in config order with cached info if any.
	for _, id := range b.config.Moderation.ChatIDs.All() {
		if info, ok := b.chatDir.get(id); ok {
			out = append(out, info)
		} else {
			out = append(out, ChatInfo{ID: id})
		}
		seen[id] = true
	}
	if id := b.config.Admin.ChatID; id != 0 && !seen[id] {
		if info, ok := b.chatDir.get(id); ok {
			out = append(out, info)
		} else {
			out = append(out, ChatInfo{ID: id})
		}
		seen[id] = true
	}
	// Append any other resolved chats (e.g. additional chats the bot was added to).
	for _, info := range resolved {
		if seen[info.ID] {
			continue
		}
		out = append(out, info)
		seen[info.ID] = true
	}
	return out
}

// chatDisplayName returns a human-readable label for the chat, using the cache
// if present and falling back to a synchronous resolve. Always returns
// something printable (never empty).
func (b *Bot) chatDisplayName(chatID int64) string {
	if info, ok := b.chatDir.get(chatID); ok && info.Title != "" {
		return info.Title
	}
	info := b.resolveChatInfo(chatID)
	if info.Title != "" {
		return info.Title
	}
	return fmt.Sprintf("Chat %d", chatID)
}

// ListChatsForUI exposes the chat directory to the web UI via the
// web.ChatLister interface. Returns []any so the web package doesn't need to
// import bot to learn the element type. The admin chat is filtered out so it
// never appears in chat-picker dropdowns (admin destinations are configured
// separately).
func (b *Bot) ListChatsForUI() []any {
	chats := b.ListChats()
	adminID := b.config.Admin.ChatID
	out := make([]any, 0, len(chats))
	for _, c := range chats {
		if adminID != 0 && c.ID == adminID {
			continue
		}
		out = append(out, c)
	}
	return out
}
