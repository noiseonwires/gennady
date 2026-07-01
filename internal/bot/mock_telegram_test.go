// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"sync"

	"gennadium/internal/telegram"
)

// mockTelegram is an in-memory telegram.Client used in tests. It records all
// outbound calls and lets each method's behaviour be customised via function
// hooks. The zero value is usable; unset hooks return zero values / nil errors.
type mockTelegram struct {
	SentMessages   []telegram.SendMessageParams
	EditedTexts    []telegram.EditMessageTextParams
	EditedMarkups  []telegram.EditMessageReplyMarkupParams
	DeletedIDs     [][2]int64 // {chatID, messageID}
	Restrictions   []telegram.RestrictChatMemberParams
	Bans           [][2]int64
	Unbans         [][2]int64
	Answered       []string
	Reactions      []reactionCall
	WebhookDeletes int

	// mu guards the recorded slices/counters so tests that drive async feature
	// goroutines (summaries, link/creative replies) can observe calls without a
	// data race. Synchronous tests may still read the exported fields directly.
	mu sync.Mutex

	// Optional hooks to control return values / inject errors.
	SendFunc        func(p telegram.SendMessageParams) (telegram.Message, error)
	EditFunc        func(p telegram.EditMessageTextParams) (telegram.Message, error)
	DeleteFunc      func(chatID int64, messageID int) error
	RestrictFunc    func(p telegram.RestrictChatMemberParams) error
	GetChatFunc     func(chatID int64) (telegram.Chat, error)
	GetChatFullFunc func(chatID int64) (telegram.ChatFull, error)
	GetMemberFunc   func(chatID, userID int64) (telegram.ChatMember, error)
	GetFileFunc     func(fileID string) (telegram.File, error)
	GetPhotosFunc   func(userID int64, limit int) (telegram.UserProfilePhotos, error)
	nextMessageID   int
}

type reactionCall struct {
	ChatID    int64
	MessageID int
	Emoji     string
}

var _ telegram.Client = (*mockTelegram)(nil)

func (m *mockTelegram) SendMessage(p telegram.SendMessageParams) (telegram.Message, error) {
	m.mu.Lock()
	m.SentMessages = append(m.SentMessages, p)
	m.nextMessageID++
	id := m.nextMessageID
	m.mu.Unlock()
	if m.SendFunc != nil {
		return m.SendFunc(p)
	}
	return telegram.Message{
		MessageID: id,
		Chat:      telegram.Chat{ID: p.ChatID},
		Text:      p.Text,
	}, nil
}

func (m *mockTelegram) EditMessageText(p telegram.EditMessageTextParams) (telegram.Message, error) {
	m.mu.Lock()
	m.EditedTexts = append(m.EditedTexts, p)
	m.mu.Unlock()
	if m.EditFunc != nil {
		return m.EditFunc(p)
	}
	return telegram.Message{MessageID: p.MessageID, Chat: telegram.Chat{ID: p.ChatID}, Text: p.Text}, nil
}

func (m *mockTelegram) EditMessageReplyMarkup(p telegram.EditMessageReplyMarkupParams) (telegram.Message, error) {
	m.mu.Lock()
	m.EditedMarkups = append(m.EditedMarkups, p)
	m.mu.Unlock()
	return telegram.Message{MessageID: p.MessageID, Chat: telegram.Chat{ID: p.ChatID}}, nil
}

func (m *mockTelegram) DeleteMessage(chatID int64, messageID int) error {
	m.mu.Lock()
	m.DeletedIDs = append(m.DeletedIDs, [2]int64{chatID, int64(messageID)})
	m.mu.Unlock()
	if m.DeleteFunc != nil {
		return m.DeleteFunc(chatID, messageID)
	}
	return nil
}

func (m *mockTelegram) RestrictChatMember(p telegram.RestrictChatMemberParams) error {
	m.mu.Lock()
	m.Restrictions = append(m.Restrictions, p)
	m.mu.Unlock()
	if m.RestrictFunc != nil {
		return m.RestrictFunc(p)
	}
	return nil
}

func (m *mockTelegram) BanChatMember(chatID, userID int64) error {
	m.mu.Lock()
	m.Bans = append(m.Bans, [2]int64{chatID, userID})
	m.mu.Unlock()
	return nil
}

func (m *mockTelegram) UnbanChatMember(chatID, userID int64, onlyIfBanned bool) error {
	m.mu.Lock()
	m.Unbans = append(m.Unbans, [2]int64{chatID, userID})
	m.mu.Unlock()
	return nil
}

func (m *mockTelegram) AnswerCallback(callbackQueryID, text string) error {
	m.mu.Lock()
	m.Answered = append(m.Answered, callbackQueryID)
	m.mu.Unlock()
	return nil
}

func (m *mockTelegram) SetMessageReaction(chatID int64, messageID int, emoji string) error {
	m.mu.Lock()
	m.Reactions = append(m.Reactions, reactionCall{ChatID: chatID, MessageID: messageID, Emoji: emoji})
	m.mu.Unlock()
	return nil
}

func (m *mockTelegram) GetChat(chatID int64) (telegram.Chat, error) {
	if m.GetChatFunc != nil {
		return m.GetChatFunc(chatID)
	}
	return telegram.Chat{ID: chatID}, nil
}

func (m *mockTelegram) GetChatFull(chatID int64) (telegram.ChatFull, error) {
	if m.GetChatFullFunc != nil {
		return m.GetChatFullFunc(chatID)
	}
	return telegram.ChatFull{Chat: telegram.Chat{ID: chatID}}, nil
}

func (m *mockTelegram) GetChatByUsername(username string) (telegram.Chat, error) {
	return telegram.Chat{Username: username}, nil
}

func (m *mockTelegram) GetChatMember(chatID, userID int64) (telegram.ChatMember, error) {
	if m.GetMemberFunc != nil {
		return m.GetMemberFunc(chatID, userID)
	}
	return telegram.ChatMember{User: &telegram.User{ID: userID}, Status: telegram.StatusMember}, nil
}

func (m *mockTelegram) GetFile(fileID string) (telegram.File, error) {
	if m.GetFileFunc != nil {
		return m.GetFileFunc(fileID)
	}
	return telegram.File{FileID: fileID}, nil
}

func (m *mockTelegram) GetUserProfilePhotos(userID int64, limit int) (telegram.UserProfilePhotos, error) {
	if m.GetPhotosFunc != nil {
		return m.GetPhotosFunc(userID, limit)
	}
	return telegram.UserProfilePhotos{}, nil
}

func (m *mockTelegram) DeleteWebhook(dropPending bool) error {
	m.mu.Lock()
	m.WebhookDeletes++
	m.mu.Unlock()
	return nil
}

// sentCount returns the number of recorded SendMessage calls under the lock,
// for use in async tests (require.Eventually / require.Never).
func (m *mockTelegram) sentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.SentMessages)
}

// sentMessagesCopy returns a snapshot of the recorded SendMessage calls under
// the lock, for content assertions in async tests.
func (m *mockTelegram) sentMessagesCopy() []telegram.SendMessageParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]telegram.SendMessageParams(nil), m.SentMessages...)
}
