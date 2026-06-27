// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

package telegram

import "strings"

// Update is a neutral representation of a single incoming Telegram update. Only
// the update kinds the bot reacts to are modeled.
type Update struct {
	UpdateID             int64
	Message              *Message
	EditedMessage        *Message
	ChannelPost          *Message
	EditedChannelPost    *Message
	CallbackQuery        *CallbackQuery
	MessageReaction      *MessageReactionUpdated
	MessageReactionCount *MessageReactionCountUpdated
}

// CallbackQuery is a neutral representation of an inline-keyboard callback.
type CallbackQuery struct {
	ID      string
	From    *User
	Message *Message
	Data    string
}

// MessageReactionUpdated is a neutral per-user reaction change (who reacted and
// the before/after emoji sets). Requires the bot to be a chat administrator.
// Custom-emoji and paid reactions are dropped - only standard emoji are kept.
type MessageReactionUpdated struct {
	Chat        Chat
	MessageID   int
	User        *User
	OldReaction []string // emoji present before the change
	NewReaction []string // emoji present after the change
}

// MessageReactionCountUpdated is the anonymous aggregate emoji→count for a
// message. Does not require admin; sent (possibly delayed) for all chats.
type MessageReactionCountUpdated struct {
	Chat      Chat
	MessageID int
	Reactions []ReactionCount
}

// ReactionCount is a single emoji's total on a message.
type ReactionCount struct {
	Emoji string
	Count int
}

// MessageEntity is a neutral text entity (mention, bot_command, blockquote, …).
type MessageEntity struct {
	Type   string
	Offset int
	Length int
}

// TextQuote is the part of a message that the sender chose to quote when
// replying - the precise highlighted span rather than the whole parent message.
type TextQuote struct {
	// Text is the quoted text content.
	Text string
	// IsManual is true when the quote was edited by the sender after selection.
	IsManual bool
}

// Sticker carries the only sticker field the bot reads.
type Sticker struct {
	Emoji string
}

// Dice carries the only dice field the bot reads.
type Dice struct {
	Emoji string
}

// The following media markers exist only so the bot can detect the presence of
// a given media kind on an inbound message. They intentionally carry no fields.
type (
	Animation struct{}
	Video     struct{}
	VideoNote struct{}
	Voice     struct{}
	Audio     struct{}
	Document  struct{}
	Poll      struct{}
	Location  struct{}
	Contact   struct{}
)

// IsCommand reports whether the message starts with a bot command entity. It
// mirrors the behaviour of the previous Telegram library's Message.IsCommand.
func (m *Message) IsCommand() bool {
	if m == nil || len(m.Entities) == 0 {
		return false
	}
	e := m.Entities[0]
	return e.Offset == 0 && e.Type == "bot_command"
}

// Command returns the command (without the leading slash and without any
// @botname suffix), or an empty string when the message is not a command. It
// mirrors the previous library's Message.Command.
func (m *Message) Command() string {
	cmd := m.commandWithAt()
	if i := strings.Index(cmd, "@"); i != -1 {
		cmd = cmd[:i]
	}
	return cmd
}

func (m *Message) commandWithAt() string {
	if !m.IsCommand() {
		return ""
	}
	entity := m.Entities[0]
	if entity.Length <= 1 || entity.Length > len(m.Text) {
		return ""
	}
	return m.Text[1:entity.Length]
}

// CommandArguments returns the text following the leading bot command, with
// surrounding whitespace trimmed, or an empty string when the message is not a
// command or carries no arguments. It mirrors the previous library's
// Message.CommandArguments.
func (m *Message) CommandArguments() string {
	if m == nil || !m.IsCommand() {
		return ""
	}
	entity := m.Entities[0]
	if entity.Length >= len(m.Text) {
		return ""
	}
	return strings.TrimSpace(m.Text[entity.Length:])
}
