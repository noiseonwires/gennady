// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

// Package tgadapter implements the telegram.Client port over the
// github.com/go-telegram/bot library and translates that library's inbound
// update/model types into the neutral telegram.* types.
//
// It is the single place in the codebase that knows about a concrete Telegram
// client library. To move to a different library, write a sibling adapter
// implementing telegram.Client (plus an inbound mapping); no business logic
// needs to change.
package tgadapter

import (
	"context"
	"errors"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"gennadium/internal/telegram"
)

// Adapter wraps a *tgbot.Bot and implements telegram.Client.
type Adapter struct {
	api *tgbot.Bot
}

// New wraps an initialized *tgbot.Bot. api may be nil; callers must guard usage
// accordingly (the bot does not issue outbound calls when Telegram is disabled).
func New(api *tgbot.Bot) *Adapter {
	return &Adapter{api: api}
}

var _ telegram.Client = (*Adapter)(nil)

// ctx returns the context used for outbound API calls. The underlying library
// applies its own HTTP timeout, so a background context is sufficient.
func (a *Adapter) ctx() context.Context { return context.Background() }

// wrapErr converts the library's error values into the neutral
// telegram.APIError so business logic can inspect the HTTP-equivalent status
// code and rate-limit hint without importing a Telegram library.
func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	var tooMany *tgbot.TooManyRequestsError
	if errors.As(err, &tooMany) {
		return &telegram.APIError{Code: 429, Message: tooMany.Message, RetryAfter: tooMany.RetryAfter}
	}
	switch {
	case errors.Is(err, tgbot.ErrorForbidden):
		return &telegram.APIError{Code: 403, Message: err.Error()}
	case errors.Is(err, tgbot.ErrorBadRequest):
		return &telegram.APIError{Code: 400, Message: err.Error()}
	case errors.Is(err, tgbot.ErrorUnauthorized):
		return &telegram.APIError{Code: 401, Message: err.Error()}
	case errors.Is(err, tgbot.ErrorNotFound):
		return &telegram.APIError{Code: 404, Message: err.Error()}
	case errors.Is(err, tgbot.ErrorConflict):
		return &telegram.APIError{Code: 409, Message: err.Error()}
	}
	return err
}

// --- outbound request/response mapping ---------------------------------------

func toMarkup(kb *telegram.InlineKeyboard) models.ReplyMarkup {
	if kb == nil {
		return nil
	}
	rows := make([][]models.InlineKeyboardButton, 0, len(kb.Rows))
	for _, row := range kb.Rows {
		out := make([]models.InlineKeyboardButton, 0, len(row))
		for _, btn := range row {
			if btn.URL != "" {
				out = append(out, models.InlineKeyboardButton{Text: btn.Text, URL: btn.URL})
			} else {
				out = append(out, models.InlineKeyboardButton{Text: btn.Text, CallbackData: btn.CallbackData})
			}
		}
		rows = append(rows, out)
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func linkPreviewOptions(disable bool) *models.LinkPreviewOptions {
	if !disable {
		return nil
	}
	return &models.LinkPreviewOptions{IsDisabled: tgbot.True()}
}

func replyParameters(replyToMessageID int) *models.ReplyParameters {
	if replyToMessageID == 0 {
		return nil
	}
	return &models.ReplyParameters{MessageID: replyToMessageID}
}

// toSentMessage maps a library message returned from a send/edit call into the
// neutral telegram.Message (the subset the bot reads from such responses).
func toSentMessage(m *models.Message) telegram.Message {
	if m == nil {
		return telegram.Message{}
	}
	out := telegram.Message{
		MessageID:       m.ID,
		Chat:            toChat(m.Chat),
		From:            toUser(m.From),
		Text:            m.Text,
		Date:            m.Date,
		MessageThreadID: m.MessageThreadID,
	}
	if m.ReplyToMessage != nil {
		out.ReplyToMessageID = m.ReplyToMessage.ID
	}
	return out
}

func toChat(c models.Chat) telegram.Chat {
	return telegram.Chat{
		ID:        c.ID,
		Type:      string(c.Type),
		Title:     c.Title,
		Username:  c.Username,
		FirstName: c.FirstName,
		LastName:  c.LastName,
		IsForum:   c.IsForum,
	}
}

func toUser(u *models.User) *telegram.User {
	if u == nil {
		return nil
	}
	return &telegram.User{
		ID:        u.ID,
		IsBot:     u.IsBot,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		Username:  u.Username,
	}
}

// --- telegram.Client implementation ------------------------------------------

// SendMessage implements telegram.Client.
func (a *Adapter) SendMessage(p telegram.SendMessageParams) (telegram.Message, error) {
	sent, err := a.api.SendMessage(a.ctx(), &tgbot.SendMessageParams{
		ChatID:              p.ChatID,
		Text:                p.Text,
		ParseMode:           models.ParseMode(p.ParseMode),
		MessageThreadID:     p.MessageThreadID,
		ReplyParameters:     replyParameters(p.ReplyToMessageID),
		LinkPreviewOptions:  linkPreviewOptions(p.DisableWebPagePreview),
		DisableNotification: p.DisableNotification,
		ReplyMarkup:         toMarkup(p.Keyboard),
	})
	if err != nil {
		return telegram.Message{}, wrapErr(err)
	}
	return toSentMessage(sent), nil
}

// EditMessageText implements telegram.Client.
func (a *Adapter) EditMessageText(p telegram.EditMessageTextParams) (telegram.Message, error) {
	sent, err := a.api.EditMessageText(a.ctx(), &tgbot.EditMessageTextParams{
		ChatID:             p.ChatID,
		MessageID:          p.MessageID,
		Text:               p.Text,
		ParseMode:          models.ParseMode(p.ParseMode),
		LinkPreviewOptions: linkPreviewOptions(p.DisableWebPagePreview),
		ReplyMarkup:        toMarkup(p.Keyboard),
	})
	if err != nil {
		return telegram.Message{}, wrapErr(err)
	}
	return toSentMessage(sent), nil
}

// EditMessageReplyMarkup implements telegram.Client.
func (a *Adapter) EditMessageReplyMarkup(p telegram.EditMessageReplyMarkupParams) (telegram.Message, error) {
	markup := toMarkup(p.Keyboard)
	if markup == nil {
		markup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}
	}
	sent, err := a.api.EditMessageReplyMarkup(a.ctx(), &tgbot.EditMessageReplyMarkupParams{
		ChatID:      p.ChatID,
		MessageID:   p.MessageID,
		ReplyMarkup: markup,
	})
	if err != nil {
		return telegram.Message{}, wrapErr(err)
	}
	return toSentMessage(sent), nil
}

// DeleteMessage implements telegram.Client.
func (a *Adapter) DeleteMessage(chatID int64, messageID int) error {
	_, err := a.api.DeleteMessage(a.ctx(), &tgbot.DeleteMessageParams{ChatID: chatID, MessageID: messageID})
	return wrapErr(err)
}

// RestrictChatMember implements telegram.Client.
func (a *Adapter) RestrictChatMember(p telegram.RestrictChatMemberParams) error {
	perm := p.Permissions
	_, err := a.api.RestrictChatMember(a.ctx(), &tgbot.RestrictChatMemberParams{
		ChatID:    p.ChatID,
		UserID:    p.UserID,
		UntilDate: int(p.UntilDate),
		Permissions: &models.ChatPermissions{
			CanSendMessages:       perm.CanSendMessages,
			CanSendAudios:         perm.CanSendMedia,
			CanSendDocuments:      perm.CanSendMedia,
			CanSendPhotos:         perm.CanSendMedia,
			CanSendVideos:         perm.CanSendMedia,
			CanSendVideoNotes:     perm.CanSendMedia,
			CanSendVoiceNotes:     perm.CanSendMedia,
			CanSendPolls:          perm.CanSendPolls,
			CanSendOtherMessages:  perm.CanSendOther,
			CanAddWebPagePreviews: perm.CanAddWebPagePreviews,
			CanChangeInfo:         perm.CanChangeInfo,
			CanInviteUsers:        perm.CanInviteUsers,
			CanPinMessages:        perm.CanPinMessages,
		},
	})
	return wrapErr(err)
}

// BanChatMember implements telegram.Client.
func (a *Adapter) BanChatMember(chatID, userID int64) error {
	_, err := a.api.BanChatMember(a.ctx(), &tgbot.BanChatMemberParams{ChatID: chatID, UserID: userID})
	return wrapErr(err)
}

// UnbanChatMember implements telegram.Client.
func (a *Adapter) UnbanChatMember(chatID, userID int64, onlyIfBanned bool) error {
	_, err := a.api.UnbanChatMember(a.ctx(), &tgbot.UnbanChatMemberParams{
		ChatID:       chatID,
		UserID:       userID,
		OnlyIfBanned: onlyIfBanned,
	})
	return wrapErr(err)
}

// AnswerCallback implements telegram.Client.
func (a *Adapter) AnswerCallback(callbackQueryID, text string) error {
	_, err := a.api.AnswerCallbackQuery(a.ctx(), &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQueryID,
		Text:            text,
	})
	return wrapErr(err)
}

// SetMessageReaction implements telegram.Client.
func (a *Adapter) SetMessageReaction(chatID int64, messageID int, emoji string) error {
	var reaction []models.ReactionType
	if emoji != "" {
		reaction = []models.ReactionType{{
			Type:              models.ReactionTypeTypeEmoji,
			ReactionTypeEmoji: &models.ReactionTypeEmoji{Emoji: emoji},
		}}
	}
	_, err := a.api.SetMessageReaction(a.ctx(), &tgbot.SetMessageReactionParams{
		ChatID:    chatID,
		MessageID: messageID,
		Reaction:  reaction,
	})
	return wrapErr(err)
}

// GetChat implements telegram.Client.
func (a *Adapter) GetChat(chatID int64) (telegram.Chat, error) {
	chat, err := a.api.GetChat(a.ctx(), &tgbot.GetChatParams{ChatID: chatID})
	if err != nil {
		return telegram.Chat{}, wrapErr(err)
	}
	return chatFromFullInfo(chat), nil
}

// GetChatFull implements telegram.Client.
func (a *Adapter) GetChatFull(chatID int64) (telegram.ChatFull, error) {
	chat, err := a.api.GetChat(a.ctx(), &tgbot.GetChatParams{ChatID: chatID})
	if err != nil {
		return telegram.ChatFull{}, wrapErr(err)
	}
	out := telegram.ChatFull{
		Chat:        chatFromFullInfo(chat),
		Bio:         chat.Bio,
		Description: chat.Description,
	}
	if chat.PersonalChat != nil {
		out.PersonalChatID = chat.PersonalChat.ID
	}
	if chat.Photo != nil {
		out.PhotoBigFileID = chat.Photo.BigFileID
	}
	return out, nil
}

// GetChatByUsername implements telegram.Client.
func (a *Adapter) GetChatByUsername(username string) (telegram.Chat, error) {
	chat, err := a.api.GetChat(a.ctx(), &tgbot.GetChatParams{ChatID: "@" + username})
	if err != nil {
		return telegram.Chat{}, wrapErr(err)
	}
	return chatFromFullInfo(chat), nil
}

func chatFromFullInfo(c *models.ChatFullInfo) telegram.Chat {
	if c == nil {
		return telegram.Chat{}
	}
	return telegram.Chat{
		ID:        c.ID,
		Type:      string(c.Type),
		Title:     c.Title,
		Username:  c.Username,
		FirstName: c.FirstName,
		LastName:  c.LastName,
		IsForum:   c.IsForum,
	}
}

// GetChatMember implements telegram.Client.
func (a *Adapter) GetChatMember(chatID, userID int64) (telegram.ChatMember, error) {
	member, err := a.api.GetChatMember(a.ctx(), &tgbot.GetChatMemberParams{ChatID: chatID, UserID: userID})
	if err != nil {
		return telegram.ChatMember{}, wrapErr(err)
	}
	return chatMemberFromModel(member), nil
}

func chatMemberFromModel(m *models.ChatMember) telegram.ChatMember {
	if m == nil {
		return telegram.ChatMember{}
	}
	switch m.Type {
	case models.ChatMemberTypeOwner:
		return telegram.ChatMember{User: toUser(m.Owner.User), Status: telegram.StatusCreator}
	case models.ChatMemberTypeAdministrator:
		u := m.Administrator.User
		return telegram.ChatMember{User: toUser(&u), Status: telegram.StatusAdministrator}
	case models.ChatMemberTypeMember:
		return telegram.ChatMember{User: toUser(m.Member.User), Status: telegram.StatusMember}
	case models.ChatMemberTypeRestricted:
		return telegram.ChatMember{User: toUser(m.Restricted.User), Status: telegram.StatusRestricted}
	case models.ChatMemberTypeLeft:
		return telegram.ChatMember{User: toUser(m.Left.User), Status: telegram.StatusLeft}
	case models.ChatMemberTypeBanned:
		return telegram.ChatMember{User: toUser(m.Banned.User), Status: telegram.StatusKicked}
	}
	return telegram.ChatMember{Status: string(m.Type)}
}

// GetFile implements telegram.Client.
func (a *Adapter) GetFile(fileID string) (telegram.File, error) {
	file, err := a.api.GetFile(a.ctx(), &tgbot.GetFileParams{FileID: fileID})
	if err != nil {
		return telegram.File{}, wrapErr(err)
	}
	return telegram.File{
		FileID:      file.FileID,
		FilePath:    file.FilePath,
		FileSize:    int(file.FileSize),
		DownloadURL: a.api.FileDownloadLink(file),
	}, nil
}

// GetUserProfilePhotos implements telegram.Client.
func (a *Adapter) GetUserProfilePhotos(userID int64, limit int) (telegram.UserProfilePhotos, error) {
	photos, err := a.api.GetUserProfilePhotos(a.ctx(), &tgbot.GetUserProfilePhotosParams{
		UserID: userID,
		Offset: 0,
		Limit:  limit,
	})
	if err != nil {
		return telegram.UserProfilePhotos{}, wrapErr(err)
	}
	out := telegram.UserProfilePhotos{TotalCount: photos.TotalCount}
	for _, sizes := range photos.Photos {
		row := make([]telegram.PhotoSize, 0, len(sizes))
		for _, s := range sizes {
			row = append(row, telegram.PhotoSize{
				FileID:   s.FileID,
				Width:    s.Width,
				Height:   s.Height,
				FileSize: int(s.FileSize),
			})
		}
		out.Photos = append(out.Photos, row)
	}
	return out, nil
}

// DeleteWebhook implements telegram.Client.
func (a *Adapter) DeleteWebhook(dropPending bool) error {
	_, err := a.api.DeleteWebhook(a.ctx(), &tgbot.DeleteWebhookParams{DropPendingUpdates: dropPending})
	return wrapErr(err)
}
