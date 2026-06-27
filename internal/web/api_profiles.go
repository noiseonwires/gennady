// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gennadium/internal/database"
)

// User-profile endpoints: list (with enrichment from name-history, first-seen
// and 7-day activity), and delete.

func (h *apiHandler) handleGetProfiles(w http.ResponseWriter, r *http.Request) {
	// Pagination + sort params (server-side, mirroring the messages list).
	limit := 25
	offset := 0
	sortKey := "discovery" // "discovery" (newest first-seen first) | "name"
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if v := r.URL.Query().Get("sort"); v == "name" || v == "discovery" {
		sortKey = v
	}
	// Optional case-insensitive username / display-name substring filter.
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))

	profiles, err := h.db.GetAllUserProfiles()
	if err != nil {
		writeWebErrf(w, errGetProfilesFailed, "failed to get profiles: %v", err)
		return
	}
	if profiles == nil {
		profiles = []database.UserProfile{}
	}

	// Enrich each profile with general tracking info (name history, 7d activity).
	// Also include users that have only general tracking data but no AI profile yet.
	type enrichedProfile struct {
		database.UserProfile
		DisplayName  string                     `json:"display_name,omitempty"`
		NameHistory  []database.UserNameHistory `json:"name_history,omitempty"`
		ActivityPlot string                     `json:"activity_plot,omitempty"`
		AIProfile    bool                       `json:"ai_profile"`
	}

	// latestNames returns the latest (username, display_name) recorded for a
	// user, with the username stripped of any leading "@". If no name history
	// is available, it returns ("", "") and the caller should fall back to the
	// AI-profile's `Username` field (which may be a pre-formatted blob).
	latestNames := func(nh []database.UserNameHistory) (string, string) {
		if len(nh) == 0 {
			return "", ""
		}
		latest := nh[len(nh)-1]
		u := strings.TrimPrefix(latest.Username, "@")
		return u, latest.DisplayName
	}

	dayKeys := last7DaysKeysWeb(time.Now())

	// Bulk-fetch all tracking data with one query each instead of N+1.
	nameHistByUser, _ := h.db.GetAllUserNameHistory()
	activityByUser, _ := h.db.GetAllUserDailyActivityRange(dayKeys)

	zeroActivity := make([]database.UserDailyActivity, len(dayKeys))
	for i, d := range dayKeys {
		zeroActivity[i] = database.UserDailyActivity{Date: d, Count: 0}
	}
	getActivity := func(uid int64) []database.UserDailyActivity {
		if a, ok := activityByUser[uid]; ok {
			return a
		}
		return zeroActivity
	}

	known := make(map[int64]bool, len(profiles))
	enriched := make([]enrichedProfile, 0, len(profiles))
	for _, p := range profiles {
		known[p.UserID] = true
		nh := nameHistByUser[p.UserID]
		username, displayName := latestNames(nh)
		// Prefer clean name-history data when available; otherwise keep the
		// pre-existing `Username` blob from message_info as fallback.
		if username != "" {
			p.Username = username
		}
		enriched = append(enriched, enrichedProfile{
			UserProfile:  p,
			DisplayName:  displayName,
			NameHistory:  nh,
			ActivityPlot: renderActivityPlotWeb(getActivity(p.UserID)),
			AIProfile:    p.Profile != "" || p.TgProfileAnalysis != "",
		})
	}

	// Append tracked-only users (no AI profile row).
	if ids, err := h.db.GetAllTrackedUserIDs(); err == nil {
		for _, uid := range ids {
			if known[uid] {
				continue
			}
			nh := nameHistByUser[uid]
			username, displayName := latestNames(nh)
			if username == "" {
				// Fallback to display name as the username slot so the UI has
				// something to render. This shouldn't happen for a tracked user
				// (we always record at least one of the two), but be safe.
				username = displayName
			}
			enriched = append(enriched, enrichedProfile{
				UserProfile: database.UserProfile{
					UserID:     uid,
					Username:   username,
					Profile:    "",
					Reputation: "neutral",
				},
				DisplayName:  displayName,
				NameHistory:  nh,
				ActivityPlot: renderActivityPlotWeb(getActivity(uid)),
				AIProfile:    false,
			})
		}
	}

	// Apply the username / display-name search filter (if any) before sorting
	// and pagination so `total` reflects the filtered result set.
	if search != "" {
		filtered := enriched[:0]
		for _, ep := range enriched {
			uname := strings.ToLower(strings.TrimPrefix(ep.Username, "@"))
			disp := strings.ToLower(ep.DisplayName)
			uid := strconv.FormatInt(ep.UserID, 10)
			if strings.Contains(uname, search) || strings.Contains(disp, search) || strings.Contains(uid, search) {
				filtered = append(filtered, ep)
			}
		}
		enriched = filtered
	}

	// Discovery time = global first-seen; fall back to the AI-profile creation
	// time (and finally zero) when no first-seen exists.
	discoveryTime := func(ep enrichedProfile) time.Time {
		if !ep.FirstSeenAt.IsZero() {
			return ep.FirstSeenAt
		}
		return ep.CreatedAt
	}
	// Name sort key: prefer @username, then display name (both lower-cased);
	// empty keys sort last.
	nameKey := func(ep enrichedProfile) string {
		if u := strings.ToLower(strings.TrimPrefix(ep.Username, "@")); u != "" {
			return u
		}
		return strings.ToLower(ep.DisplayName)
	}

	sort.SliceStable(enriched, func(i, j int) bool {
		a, b := enriched[i], enriched[j]
		if sortKey == "name" {
			ka, kb := nameKey(a), nameKey(b)
			// Push empty names to the bottom regardless of direction.
			if (ka == "") != (kb == "") {
				return ka != ""
			}
			if ka != kb {
				return ka < kb
			}
			return a.UserID < b.UserID
		}
		// Default: newest discovery first.
		ta, tb := discoveryTime(a), discoveryTime(b)
		if !ta.Equal(tb) {
			return ta.After(tb)
		}
		return a.UserID < b.UserID
	})

	total := len(enriched)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := enriched[start:end]
	if page == nil {
		page = []enrichedProfile{}
	}

	jsonResponse(w, map[string]interface{}{
		"profiles": page,
		"total":    total,
	})
}

// last7DaysKeysWeb returns YYYY-MM-DD keys for the past database.UserActivityWindowDays days (oldest first), local time.
func last7DaysKeysWeb(now time.Time) []string {
	keys := make([]string, database.UserActivityWindowDays)
	for i := 0; i < database.UserActivityWindowDays; i++ {
		keys[i] = now.AddDate(0, 0, -(database.UserActivityWindowDays-1)+i).Format("2006-01-02")
	}
	return keys
}

// renderActivityPlotWeb renders a per-day activity bar ("[..i..IiI]") for the Web UI.
func renderActivityPlotWeb(activity []database.UserDailyActivity) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for _, a := range activity {
		switch {
		case a.Count <= 0:
			sb.WriteByte('.')
		case a.Count < 10:
			sb.WriteByte('i')
		default:
			sb.WriteByte('I')
		}
	}
	sb.WriteByte(']')
	return sb.String()
}

func (h *apiHandler) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeWebErr(w, errMethodNotAllowed)
		return
	}

	req, err := decodeJSON[struct {
		UserID int64 `json:"user_id"`
	}](r)
	if err != nil {
		respondDecodeError(w, err)
		return
	}
	if req.UserID == 0 {
		writeWebErr(w, errUserIDRequired)
		return
	}

	if err := h.db.DeleteUserProfile(req.UserID); err != nil {
		writeWebErrf(w, errDeleteProfileFailed, "failed to delete profile: %v", err)
		return
	}
	// Also clear general tracking data so the user disappears from the list.
	if err := h.db.DeleteUserTrackingData(req.UserID); err != nil {
		log.Printf("Failed to delete tracking data for user %d: %v", req.UserID, err)
	}

	jsonResponse(w, map[string]string{"status": "ok"})
}
