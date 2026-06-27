// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import "time"

// Telegram protocol limits.
const (
	// MaxTelegramMessageLength is the maximum number of characters allowed in
	// a single Telegram text message.
	MaxTelegramMessageLength = 4096

	// CruelMuteLogPreviewLen is the maximum number of characters kept when
	// logging a doomed message under cruel mute.
	CruelMuteLogPreviewLen = 500
)

// Mute durations.
//
// ForeverMuteDuration is the wall-clock duration we store on a "forever" mute
// record: large enough that no human will ever see it expire, small enough to
// avoid time.Time overflow.
//
// ForeverMuteDetectionThreshold is used to *detect* a forever mute when reading
// records back: any active mute with an unmute time more than this far in the
// future is treated as permanent. It must be smaller than ForeverMuteDuration
// and larger than any user-selectable mute duration.
const (
	ForeverMuteDuration           = 100 * 365 * 24 * time.Hour
	ForeverMuteDetectionThreshold = 10 * 365 * 24 * time.Hour
)
