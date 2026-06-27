// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"container/list"
	"sync"
	"time"

	"gennadium/internal/database"
)

const (
	// profileCacheMaxEntries bounds how many user profiles are cached in memory
	// so the cache cannot grow without limit as unique users are seen over the
	// process lifetime.
	profileCacheMaxEntries = 4096
	// profileCacheTTL is how long a cached profile - including a negative
	// "no profile" result - stays valid before it is re-read from the database.
	// It bounds staleness and ensures negative entries are never pinned forever.
	profileCacheTTL = 30 * time.Minute
)

// profileCacheEntry is the value stored in the LRU list.
type profileCacheEntry struct {
	userID    int64
	profile   *database.UserProfile // may be nil (negative cache)
	expiresAt time.Time
}

// userProfileCache is a bounded, TTL'd LRU cache of AI user profiles keyed by
// user_id. It caches negative results (a nil profile) too, with the same TTL,
// so a missing profile is not re-queried on every message yet is never pinned
// forever. The least-recently-used entry is evicted once the cache is full.
//
// All methods are safe for concurrent use and tolerate a nil receiver (a nil
// *userProfileCache behaves as a no-op cache: every lookup misses and nothing
// is stored), so a Bot constructed without a cache never panics.
type userProfileCache struct {
	mu      sync.Mutex
	ll      *list.List              // front = most recently used
	entries map[int64]*list.Element // userID -> element holding *profileCacheEntry
	max     int
	ttl     time.Duration
	now     func() time.Time
}

// newUserProfileCache builds a cache with the package default bounds.
func newUserProfileCache() *userProfileCache {
	return &userProfileCache{
		ll:      list.New(),
		entries: make(map[int64]*list.Element),
		max:     profileCacheMaxEntries,
		ttl:     profileCacheTTL,
		now:     time.Now,
	}
}

// get returns the cached profile and whether a non-expired entry exists. A
// returned (nil, true) is a valid negative cache hit; (nil, false) is a miss.
func (c *userProfileCache) get(userID int64) (*database.UserProfile, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[userID]
	if !ok {
		return nil, false
	}
	ent := el.Value.(*profileCacheEntry)
	if c.now().After(ent.expiresAt) {
		c.removeElement(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return ent.profile, true
}

// put stores (or refreshes) the profile for userID, evicting the least recently
// used entry when the cache is at capacity.
func (c *userProfileCache) put(userID int64, profile *database.UserProfile) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expiresAt := c.now().Add(c.ttl)
	if el, ok := c.entries[userID]; ok {
		ent := el.Value.(*profileCacheEntry)
		ent.profile = profile
		ent.expiresAt = expiresAt
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&profileCacheEntry{userID: userID, profile: profile, expiresAt: expiresAt})
	c.entries[userID] = el
	if c.max > 0 && c.ll.Len() > c.max {
		if oldest := c.ll.Back(); oldest != nil {
			c.removeElement(oldest)
		}
	}
}

// delete removes a single entry, used to invalidate a profile after it has been
// regenerated or annotated so the next lookup re-reads the fresh row.
func (c *userProfileCache) delete(userID int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[userID]; ok {
		c.removeElement(el)
	}
}

// len reports the number of cached entries (primarily for tests).
func (c *userProfileCache) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// removeElement unlinks el from both the list and the index. Caller holds mu.
func (c *userProfileCache) removeElement(el *list.Element) {
	c.ll.Remove(el)
	ent := el.Value.(*profileCacheEntry)
	delete(c.entries, ent.userID)
}
