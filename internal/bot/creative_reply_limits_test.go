// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package bot

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreativeReply_AdminRateLimitBypass(t *testing.T) {
	tests := []struct {
		name          string
		enabled       bool
		superAdmin    bool
		statuses      map[int64]string
		lookupError   bool
		whitelisted   bool
		underLimit    bool
		outsideWindow bool
		replyToBot    bool
		wantReply     bool
	}{
		{name: "disabled for super-admin", superAdmin: true},
		{name: "disabled for chat admin", statuses: map[int64]string{-100: telegram.StatusAdministrator}},
		{name: "enabled for super-admin", enabled: true, superAdmin: true, wantReply: true},
		{name: "enabled for chat admin replying to bot", enabled: true, statuses: map[int64]string{-100: telegram.StatusAdministrator}, replyToBot: true, wantReply: true},
		{name: "enabled for chat owner", enabled: true, statuses: map[int64]string{-100: telegram.StatusCreator}, wantReply: true},
		{name: "enabled for another moderation chat admin", enabled: true, statuses: map[int64]string{-200: telegram.StatusAdministrator}, wantReply: true},
		{name: "enabled for admin chat admin", enabled: true, statuses: map[int64]string{-300: telegram.StatusAdministrator}, wantReply: true},
		{name: "enabled for regular user", enabled: true},
		{name: "admin only in an unconfigured chat", enabled: true, statuses: map[int64]string{-400: telegram.StatusAdministrator}},
		{name: "enabled for whitelisted non-admin", enabled: true, whitelisted: true},
		{name: "admin lookup fails", enabled: true, lookupError: true},
		{name: "disabled under limit", underLimit: true, wantReply: true},
		{name: "regular user under limit", enabled: true, underLimit: true, wantReply: true},
		{name: "previous message outside window", enabled: true, outsideWindow: true, wantReply: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a witty reply"}}]}`))
			})
			creativeModerationConfig(b)
			b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100, -200}}
			b.config.Admin.ChatID = -300
			b.config.AI.CreativeReplies.MaxMessages = 1
			b.config.AI.CreativeReplies.SkipRateLimitForAdmins = tt.enabled
			b.config.Reactions.CreativeReplyLimit = "limit"
			if tt.superAdmin {
				b.config.Admin.SuperAdminUserID = 7
			}
			if tt.whitelisted {
				b.config.Admin.WhitelistUserIDs = []int64{7}
			}
			memberCalls := 0
			tg.GetMemberFunc = func(chatID, userID int64) (telegram.ChatMember, error) {
				memberCalls++
				assert.Equal(t, int64(7), userID)
				if tt.lookupError {
					return telegram.ChatMember{}, errors.New("Telegram unavailable")
				}
				status := telegram.StatusMember
				if configuredStatus, ok := tt.statuses[chatID]; ok {
					status = configuredStatus
				}
				return telegram.ChatMember{Status: status}, nil
			}
			if !tt.underLimit {
				timestamp := time.Now().Add(-time.Minute)
				if tt.outsideWindow {
					timestamp = time.Now().Add(-25 * time.Hour)
				}
				require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
					MessageID: 50, ChatID: -100, UserID: b.botSelf.ID,
					Text: "previous bot message", Timestamp: timestamp,
				}))
			}

			msg := testMessage(-100, 7, 100, "@testbot hello")
			if tt.replyToBot {
				msg.Text = "hello again"
				msg.ReplyToMessage = testMessage(-100, b.botSelf.ID, 50, "previous bot message")
			}
			b.creativeReply(msg, msg, false)

			if tt.wantReply {
				require.Len(t, tg.SentMessages, 1)
				assert.Equal(t, 100, tg.SentMessages[0].ReplyToMessageID)
				assert.Contains(t, tg.SentMessages[0].Text, "a witty reply")
				assert.Empty(t, tg.Reactions)
				assert.Equal(t, 1, rt.count())
			} else {
				assert.Empty(t, tg.SentMessages)
				require.Len(t, tg.Reactions, 1)
				assert.Equal(t, reactionCall{ChatID: -100, MessageID: 100, Emoji: "limit"}, tg.Reactions[0])
				assert.Zero(t, rt.count())
			}
			if tt.superAdmin {
				assert.Zero(t, memberCalls, "the configured super-admin needs no Telegram lookup")
			} else if !tt.enabled && (tt.underLimit || tt.outsideWindow) {
				assert.Equal(t, 3, memberCalls, "only the existing prompt profile lookup should run when the bypass is disabled")
			} else if !tt.enabled {
				assert.Zero(t, memberCalls, "a disabled bypass needs no Telegram lookup")
			} else {
				assert.Positive(t, memberCalls)
			}
		})
	}
}

func TestCreativeReply_AdminRepliesExcludedFromSharedLimit(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a witty reply"}}]}`))
	})
	creativeModerationConfig(b)
	b.config.AI.CreativeReplies.MaxMessages = 1
	b.config.AI.CreativeReplies.SkipRateLimitForAdmins = true
	b.config.Admin.SuperAdminUserID = 7
	b.config.Reactions.CreativeReplyLimit = "limit"

	adminMessage := testMessage(-100, 7, 100, "@testbot hello")
	b.creativeReply(adminMessage, adminMessage, false)
	b.creativeReply(adminMessage, adminMessage, false)
	require.Len(t, tg.SentMessages, 2)
	count, err := b.db.GetRecentBotMessageCount(b.botSelf.ID, -100, 24)
	require.NoError(t, err)
	assert.Zero(t, count, "admin replies are excluded from the shared rate-limit count")

	memberMessage := testMessage(-100, 8, 101, "@testbot hello")
	b.creativeReply(memberMessage, memberMessage, false)
	require.Len(t, tg.SentMessages, 3)
	assert.Empty(t, tg.Reactions)

	count, err = b.db.GetRecentBotMessageCount(b.botSelf.ID, -100, 24)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "regular user replies still count")

	secondMember := testMessage(-100, 9, 102, "@testbot hello")
	b.creativeReply(secondMember, secondMember, false)
	assert.Len(t, tg.SentMessages, 3)
	assert.Equal(t, 3, rt.count())
	require.Len(t, tg.Reactions, 1)
	assert.Equal(t, reactionCall{ChatID: -100, MessageID: 102, Emoji: "limit"}, tg.Reactions[0])
}

func TestCreativeReply_AdminRepliesCountWhenBypassDisabled(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a witty reply"}}]}`))
	})
	creativeModerationConfig(b)
	b.config.AI.CreativeReplies.MaxMessages = 1
	b.config.Admin.SuperAdminUserID = 7
	b.config.Reactions.CreativeReplyLimit = "limit"

	adminMessage := testMessage(-100, 7, 100, "@testbot hello")
	b.creativeReply(adminMessage, adminMessage, false)
	require.Len(t, tg.SentMessages, 1)

	memberMessage := testMessage(-100, 8, 101, "@testbot hello")
	b.creativeReply(memberMessage, memberMessage, false)
	assert.Len(t, tg.SentMessages, 1)
	assert.Equal(t, 1, rt.count())
	require.Len(t, tg.Reactions, 1)
	assert.Equal(t, reactionCall{ChatID: -100, MessageID: 101, Emoji: "limit"}, tg.Reactions[0])
}

func TestCreativeReply_AdminBypassKeepsOtherRestrictions(t *testing.T) {
	for _, scenario := range []string{"AI disabled", "creative replies disabled", "excluded topic", "moderated", "same-user follow-up"} {
		t.Run(scenario, func(t *testing.T) {
			b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a witty reply"}}]}`))
			})
			creativeModerationConfig(b)
			b.config.AI.CreativeReplies.SkipRateLimitForAdmins = true
			b.config.Admin.SuperAdminUserID = 7
			msg := testMessage(-100, 7, 100, "@testbot hello")
			switch scenario {
			case "AI disabled":
				b.config.AI.Enabled = false
			case "creative replies disabled":
				b.config.AI.CreativeReplies.Enabled = false
			case "excluded topic":
				b.config.AI.CreativeReplies.ExcludedTopics = config.ChatTopicList{
					Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
				}
			case "same-user follow-up":
				b.config.AI.CreativeReplies.FollowUpOnlySameUser = true
				originalID := 40
				now := time.Now().Add(-time.Minute)
				require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
					MessageID: originalID, ChatID: -100, UserID: 8, Timestamp: now,
				}))
				require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
					MessageID: 50, ChatID: -100, UserID: b.botSelf.ID,
					ReplyToMessageID: &originalID, Timestamp: now,
				}))
				msg.Text = "hello again"
				msg.ReplyToMessage = testMessage(-100, b.botSelf.ID, 50, "previous bot message")
			}

			b.creativeReply(msg, msg, scenario == "moderated")

			assert.Empty(t, tg.SentMessages)
			assert.Empty(t, tg.Reactions)
			assert.Zero(t, rt.count())
		})
	}
}
