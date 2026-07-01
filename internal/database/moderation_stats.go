// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

// Moderation funnel stat keys. One row per (stat, day_date) in the
// moderation_stats table accumulates a daily counter; cumulative figures are
// summed across all days.
const (
	ModStatReceived      = "received"       // messages received in a moderation chat
	ModStatLightFlagged  = "light_flagged"  // flagged by the light model
	ModStatFullConfirmed = "full_confirmed" // confirmed by the full model
	ModStatAutoAction    = "auto_action"    // triggered an automatic moderation action
	ModStatManualCleared = "manual_cleared" // manually cleared by a moderator
	ModStatManualAction  = "manual_action"  // led to a manual action (warn/delete/mute)
)

// ModerationStatBuckets holds the per-window counters for a single funnel stat.
type ModerationStatBuckets struct {
	Stat      string `json:"stat"`
	Today     int64  `json:"today"`
	Yesterday int64  `json:"yesterday"`
	DayBefore int64  `json:"day_before"`
	AllTime   int64  `json:"all_time"`
}

// IncrementModerationStat bumps the daily counter for the given funnel stat by
// delta. dayDate is a "YYYY-MM-DD" string controlled by the caller (so the
// calling code owns the timezone). Non-positive deltas are ignored.
func (db *DB) IncrementModerationStat(stat, dayDate string, delta int64) error {
	if stat == "" || dayDate == "" || delta <= 0 {
		return nil
	}
	query := `INSERT INTO moderation_stats (stat, day_date, count)
		VALUES (?, ?, ?)
		ON CONFLICT(stat, day_date) DO UPDATE SET
			count = count + excluded.count`
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, stat, dayDate, delta)
		return err
	}, "increment moderation stat")
}

// GetModerationStats returns today / yesterday / day-before / all-time counters
// for every funnel stat. Callers pass the three day keys ("YYYY-MM-DD") so the
// timezone matches the increment side. Stats with no recorded rows come back
// with zeroes.
func (db *DB) GetModerationStats(today, yesterday, dayBefore string) ([]ModerationStatBuckets, error) {
	keys := []string{
		ModStatReceived, ModStatLightFlagged, ModStatFullConfirmed,
		ModStatAutoAction, ModStatManualCleared, ModStatManualAction,
	}
	stats := make(map[string]*ModerationStatBuckets, len(keys))

	err := db.retryOnTransientError(func() error {
		// Reset the accumulator on each (re)try so retries don't double-count.
		stats = make(map[string]*ModerationStatBuckets, len(keys))
		for _, k := range keys {
			stats[k] = &ModerationStatBuckets{Stat: k}
		}

		rows, err := db.conn.Query(`SELECT stat, day_date, count FROM moderation_stats`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var stat, dayDate string
			var count int64
			if err := rows.Scan(&stat, &dayDate, &count); err != nil {
				return err
			}
			s := stats[stat]
			if s == nil {
				s = &ModerationStatBuckets{Stat: stat}
				stats[stat] = s
			}
			s.AllTime += count
			switch dayDate {
			case today:
				s.Today += count
			case yesterday:
				s.Yesterday += count
			case dayBefore:
				s.DayBefore += count
			}
		}
		return rows.Err()
	}, "get moderation stats")
	if err != nil {
		return nil, err
	}

	result := make([]ModerationStatBuckets, 0, len(keys))
	for _, k := range keys {
		result = append(result, *stats[k])
	}
	return result, nil
}
