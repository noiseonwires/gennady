// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForumTopics_RoundTrip(t *testing.T) {
	db := newTestDB(t)

	// Unknown topic → ok=false, no error.
	name, ok, err := db.GetForumTopicName(-100, 7)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, "", name)

	// Insert.
	require.NoError(t, db.UpsertForumTopic(-100, 7, "General Chat"))
	name, ok, err = db.GetForumTopicName(-100, 7)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "General Chat", name)

	// Update (rename) the same topic.
	require.NoError(t, db.UpsertForumTopic(-100, 7, "Renamed"))
	name, ok, err = db.GetForumTopicName(-100, 7)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "Renamed", name)

	// A different topic in the same chat is independent.
	require.NoError(t, db.UpsertForumTopic(-100, 8, "Off-Topic"))

	all, err := db.ListAllForumTopics()
	require.NoError(t, err)
	assert.Len(t, all, 2)

	got := map[int]string{}
	for _, e := range all {
		assert.Equal(t, int64(-100), e.ChatID)
		got[e.ThreadID] = e.Name
	}
	assert.Equal(t, "Renamed", got[7])
	assert.Equal(t, "Off-Topic", got[8])
}
