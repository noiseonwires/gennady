// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/i18n"
	"gennadium/internal/telegram"
	"gennadium/internal/telegram/tgadapter"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// BotName is the internal software name used in logs, user-agent strings, and UI.
const BotName = "Gennady"

// BotURL is the project URL.
const BotURL = "https://github.com/noiseonwires/gennady"

const (
	copyrightStartYear = 2025
	copyrightOwner     = "Kirill aka Noiseonwires"
)

// publicMessageLinkRegex matches public Telegram message links
// (https://t.me/<username>/<id>). Compiled once at package load.
var publicMessageLinkRegex = regexp.MustCompile(`https://t\.me/([a-zA-Z0-9_]+)/(\d+)`)

// APICallRecorder records external API call results for diagnostics.
type APICallRecorder interface {
	RecordCall(service string, statusCode int, errMsg string, duration time.Duration, requestURL string)
}

// TelegramStatusReporter reports Telegram connection status for diagnostics.
type TelegramStatusReporter interface {
	SetTelegramConnected(mode, botUsername string)
	SetTelegramError(errMsg string)
	RecordWebhookReceived()
}

// Bot is the main bot instance.
type Bot struct {
	// tgbot is the underlying Telegram client, retained only for inbound update
	// streaming (long polling via Start). All outbound calls go through tg (the
	// library-agnostic port). May be nil when bot_token is missing but the web
	// UI is enabled.
	tgbot *tgbot.Bot
	// botCtx/botCancel drive the polling loop lifetime; Stop cancels botCtx.
	botCtx    context.Context
	botCancel context.CancelFunc
	// tg is the library-agnostic outbound Telegram port. In tests it is a mock.
	tg telegram.Client
	// botSelf is the bot's own identity, captured at startup.
	botSelf telegram.User

	// httpTransport is the RoundTripper used for all outbound 3rd-party HTTP
	// calls (Azure, Open-Meteo, RSS, link extraction, file downloads, …). When
	// nil, http.DefaultTransport is used. Tests inject a transport that routes
	// requests to in-process test servers, independent of the real host names.
	httpTransport http.RoundTripper

	// now returns the current time. Defaults to time.Now; tests may override it
	// to make time-dependent behaviour deterministic.
	now func() time.Time

	config    *config.Config
	db        *database.DB
	stopCh    chan struct{}
	startDone chan struct{} // closed when Start() returns
	taskWg    sync.WaitGroup

	// Build information
	version   string
	gitCommit string
	buildTime string

	// Web UI integration
	diagnostics    APICallRecorder
	telegramStatus TelegramStatusReporter
	generateWebOTP func() string
	httpMux        *http.ServeMux

	forcedGC bool // explicit GC after each event, enabled via FORCED_GC env

	// Resolved chat metadata (titles, forum flag) keyed by chat_id.
	chatDir *chatDirectory

	// Cached forum-topic names keyed by (chat_id, thread_id), harvested from
	// forum-topic service messages (the Bot API can't query them directly).
	topicDir *topicDirectory

	// Deduplication: track messages already sent for moderation
	moderatedMu   sync.Mutex
	moderatedMsgs map[string]time.Time

	// Deduplication: track callback_query IDs already processed so a redelivered
	// callback (e.g. a Telegram webhook retry) doesn't apply the action twice.
	callbackDedupMu sync.Mutex
	callbackDedup   map[string]time.Time

	// Bounded, TTL'd in-memory cache of user profiles for moderation prompts.
	profileCache *userProfileCache
}

// SetBuildInfo sets the build metadata displayed in the About screen.
func (b *Bot) SetBuildInfo(version, gitCommit, buildTime string) {
	b.version = version
	b.gitCommit = gitCommit
	b.buildTime = buildTime
}

// SetDiagnostics sets the API diagnostics recorder.
func (b *Bot) SetDiagnostics(d APICallRecorder) {
	b.diagnostics = d
}

// SetTelegramStatusReporter sets the Telegram status reporter for diagnostics.
func (b *Bot) SetTelegramStatusReporter(r TelegramStatusReporter) {
	b.telegramStatus = r
}

// SetWebOTPGenerator sets the function that generates web UI OTP codes.
func (b *Bot) SetWebOTPGenerator(fn func() string) {
	b.generateWebOTP = fn
}

// SendOTPToSuperAdmin sends an OTP code to the super-admin user via Telegram.
func (b *Bot) SendOTPToSuperAdmin(code string) error {
	if b.tgbot == nil {
		return fmt.Errorf("telegram API not initialized (bot_token missing)")
	}
	superAdmin := b.config.Admin.SuperAdminUserID
	if superAdmin == 0 {
		return fmt.Errorf("super_admin_user_id not configured")
	}
	log.Printf("\U0001F510 Web UI OTP: '%s' (sent to super-admin %d)", code, superAdmin)
	_, err := b.tg.SendMessage(telegram.SendMessageParams{
		ChatID:    superAdmin,
		Text:      i18n.Tf("webui.otp_text", code),
		ParseMode: telegram.ParseModeMarkdown,
	})
	return err
}

// SetHTTPMux sets the shared HTTP mux for webhook + web UI routing.
func (b *Bot) SetHTTPMux(mux *http.ServeMux) {
	b.httpMux = mux
}

// Telegram client startup tuning. The go-telegram/bot library validates the
// bot token by calling getMe when the client is created, bounded by a hardcoded
// 5s timeout. That window is too tight right after a container restart, when
// DNS and TLS to api.telegram.org are still cold: a single transient blip
// surfaces as "context deadline exceeded" and leaves the bot offline until the
// next manual restart. We raise the per-attempt timeout and retry a few times
// so transient startup failures self-heal.
const (
	telegramInitTimeout    = 10 * time.Second
	telegramInitMaxRetries = 3
	telegramInitRetryDelay = 2 * time.Second
)

// newTelegramAPI creates the Telegram client, retrying on transient network
// failures. Definitive API errors (e.g. an invalid or revoked token) are not
// retried, since another attempt cannot succeed.
func newTelegramAPI(token string, opts []tgbot.Option) (*tgbot.Bot, error) {
	opts = append(opts, tgbot.WithCheckInitTimeout(telegramInitTimeout))

	var lastErr error
	for attempt := 1; attempt <= telegramInitMaxRetries; attempt++ {
		api, err := tgbot.New(token, opts...)
		if err == nil {
			return api, nil
		}
		lastErr = err
		if !isRetryableInitError(err) {
			return nil, err
		}
		if attempt < telegramInitMaxRetries {
			delay := time.Duration(attempt) * telegramInitRetryDelay
			log.Printf("⚠️  Telegram API init attempt %d/%d failed: %v. Retrying in %s...",
				attempt, telegramInitMaxRetries, err, delay)
			time.Sleep(delay)
		}
	}
	return nil, lastErr
}

// isRetryableInitError reports whether a Telegram client init failure is worth
// retrying. Transient transport problems (timeouts, DNS, refused connections)
// are retryable; definitive API errors that indicate a bad token or request are
// not.
func isRetryableInitError(err error) bool {
	switch {
	case errors.Is(err, tgbot.ErrorUnauthorized),
		errors.Is(err, tgbot.ErrorForbidden),
		errors.Is(err, tgbot.ErrorBadRequest),
		errors.Is(err, tgbot.ErrorNotFound),
		errors.Is(err, tgbot.ErrorConflict):
		return false
	default:
		return true
	}
}

// New creates a new bot instance.
func New(cfg *config.Config, db *database.DB) (*Bot, error) {
	bot := &Bot{
		now:           time.Now,
		config:        cfg,
		db:            db,
		stopCh:        make(chan struct{}),
		startDone:     make(chan struct{}),
		forcedGC:      os.Getenv("FORCED_GC") != "",
		chatDir:       newChatDirectory(),
		topicDir:      newTopicDirectory(),
		moderatedMsgs: make(map[string]time.Time),
		callbackDedup: make(map[string]time.Time),
		profileCache:  newUserProfileCache(),
	}

	var api *tgbot.Bot
	if cfg.BotToken != "" {
		opts := []tgbot.Option{
			tgbot.WithDefaultHandler(func(_ context.Context, _ *tgbot.Bot, u *models.Update) {
				bot.processUpdate(tgadapter.ToUpdate(u))
			}),
			// Run handlers synchronously in the polling worker so updates are
			// processed in order, matching the previous library's behaviour.
			tgbot.WithNotAsyncHandlers(),
		}
		if cfg.Debug.DebugTelegram || cfg.Webhook.Debug {
			opts = append(opts, tgbot.WithDebug())
		}
		// Reaction updates are not part of Telegram's default update set, so we
		// must opt in via allowed_updates. We only do this when reaction tracking
		// is enabled - otherwise the default set is left untouched. Note that
		// setting allowed_updates means we must list every update kind we want.
		if cfg.AI.Enabled && cfg.AI.TrackReactions {
			opts = append(opts, tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{
				models.AllowedUpdateMessage,
				models.AllowedUpdateEditedMessage,
				models.AllowedUpdateChannelPost,
				models.AllowedUpdateEditedChannelPost,
				models.AllowedUpdateCallbackQuery,
				models.AllowedUpdateMessageReaction,
				models.AllowedUpdateMessageReactionCount,
			}))
		}
		var err error
		api, err = newTelegramAPI(cfg.BotToken, opts)
		if err != nil {
			if !cfg.WebUI.Enabled {
				return nil, err
			}
			log.Printf("⚠️  Failed to initialize Telegram API: %v. Web UI will still start so you can fix the token.", err)
			api = nil
		}
	} else if !cfg.WebUI.Enabled {
		return nil, fmt.Errorf("bot_token is not configured")
	} else {
		log.Printf("⚠️  bot_token is not configured. Bot will not connect to Telegram; configure it via the Web UI.")
	}

	bot.tgbot = api
	bot.tg = tgadapter.New(api)

	if api != nil {
		if me, err := api.GetMe(context.Background()); err == nil {
			bot.botSelf = telegram.User{
				ID:        me.ID,
				IsBot:     me.IsBot,
				FirstName: me.FirstName,
				LastName:  me.LastName,
				Username:  me.Username,
			}
		} else {
			log.Printf("⚠️  Failed to fetch bot identity via getMe: %v", err)
		}
	}

	return bot, nil
}

// buildAboutText returns the bot info string.
func (b *Bot) buildAboutText() string {
	hideBranding := os.Getenv("HIDE_BRANDING") == "true"
	if hideBranding {
		text := fmt.Sprintf("*%s*\n\n"+
			"%s: `%s`\n"+
			"%s: `%s`\n"+
			"%s: `%s`",
			BotName, i18n.T("about.version"), b.version, i18n.T("about.commit"), b.gitCommit, i18n.T("about.build"), b.buildTime)
		return text
	}
	text := fmt.Sprintf("*%s*\n\n"+
		"%s: `%s`\n"+
		"%s: `%s`\n"+
		"%s: `%s`\n"+
		"%s: `AGPL-3 or commercial`\n"+
		"This software is licensed under the GNU Affero General Public License v3.",
		BotName, i18n.T("about.version"), b.version, i18n.T("about.commit"), b.gitCommit, i18n.T("about.build"), b.buildTime, i18n.T("about.license"))
	text += "\n" + CopyrightNotice(b.buildTime)
	text += fmt.Sprintf("\nURL: %s", BotURL)
	return text
}

// CopyrightNotice returns a copyright line with a year derived from buildTime.
func CopyrightNotice(buildTime string) string {
	buildYear := parseBuildYear(buildTime)
	if buildYear <= copyrightStartYear {
		return fmt.Sprintf("© %d %s", copyrightStartYear, copyrightOwner)
	}
	return fmt.Sprintf("© %d-%d %s", copyrightStartYear, buildYear, copyrightOwner)
}

func parseBuildYear(buildTime string) int {
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, buildTime); err == nil {
			return t.UTC().Year()
		}
	}

	return time.Now().UTC().Year()
}

func (b *Bot) debugf(format string, args ...interface{}) {
	if b.config.Debug.DebugTelegram {
		msg := fmt.Sprintf("DEBUG: "+format, args...)
		log.Print(msg)
		b.sendDebugToSuperAdmin(msg)
	}
}

// tracef logs a verbose TRACE line gated behind debug.trace_topics. It is used
// to confirm - after a deployment - that Telegram populates forum-topic fields
// (message_thread_id, reply_to, is_forum) the way the bot expects, and that
// outbound topic targeting is correct. TRACE lines go to the process log only
// (never forwarded to the super-admin) to avoid spamming chats.
func (b *Bot) tracef(format string, args ...interface{}) {
	if b.config.Debug.TraceTopics {
		log.Printf("TRACE: "+format, args...)
	}
}

func describeUpdateType(update telegram.Update) string {
	switch {
	case update.Message != nil:
		return "message"
	case update.EditedMessage != nil:
		return "edited_message"
	case update.CallbackQuery != nil:
		return "callback_query"
	case update.ChannelPost != nil:
		return "channel_post"
	case update.EditedChannelPost != nil:
		return "edited_channel_post"
	default:
		return "other"
	}
}
