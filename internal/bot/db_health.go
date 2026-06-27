// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"context"
	"log"
	"time"

	"gennadium/internal/i18n"
)

// Remote database health-monitor tuning.
const (
	dbHealthCheckInterval = 30 * time.Second
	dbHealthPingTimeout   = 5 * time.Second
	// dbHealthFailureThreshold is how many consecutive failed pings must occur
	// before the connection is declared lost. This debounces transient blips so
	// a single dropped request doesn't trigger a false "connection lost" alert.
	dbHealthFailureThreshold = 2
)

// dbHealthTransition is the state change produced by observing a ping result.
type dbHealthTransition int

const (
	dbHealthNoChange dbHealthTransition = iota
	dbHealthLost
	dbHealthRecovered
)

// dbHealthState is a small debounced state machine that turns a stream of ping
// results into "lost" / "recovered" transitions. It is deliberately decoupled
// from timers and Telegram so it can be unit-tested in isolation.
type dbHealthState struct {
	healthy             bool
	consecutiveFailures int
	failureThreshold    int
}

func newDBHealthState(threshold int) *dbHealthState {
	// The monitor only starts after a successful DB init, so the connection is
	// healthy to begin with.
	return &dbHealthState{healthy: true, failureThreshold: threshold}
}

// observe records a ping result and reports the resulting transition. A non-nil
// pingErr counts as a failure; the connection is declared lost only after the
// failure threshold is reached. A successful ping clears the failure counter and
// reports recovery if the connection was previously considered down.
func (s *dbHealthState) observe(pingErr error) dbHealthTransition {
	if pingErr != nil {
		s.consecutiveFailures++
		if s.healthy && s.consecutiveFailures >= s.failureThreshold {
			s.healthy = false
			return dbHealthLost
		}
		return dbHealthNoChange
	}

	s.consecutiveFailures = 0
	if !s.healthy {
		s.healthy = true
		return dbHealthRecovered
	}
	return dbHealthNoChange
}

// startDBHealthMonitor periodically pings a remote database and DMs the
// super-admin (gated by admin.notify_startup) when the connection drops and
// again when it recovers. It is a no-op for local SQLite, which cannot lose a
// network connection, and runs until the bot stops.
func (b *Bot) startDBHealthMonitor() {
	if b.db == nil || b.db.IsLocal() {
		return
	}

	ticker := time.NewTicker(dbHealthCheckInterval)
	defer ticker.Stop()

	state := newDBHealthState(dbHealthFailureThreshold)

	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), dbHealthPingTimeout)
			err := b.db.Ping(ctx)
			cancel()

			switch state.observe(err) {
			case dbHealthLost:
				log.Printf("🔴 Remote database connection lost: %v", err)
				b.notifySuperAdmin(i18n.Tf("db.connection_lost", redactSensitiveText(err.Error())))
			case dbHealthRecovered:
				log.Printf("🟢 Remote database connection recovered")
				b.notifySuperAdmin(i18n.T("db.connection_recovered"))
			case dbHealthNoChange:
				// nothing to report
			}
		}
	}
}
