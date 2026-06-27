// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"
	"time"

	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
)

func TestUserProfileCache_HitMissAndNegative(t *testing.T) {
	c := newUserProfileCache()

	// Miss on an unknown key.
	_, ok := c.get(1)
	assert.False(t, ok)

	// Positive entry round-trips.
	p := &database.UserProfile{UserID: 1, Username: "alice"}
	c.put(1, p)
	got, ok := c.get(1)
	assert.True(t, ok)
	assert.Equal(t, p, got)

	// Negative entry (nil profile) is a valid cache hit, not a miss.
	c.put(2, nil)
	got, ok = c.get(2)
	assert.True(t, ok, "negative entry must be a hit so the DB isn't re-queried")
	assert.Nil(t, got)
}

func TestUserProfileCache_TTLExpiry(t *testing.T) {
	c := newUserProfileCache()
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }

	c.put(1, &database.UserProfile{UserID: 1})
	_, ok := c.get(1)
	assert.True(t, ok)

	// Advance just past the TTL: the entry expires and is evicted on access.
	now = now.Add(profileCacheTTL + time.Second)
	_, ok = c.get(1)
	assert.False(t, ok, "entry must expire after the TTL")
	assert.Equal(t, 0, c.len(), "expired entry should be removed on access")
}

func TestUserProfileCache_LRUEviction(t *testing.T) {
	c := newUserProfileCache()
	c.max = 3

	c.put(1, &database.UserProfile{UserID: 1})
	c.put(2, &database.UserProfile{UserID: 2})
	c.put(3, &database.UserProfile{UserID: 3})

	// Touch key 1 so key 2 becomes the least-recently-used.
	_, _ = c.get(1)

	// Inserting a 4th entry evicts the LRU (key 2), not key 1.
	c.put(4, &database.UserProfile{UserID: 4})
	assert.Equal(t, 3, c.len())

	_, ok := c.get(2)
	assert.False(t, ok, "least-recently-used entry should have been evicted")
	for _, id := range []int64{1, 3, 4} {
		_, ok := c.get(id)
		assert.True(t, ok, "entry %d should still be cached", id)
	}
}

func TestUserProfileCache_DeleteAndRefresh(t *testing.T) {
	c := newUserProfileCache()
	c.put(1, &database.UserProfile{UserID: 1, Reputation: "good"})

	// Refresh replaces the value in place (no duplicate entry).
	c.put(1, &database.UserProfile{UserID: 1, Reputation: "bad"})
	assert.Equal(t, 1, c.len())
	got, _ := c.get(1)
	assert.Equal(t, "bad", got.Reputation)

	// Delete invalidates.
	c.delete(1)
	_, ok := c.get(1)
	assert.False(t, ok)
	assert.Equal(t, 0, c.len())
}

func TestUserProfileCache_NilReceiverIsNoOp(t *testing.T) {
	var c *userProfileCache // nil cache behaves as a no-op
	assert.NotPanics(t, func() {
		c.put(1, &database.UserProfile{UserID: 1})
		c.delete(1)
		assert.Equal(t, 0, c.len())
		_, ok := c.get(1)
		assert.False(t, ok)
	})
}
