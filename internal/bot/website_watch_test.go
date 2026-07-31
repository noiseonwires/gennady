// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watchTarget is a literal public IP so the SSRF guard passes without DNS.
const watchTarget = "https://93.184.216.34/rules"

func TestWebsiteWatchTaskName_Deterministic(t *testing.T) {
	a := websiteWatchTaskName("https://example.com/a")
	b := websiteWatchTaskName("https://example.com/a")
	c := websiteWatchTaskName("https://example.com/b")
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
	assert.True(t, strings.HasPrefix(a, "watch_"))
}

func TestNormalizeWatchedContent(t *testing.T) {
	// Whitespace-only differences collapse to the same text.
	a := normalizeWatchedContent("Hello   world\r\n\n\n\nSecond  line   ", 0)
	b := normalizeWatchedContent("Hello world\n\nSecond line", 0)
	assert.Equal(t, b, a)
	assert.Equal(t, "Hello world\n\nSecond line", a)

	// maxRunes truncates.
	assert.LessOrEqual(t, len([]rune(normalizeWatchedContent("абвгдежзий", 5))), 5)
}

func TestHashWatchedContent(t *testing.T) {
	assert.Equal(t, hashWatchedContent("x"), hashWatchedContent("x"))
	assert.NotEqual(t, hashWatchedContent("x"), hashWatchedContent("y"))
}

func TestIsNoWebsiteChangeAnswer(t *testing.T) {
	assert.True(t, isNoWebsiteChangeAnswer("", "NO_CHANGES"))
	assert.True(t, isNoWebsiteChangeAnswer("   \n", "NO_CHANGES"))
	assert.True(t, isNoWebsiteChangeAnswer("NO_CHANGES", "NO_CHANGES"))
	assert.True(t, isNoWebsiteChangeAnswer(" no_changes.", "NO_CHANGES"))
	assert.True(t, isNoWebsiteChangeAnswer("\"NO_CHANGES\"", "NO_CHANGES"))
	assert.False(t, isNoWebsiteChangeAnswer("The price list gained two rows.", "NO_CHANGES"))
	// A marker mentioned inside a real summary must not suppress it.
	assert.False(t, isNoWebsiteChangeAnswer("Section NO_CHANGES was renamed", "NO_CHANGES"))
}

func TestGetWebsiteWatchPrompts(t *testing.T) {
	b, _ := newMockBot(t)
	site := config.WatchedSite{Name: "Rules", URL: watchTarget}

	// Unconfigured prompts yield nothing.
	s, u := b.getWebsiteWatchPrompts(site, "old", "new", "- a\n+ b")
	assert.Empty(t, s)
	assert.Empty(t, u)

	b.config.AI.WebsiteWatch.NoChangesMarker = "NO_CHANGES"
	b.config.AI.WebsiteWatch.Prompt = config.PromptPair{
		System: "answer {{marker}} when nothing changed",
		User:   "{{name}} {{url}}\n{{diff}}\n{{previous}}|{{current}}",
	}
	s, u = b.getWebsiteWatchPrompts(site, "old", "new", "- a\n+ b")
	assert.Equal(t, "answer NO_CHANGES when nothing changed", s)
	assert.Contains(t, u, "Rules "+watchTarget)
	assert.Contains(t, u, "- a\n+ b")
	assert.Contains(t, u, "old|new")
}

// newWatchBot wires an integration bot whose page fetches return *page and
// whose AI calls return *aiReply. Page extraction goes through ExtractorAPI so
// the test does not depend on the HTML heuristics of manual extraction.
func newWatchBot(t *testing.T, page, aiReply *string) (*Bot, *mockTelegram) {
	t.Helper()
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "extractor") {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "COMPLETE", "title": "Rules", "text": *page,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": *aiReply}}},
		})
	})
	b.config.AI.Enabled = true
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.AI.LinkSummaries.ExtractorAPIKey = "k"
	b.config.AI.FullModel = fullModelConfigs()
	b.config.AI.WebsiteWatch.UseFullModel = true
	b.config.AI.WebsiteWatch.MaxContentLength = 8192
	b.config.AI.WebsiteWatch.MaxDiffLength = 4096
	b.config.AI.WebsiteWatch.NoChangesMarker = "NO_CHANGES"
	b.config.AI.WebsiteWatch.Prompt = config.PromptPair{System: "watch", User: "{{diff}}"}
	return b, tg
}

func TestProcessWatchedSite_FirstRunStoresBaselineWithoutPosting(t *testing.T) {
	page, reply := "Rule one. Rule two.", "irrelevant"
	b, tg := newWatchBot(t, &page, &reply)

	b.processWatchedSite(config.WatchedSite{Name: "Rules", URL: watchTarget})

	assert.Empty(t, tg.SentMessages, "the first fetch only records the baseline")
	snap, err := b.db.GetWebsiteSnapshot(watchTarget)
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.Equal(t, "Rule one. Rule two.", snap.Content)
}

func TestProcessWatchedSite_ReportsChange(t *testing.T) {
	page, reply := "Rule one.", "A third rule was added."
	b, tg := newWatchBot(t, &page, &reply)
	site := config.WatchedSite{Name: "Rules", URL: watchTarget}

	b.processWatchedSite(site) // baseline
	page = "Rule one. Rule three."
	b.processWatchedSite(site)

	require.Len(t, tg.SentMessages, 1)
	sent := tg.SentMessages[0]
	assert.Equal(t, int64(-100), sent.ChatID)
	assert.Contains(t, sent.Text, "Rules")
	assert.Contains(t, sent.Text, "A third rule was added.")
	assert.Contains(t, sent.Text, watchTarget)

	// The reported state becomes the new baseline.
	snap, err := b.db.GetWebsiteSnapshot(watchTarget)
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.Equal(t, "Rule one. Rule three.", snap.Content)
}

func TestProcessWatchedSite_MarkerKeepsBaselineAndStaysSilent(t *testing.T) {
	page, reply := "Rule one.", "NO_CHANGES"
	b, tg := newWatchBot(t, &page, &reply)
	site := config.WatchedSite{Name: "Rules", URL: watchTarget}

	b.processWatchedSite(site) // baseline
	page = "Rule one. Views: 1234."
	b.processWatchedSite(site)

	assert.Empty(t, tg.SentMessages)
	// The baseline is kept, so a later meaningful edit is diffed against the
	// last *reported* state instead of against the noise.
	snap, err := b.db.GetWebsiteSnapshot(watchTarget)
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.Equal(t, "Rule one.", snap.Content)
}

func TestProcessWatchedSite_UnchangedPageStaysSilent(t *testing.T) {
	page, reply := "Rule one.", "should not be used"
	b, tg := newWatchBot(t, &page, &reply)
	site := config.WatchedSite{Name: "Rules", URL: watchTarget}

	b.processWatchedSite(site) // baseline
	b.processWatchedSite(site)

	assert.Empty(t, tg.SentMessages)
}

func TestProcessWatchedSite_KeepsBaselineWhenPromptMissing(t *testing.T) {
	page, reply := "Rule one.", "unused"
	b, tg := newWatchBot(t, &page, &reply)
	b.config.AI.WebsiteWatch.Prompt = config.PromptPair{}
	site := config.WatchedSite{Name: "Rules", URL: watchTarget}

	b.processWatchedSite(site) // baseline
	page = "Rule one. Rule two."
	b.processWatchedSite(site)

	assert.Empty(t, tg.SentMessages)
	snap, err := b.db.GetWebsiteSnapshot(watchTarget)
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.Equal(t, "Rule one.", snap.Content)
}

func TestPublishWebsiteChange_FallsBackToTitleAndDomain(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	b.publishWebsiteChange(config.WatchedSite{URL: watchTarget}, "Page Title", "Something changed.")
	require.Len(t, tg.SentMessages, 1)
	assert.Contains(t, tg.SentMessages[0].Text, "Page Title")

	b.publishWebsiteChange(config.WatchedSite{URL: watchTarget}, "", "Something changed.")
	require.Len(t, tg.SentMessages, 2)
	assert.Contains(t, tg.SentMessages[1].Text, "93.184.216.34")
}
