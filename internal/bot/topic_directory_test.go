// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"

	"gennadium/internal/config"
	"gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordTopicName_PersistsAndCaches(t *testing.T) {
	b, _ := newMockBot(t)
	b.topicDir = newTopicDirectory()

	b.recordTopicName(-100, 7, "Support")

	// Cached in memory.
	name, ok := b.topicDir.get(-100, 7)
	require.True(t, ok)
	assert.Equal(t, "Support", name)

	// Persisted to DB.
	dbName, found, err := b.db.GetForumTopicName(-100, 7)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Support", dbName)
}

func TestRecordTopicName_IgnoresEmptyAndMainArea(t *testing.T) {
	b, _ := newMockBot(t)
	b.topicDir = newTopicDirectory()

	b.recordTopicName(-100, 0, "MainArea") // thread 0 ignored
	b.recordTopicName(-100, 5, "")         // empty name ignored
	b.recordTopicName(0, 5, "NoChat")      // chat 0 ignored

	all, err := b.db.ListAllForumTopics()
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestTopicName_FallsBackToDB(t *testing.T) {
	b, _ := newMockBot(t)
	b.topicDir = newTopicDirectory()

	// Seed the DB only (cache empty) - topicName must read through and cache.
	require.NoError(t, b.db.UpsertForumTopic(-100, 9, "Announcements"))

	assert.Equal(t, "Announcements", b.topicName(-100, 9))
	cached, ok := b.topicDir.get(-100, 9)
	require.True(t, ok)
	assert.Equal(t, "Announcements", cached)

	// Unknown topic and the main area return "".
	assert.Equal(t, "", b.topicName(-100, 999))
	assert.Equal(t, "", b.topicName(-100, 0))
}

func TestWarmTopicDirectory(t *testing.T) {
	b, _ := newMockBot(t)
	b.topicDir = newTopicDirectory()

	require.NoError(t, b.db.UpsertForumTopic(-100, 1, "Alpha"))
	require.NoError(t, b.db.UpsertForumTopic(-100, 2, "Beta"))

	b.warmTopicDirectory()

	a, ok := b.topicDir.get(-100, 1)
	require.True(t, ok)
	assert.Equal(t, "Alpha", a)
	be, ok := b.topicDir.get(-100, 2)
	require.True(t, ok)
	assert.Equal(t, "Beta", be)
}

func TestHarvestTopicFromMessage(t *testing.T) {
	b, _ := newMockBot(t)
	b.topicDir = newTopicDirectory()

	// forum_topic_created carries the initial name.
	b.harvestTopicFromMessage(&telegram.Message{
		Chat:              telegram.Chat{ID: -100, IsForum: true},
		MessageThreadID:   42,
		ForumTopicCreated: &telegram.ForumTopicCreated{Name: "Bugs"},
	})
	assert.Equal(t, "Bugs", b.topicName(-100, 42))

	// forum_topic_edited updates it.
	b.harvestTopicFromMessage(&telegram.Message{
		Chat:             telegram.Chat{ID: -100, IsForum: true},
		MessageThreadID:  42,
		ForumTopicEdited: &telegram.ForumTopicEdited{Name: "Bugs & Crashes"},
	})
	assert.Equal(t, "Bugs & Crashes", b.topicName(-100, 42))

	// A plain message harvests nothing.
	b.harvestTopicFromMessage(&telegram.Message{
		Chat:            telegram.Chat{ID: -100, IsForum: true},
		MessageThreadID: 43,
		Text:            "hello",
	})
	assert.Equal(t, "", b.topicName(-100, 43))
}

func TestTopicContext(t *testing.T) {
	b, _ := newMockBot(t)
	b.topicDir = newTopicDirectory()

	// Main area → no context line.
	assert.Equal(t, "", b.topicContext(-100, 0))

	// Unknown topic → no line at all (id-only carries little value).
	assert.Equal(t, "", b.topicContext(-100, 7))

	// Known topic → name + id.
	b.recordTopicName(-100, 7, "Support")
	named := b.topicContext(-100, 7)
	assert.Contains(t, named, "Support")
	assert.Contains(t, named, "7")
}

func TestSeedTopicNamesFromConfig(t *testing.T) {
	b, _ := newMockBot(t)
	b.topicDir = newTopicDirectory()

	// A DB-learned (observed) name must win over the config-provided one.
	b.recordTopicName(-100, 7, "Observed Name")

	b.config.Topics = []config.TopicNameRef{
		{Chat: -100, Topic: 7, Name: "Config Name"},   // skipped: DB-learned wins
		{Chat: -100, Topic: 9, Name: "Announcements"}, // seeded: unseen topic
		{Chat: -100, Topic: 0, Name: "MainArea"},      // ignored: thread 0
		{Chat: 0, Topic: 5, Name: "NoChat"},           // ignored: chat 0
		{Chat: -100, Topic: 11, Name: ""},             // ignored: empty name
	}

	b.seedTopicNamesFromConfig()

	assert.Equal(t, "Observed Name", b.topicName(-100, 7))
	assert.Equal(t, "Announcements", b.topicName(-100, 9))
	assert.Equal(t, "", b.topicName(-100, 0))
	assert.Equal(t, "", b.topicName(0, 5))
	assert.Equal(t, "", b.topicName(-100, 11))

	// Config seeds must NOT be persisted to the DB (config remains the source
	// of truth for them; live observation persists separately).
	_, found, err := b.db.GetForumTopicName(-100, 9)
	require.NoError(t, err)
	assert.False(t, found, "config-seeded names must not be written to the DB")
}
