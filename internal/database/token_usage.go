// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import "sort"

// TokenUsageStat holds daily and cumulative token counters for a single
// (service, model) pair.
type TokenUsageStat struct {
	Service     string `json:"service"`
	Model       string `json:"model"`
	DailyInput  int64  `json:"daily_input"`
	DailyOutput int64  `json:"daily_output"`
	TotalInput  int64  `json:"total_input"`
	TotalOutput int64  `json:"total_output"`
}

// RecordTokenUsage adds the given input/output token counts to the per-service,
// per-model, per-day counters. dayDate is a "YYYY-MM-DD" string controlled by
// the caller (so the calling code owns the timezone). Zero-token calls are
// ignored.
func (db *DB) RecordTokenUsage(model, service, dayDate string, inputTokens, outputTokens int64) error {
	if model == "" || service == "" || dayDate == "" || (inputTokens == 0 && outputTokens == 0) {
		return nil
	}
	query := `INSERT INTO token_usage (model, service, day_date, input_tokens, output_tokens)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(model, service, day_date) DO UPDATE SET
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens`
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(query, model, service, dayDate, inputTokens, outputTokens)
		return err
	}, "record token usage")
}

// GetTokenUsageStats returns daily (for today's dayDate) and cumulative token
// counters for every (service, model) pair that has recorded usage, sorted by
// service then model.
func (db *DB) GetTokenUsageStats(today string) ([]TokenUsageStat, error) {
	type key struct{ service, model string }
	stats := make(map[key]*TokenUsageStat)

	err := db.retryOnTransientError(func() error {
		// Reset the accumulator on each (re)try so retries don't double-count.
		stats = make(map[key]*TokenUsageStat)

		rows, err := db.conn.Query(
			`SELECT service, model, day_date, input_tokens, output_tokens FROM token_usage`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var service, model, dayDate string
			var in, out int64
			if err := rows.Scan(&service, &model, &dayDate, &in, &out); err != nil {
				return err
			}
			k := key{service: service, model: model}
			s := stats[k]
			if s == nil {
				s = &TokenUsageStat{Service: service, Model: model}
				stats[k] = s
			}
			s.TotalInput += in
			s.TotalOutput += out
			if dayDate == today {
				s.DailyInput += in
				s.DailyOutput += out
			}
		}
		return rows.Err()
	}, "get token usage stats")
	if err != nil {
		return nil, err
	}

	result := make([]TokenUsageStat, 0, len(stats))
	for _, s := range stats {
		result = append(result, *s)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Service != result[j].Service {
			return result[i].Service < result[j].Service
		}
		return result[i].Model < result[j].Model
	})
	return result, nil
}
