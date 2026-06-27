// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// ExtractorAPIResponse represents the response from ExtractorAPI.
type ExtractorAPIResponse struct {
	URL           string   `json:"url"`
	Status        string   `json:"status"`
	Domain        string   `json:"domain"`
	DatePublished string   `json:"date_published"`
	Images        []string `json:"images"`
	Videos        []string `json:"videos"`
	Title         string   `json:"title"`
	Author        []string `json:"author"`
	Text          string   `json:"text"`
	HTML          string   `json:"html"`
}

// DiffbotAPIResponse represents the response from Diffbot Analyze API.
type DiffbotAPIResponse struct {
	HumanLanguage string `json:"humanLanguage"`
	Title         string `json:"title"`
	Objects       []struct {
		Title       string `json:"title"`
		Text        string `json:"text"`
		Description string `json:"description"`
	} `json:"objects"`
}

var blockedLinkIPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// fetchTelegramPostContent fetches the content of a public Telegram post.
func (b *Bot) fetchTelegramPostContent(channelUsername, messageID string) (string, error) {
	fetchURL := fmt.Sprintf("https://t.me/%s/%s?embed=1&mode=tme", channelUsername, messageID)

	res, err := b.doAPIWithRetries("telegram_post", &http.Client{Timeout: 10 * time.Second}, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("GET", fetchURL, nil)
		if rerr != nil {
			return nil, nil, fmt.Errorf("failed to build request: %w", rerr)
		}
		return req, nil, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to fetch post: %w", err)
	}
	if !res.IsOK() {
		return "", fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	htmlContent := string(res.Body)

	textRegex := regexp.MustCompile(`<div class="tgme_widget_message_text[^"]*"[^>]*>(.*?)</div>`)
	textMatches := textRegex.FindAllStringSubmatch(htmlContent, -1)

	var messageText string
	if len(textMatches) > 0 {
		var textParts []string
		for _, match := range textMatches {
			if len(match) >= 2 && strings.TrimSpace(match[1]) != "" {
				textParts = append(textParts, match[1])
			}
		}
		if len(textParts) > 0 {
			messageText = strings.Join(textParts, " ")
			log.Printf("Extracted text from %d tgme_widget_message_text div(s)", len(textParts))
		}
	}

	if messageText == "" {
		log.Printf("No tgme_widget_message_text divs found, falling back to body extraction")

		bodyRegex := regexp.MustCompile(`(?is)<body[^>]*>(.*?)</body>`)
		bodyMatch := bodyRegex.FindStringSubmatch(htmlContent)

		if len(bodyMatch) >= 2 {
			messageText = bodyMatch[1]
			log.Printf("Extracted content from <body> tag")
		} else {
			messageText = htmlContent
			log.Printf("No <body> tag found, processing entire HTML")
		}
	}

	scriptRegex := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	messageText = scriptRegex.ReplaceAllString(messageText, "")

	styleRegex := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	messageText = styleRegex.ReplaceAllString(messageText, "")

	messageText = regexp.MustCompile(`(?i)<br\s*/?>>`).ReplaceAllString(messageText, "\n")

	messageText = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(messageText, "")
	messageText = strings.TrimSpace(messageText)

	messageText = strings.ReplaceAll(messageText, "&lt;", "<")
	messageText = strings.ReplaceAll(messageText, "&gt;", ">")
	messageText = strings.ReplaceAll(messageText, "&amp;", "&")
	messageText = strings.ReplaceAll(messageText, "&quot;", `"`)
	messageText = strings.ReplaceAll(messageText, "&#39;", "'")
	messageText = strings.ReplaceAll(messageText, "&nbsp;", " ")

	return messageText, nil
}

// fetchAndExtractLinkContent fetches a URL and extracts its main content.
func (b *Bot) fetchAndExtractLinkContent(targetURL string) (content string, title string, language string, err error) {
	targetURL, err = b.validateLinkTargetURL(targetURL)
	if err != nil {
		return "", "", "", err
	}

	type extractResult struct {
		content  string
		title    string
		language string
	}
	var best extractResult

	tryAccept := func(c, t, l, method string) bool {
		if len(c) >= 1000 {
			return true
		}
		if len(c) > len(best.content) {
			best = extractResult{c, t, l}
			log.Printf("%s returned short content (%d chars) for %s, trying next method", method, len(c), targetURL)
		}
		return false
	}

	if b.config.AI.LinkSummaries.DiffbotAPIKey != "" {
		c, t, l, e := b.fetchWithDiffbot(targetURL)
		if e != nil {
			log.Printf("Diffbot failed for %s: %v", targetURL, e)
		} else if tryAccept(c, t, l, "Diffbot") {
			return c, t, l, nil
		}
	}

	if b.config.AI.LinkSummaries.ExtractorAPIKey != "" {
		c, t, e := b.fetchWithExtractorAPI(targetURL)
		if e != nil {
			log.Printf("ExtractorAPI failed for %s: %v", targetURL, e)
		} else if tryAccept(c, t, "", "ExtractorAPI") {
			return c, t, "", nil
		}
	}

	if b.config.AI.LinkSummaries.CloudflareAccountID != "" && b.config.AI.LinkSummaries.CloudflareAPIToken != "" {
		cfContent, cfTitle, cfErr := b.fetchWithCloudflare(targetURL)
		if cfErr == nil && len(cfContent) > 0 {
			log.Printf("Cloudflare Browser Rendering succeeded for %s (%d chars)", targetURL, len(cfContent))
			return cfContent, cfTitle, "", nil
		}
		if cfErr != nil {
			log.Printf("Cloudflare Browser Rendering failed for %s: %v", targetURL, cfErr)
		}
	}

	log.Printf("Trying manual HTML extraction for %s", targetURL)
	manualContent, manualTitle, manualErr := b.fetchWithManualExtraction(targetURL)
	if manualErr == nil && len(manualContent) > 0 {
		log.Printf("Manual extraction succeeded for %s (%d chars)", targetURL, len(manualContent))
		return manualContent, manualTitle, "", nil
	}
	if manualErr != nil {
		log.Printf("Manual extraction also failed for %s: %v", targetURL, manualErr)
	}

	if best.content != "" {
		log.Printf("All methods returned short content, using best result (%d chars) for %s", len(best.content), targetURL)
		return best.content, best.title, best.language, nil
	}

	return "", "", "", fmt.Errorf("all extraction methods failed for %s", targetURL)
}

// fetchWithExtractorAPI fetches content using ExtractorAPI.
func (b *Bot) fetchWithExtractorAPI(targetURL string) (content string, title string, err error) {
	targetURL, err = b.validateLinkTargetURL(targetURL)
	if err != nil {
		return "", "", err
	}
	client := b.createHTTPClient()

	apiURL := fmt.Sprintf("https://extractorapi.com/api/v1/extractor/?apikey=%s&url=%s",
		b.config.AI.LinkSummaries.ExtractorAPIKey, url.QueryEscape(targetURL))

	log.Printf("Fetching content via ExtractorAPI for: %s", targetURL)

	res, err := b.doAPIWithRetries("extractor_api", client, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("GET", apiURL, nil)
		if rerr != nil {
			return nil, nil, fmt.Errorf("failed to create ExtractorAPI request: %w", rerr)
		}
		if b.config.AI.LinkSummaries.Cookies != "" {
			req.Header.Set("Cookie", b.config.AI.LinkSummaries.Cookies)
		}
		return req, nil, nil
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to call ExtractorAPI: %w", err)
	}
	extractorBody := res.Body
	if !res.IsOK() {
		b.logAPIError("extractor_api", res.StatusCode, extractorBody, nil)
		return "", "", fmt.Errorf("ExtractorAPI returned status code: %d", res.StatusCode)
	}

	var extractorResp ExtractorAPIResponse
	if err := json.Unmarshal(extractorBody, &extractorResp); err != nil {
		return "", "", fmt.Errorf("failed to parse ExtractorAPI response: %w", err)
	}

	if extractorResp.Status != "COMPLETE" {
		return "", "", fmt.Errorf("ExtractorAPI status: %s", extractorResp.Status)
	}

	title = extractorResp.Title
	content = extractorResp.Text

	if content == "" {
		return "", title, fmt.Errorf("no content extracted by ExtractorAPI")
	}

	log.Printf("ExtractorAPI successfully extracted %d chars from: %s", len(content), targetURL)

	return content, title, nil
}

// fetchWithDiffbot fetches content using Diffbot Analyze API.
func (b *Bot) fetchWithDiffbot(targetURL string) (content string, title string, language string, err error) {
	targetURL, err = b.validateLinkTargetURL(targetURL)
	if err != nil {
		return "", "", "", err
	}
	client := b.createHTTPClient()

	apiURL := fmt.Sprintf("https://api.diffbot.com/v3/analyze?token=%s&url=%s",
		b.config.AI.LinkSummaries.DiffbotAPIKey, url.QueryEscape(targetURL))
	if b.config.AI.LinkSummaries.Cookies != "" {
		apiURL += "&cookies=" + url.QueryEscape(b.config.AI.LinkSummaries.Cookies)
	}

	log.Printf("Fetching content via Diffbot for: %s", targetURL)

	res, err := b.doAPIWithRetries("diffbot", client, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("GET", apiURL, nil)
		if rerr != nil {
			return nil, nil, fmt.Errorf("failed to create Diffbot request: %w", rerr)
		}
		return req, nil, nil
	})
	if err != nil {
		return "", "", "", fmt.Errorf("failed to call Diffbot: %w", err)
	}
	bodyBytes := res.Body
	if !res.IsOK() {
		log.Printf("Diffbot error response (status %d): %s", res.StatusCode, string(bodyBytes))
		return "", "", "", fmt.Errorf("Diffbot returned status code: %d", res.StatusCode)
	}

	var diffbotResp DiffbotAPIResponse
	if err := json.Unmarshal(bodyBytes, &diffbotResp); err != nil {
		log.Printf("Diffbot invalid JSON response: %s", string(bodyBytes))
		return "", "", "", fmt.Errorf("failed to parse Diffbot response: %w", err)
	}

	if len(diffbotResp.Objects) == 0 {
		log.Printf("Diffbot returned no objects, full response: %s", string(bodyBytes))
		return "", "", "", fmt.Errorf("Diffbot returned no objects")
	}

	language = diffbotResp.HumanLanguage

	var titles []string
	var texts []string
	for _, obj := range diffbotResp.Objects {
		if obj.Title != "" {
			titles = append(titles, obj.Title)
		}
		text := obj.Text
		if text == "" {
			text = obj.Description
		}
		if text != "" {
			texts = append(texts, text)
		}
	}

	content = strings.Join(texts, "\n\n")
	title = strings.Join(titles, " | ")
	if title == "" {
		title = diffbotResp.Title
	}

	if content == "" {
		log.Printf("Diffbot returned empty content, full response: %s", string(bodyBytes))
		return "", title, language, fmt.Errorf("no content extracted by Diffbot")
	}

	log.Printf("Diffbot successfully extracted %d chars from %d objects (lang: %s) from: %s", len(content), len(texts), language, targetURL)

	return content, title, language, nil
}

// CloudflareMarkdownResponse represents the response from Cloudflare Browser Rendering /markdown endpoint.
type CloudflareMarkdownResponse struct {
	Success bool   `json:"success"`
	Result  string `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// fetchWithCloudflare fetches content using Cloudflare Browser Rendering /markdown endpoint.
func (b *Bot) fetchWithCloudflare(targetURL string) (content string, title string, err error) {
	targetURL, err = b.validateLinkTargetURL(targetURL)
	if err != nil {
		return "", "", err
	}
	client := b.createHTTPClient()

	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/browser-rendering/markdown",
		b.config.AI.LinkSummaries.CloudflareAccountID)

	bodyMap := map[string]interface{}{
		"url":                 targetURL,
		"rejectResourceTypes": []string{"image", "media", "font"},
		"userAgent":           b.getLinkUserAgent(),
		"gotoOptions": map[string]string{
			"waitUntil": "networkidle0",
		},
	}

	// Pass cookies to Cloudflare Browser Rendering so consent walls are bypassed
	if b.config.AI.LinkSummaries.Cookies != "" {
		var cfCookies []map[string]string
		for _, c := range strings.Split(b.config.AI.LinkSummaries.Cookies, ";") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			parts := strings.SplitN(c, "=", 2)
			if len(parts) == 2 {
				cfCookies = append(cfCookies, map[string]string{
					"name":  strings.TrimSpace(parts[0]),
					"value": strings.TrimSpace(parts[1]),
					"url":   targetURL,
				})
			}
		}
		if len(cfCookies) > 0 {
			bodyMap["cookies"] = cfCookies
		}
	}

	reqBody, err := json.Marshal(bodyMap)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal Cloudflare request: %w", err)
	}

	log.Printf("Fetching content via Cloudflare Browser Rendering for: %s", targetURL)

	res, err := b.doAPIWithRetries("cloudflare_browser", client, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("POST", apiURL, bytes.NewReader(reqBody))
		if rerr != nil {
			return nil, nil, fmt.Errorf("failed to create Cloudflare request: %w", rerr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+b.config.AI.LinkSummaries.CloudflareAPIToken)
		return req, reqBody, nil
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to call Cloudflare Browser Rendering: %w", err)
	}
	bodyBytes := res.Body
	if !res.IsOK() {
		b.logAPIError("cloudflare_browser", res.StatusCode, bodyBytes, nil)
		return "", "", fmt.Errorf("Cloudflare Browser Rendering returned status code: %d", res.StatusCode)
	}

	var cfResp CloudflareMarkdownResponse
	if err := json.Unmarshal(bodyBytes, &cfResp); err != nil {
		return "", "", fmt.Errorf("failed to parse Cloudflare response: %w", err)
	}

	if len(cfResp.Errors) > 0 {
		var errMsgs []string
		for _, e := range cfResp.Errors {
			errMsgs = append(errMsgs, fmt.Sprintf("code %d: %s", e.Code, e.Message))
		}
		return "", "", fmt.Errorf("Cloudflare Browser Rendering errors: %s", strings.Join(errMsgs, "; "))
	}

	if !cfResp.Success || cfResp.Result == "" {
		log.Printf("Cloudflare /markdown returned empty result for %s, trying /content for JSON-LD", targetURL)
		return b.fetchWithCloudflareContent(targetURL)
	}

	content = cfResp.Result

	// Detect consent/cookie wall in rendered content
	if b.isConsentPage(content) {
		log.Printf("Cloudflare /markdown returned consent page for %s, trying /content for JSON-LD", targetURL)
		return b.fetchWithCloudflareContent(targetURL)
	}

	// Extract title from first markdown heading
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) > 0 && strings.HasPrefix(lines[0], "# ") {
		title = strings.TrimPrefix(lines[0], "# ")
		title = strings.TrimSpace(title)
	}

	// If markdown content is too short, try /content endpoint for JSON-LD extraction
	if len(content) < 200 {
		log.Printf("Cloudflare /markdown returned short content (%d chars) for %s, trying /content for JSON-LD", len(content), targetURL)
		c, t, e := b.fetchWithCloudflareContent(targetURL)
		if e == nil && len(c) > len(content) {
			return c, t, nil
		}
	}

	log.Printf("Cloudflare Browser Rendering successfully extracted %d chars from: %s", len(content), targetURL)

	return content, title, nil
}

// fetchWithCloudflareContent fetches rendered HTML via Cloudflare /content endpoint and extracts JSON-LD + meta tags.
func (b *Bot) fetchWithCloudflareContent(targetURL string) (content string, title string, err error) {
	targetURL, err = b.validateLinkTargetURL(targetURL)
	if err != nil {
		return "", "", err
	}
	client := b.createHTTPClient()

	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/browser-rendering/content",
		b.config.AI.LinkSummaries.CloudflareAccountID)

	bodyMap := map[string]interface{}{
		"url":                 targetURL,
		"rejectResourceTypes": []string{"image", "media", "font"},
		"userAgent":           b.getLinkUserAgent(),
		"gotoOptions": map[string]string{
			"waitUntil": "networkidle0",
		},
	}

	if b.config.AI.LinkSummaries.Cookies != "" {
		var cfCookies []map[string]string
		for _, c := range strings.Split(b.config.AI.LinkSummaries.Cookies, ";") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			parts := strings.SplitN(c, "=", 2)
			if len(parts) == 2 {
				cfCookies = append(cfCookies, map[string]string{
					"name":  strings.TrimSpace(parts[0]),
					"value": strings.TrimSpace(parts[1]),
					"url":   targetURL,
				})
			}
		}
		if len(cfCookies) > 0 {
			bodyMap["cookies"] = cfCookies
		}
	}

	reqBody, err := json.Marshal(bodyMap)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal Cloudflare content request: %w", err)
	}

	log.Printf("Fetching rendered HTML via Cloudflare /content for: %s", targetURL)

	res, err := b.doAPIWithRetries("cloudflare_content", client, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("POST", apiURL, bytes.NewReader(reqBody))
		if rerr != nil {
			return nil, nil, fmt.Errorf("failed to create Cloudflare content request: %w", rerr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+b.config.AI.LinkSummaries.CloudflareAPIToken)
		return req, reqBody, nil
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to call Cloudflare /content: %w", err)
	}
	bodyBytes := res.Body
	if !res.IsOK() {
		b.logAPIError("cloudflare_content", res.StatusCode, bodyBytes, nil)
		return "", "", fmt.Errorf("Cloudflare /content returned status code: %d", res.StatusCode)
	}

	htmlContent := string(bodyBytes)

	// Detect consent/cookie wall in rendered content
	if b.isConsentPage(htmlContent) {
		log.Printf("Cloudflare /content returned consent page for %s", targetURL)
		return "", "", fmt.Errorf("Cloudflare /content: consent/cookie wall detected")
	}

	// Extract title
	titleRegex := regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)
	if match := titleRegex.FindStringSubmatch(htmlContent); len(match) >= 2 {
		title = b.cleanHTMLText(match[1])
	}

	// Try JSON-LD first (best for SPAs/real estate/product pages)
	if c := b.extractStructuredContent(htmlContent); c != "" {
		log.Printf("Cloudflare /content: extracted structured content (%d chars) from: %s", len(c), targetURL)
		return c, title, nil
	}

	return "", title, fmt.Errorf("Cloudflare /content: no JSON-LD or meta content found")
}

// fetchWithManualExtraction fetches a URL and extracts content by parsing HTML directly.
func (b *Bot) fetchWithManualExtraction(targetURL string) (content string, title string, err error) {
	targetURL, err = b.validateLinkTargetURL(targetURL)
	if err != nil {
		return "", "", err
	}
	client := b.createHTTPClient()

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", b.getLinkUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,cs;q=0.8,ru;q=0.7")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	if b.config.AI.LinkSummaries.Cookies != "" {
		req.Header.Set("Cookie", b.config.AI.LinkSummaries.Cookies)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	// Detect consent wall / login redirects: if final URL host differs from original, we landed on a different site
	origHost := ""
	if parsedOrig, e := url.Parse(targetURL); e == nil {
		origHost = parsedOrig.Host
	}
	finalHost := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalHost = resp.Request.URL.Host
	}
	if origHost != "" && finalHost != "" && origHost != finalHost {
		log.Printf("Manual extraction: redirected from %s to %s (consent/login wall?)", origHost, finalHost)
		return "", "", fmt.Errorf("redirected to different domain %s (consent/login wall)", finalHost)
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	maxSize := int64(1 * 1024 * 1024) // default 1 MB
	if b.config.AI.LinkSummaries.MaxDownloadSizeBytes > 0 {
		maxSize = int64(b.config.AI.LinkSummaries.MaxDownloadSizeBytes)
	}
	if resp.ContentLength > 0 && resp.ContentLength > maxSize {
		return "", "", fmt.Errorf("response too large: %d bytes (limit %d)", resp.ContentLength, maxSize)
	}
	limitedReader := io.LimitReader(resp.Body, maxSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", "", fmt.Errorf("failed to read response: %w", err)
	}
	if int64(len(body)) > maxSize {
		return "", "", fmt.Errorf("response too large: exceeded %d bytes limit", maxSize)
	}

	htmlContent := string(body)

	titleRegex := regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)
	titleMatch := titleRegex.FindStringSubmatch(htmlContent)
	if len(titleMatch) >= 2 {
		title = b.cleanHTMLText(titleMatch[1])
	}

	var extractedContent string

	extractFromDiv := func(startPattern string, logMsg string) string {
		openRegex := regexp.MustCompile(`(?is)` + startPattern)
		openMatch := openRegex.FindStringIndex(htmlContent)
		if openMatch == nil {
			return ""
		}

		openEnd := strings.Index(htmlContent[openMatch[0]:], ">")
		if openEnd == -1 {
			return ""
		}
		contentStart := openMatch[0] + openEnd + 1

		depth := 1
		pos := contentStart
		for pos < len(htmlContent) && depth > 0 {
			nextOpen := strings.Index(htmlContent[pos:], "<div")
			nextClose := strings.Index(htmlContent[pos:], "</div>")

			if nextClose == -1 {
				return ""
			}

			if nextOpen != -1 && nextOpen < nextClose {
				depth++
				pos += nextOpen + 4
			} else {
				depth--
				if depth == 0 {
					divContent := htmlContent[contentStart : pos+nextClose]
					cleaned := b.cleanHTMLText(divContent)
					if strings.TrimSpace(cleaned) != "" {
						log.Printf("%s", logMsg)
						return cleaned
					}
					return ""
				}
				pos += nextClose + 6
			}
		}
		return ""
	}

	mainRegex := regexp.MustCompile(`(?is)<main[^>]*>(.*?)</main>`)
	if match := mainRegex.FindStringSubmatch(htmlContent); len(match) >= 2 {
		cleaned := b.cleanHTMLText(match[1])
		if strings.TrimSpace(cleaned) != "" {
			extractedContent = cleaned
			log.Printf("Manual: extracted content from <main> tag")
		}
	}

	if extractedContent == "" {
		extractedContent = extractFromDiv(`<div[^>]*class=["'][^"']*\bmw-parser-output\b[^"']*["'][^>]*>`, "Manual: extracted content from div.mw-parser-output")
	}

	if extractedContent == "" {
		patterns := []string{
			`<div[^>]+id=["']mw-content-text["'][^>]*>`,
			`<div[^>]+id=["']content["'][^>]*>`,
			`<div[^>]+id=["']main["'][^>]*>`,
			`<div[^>]+id=["']article["'][^>]*>`,
			`<div[^>]+id=["']post-content["'][^>]*>`,
			`<div[^>]+id=["']entry-content["'][^>]*>`,
		}
		for _, pattern := range patterns {
			extractedContent = extractFromDiv(pattern, "Manual: extracted content from div with id")
			if extractedContent != "" {
				break
			}
		}
	}

	if extractedContent == "" {
		extractedContent = extractFromDiv(`<div[^>]+role=["']main["'][^>]*>`, "Manual: extracted content from div with role=\"main\"")
	}

	if extractedContent == "" {
		patterns := []string{
			`<div[^>]*class=["'][^"']*\bmain\b[^"']*["'][^>]*>`,
			`<div[^>]*class=["'][^"']*\bcontent\b[^"']*["'][^>]*>`,
			`<div[^>]*class=["'][^"']*\barticle\b[^"']*["'][^>]*>`,
			`<div[^>]*class=["'][^"']*\bpost-content\b[^"']*["'][^>]*>`,
			`<div[^>]*class=["'][^"']*\bentry-content\b[^"']*["'][^>]*>`,
		}
		for _, pattern := range patterns {
			extractedContent = extractFromDiv(pattern, "Manual: extracted content from div with class")
			if extractedContent != "" {
				break
			}
		}
	}

	if extractedContent == "" {
		articleRegex := regexp.MustCompile(`(?is)<article[^>]*>(.*?)</article>`)
		if match := articleRegex.FindStringSubmatch(htmlContent); len(match) >= 2 {
			cleaned := b.cleanHTMLText(match[1])
			if strings.TrimSpace(cleaned) != "" {
				extractedContent = cleaned
				log.Printf("Manual: extracted content from <article> tag")
			}
		}
	}

	// Try JSON-LD and meta tags (common in SPAs and real estate/product listings)
	if extractedContent == "" {
		extractedContent = b.extractStructuredContent(htmlContent)
		if extractedContent != "" {
			log.Printf("Manual: extracted structured content (JSON-LD/meta)")
		}
	}

	if extractedContent == "" {
		return "", title, fmt.Errorf("no content found")
	}

	return extractedContent, title, nil
}

// cleanHTMLText removes HTML tags and unescapes HTML entities.
func (b *Bot) cleanHTMLText(html string) string {
	scriptRegex := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRegex.ReplaceAllString(html, "")

	styleRegex := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRegex.ReplaceAllString(html, "")

	hatnoteRegex := regexp.MustCompile(`(?is)<div[^>]*class=["'][^"']*\bhatnote\b[^"']*["'][^>]*>.*?</div>`)
	html = hatnoteRegex.ReplaceAllString(html, "")

	navRegex := regexp.MustCompile(`(?is)<(nav|aside|footer)[^>]*>.*?</(nav|aside|footer)>`)
	html = navRegex.ReplaceAllString(html, "")

	tableRegex := regexp.MustCompile(`(?is)<table[^>]*class=["'][^"']*\binfobox\b[^"']*["'][^>]*>.*?</table>`)
	html = tableRegex.ReplaceAllString(html, "")

	editSectionRegex := regexp.MustCompile(`(?is)<span[^>]*class=["'][^"']*\bmw-editsection\b[^"']*["'][^>]*>.*?</span>`)
	html = editSectionRegex.ReplaceAllString(html, "")

	refRegex := regexp.MustCompile(`(?is)<(sup|cite)[^>]*>.*?</(sup|cite)>`)
	html = refRegex.ReplaceAllString(html, "")

	blockTags := regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|br)>`)
	html = blockTags.ReplaceAllString(html, "\n")

	brRegex := regexp.MustCompile(`(?i)<br\s*/?>`)
	html = brRegex.ReplaceAllString(html, "\n")

	tagRegex := regexp.MustCompile(`<[^>]+>`)
	text := tagRegex.ReplaceAllString(html, " ")

	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&mdash;", "\u2014")
	text = strings.ReplaceAll(text, "&ndash;", "\u2013")
	text = strings.ReplaceAll(text, "&laquo;", "\u00AB")
	text = strings.ReplaceAll(text, "&raquo;", "\u00BB")

	text = strings.TrimSpace(text)

	multiNewlineRegex := regexp.MustCompile(`\n{3,}`)
	text = multiNewlineRegex.ReplaceAllString(text, "\n\n")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		multiSpaceRegex := regexp.MustCompile(` {2,}`)
		lines[i] = strings.TrimSpace(multiSpaceRegex.ReplaceAllString(line, " "))
	}
	text = strings.Join(lines, "\n")

	return text
}

// extractDomain extracts the domain from a URL.
func (b *Bot) extractDomain(rawURL string) string {
	domainRegex := regexp.MustCompile(`https?://([^/]+)`)
	matches := domainRegex.FindStringSubmatch(rawURL)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// isDomainExcluded checks if a domain is in the exclusion list.
// Matches exact domain or subdomains (e.g. "example.com" excludes "www.example.com" and "sub.example.com", but not "notexample.com").
func (b *Bot) isDomainExcluded(domain string) bool {
	normalizedDomain := strings.ToLower(strings.TrimPrefix(domain, "www."))

	for _, excluded := range b.config.AI.LinkSummaries.ExcludedDomains {
		normalizedExcluded := strings.ToLower(strings.TrimPrefix(excluded, "www."))
		if normalizedDomain == normalizedExcluded || strings.HasSuffix(normalizedDomain, "."+normalizedExcluded) {
			return true
		}
	}
	return false
}

// isExtensionExcluded checks if the URL path ends with an excluded file extension.
// Patterns support simple wildcard matching (e.g. ".doc*" matches ".doc" and ".docx").
func (b *Bot) isExtensionExcluded(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	ext := strings.ToLower(path.Ext(parsed.Path))
	if ext == "" {
		return false
	}
	for _, pattern := range b.config.AI.LinkSummaries.ExcludedExtensions {
		pattern = strings.ToLower(pattern)
		if !strings.HasPrefix(pattern, ".") {
			pattern = "." + pattern
		}
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(ext, prefix) {
				return true
			}
		} else if ext == pattern {
			return true
		}
	}
	return false
}

// getLinkUserAgent returns the configured user agent for link extraction, or the default.
func (b *Bot) getLinkUserAgent() string {
	if b.config.AI.LinkSummaries.UserAgent != "" {
		return b.config.AI.LinkSummaries.UserAgent
	}
	return "Mozilla/5.0 (compatible; " + BotName + "/1.0)"
}

// createHTTPClient creates an HTTP client, optionally routing through a proxy.
func (b *Bot) createHTTPClient() *http.Client {
	jar, _ := cookiejar.New(nil)

	// When an HTTP transport is injected (tests), use it directly so outbound
	// link-extraction calls can be intercepted. Production leaves it nil and
	// gets the SSRF-guarded transport below.
	if b.httpTransport != nil {
		return &http.Client{
			Timeout:   60 * time.Second,
			Jar:       jar,
			Transport: b.httpTransport,
		}
	}

	transport := &http.Transport{}
	client := &http.Client{
		Timeout:   60 * time.Second,
		Jar:       jar,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			_, err := b.validateLinkTargetURL(req.URL.String())
			return err
		},
	}

	if b.config.ProxyURL != "" {
		proxyURL, err := url.Parse(b.config.ProxyURL)
		if err != nil {
			log.Printf("Warning: invalid proxy URL %q: %v, using direct connection", b.config.ProxyURL, err)
			transport.DialContext = b.safeLinkDialContext
			return client
		}
		transport.Proxy = http.ProxyURL(proxyURL)
		return client
	}

	transport.DialContext = b.safeLinkDialContext

	return client
}

func (b *Bot) validateLinkTargetURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("URL host is required")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("URL userinfo is not allowed")
	}
	if err := validatePublicLinkHost(context.Background(), parsed.Hostname()); err != nil {
		return "", err
	}
	parsed.Scheme = scheme
	return parsed.String(), nil
}

func (b *Bot) safeLinkDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip, err := firstPublicLinkIP(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
}

func validatePublicLinkHost(ctx context.Context, host string) error {
	_, err := firstPublicLinkIP(ctx, host)
	return err
}

func firstPublicLinkIP(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedLinkIP(ip) {
			return nil, fmt.Errorf("blocked private or reserved address %s", ip.String())
		}
		return ip, nil
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("host %q did not resolve to an IP address", host)
	}

	var firstPublic net.IP
	for _, addr := range addrs {
		if isBlockedLinkIP(addr.IP) {
			return nil, fmt.Errorf("host %q resolves to blocked private or reserved address %s", host, addr.IP.String())
		}
		if firstPublic == nil {
			firstPublic = addr.IP
		}
	}
	if firstPublic == nil {
		return nil, fmt.Errorf("host %q did not resolve to a usable public IP address", host)
	}
	return firstPublic, nil
}

func isBlockedLinkIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	for _, prefix := range blockedLinkIPPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// isConsentPage checks if page content looks like a cookie consent / GDPR wall.
func (b *Bot) isConsentPage(content string) bool {
	lower := strings.ToLower(content)
	patterns := []string{
		"cookie consent",
		"cookie policy",
		"consent management",
		"nastavení souhlasu",      // Czech (Seznam.cz CMP)
		"souhlas s personalizací", // Czech
		"gdpr consent",
		"we use cookies",
		"accept all cookies",
		"согласие на обработку", // Russian
		"политика cookie",       // Russian
	}
	matches := 0
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			matches++
		}
	}
	// A real consent page usually has short content + consent keywords
	// Require at least one match AND short content to avoid false positives on real articles mentioning cookies
	return matches >= 1 && len(content) < 2000
}

// extractStructuredContent tries to extract content from HTML using JSON-LD, og:description,
// meta description, and twitter:description - in that priority order.
func (b *Bot) extractStructuredContent(htmlContent string) string {
	// Try JSON-LD structured data
	jsonLDRegex := regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	for _, match := range jsonLDRegex.FindAllStringSubmatch(htmlContent, -1) {
		if len(match) < 2 {
			continue
		}
		if text := b.extractJSONLDText(strings.TrimSpace(match[1])); strings.TrimSpace(text) != "" {
			return text
		}
	}

	// Try og:description
	ogDescRegex := regexp.MustCompile(`(?i)<meta[^>]*property=["']og:description["'][^>]*content=["']([^"']+)["']`)
	if match := ogDescRegex.FindStringSubmatch(htmlContent); len(match) >= 2 {
		if cleaned := b.cleanHTMLText(match[1]); strings.TrimSpace(cleaned) != "" {
			return cleaned
		}
	}

	// Try meta description
	descRegex := regexp.MustCompile(`(?i)<meta[^>]*name=["']description["'][^>]*content=["']([^"']+)["']`)
	if match := descRegex.FindStringSubmatch(htmlContent); len(match) >= 2 {
		if cleaned := b.cleanHTMLText(match[1]); strings.TrimSpace(cleaned) != "" {
			return cleaned
		}
	}

	// Try twitter:description
	twitterDescRegex := regexp.MustCompile(`(?i)<meta[^>]*name=["']twitter:description["'][^>]*content=["']([^"']+)["']`)
	if match := twitterDescRegex.FindStringSubmatch(htmlContent); len(match) >= 2 {
		if cleaned := b.cleanHTMLText(match[1]); strings.TrimSpace(cleaned) != "" {
			return cleaned
		}
	}

	return ""
}

// extractJSONLDText extracts meaningful text from a JSON-LD script content.
func (b *Bot) extractJSONLDText(raw string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err == nil {
		if text := b.extractJSONLDFields(data); text != "" {
			return text
		}
	}
	var arr []interface{}
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		for _, item := range arr {
			if obj, ok := item.(map[string]interface{}); ok {
				if text := b.extractJSONLDFields(obj); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

// extractJSONLDFields extracts common text fields from a JSON-LD object.
func (b *Bot) extractJSONLDFields(data map[string]interface{}) string {
	keys := []string{"name", "headline", "description", "articleBody", "text"}
	var parts []string
	for _, key := range keys {
		if v, ok := data[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
	}
	// Check @graph array (common in WordPress/Yoast JSON-LD)
	if graph, ok := data["@graph"]; ok {
		if items, ok := graph.([]interface{}); ok {
			for _, item := range items {
				if obj, ok := item.(map[string]interface{}); ok {
					if text := b.extractJSONLDFields(obj); text != "" {
						parts = append(parts, text)
					}
				}
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return ""
}
