// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// begin/end accumulate processed count and busy time; snapshotAndReset hands
// back the window and clears it for the next one.
func TestWorkerMetrics_SnapshotAndReset(t *testing.T) {
	var m workerMetrics

	start := m.begin()
	assert.Equal(t, int64(1), m.inFlight.Load(), "in-flight gauge tracks active work")
	m.end(start)
	m.end(m.begin())

	s := m.snapshotAndReset()
	assert.Equal(t, int64(2), s.processed)
	assert.GreaterOrEqual(t, s.busy, time.Duration(0))
	assert.Equal(t, int64(1), s.maxInFlight)
	assert.Equal(t, int64(0), m.inFlight.Load(), "gauge returns to zero once work completes")

	// A fresh window starts empty.
	s2 := m.snapshotAndReset()
	assert.Equal(t, int64(0), s2.processed)
}

// maxInFlight records the peak concurrency seen during the window even after the
// concurrent work drains.
func TestWorkerMetrics_MaxInFlight(t *testing.T) {
	var m workerMetrics

	const concurrent = 4
	var wg sync.WaitGroup
	release := make(chan struct{})
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := m.begin()
			<-release // hold all goroutines in-flight simultaneously
			m.end(start)
		}()
	}
	// Wait until all four are in-flight, then release them.
	require.Eventually(t, func() bool { return m.inFlight.Load() == concurrent }, time.Second, time.Millisecond)
	close(release)
	wg.Wait()

	s := m.snapshotAndReset()
	assert.Equal(t, int64(concurrent), s.processed)
	assert.Equal(t, int64(concurrent), s.maxInFlight)
}

func TestFormatWorkerStats_Recommendations(t *testing.T) {
	window := 5 * time.Minute

	// Saturated single worker: busy the whole window -> recommend raising.
	saturated := formatWorkerStats(false, 1, window, workerStats{processed: 300, busy: window, maxInFlight: 1})
	assert.Contains(t, saturated, "busy 100%")
	assert.Contains(t, saturated, "raising update_processing.workers above 1")

	// Comfortable single worker: ~10% busy -> no recommendation.
	comfy := formatWorkerStats(false, 1, window, workerStats{processed: 30, busy: window / 10, maxInFlight: 1})
	assert.Contains(t, comfy, "busy 10%")
	assert.NotContains(t, comfy, "consider raising")
	assert.NotContains(t, comfy, "could be lowered")

	// Over-provisioned multi-worker pool: very low utilization -> suggest lowering.
	idlePool := formatWorkerStats(false, 4, window, workerStats{processed: 10, busy: window / 20, maxInFlight: 1})
	assert.Contains(t, idlePool, "could be lowered")

	// Webhook mode reports concurrency, not a worker-relative figure.
	webhook := formatWorkerStats(true, 1, window, workerStats{processed: 50, busy: window / 2, maxInFlight: 6})
	assert.Contains(t, webhook, "webhook")
	assert.Contains(t, webhook, "peak concurrency 6")
	assert.NotContains(t, webhook, "busy")
}

// logWorkerStats stays silent for an idle window (nothing processed).
func TestLogWorkerStats_IdleWindowSkipped(t *testing.T) {
	b, _ := newMockBot(t)
	b.updateWorkers = 1
	// No begin/end calls -> processed == 0. Must not panic and produces no work.
	require.NotPanics(t, func() { b.logWorkerStats(time.Minute) })
	assert.Equal(t, int64(0), b.metrics.processed.Load())
}

func TestFormatWorkerStats_NotEmpty(t *testing.T) {
	out := formatWorkerStats(false, 2, time.Minute, workerStats{processed: 5, busy: 10 * time.Second, maxInFlight: 2})
	assert.True(t, strings.HasPrefix(out, "📊"))
}
