// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"log"
	"sort"
	"strconv"

	tgbotapi "gennadium/internal/telegram"
)

// reactionsToJSON encodes an emoji→count map to a compact, deterministic JSON
// object (keys sorted) so equal reaction states produce identical strings.
// Zero/negative counts are dropped. An empty map encodes to "".
func reactionsToJSON(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for k, v := range counts {
		if v > 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	// Build an ordered object via a small manual encoder to keep key order
	// stable (encoding/json sorts map keys already, but we filter first).
	ordered := make(map[string]int, len(keys))
	for _, k := range keys {
		ordered[k] = counts[k]
	}
	b, err := json.Marshal(ordered)
	if err != nil {
		return ""
	}
	return string(b)
}

// reactionsFromJSON decodes the stored reactions JSON back to an emoji→count
// map. An empty/invalid string yields an empty map.
func reactionsFromJSON(s string) map[string]int {
	if s == "" {
		return map[string]int{}
	}
	out := map[string]int{}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return map[string]int{}
	}
	return out
}

// handleMessageReactionCount processes an anonymous aggregate reaction update.
// The payload carries authoritative emoji→count totals, so we overwrite the
// stored reactions map. This is the primary source of truth (it needs no admin
// rights). No-op when the message isn't tracked in message_info.
func (b *Bot) handleMessageReactionCount(r *tgbotapi.MessageReactionCountUpdated) {
	if r == nil {
		return
	}
	counts := make(map[string]int, len(r.Reactions))
	for _, rc := range r.Reactions {
		if rc.Emoji != "" && rc.Count > 0 {
			counts[rc.Emoji] = rc.Count
		}
	}
	jsonStr := reactionsToJSON(counts)
	affected, err := b.db.StoreMessageReactions(r.MessageID, r.Chat.ID, jsonStr)
	if err != nil {
		log.Printf("Reactions: error storing aggregate counts for message %d in chat %d: %v", r.MessageID, r.Chat.ID, err)
		return
	}
	if affected > 0 {
		b.tracef("reactions (count): chat_id=%d message_id=%d reactions=%s", r.Chat.ID, r.MessageID, jsonStr)
	}
}

// handleMessageReaction processes a per-user reaction change (requires the bot
// to be a chat admin). It applies the old→new emoji delta to the stored map so
// counts stay current even in chats where the aggregate count update is delayed
// or absent. No-op when the message isn't tracked in message_info.
func (b *Bot) handleMessageReaction(r *tgbotapi.MessageReactionUpdated) {
	if r == nil {
		return
	}
	existing, err := b.db.GetMessageInfo(r.MessageID, r.Chat.ID)
	if err != nil || existing == nil {
		// We only annotate messages we already track.
		return
	}
	counts := reactionsFromJSON(existing.Reactions)
	for _, emoji := range r.OldReaction {
		if emoji == "" {
			continue
		}
		if counts[emoji] > 0 {
			counts[emoji]--
		}
		if counts[emoji] <= 0 {
			delete(counts, emoji)
		}
	}
	for _, emoji := range r.NewReaction {
		if emoji != "" {
			counts[emoji]++
		}
	}
	jsonStr := reactionsToJSON(counts)
	if _, err := b.db.StoreMessageReactions(r.MessageID, r.Chat.ID, jsonStr); err != nil {
		log.Printf("Reactions: error applying per-user delta for message %d in chat %d: %v", r.MessageID, r.Chat.ID, err)
		return
	}
	b.tracef("reactions (per-user): chat_id=%d message_id=%d reactions=%s", r.Chat.ID, r.MessageID, jsonStr)
}

// formatReactionsForContext renders a stored reactions JSON map as a compact
// inline tag like " [reactions: 👍3 🔥1]" for AI context strings, or "" when
// there are none. Emojis are ordered by descending count then emoji for
// determinism.
func formatReactionsForContext(reactionsJSON string) string {
	counts := reactionsFromJSON(reactionsJSON)
	if len(counts) == 0 {
		return ""
	}
	type kv struct {
		Emoji string
		Count int
	}
	items := make([]kv, 0, len(counts))
	for e, c := range counts {
		if c > 0 {
			items = append(items, kv{e, c})
		}
	}
	if len(items) == 0 {
		return ""
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Emoji < items[j].Emoji
	})
	out := " [reactions:"
	for _, it := range items {
		out += " " + it.Emoji + strconv.Itoa(it.Count)
	}
	out += "]"
	return out
}
