# Configuration Reference (EN)

This file is auto-generated. Do not edit manually.

## General

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `bot_token` | `BOT_TOKEN` | string 🔒 | Bot Token - Telegram Bot API token obtained from @BotFather |
| `proxy_url` | `PROXY_URL` | string | Proxy URL - HTTP/SOCKS5 proxy URL (optional) |
| `language` | `LANGUAGE` | string | Language - Bot interface language: ru or en |

## Database

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `database.provider` | `DATABASE_PROVIDER` | string | Provider - Database provider: local or remote (default: local). Leave empty to auto-detect (remote if url and auth_token are set, otherwise local) |
| `database.path` | `DATABASE_PATH` | string | Database Path - File path to the local SQLite database |
| `database.url` | `DATABASE_URL` | string | Connection URL - Remote database connection URL (for the remote provider) |
| `database.auth_token` | `DATABASE_AUTH_TOKEN` | string 🔒 | Auth Token - Authentication token for remote database providers |

## Reactions

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `reactions.suspicious_message` | `REACTIONS_SUSPICIOUS_MESSAGE` | string | Suspicious Message - Emoji reaction for suspicious messages (default: 🤔) |
| `reactions.bad_message` | `REACTIONS_BAD_MESSAGE` | string | Bad Message - Emoji reaction for confirmed bad messages (default: 🍌) |
| `reactions.content_filter` | `REACTIONS_CONTENT_FILTER` | string | Content Filter - Emoji reaction when content filter is triggered (default: 🥴) |
| `reactions.creative_reply_limit` | `REACTIONS_CREATIVE_REPLY_LIMIT` | string | Creative Reply Limit - Emoji reaction when creative reply limit is reached (default: 🥱) |
| `reactions.extracting_link` | `REACTIONS_EXTRACTING_LINK` | string | Extracting Link - Emoji reaction while extracting link content (default: ✍) |
| `reactions.extract_link_failed` | `REACTIONS_EXTRACT_LINK_FAILED` | string | Extract Link Failed - Emoji reaction when link extraction fails (default: 🌚) |
| `reactions.user_muted` | `REACTIONS_USER_MUTED` | string | User Muted - Emoji reaction when a user is muted (default: 🤮) |
| `reactions.report_acknowledged` | `REACTIONS_REPORT_ACKNOWLEDGED` | string | Report Acknowledged - Emoji reaction when report is acknowledged (default: 👌) |
| `reactions.creative_reply_error` | `REACTIONS_CREATIVE_REPLY_ERROR` | string | Creative Reply Error - Emoji reaction when creative reply generation fails (default: 😐) |

## Admin

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `admin.chat_id` | `ADMIN_CHAT_ID` | int64 | Admin Chat ID - Telegram chat ID for admin notifications |
| `admin.reply_message_ids` | `ADMIN_REPLY_MESSAGE_IDS` | []int | Reply Message IDs - Message IDs in admin chat to reply to (for topic-based chats) |
| `admin.super_admin_user_id` | `ADMIN_SUPER_ADMIN_USER_ID` | int64 | Super Admin User ID - Primary super admin's Telegram user ID for direct commands |
| `admin.notify_super_admin` | `ADMIN_NOTIFY_SUPER_ADMIN` | bool | Notify Super-Admin - Also send moderation notifications (with action keyboard) to super-admin's DM |
| `admin.notify_startup` | `ADMIN_NOTIFY_STARTUP` | bool | Notify on Startup - DM the super-admin when the bot starts or restarts, and when the remote DB connection drops or recovers |
| `admin.whitelist_user_ids` | `ADMIN_WHITELIST_USER_IDS` | []int64 | Whitelist User IDs - User IDs that bypass content filters and have admin access |

## Moderation

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `moderation.chat_id` | `MODERATION_CHAT_ID` | []int64 | Moderation Chat IDs - Telegram chat IDs to moderate (single or array) |
| `moderation.excluded_topics` | `MODERATION_EXCLUDED_TOPICS` | []chat_topic | Excluded Topics - (chat, topic) pairs excluded from AI content analysis. Use 'Any topic' for the wildcard, 'Main only' for the chat's main area, or a specific forum thread id. |
| `moderation.mute_across_all_chats` | `MODERATION_MUTE_ACROSS_ALL_CHATS` | bool | Mute Across All Chats - When muting a user, also mute in all other moderation chats |

## Message Deletion

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `message_deletion.enabled` | `MESSAGE_DELETION_ENABLED` | bool | Enabled - Enable automatic message deletion |
| `message_deletion.included_topics` | `MESSAGE_DELETION_INCLUDED_TOPICS` | []chat_topic | Included Topics - (chat, topic) pairs where messages are eligible for auto-deletion. Empty = every moderation chat / any topic. Use 'Main only' to limit to the main area. |
| `message_deletion.excluded_topics` | `MESSAGE_DELETION_EXCLUDED_TOPICS` | []chat_topic | Excluded Topics - (chat, topic) pairs that override Included Topics - messages here are never auto-deleted. |
| `message_deletion.excluded_user_ids` | `MESSAGE_DELETION_EXCLUDED_USER_IDS` | []int64 | Excluded User IDs - User IDs whose messages are never deleted |
| `message_deletion.chat_deletion_retention_hours` | `MESSAGE_DELETION_CHAT_DELETION_RETENTION_HOURS` | int | Retention Hours - Delete messages older than this many hours (default: 3) |
| `message_deletion.cleanup_interval_hours` | `MESSAGE_DELETION_CLEANUP_INTERVAL_HOURS` | int | Cleanup Interval Hours - How often to run the deletion cleanup (default: 3) |

## Database Cleanup

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `database_cleanup.cleanup_interval_hours` | `DATABASE_CLEANUP_CLEANUP_INTERVAL_HOURS` | int | Cleanup Interval Hours - How often to run database cleanup in hours (0 = disabled, default: 24) |
| `database_cleanup.message_retention_hours` | `DATABASE_CLEANUP_MESSAGE_RETENTION_HOURS` | int | Message Retention Hours - Keep message records for this many hours (default: 168 = 7 days) |
| `database_cleanup.warning_retention_hours` | `DATABASE_CLEANUP_WARNING_RETENTION_HOURS` | int | Warning Retention Hours - Keep warning records for this many hours (default: 168) |
| `database_cleanup.action_retention_hours` | `DATABASE_CLEANUP_ACTION_RETENTION_HOURS` | int | Action Retention Hours - Keep action log records for this many hours (default: 168) |
| `database_cleanup.preserve_warned_muted_messages` | `DATABASE_CLEANUP_PRESERVE_WARNED_MUTED_MESSAGES` | bool | Preserve Warned/Muted Messages - Keep messages that triggered a warning or an active mute until it is cleared or expires (default: off) |

## Scheduled Events

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `scheduled_events.missed_event_max_delay_minutes` | `SCHEDULED_EVENTS_MISSED_EVENT_MAX_DELAY_MINUTES` | int | Missed Event Max Delay - Maximum delay in minutes to still fire a missed scheduled event after bot restart (default: 120) |
| `scheduled_events.webhook_mode` | `SCHEDULED_EVENTS_WEBHOOK_MODE` | bool | Webhook Mode - When enabled, scheduled events run only when triggered via webhook (not automatically) |
| `scheduled_events.webhook_path` | `SCHEDULED_EVENTS_WEBHOOK_PATH` | string | Webhook Path - URL path for the scheduled events webhook trigger endpoint (default: /trigger-events) |
| `scheduled_events.lock_timeout_minutes` | `SCHEDULED_EVENTS_LOCK_TIMEOUT_MINUTES` | int | Lock Timeout - Minutes before a stale scheduled event lock is considered expired and can be reclaimed (default: 15) |

## Debug

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `debug.debug_telegram` | `DEBUG_DEBUG_TELEGRAM` | bool | Debug Telegram - Log detailed Telegram communication (updates, commands, callbacks) |
| `debug.debug_external_apis` | `DEBUG_DEBUG_EXTERNAL_APIS` | bool | Debug External APIs - Log all external API requests and responses |
| `debug.debug_api_errors` | `DEBUG_DEBUG_API_ERRORS` | bool | Debug API Errors - Log only failed API requests with error codes and trimmed response body |
| `debug.trace_topics` | `DEBUG_TRACE_TOPICS` | bool | Trace Topics - TRACE-log forum-topic fields (message_thread_id, reply_to) on inbound updates and outbound posts; use after deploy to verify topic handling |
| `debug.dump_moderation_messages` | `DEBUG_DUMP_MODERATION_MESSAGES` | bool | Dump Moderation Messages - Dump all moderation messages to files |
| `debug.dump_admin_messages` | `DEBUG_DUMP_ADMIN_MESSAGES` | bool | Dump Admin Messages - Dump all admin messages to files |
| `debug.message_dump_path` | `DEBUG_MESSAGE_DUMP_PATH` | string | Message Dump Path - Directory path for message dump files (default: ./logs) |
| `debug.send_to_super_admin` | `DEBUG_SEND_TO_SUPER_ADMIN` | bool | Send Debug to Super-Admin - Send enabled debug logs (API errors, chat dumps, etc.) to super-admin Telegram user |

## Server

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `server.listen_addr` | `SERVER_LISTEN_ADDR` | string | Listen Address - Address to listen on for HTTP server (default: 0.0.0.0) |
| `server.listen_port` | `SERVER_LISTEN_PORT` | int | Listen Port - Port to listen on for HTTP server (default: 8080) |
| `server.certificate_path` | `SERVER_CERTIFICATE_PATH` | string | Certificate Path - Path to TLS certificate file for self-signed certificates |

## Webhook

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `webhook.enabled` | `WEBHOOK_ENABLED` | bool | Enabled - Use webhook mode instead of long-polling |
| `webhook.debug` | `WEBHOOK_DEBUG` | bool | Debug - Enable webhook debug logging |
| `webhook.secret_token` | `WEBHOOK_SECRET_TOKEN` | string 🔒 | Secret Token - Secret token for webhook validation |
| `webhook.url` | `WEBHOOK_URL` | string | URL - Public HTTPS URL of the webhook |

## Web UI

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `web_ui.enabled` | `WEB_UI_ENABLED` | bool | Enabled - Enable the web administration panel |
| `web_ui.path_prefix` | `WEB_UI_PATH_PREFIX` | string | Path Prefix - URL path prefix for the web UI (default: /admin) |
| `web_ui.password` | `WEB_UI_PASSWORD` | string 🔒 | Password - Password for web UI authentication. Plaintext is accepted in YAML; DB-backed config stores it as hashed:pbkdf2-sha256:... |
| `web_ui.otp_enabled` | `WEB_UI_OTP_ENABLED` | bool | OTP Enabled - Enable one-time password (TOTP) for web UI login (default: true) |

## AI General

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.enabled` | `AI_ENABLED` | bool | AI Enabled - Enable AI-powered features (content moderation, summaries, etc.) |
| `ai.chat_rules` | `AI_CHAT_RULES` | string | Chat Rules - Chat rules text used in AI prompts for content moderation |
| `ai.warning_mute` | `AI_WARNING_MUTE` | string | Warning Mute Text - Mute info text added to the warning prompt via {{mute_info}} placeholder if user is muted |
| `ai.track_reactions` | `AI_TRACK_REACTIONS` | bool | Track Reactions - Store per-message emoji reactions and feed them into AI context for summaries, creative replies and user profiles. Aggregate counts work in any chat; per-user events also need the bot to be a chat admin |
| `ai.translation_prompt.system` | `AI_TRANSLATION_PROMPT_SYSTEM` | string | Translation (System) - System prompt for general translation (links, Wikipedia events) (placeholders: `{{text}}`) |
| `ai.translation_prompt.user` | `AI_TRANSLATION_PROMPT_USER` | string | Translation (User) - User prompt for general translation (links, Wikipedia events) (placeholders: `{{text}}`) |

## AI Content Moderation

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.content_moderation.enabled` | `AI_CONTENT_MODERATION_ENABLED` | bool | Enabled - Enable AI content moderation |
| `ai.content_moderation.skip_admin_users` | `AI_CONTENT_MODERATION_SKIP_ADMIN_USERS` | bool | Skip Admin Users - Skip content moderation for admin users |
| `ai.content_moderation.complaint_manual_moderation` | `AI_CONTENT_MODERATION_COMPLAINT_MANUAL_MODERATION` | bool | Manual Moderation On Complaint - When a user reports a message (reply + @bot mention) the bot first re-runs AI moderation across every configured model and acts automatically if any flags it. If every model clears it: ON (default) posts the admin decision card for manual review; OFF ends the complaint silently. |
| `ai.content_moderation.default_mute_minutes` | `AI_CONTENT_MODERATION_DEFAULT_MUTE_MINUTES` | int | Auto-Mute Duration (min) - Duration applied by the 'mute' auto-moderation action (default: 60, 0 = forever) |
| `ai.content_moderation.vision_enabled` | `AI_CONTENT_MODERATION_VISION_ENABLED` | bool | Vision Enabled - Enable Azure Vision API for image analysis |
| `ai.content_moderation.vision_endpoint` | `AI_CONTENT_MODERATION_VISION_ENDPOINT` | string | Vision Endpoint - Azure Vision API endpoint URL |
| `ai.content_moderation.vision_api_key` | `AI_CONTENT_MODERATION_VISION_API_KEY` | string 🔒 | Vision API Key - API key for Azure Vision |
| `ai.content_moderation.content_safety_enabled` | `AI_CONTENT_MODERATION_CONTENT_SAFETY_ENABLED` | bool | Content Safety Enabled - Enable Azure Content Safety API |
| `ai.content_moderation.content_safety_endpoint` | `AI_CONTENT_MODERATION_CONTENT_SAFETY_ENDPOINT` | string | Content Safety Endpoint - Azure Content Safety API endpoint URL |
| `ai.content_moderation.content_safety_api_key` | `AI_CONTENT_MODERATION_CONTENT_SAFETY_API_KEY` | string 🔒 | Content Safety API Key - API key for Azure Content Safety |
| `ai.content_moderation.new_user_profile_check_enabled` | `AI_CONTENT_MODERATION_NEW_USER_PROFILE_CHECK_ENABLED` | bool | New Member Profile Check - On a new user's first message, screen their whole public profile (name, bio, profile photo, and linked personal channel name/description/photo) with AI and image analysis (Content Safety → Vision → OCR.space), adding a notice to their profile if flagged. Works without Content Safety. |
| `ai.content_moderation.new_user_profile_prompt.system` | `AI_CONTENT_MODERATION_NEW_USER_PROFILE_PROMPT_SYSTEM` | string | New Member Profile Check (System) - System prompt for screening a new member's name, bio, photo and personal channel |
| `ai.content_moderation.new_user_profile_prompt.user` | `AI_CONTENT_MODERATION_NEW_USER_PROFILE_PROMPT_USER` | string | New Member Profile Check (User) - User prompt for screening a new member's profile (placeholders: `{{profile_text}}`) |
| `ai.content_moderation.new_user_window_hours` | `AI_CONTENT_MODERATION_NEW_USER_WINDOW_HOURS` | int | New-User Window (hours) - How long after a user's first observed message they count as 'new'. New users get a marker in the moderation context and trigger the {{new_user_rules}} placeholder (default: 24). |
| `ai.content_moderation.new_user_rules` | `AI_CONTENT_MODERATION_NEW_USER_RULES` | string | New-User Rules - Extra rules text injected into the {{new_user_rules}} moderation-prompt placeholder, only for messages from new users (see New-User Window). Empty = the placeholder expands to nothing. |
| `ai.content_moderation.reply_context_max_chars` | `AI_CONTENT_MODERATION_REPLY_CONTEXT_MAX_CHARS` | int | Reply Context Max Chars - Max length (in characters) of the quoted 'in reply to' text injected into the moderation {{reply_to}} placeholder. Longer quotes are truncated with an ellipsis (never splits a character). 0 = no limit (default: 500). |
| `ai.content_moderation.ocrspace_enabled` | `AI_CONTENT_MODERATION_OCRSPACE_ENABLED` | bool | OCR.space Enabled - Enable the OCR.space cloud API (https://ocr.space/ocrapi) for text extraction from images |
| `ai.content_moderation.ocrspace_api_key` | `AI_CONTENT_MODERATION_OCRSPACE_API_KEY` | string 🔒 | OCR.space API Key - API key for OCR.space (free tier available; test key: helloworld) |
| `ai.content_moderation.ocrspace_url` | `AI_CONTENT_MODERATION_OCRSPACE_URL` | string | OCR.space URL - OCR.space endpoint, default https://api.ocr.space/parse/image |
| `ai.content_moderation.ocrspace_language` | `AI_CONTENT_MODERATION_OCRSPACE_LANGUAGE` | string | OCR.space Language - 3-letter language code (e.g. eng, rus, cze); engines 2/3 also accept 'auto' |
| `ai.content_moderation.ocrspace_engine` | `AI_CONTENT_MODERATION_OCRSPACE_ENGINE` | int | OCR.space Engine - OCR engine: 1 (default), 2 (best all-round, memes/noisy backgrounds), 3 (highest accuracy, handwriting, 200+ languages) |
| `ai.content_moderation.prompt.system` | `AI_CONTENT_MODERATION_PROMPT_SYSTEM` | string | Content Moderation (System) - System prompt for content moderation (placeholders: `{{message}}`, `{{chat_rules}}`, `{{user_profile}}`, `{{user_reputation}}`, `{{reply_to}}`, `{{new_user_rules}}`) |
| `ai.content_moderation.prompt.user` | `AI_CONTENT_MODERATION_PROMPT_USER` | string | Content Moderation (User) - User prompt for content moderation (placeholders: `{{message}}`, `{{chat_rules}}`, `{{user_profile}}`, `{{user_reputation}}`, `{{reply_to}}`, `{{new_user_rules}}`) |
| `ai.content_moderation.warning_prompt.system` | `AI_CONTENT_MODERATION_WARNING_PROMPT_SYSTEM` | string | Warning (System) - System prompt for warning message generation (placeholders: `{{username}}`, `{{user_message}}`, `{{chat_rules}}`, `{{mute_info}}`, `{{reputation}}`) |
| `ai.content_moderation.warning_prompt.user` | `AI_CONTENT_MODERATION_WARNING_PROMPT_USER` | string | Warning (User) - User prompt for warning message generation (placeholders: `{{username}}`, `{{user_message}}`, `{{chat_rules}}`, `{{mute_info}}`, `{{reputation}}`) |

## AI Creative Replies

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.creative_replies.enabled` | `AI_CREATIVE_REPLIES_ENABLED` | bool | Enabled - Enable AI creative replies to user messages |
| `ai.creative_replies.use_full_model` | `AI_CREATIVE_REPLIES_USE_FULL_MODEL` | bool | Use Full Model - Use full model instead of light for creative replies |
| `ai.creative_replies.max_messages` | `AI_CREATIVE_REPLIES_MAX_MESSAGES` | int | Max Messages - Max creative replies per time window (default: 3) |
| `ai.creative_replies.time_window` | `AI_CREATIVE_REPLIES_TIME_WINDOW` | int | Time Window (hours) - Time window in hours for rate limiting (default: 3) |
| `ai.creative_replies.included_topics` | `AI_CREATIVE_REPLIES_INCLUDED_TOPICS` | []chat_topic | Included Topics - (chat, topic) pairs where creative replies are allowed. Empty = every moderation chat / any topic. Use 'Any topic' to enable a whole chat, or 'Main only' for the main area. |
| `ai.creative_replies.excluded_topics` | `AI_CREATIVE_REPLIES_EXCLUDED_TOPICS` | []chat_topic | Excluded Topics - (chat, topic) pairs where creative replies are suppressed even when otherwise included. |
| `ai.creative_replies.follow_up_only_same_user` | `AI_CREATIVE_REPLIES_FOLLOW_UP_ONLY_SAME_USER` | bool | Follow-up Only Same User - Only reply creatively to the same user in a follow-up |
| `ai.creative_replies.reply_chain_depth` | `AI_CREATIVE_REPLIES_REPLY_CHAIN_DEPTH` | int | Reply Chain Depth - Max messages to follow up the reply chain for dialog history context (default: 5) |
| `ai.creative_replies.reply_chain_max_age_hours` | `AI_CREATIVE_REPLIES_REPLY_CHAIN_MAX_AGE_HOURS` | int | Reply Chain Max Age (hours) - Stop walking the reply chain once a message is older than this many hours (default: 6) |
| `ai.creative_replies.reply_chain_adjacent_window` | `AI_CREATIVE_REPLIES_REPLY_CHAIN_ADJACENT_WINDOW` | int | Reply Chain Adjacent Window - Pick up other messages from chain participants whose message ID lies within this many slots around chain messages (0 = disabled). Age is bounded by reply_chain_max_age_hours. |
| `ai.creative_replies.prompt.system` | `AI_CREATIVE_REPLIES_PROMPT_SYSTEM` | string | Creative Reply (System) - System prompt for creative reply generation (placeholders: `{{message}}`, `{{context}}`, `{{quote}}`) |
| `ai.creative_replies.prompt.user` | `AI_CREATIVE_REPLIES_PROMPT_USER` | string | Creative Reply (User) - User prompt for creative reply generation (placeholders: `{{message}}`, `{{context}}`, `{{quote}}`) |

## AI Morning Greeting

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.morning_greeting.enabled` | `AI_MORNING_GREETING_ENABLED` | bool | Enabled - Enable daily morning greeting |
| `ai.morning_greeting.use_ai` | `AI_MORNING_GREETING_USE_AI` | bool | Use AI - Use AI to generate greeting (if disabled, shows holidays, events and weather only) |
| `ai.morning_greeting.use_full_model` | `AI_MORNING_GREETING_USE_FULL_MODEL` | bool | Use Full Model - Use full model instead of light for morning greeting |
| `ai.morning_greeting.time` | `AI_MORNING_GREETING_TIME` | string | Time - Time for morning greeting (HH:MM format, default: 08:00) |
| `ai.morning_greeting.post_to` | `AI_MORNING_GREETING_POST_TO` | []chat_topic | Post To - (chat, topic) destinations for the greeting. Leave empty to post to every moderation chat in its main area. |
| `ai.morning_greeting.prompt.system` | `AI_MORNING_GREETING_PROMPT_SYSTEM` | string | Morning Greeting (System) - System prompt for morning greeting generation (placeholders: `{{weekday}}`, `{{date}}`, `{{weather}}`, `{{holidays}}`, `{{events}}`) |
| `ai.morning_greeting.prompt.user` | `AI_MORNING_GREETING_PROMPT_USER` | string | Morning Greeting (User) - User prompt for morning greeting generation (placeholders: `{{weekday}}`, `{{date}}`, `{{weather}}`, `{{holidays}}`, `{{events}}`) |

## AI Daily Summary

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.daily_summary.enabled` | `AI_DAILY_SUMMARY_ENABLED` | bool | Enabled - Enable daily chat summary |
| `ai.daily_summary.time` | `AI_DAILY_SUMMARY_TIME` | string | Time - Time for daily summary (HH:MM format, default: 02:00) |
| `ai.daily_summary.use_full_model` | `AI_DAILY_SUMMARY_USE_FULL_MODEL` | bool | Use Full Model - Use full model instead of light for daily summary |
| `ai.daily_summary.post_to` | `AI_DAILY_SUMMARY_POST_TO` | []chat_topic | Post To - (chat, topic) destinations for the summary. Empty = post to every moderation chat in its main area. Listing the same chat with several topics re-posts the same text in each topic. |
| `ai.daily_summary.prompt.system` | `AI_DAILY_SUMMARY_PROMPT_SYSTEM` | string | Daily Summary (System) - System prompt for daily summary generation (placeholders: `{{messages}}`) |
| `ai.daily_summary.prompt.user` | `AI_DAILY_SUMMARY_PROMPT_USER` | string | Daily Summary (User) - User prompt for daily summary generation (placeholders: `{{messages}}`) |

## AI Message Summaries

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.message_summaries.enabled` | `AI_MESSAGE_SUMMARIES_ENABLED` | bool | Enabled - Enable AI summaries for long messages |
| `ai.message_summaries.use_full_model` | `AI_MESSAGE_SUMMARIES_USE_FULL_MODEL` | bool | Use Full Model - Use full model instead of light for message summaries |
| `ai.message_summaries.light_model_threshold` | `AI_MESSAGE_SUMMARIES_LIGHT_MODEL_THRESHOLD` | int | Light Model Threshold - Force light model when text exceeds this character count (0 = disabled) |
| `ai.message_summaries.min_length` | `AI_MESSAGE_SUMMARIES_MIN_LENGTH` | int | Min Length - Minimum message length for summary (default: 1000) |
| `ai.message_summaries.included_topics` | `AI_MESSAGE_SUMMARIES_INCLUDED_TOPICS` | []chat_topic | Included Topics - (chat, topic) pairs where long-message summarization runs. Empty = every moderation chat / any topic. |
| `ai.message_summaries.excluded_topics` | `AI_MESSAGE_SUMMARIES_EXCLUDED_TOPICS` | []chat_topic | Excluded Topics - (chat, topic) pairs excluded from message summarization. |
| `ai.message_summaries.excluded_user_ids` | `AI_MESSAGE_SUMMARIES_EXCLUDED_USER_IDS` | []int64 | Excluded User IDs - User IDs excluded from message summaries |
| `ai.message_summaries.prompt.system` | `AI_MESSAGE_SUMMARIES_PROMPT_SYSTEM` | string | Message Summary (System) - System prompt for message summarization (placeholders: `{{message}}`) |
| `ai.message_summaries.prompt.user` | `AI_MESSAGE_SUMMARIES_PROMPT_USER` | string | Message Summary (User) - User prompt for message summarization (placeholders: `{{message}}`) |

## AI Link Summaries

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.link_summaries.enabled` | `AI_LINK_SUMMARIES_ENABLED` | bool | Enabled - Enable AI summaries for shared links |
| `ai.link_summaries.use_full_model` | `AI_LINK_SUMMARIES_USE_FULL_MODEL` | bool | Use Full Model - Use full model instead of light for link summaries and translation |
| `ai.link_summaries.light_model_threshold` | `AI_LINK_SUMMARIES_LIGHT_MODEL_THRESHOLD` | int | Light Model Threshold - Force light model when text exceeds this character count (0 = disabled) |
| `ai.link_summaries.excluded_domains` | `AI_LINK_SUMMARIES_EXCLUDED_DOMAINS` | []string | Excluded Domains - Domains excluded from link summarization |
| `ai.link_summaries.excluded_extensions` | `AI_LINK_SUMMARIES_EXCLUDED_EXTENSIONS` | []string | Excluded Extensions - File extensions to skip (e.g. .pdf, .doc*), supports wildcards |
| `ai.link_summaries.excluded_user_ids` | `AI_LINK_SUMMARIES_EXCLUDED_USER_IDS` | []int64 | Excluded User IDs - User IDs excluded from link summarization |
| `ai.link_summaries.included_topics` | `AI_LINK_SUMMARIES_INCLUDED_TOPICS` | []chat_topic | Included Topics - (chat, topic) pairs where link summaries are generated. Empty = every moderation chat / any topic. |
| `ai.link_summaries.excluded_topics` | `AI_LINK_SUMMARIES_EXCLUDED_TOPICS` | []chat_topic | Excluded Topics - (chat, topic) pairs excluded from link summarization. |
| `ai.link_summaries.extractor_api_key` | `AI_LINK_SUMMARIES_EXTRACTOR_API_KEY` | string 🔒 | Extractor API Key - API key for ExtractorAPI service |
| `ai.link_summaries.diffbot_api_key` | `AI_LINK_SUMMARIES_DIFFBOT_API_KEY` | string 🔒 | Diffbot API Key - API key for Diffbot service |
| `ai.link_summaries.cloudflare_account_id` | `AI_LINK_SUMMARIES_CLOUDFLARE_ACCOUNT_ID` | string | Cloudflare Account ID - Cloudflare account ID for Browser Rendering API |
| `ai.link_summaries.cloudflare_api_token` | `AI_LINK_SUMMARIES_CLOUDFLARE_API_TOKEN` | string 🔒 | Cloudflare API Token - API token with Browser Rendering - Edit permission |
| `ai.link_summaries.cookies` | `AI_LINK_SUMMARIES_COOKIES` | string 🔒 | Cookies - Cookies to pass when fetching link content (e.g. name1=value1; name2=value2) |
| `ai.link_summaries.user_agent` | `AI_LINK_SUMMARIES_USER_AGENT` | string | User Agent - Custom User-Agent header for Cloudflare Browser Rendering and manual extraction |
| `ai.link_summaries.content_language` | `AI_LINK_SUMMARIES_CONTENT_LANGUAGE` | string | Content Language - Expected language of link content |
| `ai.link_summaries.max_extracted_content_length` | `AI_LINK_SUMMARIES_MAX_EXTRACTED_CONTENT_LENGTH` | int | Max Extracted Content Length - Maximum characters to extract from links (default: 4096) |
| `ai.link_summaries.max_download_size_bytes` | `AI_LINK_SUMMARIES_MAX_DOWNLOAD_SIZE_BYTES` | int | Max Download Size (bytes) - Maximum page download size in bytes (default: 1048576 = 1 MB) |
| `ai.link_summaries.min_summary_length` | `AI_LINK_SUMMARIES_MIN_SUMMARY_LENGTH` | int | Min Summary Length - Discard AI summaries shorter than this many characters; the link is then treated as if extraction failed (0 = disabled) |
| `ai.link_summaries.prompt.system` | `AI_LINK_SUMMARIES_PROMPT_SYSTEM` | string | Link Summary (System) - System prompt for link content summarization (placeholders: `{{title}}`, `{{url}}`, `{{content}}`, `{{truncated_suffix}}`) |
| `ai.link_summaries.prompt.user` | `AI_LINK_SUMMARIES_PROMPT_USER` | string | Link Summary (User) - User prompt for link content summarization (placeholders: `{{title}}`, `{{url}}`, `{{content}}`, `{{truncated_suffix}}`) |

## AI External Data

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.external_data.weather_latitude` | `AI_EXTERNAL_DATA_WEATHER_LATITUDE` | float64 | Weather Latitude - Latitude for weather data (default: 50.088 - Prague) |
| `ai.external_data.weather_longitude` | `AI_EXTERNAL_DATA_WEATHER_LONGITUDE` | float64 | Weather Longitude - Longitude for weather data (default: 14.4208 - Prague) |
| `ai.external_data.holidays_country` | `AI_EXTERNAL_DATA_HOLIDAYS_COUNTRY` | string | Holidays Country - ISO country code for holidays (default: CZ) |
| `ai.external_data.wikipedia_language` | `AI_EXTERNAL_DATA_WIKIPEDIA_LANGUAGE` | string | Wikipedia Language - Language for Wikipedia on-this-day events (default: cs) |
| `ai.external_data.translate_wikipedia` | `AI_EXTERNAL_DATA_TRANSLATE_WIKIPEDIA` | bool | Translate Wikipedia - Translate Wikipedia events via AI (default: true) |

## RSS Feeds

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.rss.use_full_model` | `AI_RSS_USE_FULL_MODEL` | bool | Use Full Model - Use full model instead of light for RSS translation and summarization |
| `ai.rss.light_model_threshold` | `AI_RSS_LIGHT_MODEL_THRESHOLD` | int | Light Model Threshold - Force light model when RSS text exceeds this character count (0 = disabled) |
| `ai.rss.translation_prompt.system` | `AI_RSS_TRANSLATION_PROMPT_SYSTEM` | string | RSS Translation (System) - System prompt for RSS feed translation (falls back to ai.translation_prompt) (placeholders: `{{text}}`) |
| `ai.rss.translation_prompt.user` | `AI_RSS_TRANSLATION_PROMPT_USER` | string | RSS Translation (User) - User prompt for RSS feed translation (falls back to ai.translation_prompt) (placeholders: `{{text}}`) |
| `ai.rss.summary_prompt.system` | `AI_RSS_SUMMARY_PROMPT_SYSTEM` | string | RSS Summary (System) - System prompt for RSS feed summarization (placeholders: `{{text}}`) |
| `ai.rss.summary_prompt.user` | `AI_RSS_SUMMARY_PROMPT_USER` | string | RSS Summary (User) - User prompt for RSS feed summarization (placeholders: `{{text}}`) |

## AI User Profiles

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `ai.user_profiles.enabled` | `AI_USER_PROFILES_ENABLED` | bool | Enabled - Enable daily AI-generated user behavior profiles |
| `ai.user_profiles.time` | `AI_USER_PROFILES_TIME` | string | Time - Time of day to run the AI user-profile update task (HH:MM) |
| `ai.user_profiles.prompt.system` | `AI_USER_PROFILES_PROMPT_SYSTEM` | string | New Profile (System) - System prompt for generating new user profiles (placeholders: `{{username}}`, `{{messages}}`) |
| `ai.user_profiles.prompt.user` | `AI_USER_PROFILES_PROMPT_USER` | string | New Profile (User) - User prompt for generating new user profiles (placeholders: `{{username}}`, `{{messages}}`) |
| `ai.user_profiles.update_prompt.system` | `AI_USER_PROFILES_UPDATE_PROMPT_SYSTEM` | string | Update Profile (System) - System prompt for updating existing user profiles (placeholders: `{{username}}`, `{{messages}}`, `{{existing_profile}}`) |
| `ai.user_profiles.update_prompt.user` | `AI_USER_PROFILES_UPDATE_PROMPT_USER` | string | Update Profile (User) - User prompt for updating existing user profiles (placeholders: `{{username}}`, `{{messages}}`, `{{existing_profile}}`) |
| `ai.user_profiles.skip_forever_muted_users` | `AI_USER_PROFILES_SKIP_FOREVER_MUTED_USERS` | bool | Skip Forever-Muted Users - Don't build or update AI profiles for permanently muted users |

## User Profiles (Tracking)

| YAML Key | ENV | Type | Description |
|---|---|---|---|
| `user_profiles.enabled` | `USER_PROFILES_ENABLED` | bool | Enabled - Track username/display-name history, first-seen-per-chat timestamps, and per-day message counts (independent from AI profiles) |
| `user_profiles.disable_username_reuse_alerts` | `USER_PROFILES_DISABLE_USERNAME_REUSE_ALERTS` | bool | Disable Username-Reuse Alerts - Suppress the admin-chat notification fired when a new user_id appears with a @username previously held by a different user_id (tracking still runs) |

