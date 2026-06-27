// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

// Package telegram defines a library-agnostic port for the Telegram Bot API.
//
// The goal is to keep the bot's business logic free of any direct dependency
// on a specific Telegram client library. All outbound operations (sending and
// editing messages, restricting users, reactions, lookups, …) are expressed in
// terms of the neutral request/response types and the Client interface defined
// here. A concrete adapter (see the tgadapter subpackage) translates these
// neutral types to and from a particular library's types.
//
// Swapping the underlying Telegram library later - e.g. from
// github.com/go-telegram-bot-api/telegram-bot-api to github.com/go-telegram/bot
// - only requires writing a new adapter that implements Client; no business
// logic changes.
package telegram

import "fmt"

// APIError is a neutral, structured Telegram API error. Adapters convert their
// library-specific error type into this so business logic can inspect the HTTP
// status code and rate-limit hint without importing a Telegram library.
type APIError struct {
	// Code is the HTTP-equivalent status code (e.g. 400, 429, 403).
	Code int
	// Message is the human-readable description returned by Telegram.
	Message string
	// RetryAfter is the suggested wait, in seconds, on a 429 response (0 if absent).
	RetryAfter int
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return fmt.Sprintf("telegram api error %d: %s", e.Code, e.Message)
}

// ParseMode selects how Telegram interprets message text formatting.
type ParseMode = string

const (
	// ParseModeNone sends text without any markup parsing.
	ParseModeNone ParseMode = ""
	// ParseModeMarkdown uses the legacy Markdown formatting.
	ParseModeMarkdown ParseMode = "Markdown"
	// ParseModeMarkdownV2 uses MarkdownV2 formatting.
	ParseModeMarkdownV2 ParseMode = "MarkdownV2"
	// ParseModeHTML uses HTML formatting.
	ParseModeHTML ParseMode = "HTML"
)

// Chat member status values as returned by the Telegram API.
const (
	StatusCreator       = "creator"
	StatusAdministrator = "administrator"
	StatusMember        = "member"
	StatusRestricted    = "restricted"
	StatusLeft          = "left"
	StatusKicked        = "kicked"
)

// User is a neutral representation of a Telegram user.
type User struct {
	ID        int64
	IsBot     bool
	FirstName string
	LastName  string
	Username  string
}

// Chat is a neutral representation of a Telegram chat.
type Chat struct {
	ID        int64
	Type      string
	Title     string
	Username  string
	FirstName string
	LastName  string
	IsForum   bool
}

// ChatFull is the extended metadata returned by getChat (a superset of Chat).
// It carries the fields used for analyzing a user and their linked personal
// channel: bio, description, the personal-channel id, and the profile photo's
// largest file id (empty when none).
type ChatFull struct {
	Chat
	Bio            string
	Description    string
	PersonalChatID int64
	PhotoBigFileID string
}

// Message is a neutral representation of a Telegram message. It carries the
// subset of fields the bot reads from inbound updates and from send/edit
// responses.
type Message struct {
	MessageID int
	Chat      Chat
	From      *User
	Text      string
	Caption   string
	Date      int
	// EditDate is the last edit timestamp (unix), or 0 if never edited.
	EditDate int
	// MessageThreadID is the forum topic id, when present.
	MessageThreadID int
	// ReplyToMessageID is the id of the message this one replies to, or 0.
	// It is populated on send/edit responses.
	ReplyToMessageID int
	// ReplyToMessage is the inbound message this one replies to, or nil.
	ReplyToMessage *Message
	// Quote is the specific span the sender highlighted when replying, or nil
	// when they replied without selecting a sub-quote.
	Quote *TextQuote
	// Entities are the inbound text entities (mentions, commands, blockquotes…).
	Entities []MessageEntity
	// ReplyMarkup is the inbound inline keyboard attached to the message, or nil.
	ReplyMarkup *InlineKeyboard

	// Media presence. Only the fields the bot inspects are modeled; for most
	// media types a non-nil pointer simply signals "this kind of media exists".
	Photo     []PhotoSize
	Sticker   *Sticker
	Animation *Animation
	Video     *Video
	VideoNote *VideoNote
	Voice     *Voice
	Audio     *Audio
	Document  *Document
	Poll      *Poll
	Dice      *Dice
	Location  *Location
	Contact   *Contact

	// Forward origin, neutralized from the library's MessageOrigin union.
	ForwardFrom       *User
	ForwardFromChat   *Chat
	ForwardSenderName string

	// Service-message markers.
	NewChatMembers          []User
	LeftChatMember          *User
	NewChatTitle            string
	NewChatPhoto            bool
	DeleteChatPhoto         bool
	GroupChatCreated        bool
	SuperGroupChatCreated   bool
	ChannelChatCreated      bool
	MigrateToChatID         int64
	MigrateFromChatID       int64
	PinnedMessage           *Message
	Invoice                 bool
	SuccessfulPayment       bool
	ConnectedWebsite        string
	PassportData            bool
	ProximityAlertTriggered bool

	// ForumTopicCreated / ForumTopicEdited are the forum-topic service markers.
	// They are the only way a bot can learn a topic's human-readable name
	// (the Bot API exposes no getForumTopic / getForumTopics method), so the
	// bot harvests the name from these events to build its topic directory.
	ForumTopicCreated *ForumTopicCreated
	ForumTopicEdited  *ForumTopicEdited
}

// ForumTopicCreated is the neutral form of the forum_topic_created service
// message. Name is the topic title; the message's MessageThreadID is the
// topic id it refers to.
type ForumTopicCreated struct {
	Name string
}

// ForumTopicEdited is the neutral form of the forum_topic_edited service
// message. Name is the new topic title (empty when only the icon changed).
type ForumTopicEdited struct {
	Name string
}

// ChatMember is a neutral representation of a user's membership in a chat.
type ChatMember struct {
	User   *User
	Status string
}

// IsAdmin reports whether the member is the chat creator or an administrator.
func (m ChatMember) IsAdmin() bool {
	return m.Status == StatusCreator || m.Status == StatusAdministrator
}

// PhotoSize describes one size of a photo.
type PhotoSize struct {
	FileID   string
	Width    int
	Height   int
	FileSize int
}

// File is a neutral representation of a Telegram file, including a ready-to-use
// download URL (the adapter fills DownloadURL using the bot token).
type File struct {
	FileID      string
	FilePath    string
	FileSize    int
	DownloadURL string
}

// UserProfilePhotos holds a user's profile photos, each as a slice of sizes.
type UserProfilePhotos struct {
	TotalCount int
	Photos     [][]PhotoSize
}

// Permissions describes the messaging permissions applied to a restricted user.
type Permissions struct {
	CanSendMessages       bool
	CanSendMedia          bool
	CanSendPolls          bool
	CanSendOther          bool
	CanAddWebPagePreviews bool
	CanChangeInfo         bool
	CanInviteUsers        bool
	CanPinMessages        bool
}

// InlineButton is a neutral inline-keyboard button. Exactly one of CallbackData
// or URL is normally set.
type InlineButton struct {
	Text         string
	CallbackData string
	URL          string
}

// InlineKeyboard is a neutral inline keyboard: rows of buttons.
type InlineKeyboard struct {
	Rows [][]InlineButton
}

// NewButton builds a callback-data inline button.
func NewButton(text, callbackData string) InlineButton {
	return InlineButton{Text: text, CallbackData: callbackData}
}

// NewURLButton builds a URL inline button.
func NewURLButton(text, url string) InlineButton {
	return InlineButton{Text: text, URL: url}
}

// NewRow assembles a single keyboard row.
func NewRow(buttons ...InlineButton) []InlineButton {
	return buttons
}

// NewKeyboard assembles an inline keyboard from rows.
func NewKeyboard(rows ...[]InlineButton) InlineKeyboard {
	return InlineKeyboard{Rows: rows}
}

// SendMessageParams describes a sendMessage request.
type SendMessageParams struct {
	ChatID           int64
	Text             string
	ParseMode        ParseMode
	ReplyToMessageID int
	// MessageThreadID targets a forum topic (0 = the chat's main area). Use this
	// to post into a topic; do not abuse ReplyToMessageID for topic targeting.
	MessageThreadID       int
	DisableWebPagePreview bool
	DisableNotification   bool
	Keyboard              *InlineKeyboard
}

// EditMessageTextParams describes an editMessageText request.
type EditMessageTextParams struct {
	ChatID                int64
	MessageID             int
	Text                  string
	ParseMode             ParseMode
	DisableWebPagePreview bool
	Keyboard              *InlineKeyboard
}

// EditMessageReplyMarkupParams describes an editMessageReplyMarkup request.
type EditMessageReplyMarkupParams struct {
	ChatID    int64
	MessageID int
	Keyboard  *InlineKeyboard
}

// RestrictChatMemberParams describes a restrictChatMember request.
type RestrictChatMemberParams struct {
	ChatID      int64
	UserID      int64
	UntilDate   int64
	Permissions Permissions
}

// Client is the library-agnostic outbound Telegram API port.
type Client interface {
	// SendMessage sends a text message and returns the sent message.
	SendMessage(p SendMessageParams) (Message, error)
	// EditMessageText edits a message's text (and optionally its keyboard).
	EditMessageText(p EditMessageTextParams) (Message, error)
	// EditMessageReplyMarkup replaces a message's inline keyboard.
	EditMessageReplyMarkup(p EditMessageReplyMarkupParams) (Message, error)
	// DeleteMessage deletes a message.
	DeleteMessage(chatID int64, messageID int) error
	// RestrictChatMember restricts (mutes) or unrestricts a user.
	RestrictChatMember(p RestrictChatMemberParams) error
	// BanChatMember bans a user from a chat.
	BanChatMember(chatID, userID int64) error
	// UnbanChatMember unbans a user; when onlyIfBanned is true, only acts if banned.
	UnbanChatMember(chatID, userID int64, onlyIfBanned bool) error
	// AnswerCallback acknowledges a callback query (text optional).
	AnswerCallback(callbackQueryID, text string) error
	// SetMessageReaction sets a single emoji reaction; an empty emoji clears it.
	SetMessageReaction(chatID int64, messageID int, emoji string) error
	// GetChat fetches chat metadata by numeric id.
	GetChat(chatID int64) (Chat, error)
	// GetChatFull fetches extended chat metadata (bio, description, personal
	// channel, profile photo) by numeric id.
	GetChatFull(chatID int64) (ChatFull, error)
	// GetChatByUsername fetches chat metadata by public @username (without the @).
	GetChatByUsername(username string) (Chat, error)
	// GetChatMember fetches a user's membership in a chat.
	GetChatMember(chatID, userID int64) (ChatMember, error)
	// GetFile resolves a file id to a File with a download URL.
	GetFile(fileID string) (File, error)
	// GetUserProfilePhotos fetches up to limit of a user's profile photos.
	GetUserProfilePhotos(userID int64, limit int) (UserProfilePhotos, error)
	// DeleteWebhook removes the webhook; dropPending discards queued updates.
	DeleteWebhook(dropPending bool) error
}
