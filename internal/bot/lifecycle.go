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
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gennadium/internal/i18n"
	"gennadium/internal/telegram"
	"gennadium/internal/telegram/tgadapter"

	"github.com/go-telegram/bot/models"
)

// Start starts the bot.
func (b *Bot) Start() {
	defer close(b.startDone)

	b.botCtx, b.botCancel = context.WithCancel(context.Background())

	if b.tgbot == nil {
		log.Println("⚠️  Telegram API is not initialized (bot_token missing or invalid). Skipping Telegram connection.")
		if b.config.WebUI.Enabled && b.httpMux != nil {
			go b.startWebUIServer()
		}
		<-b.stopCh
		return
	}

	log.Printf("Bot started as @%s", b.botSelf.Username)

	log.Printf("📋 Managing %d moderation chat(s)", b.config.Moderation.ChatIDs.Count())
	for _, chatID := range b.config.Moderation.ChatIDs.All() {
		log.Printf("   - Chat ID: %d", chatID)
	}

	b.resolveModerationChatsAsync()
	b.warmTopicDirectory()
	b.seedTopicNamesFromConfig()

	b.notifySuperAdminStartup()

	go b.startMuteExpirationChecker()
	go b.startScheduledTasks()
	go b.startDBHealthMonitor()

	if b.config.Webhook.Enabled {
		b.startWebhookMode()
	} else {
		if b.config.WebUI.Enabled && b.httpMux != nil {
			go b.startWebUIServer()
		}
		b.startPollingMode()
	}
}

// notifySuperAdminStartup sends a one-off "bot is up" DM to the super-admin when
// admin.notify_startup is enabled and a super-admin user id is configured. It's
// best-effort: a send failure is logged, never fatal, so it can't block startup.
func (b *Bot) notifySuperAdminStartup() {
	b.notifySuperAdmin(i18n.Tf("startup.notify", b.botSelf.Username, b.version, b.buildTime))
}

// notifySuperAdmin DMs an operational notice to the super-admin, gated by
// admin.notify_startup and the presence of a configured super-admin user id.
// Best-effort: send failures are logged, never fatal.
func (b *Bot) notifySuperAdmin(text string) {
	if !b.config.Admin.NotifyStartup || b.config.Admin.SuperAdminUserID == 0 {
		return
	}
	if _, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:                b.config.Admin.SuperAdminUserID,
		Text:                  text,
		DisableWebPagePreview: true,
	}); err != nil {
		log.Printf("Failed to send super-admin notification: %v", err)
	}
}

// Stop stops the bot, waiting for in-flight tasks to finish.
func (b *Bot) Stop() {
	close(b.stopCh)
	if b.botCancel != nil {
		b.botCancel()
	}

	// Wait for Start() to fully return (polling/webhook loop exited)
	select {
	case <-b.startDone:
	case <-time.After(5 * time.Second):
		log.Println("⚠️ Start() did not return within 5s, proceeding with shutdown")
	}

	done := make(chan struct{})
	go func() {
		b.taskWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All tasks finished, shutting down")
	case <-time.After(1 * time.Second):
		log.Println("⏳ Some tasks are still running, waiting for them to finish...")
		select {
		case <-done:
			log.Println("All tasks finished, shutting down")
		case <-time.After(29 * time.Second):
			log.Println("⚠️ Shutdown timeout: some tasks are still running after 30s, forcing exit")
		}
	}
}

// withSecurityHeaders wraps an HTTP handler to add baseline security response
// headers: clickjacking and MIME-sniffing protection plus a Content-Security-
// Policy. The CSP still allows the inline handlers/styles and the Alpine.js CDN
// the admin UI relies on, while restricting script origins and pinning
// connect/img/form targets to same-origin to limit data exfiltration via XSS.
func withSecurityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdn.jsdelivr.net https://cdnjs.cloudflare.com; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"font-src 'self'; " +
		"connect-src 'self'; " +
		"base-uri 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// startWebUIServer starts a standalone HTTP server for the web UI in polling mode.
func (b *Bot) startWebUIServer() {
	listenAddr := fmt.Sprintf("%s:%d", b.config.Server.ListenAddr, b.config.Server.ListenPort)
	log.Printf("🌐 Starting web UI server on %s", listenAddr)

	server := &http.Server{
		Addr:         listenAddr,
		Handler:      withSecurityHeaders(b.httpMux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-b.stopCh
		server.Close()
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("Web UI server error: %v", err)
	}
}

// maybeForceGC runs an explicit GC cycle when FORCED_GC is enabled.
func (b *Bot) maybeForceGC() {
	if b.forcedGC {
		runtime.GC()
	}
}

// processUpdate handles a single incoming update regardless of how it was received.
func (b *Bot) processUpdate(update telegram.Update) {
	b.debugf("TG update received: update_id=%d type=%s", update.UpdateID, describeUpdateType(update))

	switch {
	case update.Message != nil:
		b.traceInboundTopic("message", update.Message)
		b.RefreshChatFromUpdate(update.Message.Chat)
		b.harvestTopicFromMessage(update.Message)
	case update.EditedMessage != nil:
		b.traceInboundTopic("edited_message", update.EditedMessage)
		b.RefreshChatFromUpdate(update.EditedMessage.Chat)
		b.harvestTopicFromMessage(update.EditedMessage)
	case update.CallbackQuery != nil && update.CallbackQuery.Message != nil:
		b.traceInboundTopic("callback_message", update.CallbackQuery.Message)
		b.RefreshChatFromUpdate(update.CallbackQuery.Message.Chat)
	}

	if update.Message != nil {
		fromID := int64(0)
		if update.Message.From != nil {
			fromID = update.Message.From.ID
		}
		b.debugf("TG message event: chat_id=%d message_id=%d from_id=%d text_len=%d", update.Message.Chat.ID, update.Message.MessageID, fromID, len(update.Message.Text))
		b.handleMessage(update.Message, false)
	} else if update.EditedMessage != nil {
		fromID := int64(0)
		if update.EditedMessage.From != nil {
			fromID = update.EditedMessage.From.ID
		}
		b.debugf("TG edited message event: chat_id=%d message_id=%d from_id=%d text_len=%d", update.EditedMessage.Chat.ID, update.EditedMessage.MessageID, fromID, len(update.EditedMessage.Text))
		log.Printf("Processing edited message %d", update.EditedMessage.MessageID)
		b.handleMessage(update.EditedMessage, true)
	} else if update.CallbackQuery != nil {
		chatID := int64(0)
		messageID := 0
		if update.CallbackQuery.Message != nil {
			chatID = update.CallbackQuery.Message.Chat.ID
			messageID = update.CallbackQuery.Message.MessageID
		}
		b.debugf("TG callback event: from_id=%d chat_id=%d message_id=%d data=%q", update.CallbackQuery.From.ID, chatID, messageID, update.CallbackQuery.Data)
		b.handleCallbackQuery(update.CallbackQuery)
	} else if update.MessageReactionCount != nil {
		b.handleMessageReactionCount(update.MessageReactionCount)
	} else if update.MessageReaction != nil {
		b.handleMessageReaction(update.MessageReaction)
	}
}

// startPollingMode starts the bot in long polling mode.
func (b *Bot) startPollingMode() {
	log.Println("📡 Starting in long polling mode")

	if err := b.tg.DeleteWebhook(false); err != nil {
		log.Printf("Warning: failed to delete webhook (may not be set): %v", err)
	}

	if b.telegramStatus != nil {
		b.telegramStatus.SetTelegramConnected("polling", b.botSelf.Username)
	}

	// Start blocks until b.botCtx is cancelled (see Stop). Incoming updates are
	// dispatched to processUpdate via the default handler configured in New.
	b.tgbot.Start(b.botCtx)
}

// startWebhookMode starts the bot in webhook mode.
func (b *Bot) startWebhookMode() {
	listenAddr := fmt.Sprintf("%s:%d", b.config.Server.ListenAddr, b.config.Server.ListenPort)

	parsedURL, err := url.Parse(b.config.Webhook.URL)
	if err != nil {
		log.Fatalf("Webhook: invalid URL %q: %v", b.config.Webhook.URL, err)
	}
	webhookPath := parsedURL.Path
	if webhookPath == "" {
		webhookPath = "/"
	}

	log.Printf("🌐 Starting in webhook mode, listening on %s%s", listenAddr, webhookPath)
	if b.config.Webhook.SecretToken != "" {
		log.Println("🔒 Webhook secret token validation enabled")
	}

	log.Printf("🔗 Registering webhook URL with Telegram: %s", b.config.Webhook.URL)

	// Pre-read certificate data once (if needed) so we can retry without re-reading the file.
	var certData []byte
	if b.config.Server.CertificatePath != "" {
		log.Printf("📜 Uploading TLS certificate: %s", b.config.Server.CertificatePath)
		certData, err = os.ReadFile(b.config.Server.CertificatePath)
		if err != nil {
			log.Fatalf("Webhook: failed to read certificate file %q: %v", b.config.Server.CertificatePath, err)
		}
	}

	const maxWebhookAttempts = 8
	var respBody []byte
	var lastStatus int
	for attempt := 1; attempt <= maxWebhookAttempts; attempt++ {
		var resp *http.Response
		var reqErr error

		// When reaction tracking is enabled, opt in to reaction updates (not in
		// the default set). Setting allowed_updates means we must list every kind
		// we want to keep receiving.
		allowedUpdates := ""
		if b.config.AI.Enabled && b.config.AI.TrackReactions {
			allowedUpdates = `["message","edited_message","channel_post","edited_channel_post","callback_query","message_reaction","message_reaction_count"]`
		}

		if certData != nil {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			writer.WriteField("url", b.config.Webhook.URL)
			if b.config.Webhook.SecretToken != "" {
				writer.WriteField("secret_token", b.config.Webhook.SecretToken)
			}
			if allowedUpdates != "" {
				writer.WriteField("allowed_updates", allowedUpdates)
			}
			part, perr := writer.CreateFormFile("certificate", filepath.Base(b.config.Server.CertificatePath))
			if perr != nil {
				log.Fatalf("Webhook: failed to create multipart form: %v", perr)
			}
			part.Write(certData)
			writer.Close()

			apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", b.config.BotToken)
			resp, reqErr = http.Post(apiURL, writer.FormDataContentType(), &body)
		} else {
			params := url.Values{}
			params.Set("url", b.config.Webhook.URL)
			if b.config.Webhook.SecretToken != "" {
				params.Set("secret_token", b.config.Webhook.SecretToken)
			}
			if allowedUpdates != "" {
				params.Set("allowed_updates", allowedUpdates)
			}
			apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook?%s", b.config.BotToken, params.Encode())
			resp, reqErr = http.Get(apiURL)
		}

		if reqErr != nil {
			// Transient network error: back off and retry.
			if attempt == maxWebhookAttempts {
				log.Fatalf("Webhook: failed to set webhook after %d attempts: %v", attempt, reqErr)
			}
			wait := time.Duration(attempt) * 2 * time.Second
			log.Printf("Webhook: request error (attempt %d/%d): %v - retrying in %s", attempt, maxWebhookAttempts, reqErr, wait)
			time.Sleep(wait)
			continue
		}

		respBody, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		lastStatus = resp.StatusCode

		if resp.StatusCode == http.StatusOK {
			break
		}

		// Parse error response to honor retry_after when Telegram rate-limits us.
		var errResult struct {
			OK          bool   `json:"ok"`
			ErrorCode   int    `json:"error_code"`
			Description string `json:"description"`
			Parameters  struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		_ = json.Unmarshal(respBody, &errResult)

		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if !retryable || attempt == maxWebhookAttempts {
			log.Fatalf("Webhook: setWebhook returned status %d: %s", resp.StatusCode, string(respBody))
		}

		wait := time.Duration(errResult.Parameters.RetryAfter) * time.Second
		if wait <= 0 {
			wait = time.Duration(attempt) * 2 * time.Second
		}
		// Add a small buffer to avoid hitting the limit again immediately.
		wait += 500 * time.Millisecond
		log.Printf("Webhook: setWebhook status %d (attempt %d/%d): %s - retrying in %s",
			resp.StatusCode, attempt, maxWebhookAttempts, string(respBody), wait)
		time.Sleep(wait)
	}

	if lastStatus != http.StatusOK {
		log.Fatalf("Webhook: setWebhook did not succeed; last status %d: %s", lastStatus, string(respBody))
	}

	var setResult struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(respBody, &setResult); err == nil {
		if !setResult.OK {
			log.Fatalf("Webhook: setWebhook failed: %s", setResult.Description)
		}
	}
	log.Printf("✅ Webhook registered: %s", string(respBody))

	if b.telegramStatus != nil {
		b.telegramStatus.SetTelegramConnected("webhook", b.botSelf.Username)
	}

	infoURL := fmt.Sprintf("https://api.telegram.org/bot%s/getWebhookInfo", b.config.BotToken)
	infoResp, err := http.Get(infoURL)
	if err != nil {
		log.Printf("Warning: failed to get webhook info: %v", err)
	} else {
		infoBody, _ := io.ReadAll(infoResp.Body)
		infoResp.Body.Close()
		log.Printf("📋 Webhook info: %s", string(infoBody))
	}

	mux := b.httpMux
	if mux == nil {
		mux = http.NewServeMux()
	}
	mux.HandleFunc(webhookPath, b.webhookHandler)
	if !strings.HasSuffix(webhookPath, "/") {
		mux.HandleFunc(webhookPath+"/", b.webhookHandler)
	}

	server := &http.Server{
		Addr:         listenAddr,
		Handler:      withSecurityHeaders(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-b.stopCh
		log.Println("Shutting down webhook server...")
		server.Close()
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Webhook server error: %v", err)
	}
}

// webhookHandler processes incoming webhook requests from Telegram.
func (b *Bot) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if b.config.Webhook.Debug {
		log.Printf("Webhook DEBUG: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		log.Printf("Webhook DEBUG: headers: %v", r.Header)
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
		return
	}

	if b.config.Webhook.SecretToken != "" {
		token := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if token != b.config.Webhook.SecretToken {
			log.Printf("Webhook: rejected request with invalid secret token")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Webhook: error reading request body: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if b.config.Webhook.Debug {
		log.Printf("Webhook DEBUG: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		log.Printf("Webhook DEBUG: headers: %v", r.Header)
		log.Printf("Webhook DEBUG: body (%d bytes): %s", len(body), string(body))
	}

	var update models.Update
	if err := json.Unmarshal(body, &update); err != nil {
		log.Printf("Webhook: error parsing update JSON: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if b.config.Webhook.Debug {
		log.Printf("Webhook DEBUG: update_id=%d, message=%v, callback=%v",
			update.ID, update.Message != nil, update.CallbackQuery != nil)
	}

	w.WriteHeader(http.StatusOK)

	if b.telegramStatus != nil {
		b.telegramStatus.RecordWebhookReceived()
	}

	b.processUpdate(tgadapter.ToUpdate(&update))
}
