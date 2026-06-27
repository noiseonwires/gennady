// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDBHealthState_DebouncesTransientFailures(t *testing.T) {
	s := newDBHealthState(2)
	boom := errors.New("dial tcp: i/o timeout")

	// First failure is below the threshold -> no alert yet.
	assert.Equal(t, dbHealthNoChange, s.observe(boom))
	// Second consecutive failure crosses the threshold -> connection lost.
	assert.Equal(t, dbHealthLost, s.observe(boom))
	// Further failures while already down must not re-alert.
	assert.Equal(t, dbHealthNoChange, s.observe(boom))

	// First success after being down -> recovered.
	assert.Equal(t, dbHealthRecovered, s.observe(nil))
	// Subsequent successes are steady-state, no alert.
	assert.Equal(t, dbHealthNoChange, s.observe(nil))
}

func TestDBHealthState_SingleBlipDoesNotAlert(t *testing.T) {
	s := newDBHealthState(2)
	// One failure followed by a success must never report a loss.
	assert.Equal(t, dbHealthNoChange, s.observe(errors.New("blip")))
	assert.Equal(t, dbHealthNoChange, s.observe(nil))
}

func TestDBHealthState_ThresholdOne(t *testing.T) {
	s := newDBHealthState(1)
	assert.Equal(t, dbHealthLost, s.observe(errors.New("down")))
	assert.Equal(t, dbHealthRecovered, s.observe(nil))
}

func TestStartDBHealthMonitor_NoopForLocalDB(t *testing.T) {
	b, _ := newMockBot(t)
	// newMockBot wires a local SQLite DB; the monitor must return immediately
	// rather than block, so this call completing is the assertion.
	b.startDBHealthMonitor()
}
