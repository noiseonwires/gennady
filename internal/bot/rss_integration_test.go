// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- pure helpers -------------------------------------------------------------

func TestRSSTaskName_Deterministic(t *testing.T) {
	a := rssTaskName("https://example.com/rss")
	b := rssTaskName("https://example.com/rss")
	c := rssTaskName("https://other.com/rss")
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
	assert.True(t, strings.HasPrefix(a, "rss_"))
}

func TestParseRSSDate(t *testing.T) {
	_, ok := parseRSSDate("")
	assert.False(t, ok)

	tm, ok := parseRSSDate("Mon, 02 Jan 2006 15:04:05 -0700")
	require.True(t, ok)
	assert.Equal(t, 2006, tm.Year())

	tm, ok = parseRSSDate("2 Jan 2006 15:04:05 -0700")
	require.True(t, ok)
	assert.Equal(t, 2006, tm.Year())

	_, ok = parseRSSDate("not a date")
	assert.False(t, ok)
}

func TestParseRSSDate_ZakonyCutoff(t *testing.T) {
	cutoff := time.Date(2026, time.November, 4, 9, 35, 10, 0, time.UTC)
	for _, raw := range []string{
		"Thu, 05 Nov 2026 03:04:58 +0000",
		"Thu, 05 Nov 2026 02:50:41 +0000",
		"Wed, 04 Nov 2026 11:11:22 +0000",
		"Wed, 04 Nov 2026 11:10:11 +0000",
	} {
		t.Run(raw, func(t *testing.T) {
			published, ok := parseRSSDate(raw)
			require.True(t, ok)
			assert.True(t, published.After(cutoff))
		})
	}
}

func TestFilterRSSItems_ZakonyCutoff(t *testing.T) {
	cutoff := time.Date(2026, time.November, 4, 9, 35, 10, 0, time.UTC)
	dates := []string{
		"Thu, 05 Nov 2026 03:04:58 +0000",
		"Thu, 05 Nov 2026 03:03:45 +0000",
		"Thu, 05 Nov 2026 03:02:33 +0000",
		"Thu, 05 Nov 2026 03:01:20 +0000",
		"Thu, 05 Nov 2026 03:00:08 +0000",
		"Thu, 05 Nov 2026 02:58:57 +0000",
		"Thu, 05 Nov 2026 02:57:47 +0000",
		"Thu, 05 Nov 2026 02:56:37 +0000",
		"Thu, 05 Nov 2026 02:55:26 +0000",
		"Thu, 05 Nov 2026 02:54:16 +0000",
		"Thu, 05 Nov 2026 02:53:05 +0000",
		"Thu, 05 Nov 2026 02:51:54 +0000",
		"Thu, 05 Nov 2026 02:50:41 +0000",
		"Wed, 04 Nov 2026 11:11:22 +0000",
		"Wed, 04 Nov 2026 11:10:11 +0000",
		"Wed, 04 Nov 2026 03:38:07 +0000",
		"Wed, 04 Nov 2026 03:36:55 +0000",
		"Wed, 04 Nov 2026 03:35:45 +0000",
		"Wed, 04 Nov 2026 03:34:35 +0000",
		"Wed, 04 Nov 2026 03:33:24 +0000",
	}
	items := make([]rssItem, 0, len(dates))
	for _, raw := range dates {
		items = append(items, rssItem{Title: raw, PubDate: raw})
	}
	selected := filterRSSItems("Zakony", items, cutoff)
	require.Len(t, selected, 15)
	assert.Equal(t, items[:15], selected)
}

func TestFilterRSSItems_CutoffAndInvalidDates(t *testing.T) {
	cutoff := time.Date(2026, time.November, 4, 9, 35, 10, 0, time.UTC)
	items := []rssItem{
		{Title: "Same instant", PubDate: "2026-11-04T10:35:10+01:00"},
		{Title: "Invalid", PubDate: "not a date"},
		{Title: "Missing date"},
		{Title: "New", PubDate: "2026-11-04T09:35:11Z"},
		{Title: "Old", PubDate: "2026-11-04T09:35:09Z"},
	}
	assert.Equal(t, items[3:4], filterRSSItems("Feed", items, cutoff))
	assert.Empty(t, filterRSSItems("Feed", nil, cutoff))
}

func TestRSSItemGetContent(t *testing.T) {
	// content:encoded preferred over description.
	item := rssItem{Description: "desc", ContentEncoded: "encoded"}
	assert.Equal(t, "encoded", item.getContent())

	item2 := rssItem{Description: "desc"}
	assert.Equal(t, "desc", item2.getContent())
}

func TestHTMLToMarkdown(t *testing.T) {
	assert.Equal(t, "", htmlToMarkdown(""))

	out := htmlToMarkdown("<p>Hello <b>world</b></p><p>Second</p>")
	assert.Contains(t, out, "Hello *world*")
	assert.Contains(t, out, "Second")

	// Anchor → markdown link.
	link := htmlToMarkdown(`<a href="https://x.com">click</a>`)
	assert.Equal(t, "[click](https://x.com)", link)

	// Headings → bold; entities decoded.
	h := htmlToMarkdown("<h2>Title</h2>A &amp; B")
	assert.Contains(t, h, "*Title*")
	assert.Contains(t, h, "A & B")

	// List items → bullets.
	li := htmlToMarkdown("<ul><li>one</li><li>two</li></ul>")
	assert.Contains(t, li, "• one")
	assert.Contains(t, li, "• two")
}

func TestGetRSSPrompts_Unconfigured(t *testing.T) {
	b, _ := newMockBot(t)
	s, u := b.getRssTranslationPrompts("text")
	assert.Empty(t, s)
	assert.Empty(t, u)

	s, u = b.getRssSummaryPrompts("text")
	assert.Empty(t, s)
	assert.Empty(t, u)
}

func TestGetRSSPrompts_Configured(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.Rss.TranslationPrompt = config.PromptPair{System: "sys", User: "translate {{text}}"}
	s, u := b.getRssTranslationPrompts("hello")
	assert.Equal(t, "sys", s)
	assert.Equal(t, "translate hello", u)

	b.config.AI.Rss.SummaryPrompt = config.PromptPair{System: "ssys", User: "sum {{text}}"}
	s, u = b.getRssSummaryPrompts("body")
	assert.Equal(t, "ssys", s)
	assert.Equal(t, "sum body", u)
}

// --- fetch + publish ----------------------------------------------------------

const sampleRSS = `<?xml version="1.0"?>
<rss version="2.0"><channel>
<title>Feed</title>
<item><title>Item One</title><link>https://x.com/1</link>
<description>Body text one</description>
<pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate></item>
</channel></rss>`

func TestFetchRSSFeed(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleRSS))
	})

	items, err := b.fetchRSSFeed("Feed", "https://feeds.example.com/rss")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "Item One", items[0].Title)
	assert.Equal(t, "Body text one", items[0].Description)
	assert.Contains(t, rt.last().Host, "feeds.example.com")
}

func TestFetchRSSFeed_ErrorStatus(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		// 4xx is non-retryable, keeping the test fast.
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := b.fetchRSSFeed("Feed", "https://feeds.example.com/rss")
	require.Error(t, err)
}

func TestFetchRSSFeed_BadXML(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><not closed`))
	})
	_, err := b.fetchRSSFeed("Feed", "https://feeds.example.com/rss")
	require.Error(t, err)
}

func TestTranslateAndPublishRssItem_NoAI(t *testing.T) {
	// With AI disabled, the raw item is published to all configured destinations.
	b, tg := newMockBot(t)
	b.config.AI.Enabled = false
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	feed := config.RssFeed{Name: "Court News", MaxMessageLength: 512}
	item := rssItem{Title: "Ruling", Description: "<p>The court decided.</p>", Link: "https://court.example/1"}

	b.translateAndPublishRssItem(feed, item)

	require.NotEmpty(t, tg.SentMessages)
	sent := tg.SentMessages[0]
	assert.Equal(t, int64(-100), sent.ChatID)
	assert.Contains(t, sent.Text, "Court News")
	assert.Contains(t, sent.Text, "Ruling")
	assert.Contains(t, sent.Text, "court.example/1")
}

func TestProcessRssFeed_EmptyFeed(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss version="2.0"><channel><title>F</title></channel></rss>`))
	})
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	b.processRssFeed(config.RssFeed{Name: "F", URL: "https://feeds.example.com/rss"})
	// No items -> nothing published.
	assert.Empty(t, tg.SentMessages)
}
