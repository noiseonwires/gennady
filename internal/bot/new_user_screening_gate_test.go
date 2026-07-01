// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"sync/atomic"
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the pipeline gate around new-user profile screening (in
// recordNewMessage): screening runs once on a user's first message in a
// moderated chat, and is bypassed for repeat messages, whitelisted users, and
// moderation-excluded topics. checkFirstMessageUserProfile starts by calling
// GetChatFull, so counting that call is a clean signal that screening ran.

// countChatFull installs a GetChatFull hook that counts invocations.
func countChatFull(tg *mockTelegram, counter *int32) {
	tg.GetChatFullFunc = func(chatID int64) (telegram.ChatFull, error) {
		atomic.AddInt32(counter, 1)
		return telegram.ChatFull{Chat: telegram.Chat{ID: chatID}}, nil
	}
}

// A user's first message in a moderated chat triggers profile screening.
func TestNewUserScreening_FirstMessage_Screened(t *testing.T) {
	b, tg := newProfileScreeningBot(t, "CLEAN")
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	var fullCalls int32
	countChatFull(tg, &fullCalls)

	b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "first message"), false))

	assert.GreaterOrEqual(t, int(atomic.LoadInt32(&fullCalls)), 1, "a user's first message must trigger profile screening")
}

// Screening is per chat and runs only once: a user with a prior recorded message
// is no longer screened.
func TestNewUserScreening_NotFirstMessage_NotScreened(t *testing.T) {
	b, tg := newProfileScreeningBot(t, "CLEAN")
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 40, ChatID: -100, UserID: 7, Text: "earlier", Timestamp: time.Now(),
	}))
	var fullCalls int32
	countChatFull(tg, &fullCalls)

	b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "second message"), false))

	assert.Equal(t, int32(0), atomic.LoadInt32(&fullCalls), "screening runs only on the first message per chat")
}

// Whitelisted users bypass profile screening.
func TestNewUserScreening_Whitelisted_Bypassed(t *testing.T) {
	b, tg := newProfileScreeningBot(t, "CLEAN")
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.Admin.WhitelistUserIDs = []int64{7}
	var fullCalls int32
	countChatFull(tg, &fullCalls)

	b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "first message"), false))

	assert.Equal(t, int32(0), atomic.LoadInt32(&fullCalls), "whitelisted users must bypass profile screening")
}

// A moderation-excluded topic bypasses profile screening (it is moderation-only
// work).
func TestNewUserScreening_ExcludedTopic_Bypassed(t *testing.T) {
	b, tg := newProfileScreeningBot(t, "CLEAN")
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.Moderation.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}
	var fullCalls int32
	countChatFull(tg, &fullCalls)

	b.analyzeMessage(b.newInboundContext(testMessage(-100, 7, 55, "first message"), false))

	assert.Equal(t, int32(0), atomic.LoadInt32(&fullCalls), "moderation-excluded topics must bypass profile screening")
}
