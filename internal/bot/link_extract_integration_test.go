// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publicTarget is a target URL whose host is a public IP literal. Using an IP
// literal means validateLinkTargetURL does not perform a DNS lookup, keeping
// the link-extraction tests fully offline. The actual fetch is redirected to
// the test server by the injected transport.
const publicTarget = "https://93.184.216.34/article"

// --- pure helpers -------------------------------------------------------------

func TestExtractDomain(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	assert.Equal(t, "example.com", b.extractDomain("https://example.com/path"))
	assert.Equal(t, "sub.example.com:8080", b.extractDomain("http://sub.example.com:8080/x"))
	assert.Equal(t, "", b.extractDomain("not a url"))
}

func TestIsDomainExcluded(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.LinkSummaries.ExcludedDomains = []string{"t.me", "example.com"}

	assert.True(t, b.isDomainExcluded("example.com"))
	assert.True(t, b.isDomainExcluded("www.example.com"))
	assert.True(t, b.isDomainExcluded("sub.example.com"))
	assert.False(t, b.isDomainExcluded("notexample.com"))
	assert.False(t, b.isDomainExcluded("other.org"))
}

func TestIsExtensionExcluded(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.LinkSummaries.ExcludedExtensions = []string{".pdf", ".doc*"}

	assert.True(t, b.isExtensionExcluded("https://x.com/file.pdf"))
	assert.True(t, b.isExtensionExcluded("https://x.com/file.doc"))
	assert.True(t, b.isExtensionExcluded("https://x.com/file.docx"))
	assert.False(t, b.isExtensionExcluded("https://x.com/page.html"))
	assert.False(t, b.isExtensionExcluded("https://x.com/noext"))
}

func TestGetLinkUserAgent(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	assert.Contains(t, b.getLinkUserAgent(), BotName)

	b.config.AI.LinkSummaries.UserAgent = "Custom/1.0"
	assert.Equal(t, "Custom/1.0", b.getLinkUserAgent())
}

func TestIsConsentPage(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	assert.True(t, b.isConsentPage("Please accept all cookies to continue"))
	assert.False(t, b.isConsentPage("A normal article that briefly mentions cookies in a recipe."))
	// Long content with a keyword is not treated as a consent wall.
	long := "we use cookies " + string(make([]byte, 2500))
	assert.False(t, b.isConsentPage(long))
}

func TestExtractStructuredContent(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})

	// og:description path.
	og := `<html><head><meta property="og:description" content="OG summary here"></head></html>`
	assert.Equal(t, "OG summary here", b.extractStructuredContent(og))

	// meta description path.
	meta := `<meta name="description" content="Meta summary">`
	assert.Equal(t, "Meta summary", b.extractStructuredContent(meta))

	// Nothing extractable.
	assert.Equal(t, "", b.extractStructuredContent("<html><body>plain</body></html>"))
}

func TestValidateLinkTargetURL(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})

	// Public IP literal passes (no DNS).
	out, err := b.validateLinkTargetURL("https://93.184.216.34/x")
	require.NoError(t, err)
	assert.Equal(t, "https://93.184.216.34/x", out)

	// Private IP is rejected (SSRF guard).
	_, err = b.validateLinkTargetURL("http://127.0.0.1/x")
	require.Error(t, err)

	// Bad scheme rejected.
	_, err = b.validateLinkTargetURL("ftp://example.com/x")
	require.Error(t, err)

	// userinfo rejected.
	_, err = b.validateLinkTargetURL("https://user:pass@93.184.216.34/x")
	require.Error(t, err)

	// Missing host rejected.
	_, err = b.validateLinkTargetURL("https:///path")
	require.Error(t, err)
}

func TestIsBlockedLinkIP(t *testing.T) {
	assert.True(t, isBlockedLinkIP(net.ParseIP("127.0.0.1")))
	assert.True(t, isBlockedLinkIP(net.ParseIP("10.0.0.1")))
	assert.True(t, isBlockedLinkIP(net.ParseIP("169.254.0.1")))
	assert.True(t, isBlockedLinkIP(net.ParseIP("::1")))
	assert.True(t, isBlockedLinkIP(nil))
	assert.False(t, isBlockedLinkIP(net.ParseIP("93.184.216.34")))
	assert.False(t, isBlockedLinkIP(net.ParseIP("8.8.8.8")))
}

// --- fetchers -----------------------------------------------------------------

func TestFetchTelegramPostContent(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<div class="tgme_widget_message_text js-message_text">Hello from channel</div>`))
	})

	content, err := b.fetchTelegramPostContent("durov", "123")
	require.NoError(t, err)
	assert.Contains(t, content, "Hello from channel")
	assert.Contains(t, rt.last().Host, "t.me")
}

func TestFetchTelegramPostContent_ErrorStatus(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := b.fetchTelegramPostContent("durov", "123")
	require.Error(t, err)
}

func TestFetchWithExtractorAPI(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"COMPLETE","title":"Title","text":"Extracted body text"}`))
	})
	b.config.AI.LinkSummaries.ExtractorAPIKey = "k"

	content, title, err := b.fetchWithExtractorAPI(publicTarget)
	require.NoError(t, err)
	assert.Equal(t, "Title", title)
	assert.Equal(t, "Extracted body text", content)
	assert.Contains(t, rt.last().Host, "extractorapi.com")
}

func TestFetchWithExtractorAPI_NotComplete(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"PENDING"}`))
	})
	b.config.AI.LinkSummaries.ExtractorAPIKey = "k"

	_, _, err := b.fetchWithExtractorAPI(publicTarget)
	require.Error(t, err)
}

func TestFetchWithExtractorAPI_BlockedTarget(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	_, _, err := b.fetchWithExtractorAPI("http://127.0.0.1/x")
	require.Error(t, err)
}

func TestFetchWithDiffbot(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"humanLanguage":"en","title":"Doc",
			"objects":[{"title":"Doc","text":"Article body"}]}`))
	})
	b.config.AI.LinkSummaries.DiffbotAPIKey = "k"

	content, title, lang, err := b.fetchWithDiffbot(publicTarget)
	require.NoError(t, err)
	assert.Equal(t, "Article body", content)
	assert.Equal(t, "Doc", title)
	assert.Equal(t, "en", lang)
	assert.Contains(t, rt.last().Host, "diffbot.com")
}

func TestFetchWithDiffbot_NoObjects(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"objects":[]}`))
	})
	b.config.AI.LinkSummaries.DiffbotAPIKey = "k"

	_, _, _, err := b.fetchWithDiffbot(publicTarget)
	require.Error(t, err)
}

func TestFetchWithCloudflare(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":"# Heading\n\nMarkdown body content here"}`))
	})
	b.config.AI.LinkSummaries.CloudflareAccountID = "acct"
	b.config.AI.LinkSummaries.CloudflareAPIToken = "token"

	content, _, err := b.fetchWithCloudflare(publicTarget)
	require.NoError(t, err)
	assert.Contains(t, content, "Markdown body content")
	assert.Contains(t, rt.last().Host, "cloudflare.com")
}

func TestFetchWithCloudflareContent(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Page Title</title>
			<meta property="og:description" content="The rendered summary"></head><body>x</body></html>`))
	})
	b.config.AI.LinkSummaries.CloudflareAccountID = "acct"
	b.config.AI.LinkSummaries.CloudflareAPIToken = "token"

	content, title, err := b.fetchWithCloudflareContent(publicTarget)
	require.NoError(t, err)
	assert.Equal(t, "Page Title", title)
	assert.Contains(t, content, "The rendered summary")
}
