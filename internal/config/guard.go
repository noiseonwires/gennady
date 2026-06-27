// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import "sync"

// guard serializes concurrent access to the process-wide Config that is shared
// between the Telegram bot (reader) and the web admin UI (writer). The bot reads
// configuration fields on its event-processing goroutines while the web UI
// mutates the same *Config from HTTP-handler goroutines. Without synchronization,
// replacing a slice/map/string-valued field (e.g. the moderation rule list)
// concurrently with a read can yield a torn value - for instance a slice header
// pairing a new backing pointer with a stale length.
//
// There is exactly one live Config per process, so a package-level lock is
// sufficient and avoids embedding a sync primitive in Config itself (which would
// trip go vet's copylocks check at the many places Config is copied by value,
// e.g. yaml.Unmarshal into a temporary, ConfigToStringMap, the env reflection
// walkers, etc.).
var guard sync.RWMutex

// Lock acquires the configuration write lock. Web UI handlers that mutate the
// shared Config must hold it for the full duration of the mutation.
func Lock() { guard.Lock() }

// Unlock releases the configuration write lock.
func Unlock() { guard.Unlock() }

// RLock acquires the configuration read lock.
func RLock() { guard.RLock() }

// RUnlock releases the configuration read lock.
func RUnlock() { guard.RUnlock() }

// ModerationRules returns the configured auto-moderation rule list under the
// read lock. The web UI only ever *replaces* this slice (never mutates it in
// place), so the returned header is a stable, immutable snapshot that callers
// may range over after the lock has been released.
func (c *Config) ModerationRules() []ModerationRule {
	RLock()
	defer RUnlock()
	return c.AI.ContentModeration.Rules
}

// ReplaceContents atomically replaces the entire configuration with the contents
// of src under the write lock. Used by the "upload config" admin action so that
// the wholesale in-memory refresh does not race with the bot reading individual
// fields.
func (c *Config) ReplaceContents(src *Config) {
	Lock()
	defer Unlock()
	*c = *src
}
