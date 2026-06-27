// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gennadium/internal/config"
)

// RSS XML structures
type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Description string    `xml:"description"`
	Link        string    `xml:"link"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title          string `xml:"title"`
	Link           string `xml:"link"`
	Description    string `xml:"description"`
	ContentEncoded string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"` // <content:encoded>
	PubDate        string `xml:"pubDate"`
	GUID           string `xml:"guid"`
}

// getContent returns the best available content for an RSS item
// (prefers content:encoded over description)
func (item rssItem) getContent() string {
	if item.ContentEncoded != "" {
		return item.ContentEncoded
	}
	return item.Description
}

// rssTaskName returns the scheduled-event name for a given feed URL.
func rssTaskName(feedURL string) string {
	h := sha256.Sum256([]byte(feedURL))
	return fmt.Sprintf("rss_%s", hex.EncodeToString(h[:8]))
}

// rssDateFormats lists common RFC-822 / RFC-2822 date layouts found in RSS feeds.
var rssDateFormats = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700",
	time.RFC822Z, // "02 Jan 06 15:04 -0700"
	time.RFC822,  // "02 Jan 06 15:04 MST"
	"2 Jan 06 15:04 -0700",
	"2 Jan 06 15:04 MST",
	time.RFC3339,
}

// parseRSSDate tries several common date formats and returns the parsed time.
func parseRSSDate(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range rssDateFormats {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// processRssFeed fetches an RSS feed, finds new items by pubDate, translates and publishes them
func (b *Bot) processRssFeed(feed config.RssFeed) {
	log.Printf("📰 RssUpdates: fetching feed %q from %s", feed.Name, feed.URL)

	// Fetch and parse RSS
	items, err := b.fetchRSSFeed(feed.Name, feed.URL)
	if err != nil {
		log.Printf("📰 RssUpdates: error fetching feed %q: %v", feed.Name, err)
		return
	}

	if len(items) == 0 {
		log.Printf("📰 RssUpdates: feed %q returned 0 items", feed.Name)
		return
	}

	log.Printf("📰 RssUpdates: feed %q returned %d items", feed.Name, len(items))

	// Determine the cutoff time: only items published after the last task run.
	// Look up the scheduled_events record for this feed's task.
	taskName := rssTaskName(feed.URL)
	event, err := b.db.GetScheduledEvent(taskName)
	if err != nil {
		log.Printf("📰 RssUpdates: error reading scheduled event for %q: %v", feed.Name, err)
		return
	}

	// First run (no event record or zero timestamp from EnsureScheduledEventExists) -
	// skip publishing so we don't flood the chat with all historical items.
	if event == nil || event.LastFiredAt.IsZero() {
		log.Printf("📰 RssUpdates: first run for %q, skipping publish (items will be picked up next run)", feed.Name)
		return
	}

	cutoff := event.LastFiredAt
	log.Printf("📰 RssUpdates: publishing items from %q newer than %s", feed.Name, cutoff.Format(time.RFC3339))

	// Filter items whose pubDate is after the cutoff
	var newItems []rssItem
	for _, item := range items {
		pubTime, ok := parseRSSDate(item.PubDate)
		if !ok {
			log.Printf("📰 RssUpdates: skipping item %q - unparseable pubDate %q", item.Title, item.PubDate)
			continue
		}
		if pubTime.After(cutoff) {
			newItems = append(newItems, item)
		}
	}

	if len(newItems) == 0 {
		log.Printf("📰 RssUpdates: no new items in %q", feed.Name)
		return
	}

	log.Printf("📰 RssUpdates: %d new items in %q, translating and publishing", len(newItems), feed.Name)

	// Process new items (oldest first - reverse order since RSS usually newest first)
	for i := len(newItems) - 1; i >= 0; i-- {
		b.translateAndPublishRssItem(feed, newItems[i])
		// Small delay between messages to avoid rate limiting
		if i > 0 {
			time.Sleep(5 * time.Second)
		}
	}
}

// translateAndPublishRssItem translates a single RSS item and publishes it to moderation chats
func (b *Bot) translateAndPublishRssItem(feed config.RssFeed, item rssItem) {
	// Build the original text: title + content (prefer content:encoded over description)
	originalText := item.Title
	content := htmlToMarkdown(item.getContent())
	if content != "" {
		originalText += "\n\n" + content
	}

	// Determine max message length for this feed
	maxLen := feed.MaxMessageLength
	if maxLen <= 0 {
		maxLen = MaxTelegramMessageLength
	}

	header := fmt.Sprintf("📰 *%s*\n\n", feed.Name)
	footer := ""
	if item.Link != "" {
		footer = fmt.Sprintf("\n\n🔗 %s", item.Link)
	}

	// If AI is globally disabled, publish raw content (hard-truncated)
	if !b.config.AI.Enabled {
		bodyBudget := maxLen - utf8.RuneCountInString(header) - utf8.RuneCountInString(footer)
		body := truncateMessage(originalText, bodyBudget)
		message := header + body + footer

		targets := b.config.EffectivePostTo(feed.PostTo)
		for _, ref := range targets {
			b.sendToModerationChatPermanent(ref.Chat, message, ref.Topic)
		}

		log.Printf("📰 RssUpdates: published item %q without AI to %d destination(s)", item.Title, len(targets))
		return
	}

	// Calculate space available for the body
	bodyBudgetFirst := maxLen - utf8.RuneCountInString(header) - utf8.RuneCountInString(footer)
	bodyBudgetSecond := MaxTelegramMessageLength - utf8.RuneCountInString(header) - utf8.RuneCountInString(footer)

	var body string
	if feed.IsTranslate() {
		log.Printf("📰 RssUpdates: translating item %q (len=%d)", item.Title, utf8.RuneCountInString(originalText))

		// Determine prompts for translation
		systemPrompt, userPrompt := b.getRssTranslationPrompts(originalText)
		if systemPrompt == "" || userPrompt == "" {
			return
		}

		// Select model based on use_full_model config and threshold
		origLen := utf8.RuneCountInString(originalText)
		threshold := b.config.AI.Rss.LightModelThreshold
		var modelConfigs config.AIModelConfigs
		if b.config.AI.Rss.UseFullModel && (threshold <= 0 || origLen <= threshold) {
			modelConfigs = b.config.AI.FullModel
		} else {
			modelConfigs = b.config.AI.LightModel
			if threshold > 0 && origLen > threshold {
				log.Printf("📰 RssUpdates: item is very long (%d chars > threshold %d), using light model", origLen, threshold)
			}
		}

		// Translate using selected model
		translated, err := b.callAzureOpenAIWithRetriesAndBackoff(
			"rss_translation", userPrompt, systemPrompt, modelConfigs, 2000, 4, scheduledTaskBackoff,
		)
		if err != nil {
			log.Printf("📰 RssUpdates: translation failed for %q: %v", item.Title, err)
			return
		}
		body = translated
	} else {
		log.Printf("📰 RssUpdates: translation disabled for %q, using original text (len=%d)", item.Title, utf8.RuneCountInString(originalText))
		body = originalText
	}

	// If body exceeds the budget, either summarize via AI or hard-truncate based on per-feed setting.
	if utf8.RuneCountInString(body) > bodyBudgetFirst {
		if feed.IsSummarizeIfLong() {
			log.Printf("📰 RssUpdates: message too long (%d chars, body budget %d), summarizing", utf8.RuneCountInString(body), bodyBudgetFirst)
			body = b.summarizeRssBody(body, bodyBudgetSecond)
		} else {
			log.Printf("📰 RssUpdates: message too long (%d chars, body budget %d), truncating", utf8.RuneCountInString(body), bodyBudgetFirst)
			body = truncateMessage(body, bodyBudgetFirst)
		}
	}

	message := header + body + footer

	// Publish to configured destinations - explicit feed.post_to, or default
	// (every moderation chat, main area).
	targets := b.config.EffectivePostTo(feed.PostTo)
	for _, ref := range targets {
		b.sendToModerationChatPermanent(ref.Chat, message, ref.Topic)
	}

	log.Printf("📰 RssUpdates: published item %q to %d destination(s)", item.Title, len(targets))
}

// summarizeRssBody creates a shorter AI summary of the body text, truncating as fallback.
func (b *Bot) summarizeRssBody(translatedText string, maxRunes int) string {
	systemPrompt, userPrompt := b.getRssSummaryPrompts(translatedText)
	if systemPrompt == "" || userPrompt == "" {
		return truncateMessage(translatedText, maxRunes)
	}

	textLen := utf8.RuneCountInString(translatedText)
	threshold := b.config.AI.Rss.LightModelThreshold
	var modelConfigs config.AIModelConfigs
	if b.config.AI.Rss.UseFullModel && (threshold <= 0 || textLen <= threshold) {
		modelConfigs = b.config.AI.FullModel
	} else {
		modelConfigs = b.config.AI.LightModel
	}

	summary, err := b.callAzureOpenAIWithRetriesAndBackoff(
		"rss_summary", userPrompt, systemPrompt, modelConfigs, 1000, 4, scheduledTaskBackoff,
	)
	if err != nil {
		log.Printf("📰 RssUpdates: summary failed, using truncated text: %v", err)
		summary = translatedText
	}

	// If still too long, hard-truncate the body only
	return truncateMessage(summary, maxRunes)
}

// getRssTranslationPrompts returns the system and user prompts for RSS item translation
func (b *Bot) getRssTranslationPrompts(originalText string) (string, string) {
	prompts := b.config.AI.Rss.TranslationPrompt
	if prompts.System == "" || prompts.User == "" {
		log.Printf("📰 RssUpdates: rss_translation_prompt not configured, skipping translation")
		return "", ""
	}

	replacements := map[string]string{"text": originalText}
	systemPrompt := applyReplacements(prompts.System, replacements)
	userPrompt := applyReplacements(prompts.User, replacements)
	return systemPrompt, userPrompt
}

// getRssSummaryPrompts returns the system and user prompts for summarizing a long RSS item
func (b *Bot) getRssSummaryPrompts(translatedText string) (string, string) {
	prompts := b.config.AI.Rss.SummaryPrompt
	if prompts.System == "" || prompts.User == "" {
		log.Printf("📰 RssUpdates: rss_summary_prompt not configured, skipping summary")
		return "", ""
	}

	replacements := map[string]string{"text": translatedText}
	systemPrompt := applyReplacements(prompts.System, replacements)
	userPrompt := applyReplacements(prompts.User, replacements)
	return systemPrompt, userPrompt
}

// --- RSS fetching helpers ---

// fetchRSSFeed downloads and parses an RSS feed. The feedName is used purely
// as the diagnostics/log label so each configured feed appears as its own
// row in the Diagnostics page.
func (b *Bot) fetchRSSFeed(feedName, feedURL string) ([]rssItem, error) {
	service := "rss:" + feedName
	res, err := b.doAPIWithRetries(service, &http.Client{Timeout: 30 * time.Second}, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("GET", feedURL, nil)
		if rerr != nil {
			return nil, nil, fmt.Errorf("HTTP request build failed: %w", rerr)
		}
		return req, nil, nil
	})
	if err != nil {
		return nil, fmt.Errorf("HTTP GET failed: %w", err)
	}
	if !res.IsOK() {
		b.logAPIError(service, res.StatusCode, res.Body, nil)
		return nil, fmt.Errorf("HTTP status %d", res.StatusCode)
	}

	var feed rssFeed
	if err := xml.Unmarshal(res.Body, &feed); err != nil {
		return nil, fmt.Errorf("XML parse error: %w", err)
	}

	return feed.Channel.Items, nil
}

// Precompiled regexes for HTML-to-Markdown conversion
var (
	htmlTagRegex     = regexp.MustCompile(`</?[^>]+>`)
	htmlAnchorRegex  = regexp.MustCompile(`(?i)<a\s[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	htmlImgRegex     = regexp.MustCompile(`(?i)<img\s[^>]*alt=["']([^"']*)["'][^>]*/?>`)
	htmlHeadingRegex = regexp.MustCompile(`(?i)<h[1-6][^>]*>(.*?)</h[1-6]>`)
	htmlMultiNewline = regexp.MustCompile(`\n{3,}`)
	htmlMultiSpace   = regexp.MustCompile(`[^\S\n]{2,}`)
)

// htmlToMarkdown converts common HTML tags to Markdown for Telegram
func htmlToMarkdown(s string) string {
	if s == "" {
		return ""
	}

	text := s

	// Block-level elements → newlines (before stripping tags)
	for _, tag := range []string{"</p>", "</div>", "</li>", "</tr>", "</blockquote>"} {
		text = strings.ReplaceAll(text, tag, "\n")
	}
	for _, tag := range []string{"<br>", "<br/>", "<br />"} {
		text = strings.ReplaceAll(text, tag, "\n")
	}

	// Headings → bold
	text = htmlHeadingRegex.ReplaceAllString(text, "*$1*\n")

	// Bold / strong
	for _, tag := range []string{"b", "strong"} {
		text = strings.ReplaceAll(text, "<"+tag+">", "*")
		text = strings.ReplaceAll(text, "</"+tag+">", "*")
		text = strings.ReplaceAll(text, "<"+strings.ToUpper(tag)+">", "*")
		text = strings.ReplaceAll(text, "</"+strings.ToUpper(tag)+">", "*")
	}

	// Italic / em
	for _, tag := range []string{"i", "em"} {
		text = strings.ReplaceAll(text, "<"+tag+">", "_")
		text = strings.ReplaceAll(text, "</"+tag+">", "_")
		text = strings.ReplaceAll(text, "<"+strings.ToUpper(tag)+">", "_")
		text = strings.ReplaceAll(text, "</"+strings.ToUpper(tag)+">", "_")
	}

	// Underline - Telegram Markdown doesn't support it, just strip the tags
	text = regexp.MustCompile(`(?i)</?u>`).ReplaceAllString(text, "")

	// Links: <a href="url">text</a> → [text](url)
	text = htmlAnchorRegex.ReplaceAllString(text, "[$2]($1)")

	// Images - strip entirely
	text = htmlImgRegex.ReplaceAllString(text, "")

	// List items: <li> → bullet
	text = regexp.MustCompile(`(?i)<li[^>]*>`).ReplaceAllString(text, "• ")

	// Strip all remaining HTML tags
	text = htmlTagRegex.ReplaceAllString(text, "")

	// Decode common HTML entities
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&mdash;", "-")
	text = strings.ReplaceAll(text, "&ndash;", "–")
	text = strings.ReplaceAll(text, "&laquo;", "«")
	text = strings.ReplaceAll(text, "&raquo;", "»")

	// Collapse whitespace
	text = htmlMultiSpace.ReplaceAllString(text, " ")
	text = htmlMultiNewline.ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)

	return text
}
