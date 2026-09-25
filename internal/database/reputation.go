// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	ReputationComponentCommunity        = "community"
	ReputationComponentModeration       = "moderation"
	ReputationComponentRecovery         = "recovery"
	ReputationComponentAI               = "ai"
	ReputationComponentProfileScreening = "profile_screening"
	reputationKindProfileScreeningText  = "profile_screening_text"
	reputationKindProfileScreeningPhoto = "profile_screening_photo"
	profileScreeningSyncBatchSize       = 75
)

func profileScreeningSourceKey(userID int64, kind string) string {
	return fmt.Sprintf("profile_screening:%s:%d", kind, userID)
}

func profileScreeningFindingCounts(analysis string) (textCount, photoCount int) {
	for _, line := range strings.Split(analysis, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "flagged by content safety") || strings.Contains(lower, "помечено системой контент-безопасности") {
			photoCount++
		} else {
			textCount++
		}
	}
	return textCount, photoCount
}

func setProfileScreeningEventsTx(tx *sql.Tx, userID int64, analysis string, occurredAt time.Time) error {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	textCount, photoCount := profileScreeningFindingCounts(analysis)
	for kind, count := range map[string]int{
		reputationKindProfileScreeningText:  textCount,
		reputationKindProfileScreeningPhoto: photoCount,
	} {
		key := profileScreeningSourceKey(userID, kind)
		if count <= 0 {
			if _, err := tx.Exec(`DELETE FROM reputation_events WHERE source_key = ?`, key); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(`INSERT INTO reputation_events
			(source_key, user_id, chat_id, kind, component, value, severity, half_life_days, occurred_at, updated_at)
			VALUES (?, ?, 0, ?, ?, ?, 0, 0, ?, ?)
			ON CONFLICT(source_key) DO UPDATE SET
				user_id = excluded.user_id, chat_id = 0, kind = excluded.kind,
				component = excluded.component, value = excluded.value,
				severity = 0, half_life_days = 0,
				occurred_at = excluded.occurred_at, updated_at = excluded.updated_at`,
			key, userID, kind, ReputationComponentProfileScreening,
			count, occurredAt, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

type profileScreeningSyncEvent struct {
	sourceKey    string
	userID       int64
	chatID       int64
	kind         string
	component    string
	value        float64
	severity     int
	halfLifeDays float64
	occurredAt   time.Time
}

func (event profileScreeningSyncEvent) matches(other profileScreeningSyncEvent) bool {
	return event.sourceKey == other.sourceKey &&
		event.userID == other.userID &&
		event.chatID == other.chatID &&
		event.kind == other.kind &&
		event.component == other.component &&
		event.value == other.value &&
		event.severity == other.severity &&
		event.halfLifeDays == other.halfLifeDays
}

// syncProfileScreeningReputationEvents reconciles the persistent screening
// characteristic from user_profiles. Normal profile writes maintain these
// events transactionally, so startup only writes missing or inconsistent rows.
func (db *DB) syncProfileScreeningReputationEvents() error {
	rows, err := db.conn.Query(`SELECT user_id, tg_profile_analysis, updated_at
		FROM user_profiles WHERE TRIM(tg_profile_analysis) <> ''`)
	if err != nil {
		return err
	}
	type screeningProfile struct {
		userID    int64
		analysis  string
		updatedAt string
	}
	var profiles []screeningProfile
	for rows.Next() {
		var profile screeningProfile
		if err := rows.Scan(&profile.userID, &profile.analysis, &profile.updatedAt); err != nil {
			rows.Close()
			return err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	desired := make(map[string]profileScreeningSyncEvent, len(profiles)*2)
	for _, profile := range profiles {
		occurredAt := parseTime(profile.updatedAt)
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		textCount, photoCount := profileScreeningFindingCounts(profile.analysis)
		for kind, count := range map[string]int{
			reputationKindProfileScreeningText:  textCount,
			reputationKindProfileScreeningPhoto: photoCount,
		} {
			if count <= 0 {
				continue
			}
			sourceKey := profileScreeningSourceKey(profile.userID, kind)
			desired[sourceKey] = profileScreeningSyncEvent{
				sourceKey:    sourceKey,
				userID:       profile.userID,
				chatID:       0,
				kind:         kind,
				component:    ReputationComponentProfileScreening,
				value:        float64(count),
				severity:     0,
				halfLifeDays: 0,
				occurredAt:   occurredAt,
			}
		}
	}

	eventRows, err := db.conn.Query(`SELECT source_key, user_id, chat_id, kind, component, value, severity, half_life_days
		FROM reputation_events WHERE kind IN (?, ?)`,
		reputationKindProfileScreeningText, reputationKindProfileScreeningPhoto)
	if err != nil {
		return err
	}
	var staleSourceKeys []string
	for eventRows.Next() {
		var current profileScreeningSyncEvent
		if err := eventRows.Scan(
			&current.sourceKey,
			&current.userID,
			&current.chatID,
			&current.kind,
			&current.component,
			&current.value,
			&current.severity,
			&current.halfLifeDays,
		); err != nil {
			eventRows.Close()
			return err
		}
		expected, exists := desired[current.sourceKey]
		if exists && current.matches(expected) {
			delete(desired, current.sourceKey)
			continue
		}
		staleSourceKeys = append(staleSourceKeys, current.sourceKey)
	}
	if err := eventRows.Close(); err != nil {
		return err
	}
	if err := eventRows.Err(); err != nil {
		return err
	}

	if len(desired) == 0 && len(staleSourceKeys) == 0 {
		return nil
	}

	pending := make([]profileScreeningSyncEvent, 0, len(desired))
	for _, event := range desired {
		pending = append(pending, event)
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].sourceKey < pending[j].sourceKey
	})
	sort.Strings(staleSourceKeys)

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteProfileScreeningEventsTx(tx, staleSourceKeys); err != nil {
		return err
	}
	if err := upsertProfileScreeningEventsTx(tx, pending); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("✓ Reconciled profile-screening reputation events: %d upserted, %d removed", len(pending), len(staleSourceKeys))
	return nil
}

func deleteProfileScreeningEventsTx(tx *sql.Tx, sourceKeys []string) error {
	for start := 0; start < len(sourceKeys); start += profileScreeningSyncBatchSize {
		end := min(start+profileScreeningSyncBatchSize, len(sourceKeys))
		placeholders := strings.TrimSuffix(strings.Repeat("?,", end-start), ",")
		args := make([]any, 0, end-start)
		for _, sourceKey := range sourceKeys[start:end] {
			args = append(args, sourceKey)
		}
		if _, err := tx.Exec(`DELETE FROM reputation_events WHERE source_key IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

func upsertProfileScreeningEventsTx(tx *sql.Tx, events []profileScreeningSyncEvent) error {
	updatedAt := time.Now().UTC()
	for start := 0; start < len(events); start += profileScreeningSyncBatchSize {
		end := min(start+profileScreeningSyncBatchSize, len(events))
		values := strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?),", end-start), ",")
		args := make([]any, 0, (end-start)*10)
		for _, event := range events[start:end] {
			args = append(args,
				event.sourceKey,
				event.userID,
				event.chatID,
				event.kind,
				event.component,
				event.value,
				event.severity,
				event.halfLifeDays,
				event.occurredAt,
				updatedAt,
			)
		}
		if _, err := tx.Exec(`INSERT INTO reputation_events
			(source_key, user_id, chat_id, kind, component, value, severity, half_life_days, occurred_at, updated_at)
			VALUES `+values+`
			ON CONFLICT(source_key) DO UPDATE SET
				user_id = excluded.user_id,
				chat_id = excluded.chat_id,
				kind = excluded.kind,
				component = excluded.component,
				value = excluded.value,
				severity = excluded.severity,
				half_life_days = excluded.half_life_days,
				occurred_at = excluded.occurred_at,
				updated_at = excluded.updated_at`, args...); err != nil {
			return err
		}
	}
	return nil
}

// ReputationEvent is one replaceable piece of reputation evidence. ChatID 0
// denotes global evidence (currently AI profile assessments) that contributes
// to every chat-local score.
type ReputationEvent struct {
	SourceKey    string    `json:"source_key"`
	UserID       int64     `json:"user_id"`
	ChatID       int64     `json:"chat_id"`
	Kind         string    `json:"kind"`
	Component    string    `json:"component"`
	Value        float64   `json:"value"`
	Severity     int       `json:"severity"`
	HalfLifeDays float64   `json:"half_life_days"`
	OccurredAt   time.Time `json:"occurred_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ReputationPolicy contains calculator limits. Event weights and half-lives
// are captured on each event so later configuration changes affect new
// evidence without rewriting history.
type ReputationPolicy struct {
	MinScore            float64
	MaxScore            float64
	CommunityMin        float64
	CommunityMax        float64
	AIMin               float64
	AIMax               float64
	ProfileScreeningMin float64
	RecoveryMax         float64
	Weights             map[string]float64
	EventLimits         map[string]float64
	HalfLives           map[string]float64
	VeryBadAt           float64
	BadBelow            float64
	GoodAbove           float64
	VeryGoodAt          float64
}

// ReputationScore is the effective score for one user in one chat.
type ReputationScore struct {
	UserID     int64              `json:"user_id"`
	ChatID     int64              `json:"chat_id"`
	Score      float64            `json:"score"`
	Grade      string             `json:"grade"`
	Components map[string]float64 `json:"components,omitempty"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

// UpsertReputationEvent inserts evidence or replaces the current value from
// the same source. This makes Telegram retries and reaction snapshots safe.
func (db *DB) UpsertReputationEvent(event ReputationEvent) error {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.UpdatedAt.IsZero() {
		event.UpdatedAt = time.Now().UTC()
	}
	_, err := db.conn.Exec(`INSERT INTO reputation_events
		(source_key, user_id, chat_id, kind, component, value, severity, half_life_days, occurred_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_key) DO UPDATE SET
			user_id = excluded.user_id,
			chat_id = excluded.chat_id,
			kind = excluded.kind,
			component = excluded.component,
			value = excluded.value,
			severity = excluded.severity,
			half_life_days = excluded.half_life_days,
			occurred_at = excluded.occurred_at,
			updated_at = excluded.updated_at
		WHERE excluded.severity >= reputation_events.severity`,
		event.SourceKey, event.UserID, event.ChatID, event.Kind, event.Component,
		event.Value, event.Severity, event.HalfLifeDays, event.OccurredAt, event.UpdatedAt)
	return err
}

// DeleteReputationEvent removes evidence that was explicitly reversed.
func (db *DB) DeleteReputationEvent(sourceKey string) error {
	_, err := db.conn.Exec(`DELETE FROM reputation_events WHERE source_key = ?`, sourceKey)
	return err
}

// DeleteReputationEventIfKind removes a source only if it still represents the
// expected lower-severity event. For example, removing a warning must not erase
// a mute that later replaced it for the same incident.
func (db *DB) DeleteReputationEventIfKind(sourceKey, kind string) error {
	_, err := db.conn.Exec(`DELETE FROM reputation_events WHERE source_key = ? AND kind = ?`, sourceKey, kind)
	return err
}

// GetReputationUserIDs returns every user with at least one reputation event.
func (db *DB) GetReputationUserIDs() ([]int64, error) {
	rows, err := db.conn.Query(`SELECT DISTINCT user_id FROM reputation_events ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetReputationScore calculates a chat-local score from current evidence.
func (db *DB) GetReputationScore(userID, chatID int64, policy ReputationPolicy, now time.Time) (ReputationScore, error) {
	rows, err := db.conn.Query(`SELECT kind, component, value, half_life_days, occurred_at, updated_at
		FROM reputation_events WHERE user_id = ? AND (chat_id = ? OR chat_id = 0)`, userID, chatID)
	if err != nil {
		return ReputationScore{}, err
	}
	defer rows.Close()

	acc := newReputationAccumulator()
	for rows.Next() {
		var kind, component, occurredRaw, updatedRaw string
		var value, halfLifeDays float64
		if err := rows.Scan(&kind, &component, &value, &halfLifeDays, &occurredRaw, &updatedRaw); err != nil {
			return ReputationScore{}, err
		}
		acc.add(kind, component, value, halfLifeDays, occurredRaw, updatedRaw, policy, now)
	}
	if err := rows.Err(); err != nil {
		return ReputationScore{}, err
	}
	return acc.score(userID, chatID, policy), nil
}

// GetReputationScores calculates all requested user/chat pairs with one query.
// Global evidence (chat_id=0) is applied to every requested chat.
func (db *DB) GetReputationScores(userIDs, chatIDs []int64, policy ReputationPolicy, now time.Time) (map[int64]map[int64]ReputationScore, error) {
	result := make(map[int64]map[int64]ReputationScore, len(userIDs))
	if len(userIDs) == 0 || len(chatIDs) == 0 {
		return result, nil
	}

	accs := make(map[int64]map[int64]*reputationAccumulator, len(userIDs))
	for _, userID := range userIDs {
		byChat := make(map[int64]*reputationAccumulator, len(chatIDs))
		for _, chatID := range chatIDs {
			byChat[chatID] = newReputationAccumulator()
		}
		accs[userID] = byChat
	}

	userMarks := strings.Join(makeQMarks(len(userIDs)), ",")
	chatMarks := strings.Join(makeQMarks(len(chatIDs)), ",")
	query := `SELECT user_id, chat_id, kind, component, value, half_life_days, occurred_at, updated_at
		FROM reputation_events WHERE user_id IN (` + userMarks + `)
		AND (chat_id = 0 OR chat_id IN (` + chatMarks + `))`
	args := make([]any, 0, len(userIDs)+len(chatIDs))
	for _, userID := range userIDs {
		args = append(args, userID)
	}
	for _, chatID := range chatIDs {
		args = append(args, chatID)
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, eventChatID int64
		var kind, component, occurredRaw, updatedRaw string
		var value, halfLifeDays float64
		if err := rows.Scan(&userID, &eventChatID, &kind, &component, &value, &halfLifeDays, &occurredRaw, &updatedRaw); err != nil {
			return nil, err
		}
		byChat := accs[userID]
		if eventChatID == 0 {
			for _, acc := range byChat {
				acc.add(kind, component, value, halfLifeDays, occurredRaw, updatedRaw, policy, now)
			}
		} else if acc := byChat[eventChatID]; acc != nil {
			acc.add(kind, component, value, halfLifeDays, occurredRaw, updatedRaw, policy, now)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for userID, byChat := range accs {
		scores := make(map[int64]ReputationScore, len(byChat))
		for chatID, acc := range byChat {
			scores[chatID] = acc.score(userID, chatID, policy)
		}
		result[userID] = scores
	}
	return result, nil
}

type reputationAccumulator struct {
	components map[string]float64
	updatedAt  time.Time
}

func newReputationAccumulator() *reputationAccumulator {
	return &reputationAccumulator{components: make(map[string]float64)}
}

func (a *reputationAccumulator) add(kind, component string, value, halfLifeDays float64, occurredRaw, updatedRaw string, policy ReputationPolicy, now time.Time) {
	if weight, ok := policy.Weights[kind]; ok {
		value *= weight
	}
	if configuredHalfLife, ok := policy.HalfLives[kind]; ok {
		halfLifeDays = configuredHalfLife
	}
	if limit := policy.EventLimits[kind]; limit > 0 {
		value = clampFloat(value, -limit, limit)
	}
	occurredAt := parseTime(occurredRaw)
	if halfLifeDays > 0 && now.After(occurredAt) {
		ageDays := now.Sub(occurredAt).Hours() / 24
		value *= math.Pow(0.5, ageDays/halfLifeDays)
	}
	a.components[component] += value
	if eventUpdatedAt := parseTime(updatedRaw); eventUpdatedAt.After(a.updatedAt) {
		a.updatedAt = eventUpdatedAt
	}
}

func (a *reputationAccumulator) score(userID, chatID int64, policy ReputationPolicy) ReputationScore {
	components := a.components

	components[ReputationComponentCommunity] = clampFloat(components[ReputationComponentCommunity], policy.CommunityMin, policy.CommunityMax)
	components[ReputationComponentAI] = clampFloat(components[ReputationComponentAI], policy.AIMin, policy.AIMax)
	components[ReputationComponentProfileScreening] = clampFloat(components[ReputationComponentProfileScreening], policy.ProfileScreeningMin, 0)
	components[ReputationComponentRecovery] = clampFloat(components[ReputationComponentRecovery], 0, policy.RecoveryMax)
	nonRecovery := 0.0
	for component, value := range components {
		if component != ReputationComponentRecovery {
			nonRecovery += value
		}
	}
	components[ReputationComponentRecovery] = math.Min(components[ReputationComponentRecovery], math.Max(0, -nonRecovery))
	total := nonRecovery + components[ReputationComponentRecovery]
	total = clampFloat(total, policy.MinScore, policy.MaxScore)

	return ReputationScore{
		UserID: userID, ChatID: chatID, Score: total, Grade: reputationGradeForPolicy(total, policy),
		Components: components, UpdatedAt: a.updatedAt,
	}
}

// RecordCleanReputationMessage advances rate-limited recovery progress and
// emits one recovery event whenever the configured message interval is met.
func (db *DB) RecordCleanReputationMessage(userID, chatID int64, messagesPerPoint, dailyMax int, now time.Time) (bool, error) {
	if messagesPerPoint <= 0 || dailyMax <= 0 {
		return false, nil
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var cleanCount, sequence, dayCount int
	var dayDate string
	err = tx.QueryRow(`SELECT clean_count, sequence, day_date, day_count
		FROM reputation_clean_state WHERE user_id = ? AND chat_id = ?`, userID, chatID).
		Scan(&cleanCount, &sequence, &dayDate, &dayCount)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	today := now.UTC().Format("2006-01-02")
	if dayDate != today {
		dayDate, dayCount = today, 0
	}
	if dayCount >= dailyMax {
		return false, tx.Commit()
	}

	cleanCount++
	dayCount++
	emitted := false
	if cleanCount >= messagesPerPoint {
		cleanCount -= messagesPerPoint
		sequence++
		emitted = true
		_, err = tx.Exec(`INSERT INTO reputation_events
			(source_key, user_id, chat_id, kind, component, value, severity, half_life_days, occurred_at, updated_at)
			VALUES (?, ?, ?, 'clean_recovery', ?, 1, 0, 0, ?, ?)`,
			recoverySourceKey(userID, chatID, sequence), userID, chatID,
			ReputationComponentRecovery, now, now)
		if err != nil {
			return false, err
		}
	}
	_, err = tx.Exec(`INSERT INTO reputation_clean_state
		(user_id, chat_id, clean_count, sequence, day_date, day_count)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, chat_id) DO UPDATE SET
			clean_count = excluded.clean_count, sequence = excluded.sequence,
			day_date = excluded.day_date, day_count = excluded.day_count`,
		userID, chatID, cleanCount, sequence, dayDate, dayCount)
	if err != nil {
		return false, err
	}
	return emitted, tx.Commit()
}

// ResetReputationRecovery starts clean-message recovery over after a confirmed
// incident and removes recovery earned before that incident.
func (db *DB) ResetReputationRecovery(userID, chatID int64) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM reputation_clean_state WHERE user_id = ? AND chat_id = ?`, userID, chatID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM reputation_events WHERE user_id = ? AND chat_id = ? AND component = ?`,
		userID, chatID, ReputationComponentRecovery); err != nil {
		return err
	}
	return tx.Commit()
}

func recoverySourceKey(userID, chatID int64, sequence int) string {
	return fmt.Sprintf("recovery:%d:%d:%d", chatID, userID, sequence)
}

// ReputationGrade maps a numeric score to the public five-grade scale.
func ReputationGrade(score float64) string {
	switch {
	case score <= -20:
		return "very_bad"
	case score < -5:
		return "bad"
	case score <= 5:
		return "neutral"
	case score < 20:
		return "good"
	default:
		return "very_good"
	}
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if minValue < maxValue {
		return math.Max(minValue, math.Min(maxValue, value))
	}
	return value
}

func reputationGradeForPolicy(score float64, policy ReputationPolicy) string {
	veryBad, bad, good, veryGood := policy.VeryBadAt, policy.BadBelow, policy.GoodAbove, policy.VeryGoodAt
	if !(veryBad < bad && bad < good && good < veryGood) {
		return ReputationGrade(score)
	}
	switch {
	case score <= veryBad:
		return "very_bad"
	case score < bad:
		return "bad"
	case score <= good:
		return "neutral"
	case score < veryGood:
		return "good"
	default:
		return "very_good"
	}
}
