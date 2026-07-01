// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// workerMetrics tracks how busy the update-processing worker pool is so the
// operator can tell whether to change update_processing.workers. Every inbound
// update flows through processUpdate, which brackets its work with begin/end;
// all counters are lock-free atomics updated on that hot path.
type workerMetrics struct {
	processed   atomic.Int64 // updates completed in the current window
	busyNanos   atomic.Int64 // cumulative processing time in the current window
	inFlight    atomic.Int64 // updates being processed right now (live gauge)
	maxInFlight atomic.Int64 // peak concurrent in-flight during the window
}

// begin records the start of processing one update and returns its start time.
func (m *workerMetrics) begin() time.Time {
	n := m.inFlight.Add(1)
	for {
		peak := m.maxInFlight.Load()
		if n <= peak || m.maxInFlight.CompareAndSwap(peak, n) {
			break
		}
	}
	return time.Now()
}

// end records the completion of processing the update that began at start.
func (m *workerMetrics) end(start time.Time) {
	m.inFlight.Add(-1)
	m.busyNanos.Add(int64(time.Since(start)))
	m.processed.Add(1)
}

// workerStats is a point-in-time snapshot of one window.
type workerStats struct {
	processed   int64
	busy        time.Duration
	maxInFlight int64
}

// snapshotAndReset returns the accumulated window counters and resets them for
// the next window. The live in-flight gauge is preserved.
func (m *workerMetrics) snapshotAndReset() workerStats {
	return workerStats{
		processed:   m.processed.Swap(0),
		busy:        time.Duration(m.busyNanos.Swap(0)),
		maxInFlight: m.maxInFlight.Swap(0),
	}
}

// startWorkerStatsLogger periodically logs worker-pool utilization until the bot
// stops. It is launched only when update_processing.stats_interval_seconds > 0.
func (b *Bot) startWorkerStatsLogger(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-b.stopCh:
			return
		case <-b.botCtx.Done():
			return
		case <-ticker.C:
			b.logWorkerStats(interval)
		}
	}
}

// logWorkerStats emits one utilization summary for the window just elapsed, with
// a recommendation when the pool looks saturated or over-provisioned. Idle
// windows (nothing processed) are skipped so quiet bots stay silent.
func (b *Bot) logWorkerStats(window time.Duration) {
	s := b.metrics.snapshotAndReset()
	if s.processed == 0 {
		return
	}
	workers := b.updateWorkers
	if workers < 1 {
		workers = 1
	}
	log.Print(formatWorkerStats(b.config.Webhook.Enabled, workers, window, s))
}

// formatWorkerStats renders the human-readable utilization line (and any tuning
// recommendation) for a window. Pure so the thresholds can be unit-tested.
func formatWorkerStats(webhook bool, workers int, window time.Duration, s workerStats) string {
	rate := float64(s.processed) / window.Seconds()
	avg := s.busy / time.Duration(s.processed)

	// In webhook mode the HTTP server runs each update in its own goroutine, so
	// update_processing.workers does not bound concurrency - report peak
	// concurrency instead of a worker-relative utilization figure.
	if webhook {
		return fmt.Sprintf("📊 Update processing (webhook): %d in %s (%.2f/s), avg %s/update, peak concurrency %d",
			s.processed, window.Truncate(time.Second), rate, avg.Truncate(time.Millisecond), s.maxInFlight)
	}

	if workers < 1 {
		workers = 1
	}
	// Utilization = busy worker-time / available worker-time. Near 100% means the
	// pool was busy the whole window and updates were likely queueing.
	utilization := float64(s.busy) / (float64(workers) * float64(window))
	if utilization > 1 {
		utilization = 1
	}

	line := fmt.Sprintf("📊 Update workers: %d in %s (%.2f/s), avg %s/update, peak concurrency %d/%d, busy %.0f%%",
		s.processed, window.Truncate(time.Second), rate, avg.Truncate(time.Millisecond), s.maxInFlight, workers, utilization*100)

	switch {
	case utilization >= 0.75:
		line += fmt.Sprintf(" — workers saturated; consider raising update_processing.workers above %d", workers)
	case workers > 1 && utilization < 0.20:
		line += fmt.Sprintf(" — plenty of headroom; update_processing.workers (%d) could be lowered", workers)
	}
	return line
}
