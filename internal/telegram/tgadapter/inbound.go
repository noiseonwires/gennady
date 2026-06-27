// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

package tgadapter

import (
	"github.com/go-telegram/bot/models"

	"gennadium/internal/telegram"
)

// ToUpdate maps a library update into the neutral telegram.Update consumed by
// the bot's business logic.
func ToUpdate(u *models.Update) telegram.Update {
	if u == nil {
		return telegram.Update{}
	}
	return telegram.Update{
		UpdateID:             u.ID,
		Message:              toInboundMessage(u.Message),
		EditedMessage:        toInboundMessage(u.EditedMessage),
		ChannelPost:          toInboundMessage(u.ChannelPost),
		EditedChannelPost:    toInboundMessage(u.EditedChannelPost),
		CallbackQuery:        toCallbackQuery(u.CallbackQuery),
		MessageReaction:      toMessageReaction(u.MessageReaction),
		MessageReactionCount: toMessageReactionCount(u.MessageReactionCount),
	}
}

// emojiReactions extracts the standard-emoji reactions from a reaction list,
// dropping custom-emoji and paid reactions (which have no comparable emoji).
func emojiReactions(in []models.ReactionType) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, r := range in {
		if r.Type == models.ReactionTypeTypeEmoji && r.ReactionTypeEmoji != nil {
			out = append(out, r.ReactionTypeEmoji.Emoji)
		}
	}
	return out
}

func toMessageReaction(r *models.MessageReactionUpdated) *telegram.MessageReactionUpdated {
	if r == nil {
		return nil
	}
	return &telegram.MessageReactionUpdated{
		Chat:        toChat(r.Chat),
		MessageID:   r.MessageID,
		User:        toUser(r.User),
		OldReaction: emojiReactions(r.OldReaction),
		NewReaction: emojiReactions(r.NewReaction),
	}
}

func toMessageReactionCount(r *models.MessageReactionCountUpdated) *telegram.MessageReactionCountUpdated {
	if r == nil {
		return nil
	}
	out := &telegram.MessageReactionCountUpdated{
		Chat:      toChat(r.Chat),
		MessageID: r.MessageID,
	}
	for _, rc := range r.Reactions {
		if rc.Type.Type == models.ReactionTypeTypeEmoji && rc.Type.ReactionTypeEmoji != nil {
			out.Reactions = append(out.Reactions, telegram.ReactionCount{
				Emoji: rc.Type.ReactionTypeEmoji.Emoji,
				Count: rc.TotalCount,
			})
		}
	}
	return out
}

func toCallbackQuery(q *models.CallbackQuery) *telegram.CallbackQuery {
	if q == nil {
		return nil
	}
	out := &telegram.CallbackQuery{
		ID:   q.ID,
		From: toUser(&q.From),
		Data: q.Data,
	}
	if q.Message.Message != nil {
		out.Message = toInboundMessage(q.Message.Message)
	}
	return out
}

// toInboundMessage performs the full inbound mapping (media presence, forward
// origin, service markers, reply target and inline keyboard).
func toInboundMessage(m *models.Message) *telegram.Message {
	if m == nil {
		return nil
	}
	out := &telegram.Message{
		MessageID:       m.ID,
		Chat:            toChat(m.Chat),
		From:            toUser(m.From),
		Text:            m.Text,
		Caption:         m.Caption,
		Date:            m.Date,
		EditDate:        m.EditDate,
		MessageThreadID: m.MessageThreadID,

		NewChatTitle:            m.NewChatTitle,
		NewChatPhoto:            len(m.NewChatPhoto) > 0,
		DeleteChatPhoto:         m.DeleteChatPhoto,
		GroupChatCreated:        m.GroupChatCreated,
		SuperGroupChatCreated:   m.SupergroupChatCreated,
		ChannelChatCreated:      m.ChannelChatCreated,
		MigrateToChatID:         m.MigrateToChatID,
		MigrateFromChatID:       m.MigrateFromChatID,
		ConnectedWebsite:        m.ConnectedWebsite,
		Invoice:                 m.Invoice != nil,
		SuccessfulPayment:       m.SuccessfulPayment != nil,
		PassportData:            m.PassportData != nil,
		ProximityAlertTriggered: m.ProximityAlertTriggered != nil,
	}

	if m.ReplyToMessage != nil {
		out.ReplyToMessage = toInboundMessage(m.ReplyToMessage)
	}
	if m.Quote != nil {
		out.Quote = &telegram.TextQuote{Text: m.Quote.Text, IsManual: m.Quote.IsManual}
	}
	out.Entities = toEntities(m.Entities)
	out.ReplyMarkup = toInboundKeyboard(m.ReplyMarkup)

	// Media presence.
	out.Photo = toPhotoSizes(m.Photo)
	if m.Sticker != nil {
		out.Sticker = &telegram.Sticker{Emoji: m.Sticker.Emoji}
	}
	if m.Dice != nil {
		out.Dice = &telegram.Dice{Emoji: m.Dice.Emoji}
	}
	if m.Animation != nil {
		out.Animation = &telegram.Animation{}
	}
	if m.Video != nil {
		out.Video = &telegram.Video{}
	}
	if m.VideoNote != nil {
		out.VideoNote = &telegram.VideoNote{}
	}
	if m.Voice != nil {
		out.Voice = &telegram.Voice{}
	}
	if m.Audio != nil {
		out.Audio = &telegram.Audio{}
	}
	if m.Document != nil {
		out.Document = &telegram.Document{}
	}
	if m.Poll != nil {
		out.Poll = &telegram.Poll{}
	}
	if m.Location != nil {
		out.Location = &telegram.Location{}
	}
	if m.Contact != nil {
		out.Contact = &telegram.Contact{}
	}

	// Service members.
	if len(m.NewChatMembers) > 0 {
		out.NewChatMembers = make([]telegram.User, 0, len(m.NewChatMembers))
		for i := range m.NewChatMembers {
			if u := toUser(&m.NewChatMembers[i]); u != nil {
				out.NewChatMembers = append(out.NewChatMembers, *u)
			}
		}
	}
	out.LeftChatMember = toUser(m.LeftChatMember)
	if m.PinnedMessage != nil && m.PinnedMessage.Message != nil {
		out.PinnedMessage = toInboundMessage(m.PinnedMessage.Message)
	}

	// Forum-topic service markers (source of human-readable topic names).
	if m.ForumTopicCreated != nil {
		out.ForumTopicCreated = &telegram.ForumTopicCreated{Name: m.ForumTopicCreated.Name}
	}
	if m.ForumTopicEdited != nil {
		out.ForumTopicEdited = &telegram.ForumTopicEdited{Name: m.ForumTopicEdited.Name}
	}

	// Forward origin.
	applyForwardOrigin(out, m.ForwardOrigin)

	return out
}

func applyForwardOrigin(out *telegram.Message, origin *models.MessageOrigin) {
	if origin == nil {
		return
	}
	switch origin.Type {
	case models.MessageOriginTypeUser:
		if origin.MessageOriginUser != nil {
			u := origin.MessageOriginUser.SenderUser
			out.ForwardFrom = toUser(&u)
		}
	case models.MessageOriginTypeHiddenUser:
		if origin.MessageOriginHiddenUser != nil {
			out.ForwardSenderName = origin.MessageOriginHiddenUser.SenderUserName
		}
	case models.MessageOriginTypeChat:
		if origin.MessageOriginChat != nil {
			c := toChat(origin.MessageOriginChat.SenderChat)
			out.ForwardFromChat = &c
		}
	case models.MessageOriginTypeChannel:
		if origin.MessageOriginChannel != nil {
			c := toChat(origin.MessageOriginChannel.Chat)
			out.ForwardFromChat = &c
		}
	}
}

func toEntities(in []models.MessageEntity) []telegram.MessageEntity {
	if len(in) == 0 {
		return nil
	}
	out := make([]telegram.MessageEntity, 0, len(in))
	for _, e := range in {
		out = append(out, telegram.MessageEntity{
			Type:   string(e.Type),
			Offset: e.Offset,
			Length: e.Length,
		})
	}
	return out
}

func toPhotoSizes(in []models.PhotoSize) []telegram.PhotoSize {
	if len(in) == 0 {
		return nil
	}
	out := make([]telegram.PhotoSize, 0, len(in))
	for _, s := range in {
		out = append(out, telegram.PhotoSize{
			FileID:   s.FileID,
			Width:    s.Width,
			Height:   s.Height,
			FileSize: int(s.FileSize),
		})
	}
	return out
}

func toInboundKeyboard(markup *models.InlineKeyboardMarkup) *telegram.InlineKeyboard {
	if markup == nil {
		return nil
	}
	rows := make([][]telegram.InlineButton, 0, len(markup.InlineKeyboard))
	for _, row := range markup.InlineKeyboard {
		out := make([]telegram.InlineButton, 0, len(row))
		for _, btn := range row {
			out = append(out, telegram.InlineButton{
				Text:         btn.Text,
				CallbackData: btn.CallbackData,
				URL:          btn.URL,
			})
		}
		rows = append(rows, out)
	}
	return &telegram.InlineKeyboard{Rows: rows}
}
