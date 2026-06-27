//go:build ignore

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Copy of RSS structs from rss.go
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
	ContentEncoded string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	PubDate        string `xml:"pubDate"`
	GUID           string `xml:"guid"`
}

func (item rssItem) getContent() string {
	if item.ContentEncoded != "" {
		return item.ContentEncoded
	}
	return item.Description
}

// Copy of htmlToMarkdown from rss.go
var (
	htmlTagRegex     = regexp.MustCompile(`</?[^>]+>`)
	htmlAnchorRegex  = regexp.MustCompile(`(?i)<a\s[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	htmlImgRegex     = regexp.MustCompile(`(?i)<img\s[^>]*alt=["']([^"']*)["'][^>]*/?>`)
	htmlHeadingRegex = regexp.MustCompile(`(?i)<h[1-6][^>]*>(.*?)</h[1-6]>`)
	htmlMultiNewline = regexp.MustCompile(`\n{3,}`)
	htmlMultiSpace   = regexp.MustCompile(`[^\S\n]{2,}`)
)

func htmlToMarkdown(s string) string {
	if s == "" {
		return ""
	}
	text := s
	for _, tag := range []string{"</p>", "</div>", "</li>", "</tr>", "</blockquote>"} {
		text = strings.ReplaceAll(text, tag, "\n")
	}
	for _, tag := range []string{"<br>", "<br/>", "<br />"} {
		text = strings.ReplaceAll(text, tag, "\n")
	}
	text = htmlHeadingRegex.ReplaceAllString(text, "*$1*\n")
	for _, tag := range []string{"b", "strong"} {
		text = strings.ReplaceAll(text, "<"+tag+">", "*")
		text = strings.ReplaceAll(text, "</"+tag+">", "*")
		text = strings.ReplaceAll(text, "<"+strings.ToUpper(tag)+">", "*")
		text = strings.ReplaceAll(text, "</"+strings.ToUpper(tag)+">", "*")
	}
	for _, tag := range []string{"i", "em"} {
		text = strings.ReplaceAll(text, "<"+tag+">", "_")
		text = strings.ReplaceAll(text, "</"+tag+">", "_")
		text = strings.ReplaceAll(text, "<"+strings.ToUpper(tag)+">", "_")
		text = strings.ReplaceAll(text, "</"+strings.ToUpper(tag)+">", "_")
	}
	text = regexp.MustCompile(`(?i)</?u>`).ReplaceAllString(text, "")
	text = htmlAnchorRegex.ReplaceAllString(text, "[$2]($1)")
	text = htmlImgRegex.ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`(?i)<li[^>]*>`).ReplaceAllString(text, "Ã¢â‚¬Â¢ ")
	text = htmlTagRegex.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&mdash;", "Ã¢â‚¬â€")
	text = strings.ReplaceAll(text, "&ndash;", "Ã¢â‚¬â€œ")
	text = strings.ReplaceAll(text, "&laquo;", "Ã‚Â«")
	text = strings.ReplaceAll(text, "&raquo;", "Ã‚Â»")
	text = htmlMultiSpace.ReplaceAllString(text, " ")
	text = htmlMultiNewline.ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)
	return text
}

func main() {
	resp, err := http.Get("https://www.usoud.cz/rss")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fetch error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		fmt.Fprintf(os.Stderr, "XML parse error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Feed: %s\n", feed.Channel.Title)
	fmt.Printf("Items: %d\n\n", len(feed.Channel.Items))

	for i, item := range feed.Channel.Items {
		fmt.Printf("=== ITEM %d ===\n", i+1)
		fmt.Printf("Title: %s\n", item.Title)
		fmt.Printf("Link: %s\n", item.Link)
		fmt.Printf("PubDate: %s\n", item.PubDate)
		fmt.Printf("Description empty: %v\n", item.Description == "")
		fmt.Printf("ContentEncoded length: %d\n", len(item.ContentEncoded))

		content := htmlToMarkdown(item.getContent())
		fmt.Printf("Converted content length: %d chars\n", utf8.RuneCountInString(content))

		// Show first 500 chars of converted content
		preview := content
		if utf8.RuneCountInString(preview) > 500 {
			runes := []rune(preview)
			preview = string(runes[:500]) + "..."
		}
		fmt.Printf("Content preview:\n%s\n\n", preview)

		// Check if final message would exceed Telegram limit
		msg := fmt.Sprintf("Ã°Å¸â€œÂ° *Test Feed*\n\n%s\n\nÃ°Å¸â€â€” %s", content, item.Link)
		msgLen := utf8.RuneCountInString(msg)
		fmt.Printf("Full message length: %d chars (limit: 4096, needs summary: %v)\n\n", msgLen, msgLen > 4096)
	}
}
