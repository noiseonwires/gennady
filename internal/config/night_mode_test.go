// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// at builds a local time at the given weekday-relative clock. We anchor on a
// known date whose weekday is Monday (2024-01-01) and add the requested day
// offset so tests can reason in weekdays.
func at(t *testing.T, dayOffset, hour, min int) time.Time {
	t.Helper()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local) // Monday
	require.Equal(t, time.Monday, base.Weekday())
	return base.AddDate(0, 0, dayOffset).Add(time.Duration(hour)*time.Hour + time.Duration(min)*time.Minute)
}

func TestNightModeInWindow_Overnight(t *testing.T) {
	n := &NightModeConfig{StartTime: "23:00", EndTime: "08:00"}

	// Evening portion, same day.
	assert.True(t, n.inWindow(at(t, 0, 23, 30)))
	// Morning portion, next day.
	assert.True(t, n.inWindow(at(t, 1, 2, 0)))
	// Boundary: exactly start is inside, exactly end is outside.
	assert.True(t, n.inWindow(at(t, 0, 23, 0)))
	assert.False(t, n.inWindow(at(t, 1, 8, 0)))
	// Daytime is outside.
	assert.False(t, n.inWindow(at(t, 0, 12, 0)))
}

func TestNightModeInWindow_SameDay(t *testing.T) {
	n := &NightModeConfig{StartTime: "08:00", EndTime: "23:00"}
	assert.True(t, n.inWindow(at(t, 0, 12, 0)))
	assert.False(t, n.inWindow(at(t, 0, 7, 0)))
	assert.False(t, n.inWindow(at(t, 0, 23, 0)))
}

func TestNightModeInWindow_InvalidOrEqual(t *testing.T) {
	assert.False(t, (&NightModeConfig{StartTime: "bad", EndTime: "08:00"}).inWindow(at(t, 0, 2, 0)))
	assert.False(t, (&NightModeConfig{StartTime: "23:00", EndTime: "23:00"}).inWindow(at(t, 0, 23, 0)))
}

func TestNightModeDays_OvernightAttribution(t *testing.T) {
	// Only Fridays: the window runs Fri 23:00 → Sat 08:00.
	n := &NightModeConfig{StartTime: "23:00", EndTime: "08:00", Days: []string{"fri"}}

	// Friday is dayOffset 4 from Monday.
	friEvening := at(t, 4, 23, 30)
	require.Equal(t, time.Friday, friEvening.Weekday())
	assert.True(t, n.inWindow(friEvening))

	// Saturday 02:00 belongs to the Friday-night window.
	satMorning := at(t, 5, 2, 0)
	require.Equal(t, time.Saturday, satMorning.Weekday())
	assert.True(t, n.inWindow(satMorning))

	// Saturday 23:30 (a Saturday-night window) is NOT enabled.
	assert.False(t, n.inWindow(at(t, 5, 23, 30)))
	// Friday 02:00 belongs to the Thursday-night window, which is not enabled.
	assert.False(t, n.inWindow(at(t, 4, 2, 0)))
}

func TestNightModeDays_Tokens(t *testing.T) {
	n := &NightModeConfig{Days: []string{" Monday ", "TUE", "wed"}}
	assert.True(t, n.dayEnabled(time.Monday))
	assert.True(t, n.dayEnabled(time.Tuesday))
	assert.True(t, n.dayEnabled(time.Wednesday))
	assert.False(t, n.dayEnabled(time.Thursday))

	// Empty list = every day.
	assert.True(t, (&NightModeConfig{}).dayEnabled(time.Sunday))
}

func TestIsNightModeActive_ScopeAndEnable(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}
	c.NightMode = NightModeConfig{Enabled: true, StartTime: "23:00", EndTime: "08:00"}
	now := at(t, 0, 23, 30)

	assert.True(t, c.IsNightModeActive(-100, 0, now))
	// Disabled.
	c.NightMode.Enabled = false
	assert.False(t, c.IsNightModeActive(-100, 0, now))
	c.NightMode.Enabled = true
	// Not a moderation chat.
	assert.False(t, c.IsNightModeActive(-999, 0, now))
	// Excluded topic overrides.
	c.NightMode.ExcludedTopics = ChatTopicList{Refs: []ChatTopicRef{{Chat: -100, Topic: 5}}}
	assert.False(t, c.IsNightModeActive(-100, 5, now))
	assert.True(t, c.IsNightModeActive(-100, 6, now))
}

func TestValidateNightMode(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}
	c.NightMode = NightModeConfig{Enabled: true, StartTime: "23:00", EndTime: "08:00", Days: []string{"mon"}}
	require.NoError(t, c.validateNightMode())

	// Bad time + equal times + bad day all reported.
	c.NightMode = NightModeConfig{Enabled: true, StartTime: "25:99", EndTime: "25:99", Days: []string{"funday"}}
	err := c.validateNightMode()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start_time")
	assert.Contains(t, err.Error(), "funday")

	// Disabled = no validation.
	c.NightMode.Enabled = false
	assert.NoError(t, c.validateNightMode())
}

func TestNightModeDefault(t *testing.T) {
	c := &Config{}
	setDefaults(c)
	assert.Equal(t, 5, c.NightMode.ReplyDeleteSeconds)
}

func TestNightModeWindowEnd(t *testing.T) {
	// Overnight window 23:00-08:00.
	overnight := &Config{NightMode: NightModeConfig{StartTime: "23:00", EndTime: "08:00"}}
	// Evening portion (Friday 23:30) ends Saturday 08:00.
	end := overnight.NightModeWindowEnd(at(t, 4, 23, 30))
	assert.Equal(t, at(t, 5, 8, 0), end)
	// Morning portion (Saturday 02:00) ends the same Saturday 08:00.
	end = overnight.NightModeWindowEnd(at(t, 5, 2, 0))
	assert.Equal(t, at(t, 5, 8, 0), end)

	// Same-day window 08:00-23:00 ends 23:00 today.
	sameDay := &Config{NightMode: NightModeConfig{StartTime: "08:00", EndTime: "23:00"}}
	assert.Equal(t, at(t, 0, 23, 0), sameDay.NightModeWindowEnd(at(t, 0, 12, 0)))

	// Unparseable / equal bounds -> zero time.
	assert.True(t, (&Config{NightMode: NightModeConfig{StartTime: "x", EndTime: "08:00"}}).NightModeWindowEnd(at(t, 0, 2, 0)).IsZero())
}
