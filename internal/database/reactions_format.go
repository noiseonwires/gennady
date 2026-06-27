// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// formatReactionsTag renders a stored reactions JSON map (emoji→count) as a
// compact inline tag like " [reactions: 👍3 🔥1]" for AI context strings, or ""
// when there are none. Emojis are ordered by descending count then emoji.
//
// It mirrors the bot-layer formatter; the two live in different packages so the
// DB-side string builders (recent-message context) can annotate without an
// import cycle.
func formatReactionsTag(reactionsJSON string) string {
	if reactionsJSON == "" {
		return ""
	}
	counts := map[string]int{}
	if err := json.Unmarshal([]byte(reactionsJSON), &counts); err != nil {
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

// FormatReactionsDisplay renders a stored reactions JSON map (emoji→count) as a
// human-friendly inline string like "👍 3   🔥 1" for the web UI, or "" when
// there are none. Emojis are ordered by descending count then emoji.
func FormatReactionsDisplay(reactionsJSON string) string {
	if reactionsJSON == "" {
		return ""
	}
	counts := map[string]int{}
	if err := json.Unmarshal([]byte(reactionsJSON), &counts); err != nil {
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
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, it.Emoji+" "+strconv.Itoa(it.Count))
	}
	return strings.Join(parts, "   ")
}
