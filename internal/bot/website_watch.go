// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strings"
	"unicode/utf8"

	"gennadium/internal/config"
)

// websiteWatchTaskName returns the scheduled-event name for a watched page.
func websiteWatchTaskName(pageURL string) string {
	h := sha256.Sum256([]byte(pageURL))
	return fmt.Sprintf("watch_%s", hex.EncodeToString(h[:8]))
}

var watchWhitespaceRegex = regexp.MustCompile(`[^\S\n]{2,}`)
var watchBlankLinesRegex = regexp.MustCompile(`\n{3,}`)

// normalizeWatchedContent trims incidental whitespace differences so that
// re-flowed markup alone does not look like a change, then caps the text at
// maxRunes.
func normalizeWatchedContent(content string, maxRunes int) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(watchWhitespaceRegex.ReplaceAllString(line, " "))
	}
	text := strings.TrimSpace(watchBlankLinesRegex.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
	if maxRunes > 0 {
		text = truncateMessage(text, maxRunes)
	}
	return text
}

// hashWatchedContent returns the fingerprint stored alongside a snapshot, used
// for the cheap "nothing changed at all" check before diffing.
func hashWatchedContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// processWatchedSite fetches a watched page, diffs it against the last reported
// snapshot and publishes an AI-written summary of the change. The stored
// snapshot only advances when something was actually reported, so a series of
// individually insignificant edits still adds up to a reportable change.
func (b *Bot) processWatchedSite(site config.WatchedSite) {
	log.Printf("🌐 WebsiteWatch: fetching %q from %s", site.Name, site.URL)

	content, title, _, err := b.fetchAndExtractLinkContent(site.URL)
	if err != nil {
		log.Printf("🌐 WebsiteWatch: error fetching %q: %v", site.Name, err)
		return
	}

	current := normalizeWatchedContent(content, b.config.AI.WebsiteWatch.MaxContentLength)
	if current == "" {
		log.Printf("🌐 WebsiteWatch: %q returned no extractable text, skipping", site.Name)
		return
	}
	hash := hashWatchedContent(current)

	snapshot, err := b.db.GetWebsiteSnapshot(site.URL)
	if err != nil {
		log.Printf("🌐 WebsiteWatch: error reading snapshot for %q: %v", site.Name, err)
		return
	}

	// First run: remember the page as-is without reporting the whole thing.
	if snapshot == nil {
		if err := b.db.SaveWebsiteSnapshot(site.URL, current, hash); err != nil {
			log.Printf("🌐 WebsiteWatch: error saving first snapshot for %q: %v", site.Name, err)
			return
		}
		log.Printf("🌐 WebsiteWatch: stored baseline for %q (%d chars), changes will be reported from the next check",
			site.Name, utf8.RuneCountInString(current))
		return
	}

	if snapshot.ContentHash == hash {
		log.Printf("🌐 WebsiteWatch: %q is unchanged", site.Name)
		b.touchWatchedSite(site)
		return
	}

	diff := computeTextDiff(snapshot.Content, current)
	if strings.TrimSpace(diff) == "" {
		log.Printf("🌐 WebsiteWatch: %q changed only in whitespace, skipping", site.Name)
		b.touchWatchedSite(site)
		return
	}
	if maxDiff := b.config.AI.WebsiteWatch.MaxDiffLength; maxDiff > 0 {
		diff = truncateMessage(diff, maxDiff)
	}
	log.Printf("🌐 WebsiteWatch: %q changed, diff is %d chars", site.Name, utf8.RuneCountInString(diff))

	summary, ok := b.summarizeWebsiteChange(site, snapshot.Content, current, diff)
	if !ok {
		b.touchWatchedSite(site)
		return
	}

	b.publishWebsiteChange(site, title, summary)

	if err := b.db.SaveWebsiteSnapshot(site.URL, current, hash); err != nil {
		log.Printf("🌐 WebsiteWatch: error saving snapshot for %q: %v", site.Name, err)
	}
}

// touchWatchedSite records the check without advancing the stored baseline.
func (b *Bot) touchWatchedSite(site config.WatchedSite) {
	if err := b.db.TouchWebsiteSnapshot(site.URL); err != nil {
		log.Printf("🌐 WebsiteWatch: error updating check time for %q: %v", site.Name, err)
	}
}

// summarizeWebsiteChange asks the AI to describe the change. It returns
// ok=false when the AI reports no meaningful change (empty answer or the
// configured marker), when the prompts are missing, or when the call fails.
func (b *Bot) summarizeWebsiteChange(site config.WatchedSite, previous, current, diff string) (string, bool) {
	systemPrompt, userPrompt := b.getWebsiteWatchPrompts(site, previous, current, diff)
	if systemPrompt == "" || userPrompt == "" {
		return "", false
	}

	diffLen := utf8.RuneCountInString(diff)
	threshold := b.config.AI.WebsiteWatch.LightModelThreshold
	var modelConfigs config.AIModelConfigs
	if b.config.AI.WebsiteWatch.UseFullModel && (threshold <= 0 || diffLen <= threshold) {
		modelConfigs = b.config.AI.FullModel
	} else {
		modelConfigs = b.config.AI.LightModel
		if threshold > 0 && diffLen > threshold {
			log.Printf("🌐 WebsiteWatch: diff is very long (%d chars > threshold %d), using light model", diffLen, threshold)
		}
	}

	answer, err := b.callAzureOpenAIWithRetriesAndBackoff(
		"website_watch", userPrompt, systemPrompt, modelConfigs, 1000, 4, scheduledTaskBackoff,
	)
	if err != nil {
		log.Printf("🌐 WebsiteWatch: AI analysis failed for %q: %v", site.Name, err)
		return "", false
	}

	summary := strings.TrimSpace(answer)
	if isNoWebsiteChangeAnswer(summary, b.config.AI.WebsiteWatch.NoChangesMarker) {
		log.Printf("🌐 WebsiteWatch: AI found no meaningful change in %q", site.Name)
		return "", false
	}
	return summary, true
}

// isNoWebsiteChangeAnswer reports whether the AI answer means "nothing worth
// reporting". Punctuation and quoting around the marker are tolerated.
func isNoWebsiteChangeAnswer(answer, marker string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return true
	}
	if marker == "" {
		return false
	}
	trimmed = strings.Trim(trimmed, "\"'`*.!。 \t\n")
	return strings.EqualFold(trimmed, strings.TrimSpace(marker))
}

// getWebsiteWatchPrompts renders the change-analysis prompts for a site.
func (b *Bot) getWebsiteWatchPrompts(site config.WatchedSite, previous, current, diff string) (string, string) {
	prompts := b.config.AI.WebsiteWatch.Prompt
	if prompts.System == "" || prompts.User == "" {
		log.Printf("🌐 WebsiteWatch: prompt is not configured, skipping analysis of %q", site.Name)
		return "", ""
	}

	replacements := map[string]string{
		"name":     site.Name,
		"url":      site.URL,
		"diff":     diff,
		"previous": previous,
		"current":  current,
		"marker":   b.config.AI.WebsiteWatch.NoChangesMarker,
	}
	return applyReplacements(prompts.System, replacements), applyReplacements(prompts.User, replacements)
}

// publishWebsiteChange posts the change report to the site's destinations.
func (b *Bot) publishWebsiteChange(site config.WatchedSite, title, summary string) {
	name := site.Name
	if name == "" {
		name = title
	}
	if name == "" {
		name = b.extractDomain(site.URL)
	}

	maxLen := site.MaxMessageLength
	if maxLen <= 0 {
		maxLen = MaxTelegramMessageLength
	}

	header := fmt.Sprintf("🌐 *%s*\n\n", name)
	footer := fmt.Sprintf("\n\n🔗 %s", site.URL)
	budget := maxLen - utf8.RuneCountInString(header) - utf8.RuneCountInString(footer)
	if budget < 1 {
		budget = 1
	}
	message := header + truncateMessage(summary, budget) + footer

	targets := b.config.EffectivePostTo(site.PostTo)
	for _, ref := range targets {
		b.sendToModerationChatPermanent(ref.Chat, message, ref.Topic)
	}
	log.Printf("🌐 WebsiteWatch: published change report for %q to %d destination(s)", name, len(targets))
}
