// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"

	"gennadium/internal/i18n"
	"gennadium/internal/telegram"
)

// muteDurationOption describes a single mute-duration button shown in the
// admin moderation keyboard. It is the single source of truth for callback
// strings, i18n labels and durations.
type muteDurationOption struct {
	// Minutes is the mute duration; 0 means "forever".
	Minutes int
	// LabelKey is the i18n key for the button label.
	LabelKey string
	// Ask is true when the action requires a confirmation step
	// (callback prefix "muteask_" / "cmuteask_" instead of "mute_" / "cmute_").
	Ask bool
}

// regularMuteDurations are the durations offered for a standard Telegram mute.
// Layout into rows is performed by createModerationKeyboard.
var regularMuteDurations = []muteDurationOption{
	{Minutes: 15, LabelKey: "btn.mute_15m"},
	{Minutes: 30, LabelKey: "btn.mute_30m"},
	{Minutes: 60, LabelKey: "btn.mute_1h"},
	{Minutes: 180, LabelKey: "btn.mute_3h"},
	{Minutes: 1440, LabelKey: "btn.mute_24h"},
	{Minutes: 43200, LabelKey: "btn.mute_30d", Ask: true},
	{Minutes: 0, LabelKey: "btn.mute_forever", Ask: true},
}

// cruelMuteDurations are the durations offered for cruel-mute (silent
// auto-deletion, no Telegram restriction).
var cruelMuteDurations = []muteDurationOption{
	{Minutes: 60, LabelKey: "btn.cmute_1h"},
	{Minutes: 1440, LabelKey: "btn.cmute_24h"},
	{Minutes: 0, LabelKey: "btn.cmute_forever", Ask: true},
}

// muteCallbackData builds the callback-data string for a mute button given the
// callback prefixes for normal vs. confirmation flow.
func muteCallbackData(opt muteDurationOption, normalPrefix, askPrefix string, chatID int64, messageID int, userID int64, threadID int) string {
	prefix := normalPrefix
	if opt.Ask {
		prefix = askPrefix
	}
	return fmt.Sprintf("%s_%d_%d_%d_%d_%d", prefix, opt.Minutes, chatID, messageID, userID, threadID)
}

// muteButton builds a single inline keyboard button for the given mute option.
func muteButton(opt muteDurationOption, normalPrefix, askPrefix string, chatID int64, messageID int, userID int64, threadID int) telegram.InlineButton {
	return telegram.NewButton(
		i18n.T(opt.LabelKey),
		muteCallbackData(opt, normalPrefix, askPrefix, chatID, messageID, userID, threadID),
	)
}

// regularMuteRows lays out the regular-mute buttons into rows for display in
// the moderation keyboard. The layout is fixed (2 + 2 + 1 + 2 columns).
func regularMuteRows(chatID int64, messageID int, userID int64, threadID int) [][]telegram.InlineButton {
	btn := func(opt muteDurationOption) telegram.InlineButton {
		return muteButton(opt, "mute", "muteask", chatID, messageID, userID, threadID)
	}
	// Layout: pairs of two until the last 3 which span two rows (1 + 2) to
	// keep the destructive "forever" option separated.
	d := regularMuteDurations
	return [][]telegram.InlineButton{
		telegram.NewRow(btn(d[0]), btn(d[1])),
		telegram.NewRow(btn(d[2]), btn(d[3])),
		telegram.NewRow(btn(d[4])),
		telegram.NewRow(btn(d[5]), btn(d[6])),
	}
}
