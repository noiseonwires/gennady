// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

// All top-level configuration struct types plus their small one-line helper
// methods. Anything that builds, parses or persists configuration lives in
// the sibling files (load.go, defaults.go, env_apply.go, etc).

// Config is the top-level configuration structure.
// All scalar fields can be overridden via environment variables using the naming
// convention: YAML path segments joined with underscores, uppercased.
//
//	bot_token                          → BOT_TOKEN
//	admin.chat_id                      → ADMIN_CHAT_ID
//	ai.content_moderation.enabled      → AI_CONTENT_MODERATION_ENABLED
type Config struct {
	BotToken string `yaml:"bot_token" web:"sensitive"`
	ProxyURL string `yaml:"proxy_url"`
	Language string `yaml:"language"`

	Database         DatabaseConfig            `yaml:"database"`
	Reactions        ReactionsConfig           `yaml:"reactions"`
	Admin            AdminConfig               `yaml:"admin"`
	Moderation       ModerationConfig          `yaml:"moderation"`
	MessageDeletion  MessageDeletionConfig     `yaml:"message_deletion"`
	DatabaseCleanup  DatabaseCleanupConfig     `yaml:"database_cleanup"`
	ScheduledEvents  ScheduledEventsConfig     `yaml:"scheduled_events"`
	Debug            DebugConfig               `yaml:"debug"`
	Server           ServerConfig              `yaml:"server"`
	Webhook          WebhookConfig             `yaml:"webhook"`
	UpdateProcessing UpdateProcessingConfig    `yaml:"update_processing"`
	WebUI            WebUIConfig               `yaml:"web_ui"`
	AI               AzureAIConfig             `yaml:"ai"`
	UserProfiles     UserProfilesGeneralConfig `yaml:"user_profiles"`

	// Topics is an optional, static forum-topic-name registry. The Telegram
	// Bot API cannot look up topic names by id, so the bot normally learns them
	// passively from forum-topic service messages. This list lets operators
	// pre-seed names (e.g. for topics created before the bot joined) so they
	// appear in moderation reports immediately. Live-observed names take
	// precedence over these static entries.
	Topics []TopicNameRef `yaml:"topics"`
}

// TopicNameRef maps a specific forum topic (chat + thread id) to a
// human-readable name. Used to pre-seed the topic-name registry from config.
type TopicNameRef struct {
	Chat  int64  `yaml:"chat" json:"chat"`
	Topic int    `yaml:"topic" json:"topic"`
	Name  string `yaml:"name" json:"name"`
}

// UserProfilesGeneralConfig contains non-AI user tracking settings:
// username/display-name history, first-seen-per-chat timestamps, and per-day
// message-count statistics. This is independent from ai.user_profiles (the
// AI-generated behavior profiles).
//
// All tracking happens on the message hot path; there is no daily schedule of
// its own. Stale rows in user_daily_activity are pruned as part of the regular
// database_cleanup interval task.
type UserProfilesGeneralConfig struct {
	Enabled bool `yaml:"enabled"`
	// DisableUsernameReuseAlerts opts out of the admin-chat notification that
	// fires when a newly tracked user_id is using a @username previously held
	// by a different user_id. Profile tracking still occurs; only the alert
	// is suppressed. Default false (alerts enabled) preserves prior behavior.
	DisableUsernameReuseAlerts bool `yaml:"disable_username_reuse_alerts"`
}

// DatabaseConfig contains database connection settings.
// Supported providers: "local" (default) and "remote". When the provider value
// is empty or unrecognised, the provider is auto-detected: if both URL and
// AuthToken are set the database is treated as "remote", otherwise "local".
type DatabaseConfig struct {
	Provider  string `yaml:"provider"`                   // "local" or "remote" (auto-detected when empty/unknown)
	Path      string `yaml:"path"`                       // File path for local SQLite
	URL       string `yaml:"url"`                        // Connection URL for remote providers
	AuthToken string `yaml:"auth_token" web:"sensitive"` // Auth token for remote providers
}

// ReactionsConfig holds emoji overrides for bot reactions.
type ReactionsConfig struct {
	SuspiciousMessage  string `yaml:"suspicious_message"`
	BadMessage         string `yaml:"bad_message"`
	ContentFilter      string `yaml:"content_filter"`
	CreativeReplyLimit string `yaml:"creative_reply_limit"`
	ExtractingLink     string `yaml:"extracting_link"`
	ExtractLinkFailed  string `yaml:"extract_link_failed"`
	UserMuted          string `yaml:"user_muted"`
	ReportAcknowledged string `yaml:"report_acknowledged"`
	CreativeReplyError string `yaml:"creative_reply_error"`
}

// AdminConfig contains admin chat and user settings.
type AdminConfig struct {
	ChatID           int64   `yaml:"chat_id"`
	ReplyMessageIDs  []int   `yaml:"reply_message_ids"`
	SuperAdminUserID int64   `yaml:"super_admin_user_id"`
	NotifySuperAdmin bool    `yaml:"notify_super_admin"`
	NotifyStartup    bool    `yaml:"notify_startup"`
	WhitelistUserIDs []int64 `yaml:"whitelist_user_ids"`
}

// ModerationConfig contains moderation chat settings.
type ModerationConfig struct {
	ChatIDs            ChatIDList    `yaml:"chat_id"` // Supports both single int64 and array
	ExcludedTopics     ChatTopicList `yaml:"excluded_topics"`
	MuteAcrossAllChats bool          `yaml:"mute_across_all_chats"`
}

// MessageDeletionConfig contains automatic message deletion from chat settings.
type MessageDeletionConfig struct {
	Enabled                    bool          `yaml:"enabled"`
	IncludedTopics             ChatTopicList `yaml:"included_topics"`
	ExcludedTopics             ChatTopicList `yaml:"excluded_topics"`
	ExcludedUserIDs            []int64       `yaml:"excluded_user_ids"`
	ChatDeletionRetentionHours int           `yaml:"chat_deletion_retention_hours"`
	CleanupIntervalHours       int           `yaml:"cleanup_interval_hours"`
}

// DatabaseCleanupConfig contains settings for periodic database record purging.
type DatabaseCleanupConfig struct {
	CleanupIntervalHours  int `yaml:"cleanup_interval_hours"`
	MessageRetentionHours int `yaml:"message_retention_hours"`
	WarningRetentionHours int `yaml:"warning_retention_hours"`
	ActionRetentionHours  int `yaml:"action_retention_hours"`
	// PreserveWarnedMutedMessages keeps message_info rows that triggered a
	// warning or an active mute, so they aren't purged by the retention sweep
	// until the related warning is cleaned up or the mute expires/is lifted.
	PreserveWarnedMutedMessages bool `yaml:"preserve_warned_muted_messages"`
}

// ScheduledEventsConfig contains settings for missed scheduled event recovery.
type ScheduledEventsConfig struct {
	MissedEventMaxDelayMinutes int    `yaml:"missed_event_max_delay_minutes"`
	WebhookMode                bool   `yaml:"webhook_mode"`
	WebhookPath                string `yaml:"webhook_path"`
	LockTimeoutMinutes         int    `yaml:"lock_timeout_minutes"`
}

// DebugConfig contains debug logging options.
type DebugConfig struct {
	DebugTelegram     bool `yaml:"debug_telegram"`
	DebugExternalAPIs bool `yaml:"debug_external_apis"`
	DebugAPIErrors    bool `yaml:"debug_api_errors"`
	// TraceTopics enables verbose TRACE logging of forum-topic fields
	// (message_thread_id, reply_to, computed topic) on inbound updates and of
	// the topic targeted on outbound post_to sends. Useful right after a
	// deployment to confirm Telegram populates the fields as expected.
	TraceTopics            bool   `yaml:"trace_topics"`
	DumpModerationMessages bool   `yaml:"dump_moderation_messages"`
	DumpAdminMessages      bool   `yaml:"dump_admin_messages"`
	MessageDumpPath        string `yaml:"message_dump_path"`
	SendToSuperAdmin       bool   `yaml:"send_to_super_admin"`
}

// WebhookConfig contains webhook server settings.
type WebhookConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Debug       bool   `yaml:"debug"`
	SecretToken string `yaml:"secret_token" web:"sensitive"`
	URL         string `yaml:"url"`
}

// UpdateProcessingConfig tunes the worker pool that processes inbound Telegram
// updates. Handlers run synchronously inside each worker, so the moderation
// decision (recording + the AI moderation call) blocks the worker until it
// finishes; the slower side features (link summaries, creative replies, message
// summaries) are already offloaded to a separate background goroutine.
type UpdateProcessingConfig struct {
	// Workers is the number of concurrent update-processing workers draining the
	// Telegram update queue in long-polling mode. Default 1 (in-order processing,
	// matching the bot's historical behaviour). Raise it to run several moderation
	// pipelines at once when a busy chat makes updates back up. Has no effect in
	// webhook mode, where each update is already handled in its own HTTP goroutine.
	Workers int `yaml:"workers"`
	// StatsIntervalSeconds controls how often a worker-pool utilization summary is
	// logged so you can decide whether to change Workers. Defaults to 600 (10 min);
	// the line is only emitted for windows that actually processed updates. Set a
	// negative value to disable the logging entirely.
	StatsIntervalSeconds int `yaml:"stats_interval_seconds"`
}

// WebUIConfig contains settings for the web administration panel.
type WebUIConfig struct {
	Enabled    bool   `yaml:"enabled"`
	PathPrefix string `yaml:"path_prefix"`
	Password   string `yaml:"password" web:"sensitive"`
	OTPEnabled *bool  `yaml:"otp_enabled"`
	// ModeratorPathPrefix is the URL path prefix for the isolated, limited
	// moderator web UI (default: /mod). It must differ from PathPrefix. The
	// moderator surface exposes only moderation/messages/profiles and a
	// read-only diagnostics view; configuration, logs, system and file/DB
	// endpoints are never registered under this prefix.
	ModeratorPathPrefix string `yaml:"moderator_path_prefix"`
	// PublicURL is the externally-reachable base URL of the web UI (e.g.
	// https://bot.example.com), without any path prefix. It is used to build
	// the one-time moderator login link sent over Telegram. When empty, it
	// falls back to the scheme+host of Webhook.URL (if set); the "Access Web
	// UI" button is hidden when no public URL can be resolved.
	PublicURL string `yaml:"public_url"`
}

// IsOTPEnabled returns whether OTP is enabled (defaults to true if not set).
func (w *WebUIConfig) IsOTPEnabled() bool {
	if w.OTPEnabled == nil {
		return true
	}
	return *w.OTPEnabled
}

// ServerConfig contains shared HTTP server settings for webhook and web UI.
type ServerConfig struct {
	ListenAddr      string `yaml:"listen_addr"`
	ListenPort      int    `yaml:"listen_port"`
	CertificatePath string `yaml:"certificate_path"`
}

// AzureAIConfig contains all AI-related settings.
type AzureAIConfig struct {
	Enabled            bool                `yaml:"enabled"`
	ChatRules          string              `yaml:"chat_rules"`
	ChatRulesOverrides []ChatRulesOverride `yaml:"chat_rules_overrides,omitempty"`
	WarningMute        string              `yaml:"warning_mute"`
	// TrackReactions subscribes to message-reaction updates (requires opting in
	// via allowed_updates) and stores per-message emoji→count, surfaced to the
	// daily summary, creative replies and user-profile prompts. Per-user
	// reaction events additionally require the bot to be a chat administrator;
	// anonymous aggregate counts work without admin.
	TrackReactions bool           `yaml:"track_reactions"`
	LightModel     AIModelConfigs `yaml:"light_model"`
	FullModel      AIModelConfigs `yaml:"full_model"`

	ContentModeration ContentModerationConfig `yaml:"content_moderation"`
	CreativeReplies   CreativeRepliesConfig   `yaml:"creative_replies"`
	MorningGreeting   MorningGreetingConfig   `yaml:"morning_greeting"`
	DailySummary      DailySummaryConfig      `yaml:"daily_summary"`
	MessageSummaries  MessageSummariesConfig  `yaml:"message_summaries"`
	LinkSummaries     LinkSummariesConfig     `yaml:"link_summaries"`
	ExternalData      ExternalDataConfig      `yaml:"external_data"`
	Rss               RssConfig               `yaml:"rss"`
	UserProfiles      UserProfilesConfig      `yaml:"user_profiles"`

	TranslationPrompt PromptPair `yaml:"translation_prompt,omitempty"`
}

// ChatRulesOverride appends extra chat-specific rules text to the generic
// AI.ChatRules whenever a moderation/warning prompt is rendered for the given
// chat. Use it to add rules that only apply to one chat without duplicating
// the shared baseline.
type ChatRulesOverride struct {
	Chat  int64  `yaml:"chat" json:"chat"`
	Rules string `yaml:"rules" json:"rules"`
}

// Auto-moderation action types triggered when an LLM-detected rule matches.
const (
	ModerationActionReport = "report" // forward to admin chat for manual decision
	ModerationActionWarn   = "warn"   // auto-warn the user
	ModerationActionMute   = "mute"   // auto-mute the user (duration = ContentModeration.DefaultMuteMinutes)
	ModerationActionDelete = "delete" // auto-delete the message
)

// ModerationRule maps a substring expected in the LLM's moderation output to
// an automatic action. Rules are evaluated in declaration order and every rule
// whose Trigger occurs (case-insensitively) inside the LLM response fires -
// so stacking e.g. {warn, report} on the same trigger runs both actions.
type ModerationRule struct {
	Trigger     string `yaml:"trigger" json:"trigger"`                               // substring to look for in LLM output (case-insensitive)
	Action      string `yaml:"action" json:"action"`                                 // one of: report, warn, mute, delete
	Description string `yaml:"description,omitempty" json:"description,omitempty"`   // optional human-readable note shown in UI / logs
	NotifyAdmin *bool  `yaml:"notify_admin,omitempty" json:"notify_admin,omitempty"` // whether to post a notice to the admin chat (default: true)
}

// IsNotifyAdmin reports whether this rule should send an admin-chat notice when
// triggered. Defaults to true when the field is unset.
func (r ModerationRule) IsNotifyAdmin() bool {
	if r.NotifyAdmin == nil {
		return true
	}
	return *r.NotifyAdmin
}

// ContentModerationConfig contains AI content moderation settings.
type ContentModerationConfig struct {
	Enabled        bool `yaml:"enabled"`
	SkipAdminUsers bool `yaml:"skip_admin_users"`
	// ComplaintManualModeration controls what happens when a user reports a
	// message by replying to it and mentioning the bot in a moderation chat.
	// The bot always first re-runs AI moderation across every distinct
	// configured model (like the WebUI "moderate again" action) and acts on the
	// message automatically if any model flags it. When this is true (the
	// default) and every model clears the message, the bot then falls back to
	// manual moderation by posting the admin decision card. When false, a clean
	// cross-model verdict ends the complaint silently without bothering the
	// admins. Defaults to true.
	ComplaintManualModeration *bool  `yaml:"complaint_manual_moderation"`
	DefaultMuteMinutes        int    `yaml:"default_mute_minutes"` // duration applied by the "mute" auto-action; 0 = forever
	VisionEnabled             bool   `yaml:"vision_enabled"`
	VisionEndpoint            string `yaml:"vision_endpoint"`
	VisionAPIKey              string `yaml:"vision_api_key" web:"sensitive"`
	ContentSafetyEnabled      bool   `yaml:"content_safety_enabled"`
	ContentSafetyEndpoint     string `yaml:"content_safety_endpoint"`
	ContentSafetyAPIKey       string `yaml:"content_safety_api_key" web:"sensitive"`
	// NewUserProfileCheckEnabled runs a one-shot screening of a new member's
	// whole public profile on their first message in a moderation chat: their
	// name, bio and profile photo, plus their linked personal channel (title,
	// description, photo) when present. Photos are screened with Azure Content
	// Safety first and only described via Vision / OCR.space when Content Safety
	// is unavailable or fails; all the gathered text is judged by the
	// NewUserProfilePrompt AI prompt. Findings are appended to the user's
	// profile before AI moderation runs. It does not require content_safety_enabled.
	NewUserProfileCheckEnabled bool `yaml:"new_user_profile_check_enabled"`
	// NewUserProfileUseFullModel makes the new-member profile screening judge
	// the gathered profile text with the full model instead of the light model.
	// The full model is better at spotting subtle spam/scam/promo cues in a
	// profile's name, bio and channel text, at a higher per-call cost. Off by
	// default (uses the light model). Only affects the AI text verdict; photo
	// screening still goes through Content Safety / Vision / OCR.space.
	NewUserProfileUseFullModel bool       `yaml:"new_user_profile_use_full_model"`
	NewUserProfilePrompt       PromptPair `yaml:"new_user_profile_prompt"`
	// NewUserWindowHours defines how long after a user's first observed message
	// they are still considered "new" for moderation purposes. A new user gets
	// a prominent marker injected into their {{user_profile}} / {{user_reputation}}
	// moderation context, and the {{new_user_rules}} placeholder (below) expands
	// to NewUserRules for them. Defaults to 24 when unset (<= 0).
	NewUserWindowHours int `yaml:"new_user_window_hours"`
	// NewUserRules is extra rules text injected into the {{new_user_rules}}
	// moderation-prompt placeholder, but only when the message being moderated
	// is from a new user (see NewUserWindowHours). Empty by default - the
	// placeholder expands to nothing until configured.
	NewUserRules string `yaml:"new_user_rules"`
	// FullModelFirstMessages double-checks a user's first N messages in a
	// moderation chat with the full model even when the light model found
	// nothing. Moderation normally runs the cheap light model first and only
	// escalates to the full model when the light model flags something; this
	// catches subtle spam from brand-new members that the light model may miss,
	// at the cost of one extra full-model call for each of a user's first N
	// messages. Counted per user per chat from their recorded message history.
	// 0 (default) disables the double-check.
	FullModelFirstMessages int `yaml:"full_model_first_messages"`
	// ReplyContextMaxChars caps the length (in UTF-8 runes, not bytes) of the
	// quoted "in reply to" text injected into the moderation prompt's
	// {{reply_to}} placeholder. Longer parent/quoted messages are truncated
	// with an ellipsis so an oversized quoted message can't bloat the prompt.
	// <= 0 disables truncation. Defaults to 500.
	ReplyContextMaxChars int              `yaml:"reply_context_max_chars"`
	OCRSpaceEnabled      bool             `yaml:"ocrspace_enabled"`
	OCRSpaceAPIKey       string           `yaml:"ocrspace_api_key" web:"sensitive"`
	OCRSpaceURL          string           `yaml:"ocrspace_url"`
	OCRSpaceLanguage     string           `yaml:"ocrspace_language"`
	OCRSpaceEngine       int              `yaml:"ocrspace_engine"`
	Prompt               PromptPair       `yaml:"prompt"`
	WarningPrompt        PromptPair       `yaml:"warning_prompt"`
	Rules                []ModerationRule `yaml:"rules,omitempty"`
}

// IsComplaintManualModeration reports whether a user complaint that survives
// cross-model re-moderation should fall back to manual moderation. Defaults to
// true when unset.
func (c *ContentModerationConfig) IsComplaintManualModeration() bool {
	if c.ComplaintManualModeration == nil {
		return true
	}
	return *c.ComplaintManualModeration
}

// CreativeRepliesConfig contains creative AI reply settings.
type CreativeRepliesConfig struct {
	Enabled                  bool          `yaml:"enabled"`
	UseFullModel             bool          `yaml:"use_full_model"`
	MaxMessages              int           `yaml:"max_messages"`
	TimeWindow               int           `yaml:"time_window"`
	IncludedTopics           ChatTopicList `yaml:"included_topics"`
	ExcludedTopics           ChatTopicList `yaml:"excluded_topics"`
	FollowUpOnlySameUser     bool          `yaml:"follow_up_only_same_user"`
	ReplyChainDepth          int           `yaml:"reply_chain_depth"`
	ReplyChainMaxAgeHours    int           `yaml:"reply_chain_max_age_hours"`
	ReplyChainAdjacentWindow int           `yaml:"reply_chain_adjacent_window"`
	Prompt                   PromptPair    `yaml:"prompt"`
}

// MorningGreetingConfig contains morning greeting settings.
type MorningGreetingConfig struct {
	Enabled      bool          `yaml:"enabled"`
	UseAI        *bool         `yaml:"use_ai"`
	UseFullModel bool          `yaml:"use_full_model"`
	Time         string        `yaml:"time"`
	PostTo       ChatTopicList `yaml:"post_to"`
	Prompt       PromptPair    `yaml:"prompt"`
}

// IsUseAI returns whether AI is used for morning greeting (defaults to true if not set).
func (c *MorningGreetingConfig) IsUseAI() bool {
	if c.UseAI == nil {
		return true
	}
	return *c.UseAI
}

// DailySummaryConfig contains daily summary settings.
type DailySummaryConfig struct {
	Enabled      bool          `yaml:"enabled"`
	Time         string        `yaml:"time"`
	UseFullModel bool          `yaml:"use_full_model"`
	PostTo       ChatTopicList `yaml:"post_to"`
	Prompt       PromptPair    `yaml:"prompt"`
}

// MessageSummariesConfig contains long message summary settings.
type MessageSummariesConfig struct {
	Enabled             bool          `yaml:"enabled"`
	UseFullModel        bool          `yaml:"use_full_model"`
	LightModelThreshold int           `yaml:"light_model_threshold"`
	MinLength           int           `yaml:"min_length"`
	IncludedTopics      ChatTopicList `yaml:"included_topics"`
	ExcludedTopics      ChatTopicList `yaml:"excluded_topics"`
	ExcludedUserIDs     []int64       `yaml:"excluded_user_ids"`
	Prompt              PromptPair    `yaml:"prompt"`
}

// LinkSummariesConfig contains link summarization settings.
type LinkSummariesConfig struct {
	Enabled                   bool          `yaml:"enabled"`
	UseFullModel              bool          `yaml:"use_full_model"`
	LightModelThreshold       int           `yaml:"light_model_threshold"`
	ExcludedDomains           []string      `yaml:"excluded_domains"`
	ExcludedExtensions        []string      `yaml:"excluded_extensions"`
	ExcludedUserIDs           []int64       `yaml:"excluded_user_ids"`
	IncludedTopics            ChatTopicList `yaml:"included_topics"`
	ExcludedTopics            ChatTopicList `yaml:"excluded_topics"`
	ExtractorAPIKey           string        `yaml:"extractor_api_key" web:"sensitive"`
	DiffbotAPIKey             string        `yaml:"diffbot_api_key" web:"sensitive"`
	CloudflareAccountID       string        `yaml:"cloudflare_account_id"`
	CloudflareAPIToken        string        `yaml:"cloudflare_api_token" web:"sensitive"`
	Cookies                   string        `yaml:"cookies" web:"sensitive"`
	UserAgent                 string        `yaml:"user_agent"`
	ContentLanguage           string        `yaml:"content_language"`
	MaxExtractedContentLength int           `yaml:"max_extracted_content_length"`
	MaxDownloadSizeBytes      int           `yaml:"max_download_size_bytes"`
	MinSummaryLength          int           `yaml:"min_summary_length"`
	Prompt                    PromptPair    `yaml:"prompt"`
}

// ExternalDataConfig contains external data source settings.
type ExternalDataConfig struct {
	WeatherLatitude    float64 `yaml:"weather_latitude"`
	WeatherLongitude   float64 `yaml:"weather_longitude"`
	HolidaysCountry    string  `yaml:"holidays_country"`
	WikipediaLanguage  string  `yaml:"wikipedia_language"`
	TranslateWikipedia *bool   `yaml:"translate_wikipedia"`
}

// UserProfilesConfig contains AI user profiling settings.
type UserProfilesConfig struct {
	Enabled      bool       `yaml:"enabled"`
	Time         string     `yaml:"time"` // Daily run time (HH:MM) for the AI profile update task
	Prompt       PromptPair `yaml:"prompt"`
	UpdatePrompt PromptPair `yaml:"update_prompt"`
	// SkipForeverMutedUsers, when true, skips generating/updating AI profiles
	// for users who currently have a permanent ("forever") mute in any
	// moderation chat - there's little value profiling permanently silenced users.
	SkipForeverMutedUsers bool `yaml:"skip_forever_muted_users"`
}

// RssConfig contains RSS feed monitoring settings and associated AI prompts.
type RssConfig struct {
	UseFullModel        bool       `yaml:"use_full_model"`
	LightModelThreshold int        `yaml:"light_model_threshold"`
	Feeds               []RssFeed  `yaml:"feeds"`
	TranslationPrompt   PromptPair `yaml:"translation_prompt,omitempty"`
	SummaryPrompt       PromptPair `yaml:"summary_prompt,omitempty"`
}

// RssFeed represents a single RSS feed to monitor.
// RssFeed configures a single RSS feed source.
//
// PostTo selects where items are published. When empty, items are broadcast
// to every moderation chat in its main area.
type RssFeed struct {
	Name             string        `yaml:"name" json:"name"`
	URL              string        `yaml:"url" json:"url"`
	Time             string        `yaml:"time" json:"time"`
	Enabled          bool          `yaml:"enabled" json:"enabled"`
	Translate        *bool         `yaml:"translate" json:"translate"`
	SummarizeIfLong  *bool         `yaml:"summarize_if_long" json:"summarize_if_long"`
	PostTo           ChatTopicList `yaml:"post_to" json:"post_to"`
	MaxMessageLength int           `yaml:"max_message_length" json:"max_message_length"`
}

// IsTranslate returns whether items should be translated via AI before publishing
// (defaults to true if not set). When false, the original text is used as the body.
func (f *RssFeed) IsTranslate() bool {
	if f.Translate == nil {
		return true
	}
	return *f.Translate
}

// IsSummarizeIfLong returns whether to ask the AI to summarize the body when it exceeds
// MaxMessageLength (defaults to true if not set). When false, the body is hard-truncated.
func (f *RssFeed) IsSummarizeIfLong() bool {
	if f.SummarizeIfLong == nil {
		return true
	}
	return *f.SummarizeIfLong
}

// PromptPair contains system and user prompts for a single AI task.
type PromptPair struct {
	System string `yaml:"system"`
	User   string `yaml:"user"`
}
