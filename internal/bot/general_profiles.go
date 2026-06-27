// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"log"
	"strings"
	"time"

	"gennadium/internal/database"
	"gennadium/internal/i18n"
)

// last7DaysKeys returns the YYYY-MM-DD keys for the past database.UserActivityWindowDays
// days, oldest first, using local time (matches RecordIncomingMessage's day key).
func last7DaysKeys(now time.Time) []string {
	keys := make([]string, database.UserActivityWindowDays)
	for i := 0; i < database.UserActivityWindowDays; i++ {
		d := now.AddDate(0, 0, -(database.UserActivityWindowDays-1)+i)
		keys[i] = d.Format("2006-01-02")
	}
	return keys
}

// renderActivityPlot renders a compact per-day activity bar:
//
//	'.' = 0 messages, 'i' = 1..9 messages, 'I' = 10+ messages.
//
// The output is wrapped with brackets, e.g. "[..i..IiI]".
func renderActivityPlot(counts []int) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for _, c := range counts {
		switch {
		case c <= 0:
			sb.WriteByte('.')
		case c < 10:
			sb.WriteByte('i')
		default:
			sb.WriteByte('I')
		}
	}
	sb.WriteByte(']')
	return sb.String()
}

// GeneralProfileSummary is a small struct collecting the most relevant general
// tracking info for display in moderation messages and the Web UI.
type GeneralProfileSummary struct {
	UserID        int64                        `json:"user_id"`
	Username      string                       `json:"username"`
	DisplayName   string                       `json:"display_name"`
	NameHistory   []database.UserNameHistory   `json:"name_history,omitempty"`
	FirstSeen     time.Time                    `json:"first_seen,omitempty"`
	DailyActivity []database.UserDailyActivity `json:"daily_activity,omitempty"`
	ActivityPlot  string                       `json:"activity_plot,omitempty"`
}

// getGeneralProfileSummary fetches name history, first-seen entries, and the
// last-7-days activity counts for a user.
func (b *Bot) getGeneralProfileSummary(userID int64) (*GeneralProfileSummary, error) {
	history, err := b.db.GetUserNameHistory(userID)
	if err != nil {
		return nil, err
	}
	firstSeen, err := b.db.GetUserFirstSeen(userID)
	if err != nil {
		return nil, err
	}
	keys := last7DaysKeys(time.Now())
	activity, _ := b.db.GetUserDailyActivityRange(userID, keys)

	counts := make([]int, len(activity))
	for i, a := range activity {
		counts[i] = a.Count
	}

	summary := &GeneralProfileSummary{
		UserID:        userID,
		NameHistory:   history,
		FirstSeen:     firstSeen,
		DailyActivity: activity,
		ActivityPlot:  renderActivityPlot(counts),
	}
	if len(history) > 0 {
		latest := history[len(history)-1]
		summary.Username = latest.Username
		summary.DisplayName = latest.DisplayName
	}
	return summary, nil
}

// formatGeneralProfileForModeration formats general-profile info as a short
// human-readable block to include in moderation messages. Returns an empty
// string if the feature is disabled or no data is available.
func (b *Bot) formatGeneralProfileForModeration(userID int64) string {
	if !b.config.UserProfiles.Enabled {
		return ""
	}
	s, err := b.getGeneralProfileSummary(userID)
	if err != nil || s == nil {
		return ""
	}
	if len(s.NameHistory) == 0 && s.FirstSeen.IsZero() && s.ActivityPlot == "[.......]" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("👤 ")

	// Activity plot.
	if s.ActivityPlot != "" {
		sb.WriteString("7d: ")
		sb.WriteString(s.ActivityPlot)
	}

	// Name history (only if more than one entry - first one is the initial seen value).
	if len(s.NameHistory) > 1 {
		sb.WriteString(fmt.Sprintf(" | names: %d changes", len(s.NameHistory)-1))
		// Show last up to 3 prior names.
		prior := s.NameHistory[:len(s.NameHistory)-1]
		start := 0
		if len(prior) > 3 {
			start = len(prior) - 3
		}
		labels := make([]string, 0, len(prior)-start)
		for _, h := range prior[start:] {
			label := h.Username
			if label == "" {
				label = h.DisplayName
			}
			if label == "" {
				continue
			}
			labels = append(labels, label)
		}
		if len(labels) > 0 {
			sb.WriteString(" (was: ")
			sb.WriteString(strings.Join(labels, ", "))
			sb.WriteString(")")
		}
	}

	// First-seen (global earliest across all chats).
	if !s.FirstSeen.IsZero() {
		sb.WriteString(" | ")
		sb.WriteString(i18n.T("prof.first_seen"))
		sb.WriteString(": ")
		days := int(time.Since(s.FirstSeen).Hours() / 24)
		if days <= 0 {
			sb.WriteString(i18n.T("prof.today"))
		} else {
			sb.WriteString(i18n.Tf("prof.days_ago", days))
		}
	}

	return sb.String()
}

// notifyAdminsOnUsernameReuse posts an admin-chat alert when a user_id we have
// just started tracking is using a @username previously held by one or more
// different user_ids. This catches the "delete + re-register with the same
// handle" pattern (Telegram never reuses numeric IDs, but a re-registered
// account can grab a freed @username).
//
// Safe to call from a goroutine: it reads from the DB and posts a Telegram
// message, but performs no writes that would conflict with the caller.
func (b *Bot) notifyAdminsOnUsernameReuse(newUserID int64, newUsername, newDisplayName string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("notifyAdminsOnUsernameReuse panic: %v", r)
		}
	}()

	newUsername = strings.TrimSpace(strings.TrimPrefix(newUsername, "@"))
	if newUsername == "" {
		return
	}
	if b.config.Admin.ChatID == 0 {
		return
	}
	if b.config.UserProfiles.DisableUsernameReuseAlerts {
		return
	}

	reusers, err := b.db.FindUsernameReusers(newUsername, newUserID)
	if err != nil {
		log.Printf("FindUsernameReusers(@%s, %d) failed: %v", newUsername, newUserID, err)
		return
	}
	if len(reusers) == 0 {
		return
	}

	// Build a compact list of prior holders (cap to avoid huge messages).
	const maxPriors = 5
	var lines []string
	for i, r := range reusers {
		if i >= maxPriors {
			lines = append(lines, fmt.Sprintf("  • … and %d more", len(reusers)-maxPriors))
			break
		}
		name := strings.TrimSpace(r.DisplayName)
		if name == "" {
			name = "(no display name)"
		}
		lines = append(lines, fmt.Sprintf("  • %s - id %d, last seen %s",
			name, r.UserID, r.LastUsedAt.Format("2006-01-02 15:04")))
	}
	priorList := strings.Join(lines, "\n")

	newName := strings.TrimSpace(newDisplayName)
	if newName == "" {
		newName = "(no display name)"
	}

	b.sendToAdminChat(i18n.Tf("admin.username_reuse_alert",
		newUsername, newName, newUserID, priorList))
}
