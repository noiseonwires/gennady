// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	tgbotapi "gennadium/internal/telegram"
)

// callbackHandler is the signature every inline-keyboard action handler must
// implement. The `parts` slice is the underscore-split callback data, with
// parts[0] being the action prefix used for registry lookup.
type callbackHandler func(b *Bot, query *tgbotapi.CallbackQuery, parts []string)

// callbackRegistry maps an action prefix (the part of callback_data before the
// first underscore) to the handler that should process it.
//
// Adding a new inline-keyboard action is a one-line change here, plus the
// handler function itself. Handlers must validate parts length before use
// (see parseModCallbackIDs for the standard ID-tuple validation).
var callbackRegistry = map[string]callbackHandler{
	// Warning actions
	"warn":    (*Bot).handleWarningAction,
	"delwarn": (*Bot).handleDeleteWarningAction,

	// Regular mute actions (Telegram-restricted)
	"mute":        (*Bot).handleMuteAction,
	"muteask":     (*Bot).handleMuteAskConfirmation,
	"muteconfirm": (*Bot).handleMuteConfirmed,
	"mutecancel":  (*Bot).handleMuteCancelled,
	"unmute":      (*Bot).handleUnmuteAction,

	// Cruel-mute actions (silent auto-delete, no Telegram restriction)
	"cmute":        (*Bot).handleCruelMuteAction,
	"cmuteask":     (*Bot).handleCruelMuteAskConfirmation,
	"cmuteconfirm": (*Bot).handleCruelMuteConfirmed,
	"cmutecancel":  (*Bot).handleCruelMuteCancelled,

	// Message-level actions
	"delete":  (*Bot).handleDeleteAction,
	"restore": (*Bot).handleRestoreAction,
	"notbad":  (*Bot).handleNotBadAction,
	"delmsg":  (*Bot).handleDeleteUserMessagesAction,

	// Misc
	"admin": (*Bot).handleAdminAction,
}
