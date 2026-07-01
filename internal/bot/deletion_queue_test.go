// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"

	tgbotapi "gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the message-deletion-queue stages (prepare-deletion and
// finalize-deletion) and their independence from moderation.

// A normal message in a deletion-enabled chat is added to the deletion queue,
// even when moderation is turned off for that chat (the two scopes are
// independent).
func TestDeletion_NormalMessage_AddedToQueueViaPipeline(t *testing.T) {
	b, _ := newMockBot(t)
	b.moderatedMsgs = make(map[string]time.Time)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.MessageDeletion.Enabled = true
	// Moderation off for the whole chat - deletion must still happen.
	b.config.Moderation.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}

	b.handleMessage(testMessage(-100, 7, 55, "some message"), false)

	inQueue, err := b.db.IsMessageInDeletionQueue(55, -100)
	require.NoError(t, err)
	assert.True(t, inQueue, "a message in a deletion-enabled chat must be queued for deletion")
}

// A message from an excluded user is queued but pinned, so it is never
// auto-deleted.
func TestDeletion_ExcludedUser_MarkedPinned(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.MessageDeletion.Enabled = true
	b.config.MessageDeletion.ExcludedUserIDs = []int64{7}

	mc := b.newInboundContext(testMessage(-100, 7, 55, "msg"), false)
	b.stagePrepareDeletion(mc)

	assert.True(t, mc.AddToDeletion)
	assert.True(t, mc.DeletionPinned, "a message from an excluded user must be pinned (never auto-deleted)")
}

// Service messages (joins/pins/etc.) are never queued for deletion.
func TestDeletion_ServiceMessage_NotQueued(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.MessageDeletion.Enabled = true

	svc := testMessage(-100, 7, 55, "")
	svc.NewChatMembers = []tgbotapi.User{{ID: 8}} // makes it a service message

	mc := b.newInboundContext(svc, false)
	b.stagePrepareDeletion(mc)

	assert.False(t, mc.AddToDeletion, "service messages must not be queued for deletion")
}

// When the moderate stage bypassed analyzeMessage (e.g. the content-safety
// route), the finalize stage performs the standalone deletion-queue insert.
func TestDeletion_FinalizeFallback_InsertsWhenRecordingBypassed(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	mc := b.newInboundContext(testMessage(-100, 7, 55, "img"), false)
	mc.AddToDeletion = true
	// deletionHandled stays false (recording was bypassed).

	b.stageFinalizeDeletion(mc)

	inQueue, err := b.db.IsMessageInDeletionQueue(55, -100)
	require.NoError(t, err)
	assert.True(t, inQueue, "the finalize stage must insert the deletion row when recording was bypassed")
}

// A pin service event marks the pinned target message as pinned in the deletion
// queue, so it is excluded from auto-deletion cleanup.
func TestDeletion_PinnedServiceMessage_MarksOriginalPinned(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	// The target message starts queued and unpinned.
	require.NoError(t, b.db.AddMessageForDeletion(50, -100))
	unpinned, err := b.db.GetOldMessages(time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.True(t, deletionQueueHas(unpinned, 50, -100), "the target must start as an unpinned cleanup candidate")

	// A pin service event referencing that message flows through finalize.
	svc := testMessage(-100, 7, 55, "")
	svc.PinnedMessage = &tgbotapi.Message{MessageID: 50, Chat: tgbotapi.Chat{ID: -100}}
	b.stageFinalizeDeletion(b.newInboundContext(svc, false))

	stillUnpinned, err := b.db.GetOldMessages(time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, deletionQueueHas(stillUnpinned, 50, -100), "a pin event must mark the target as pinned (excluded from cleanup)")
}

func deletionQueueHas(items []database.MessageForDeletion, msgID int, chatID int64) bool {
	for _, m := range items {
		if m.MessageID == msgID && m.ChatID == chatID {
			return true
		}
	}
	return false
}
