// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storeAlbumMsg(t *testing.T, db *DB, msgID int, chatID, userID int64, username, text, mediaGroupID string, ts time.Time) {
	t.Helper()
	require.NoError(t, db.StoreMessageInfo(&MessageInfo{
		MessageID: msgID, ChatID: chatID, UserID: userID, Username: username,
		Text: text, MediaGroupID: mediaGroupID, Timestamp: ts,
	}))
}

// An album's items must be resolvable from any one of them, so a moderation
// action on a single item can be applied to the whole logical post.
func TestGetAlbumSiblingIDs(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	storeAlbumMsg(t, db, 10, -1, 5, "@alice", "<photo> caption", "grp1", now)
	storeAlbumMsg(t, db, 11, -1, 5, "@alice", "<photo>", "grp1", now)
	storeAlbumMsg(t, db, 12, -1, 5, "@alice", "<photo>", "grp1", now)
	storeAlbumMsg(t, db, 13, -1, 5, "@alice", "<photo>", "grp2", now)
	storeMsg(t, db, 14, -1, 5, "@alice", "standalone", now)

	ids, err := db.GetAlbumSiblingIDs(11, -1)
	require.NoError(t, err)
	assert.Equal(t, []int{10, 12}, ids)

	// A single-item group has no siblings, and a standalone message never does.
	ids, err = db.GetAlbumSiblingIDs(13, -1)
	require.NoError(t, err)
	assert.Empty(t, ids)

	ids, err = db.GetAlbumSiblingIDs(14, -1)
	require.NoError(t, err)
	assert.Empty(t, ids)

	// Album ids don't leak across chats.
	storeAlbumMsg(t, db, 20, -2, 5, "@alice", "<photo>", "grp1", now)
	ids, err = db.GetAlbumSiblingIDs(20, -2)
	require.NoError(t, err)
	assert.Empty(t, ids)
}

// The AI chat history must see an album as one post, not as N separate
// single-photo messages.
func TestGetRecentMessagesWithUsernames_FoldsAlbums(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	storeAlbumMsg(t, db, 1, -1, 5, "@alice", "<photo> look at this", "grp1", now)
	storeAlbumMsg(t, db, 2, -1, 5, "@alice", "<photo> [Image: a cat]", "grp1", now)
	storeAlbumMsg(t, db, 3, -1, 5, "@alice", "<photo> [Image: a dog]", "grp1", now)
	storeMsg(t, db, 4, -1, 6, "@bob", "nice", now)

	msgs, err := db.GetRecentMessagesWithUsernames(-1, 24, 999)
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	assert.Contains(t, msgs[0], "look at this")
	assert.Contains(t, msgs[0], "a cat")
	assert.Contains(t, msgs[0], "a dog")
	// The folded items must not repeat the "(time) [user]:" header.
	assert.Equal(t, 1, countSubstr(msgs[0], "[alice]"))
	assert.Contains(t, msgs[1], "nice")
}

func countSubstr(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
