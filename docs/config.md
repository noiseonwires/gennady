# Configuration Guide

*Languages: **English** | [Русский](config_ru.md)*

This guide explains how to configure **Gennady** with practical examples. For the
exhaustive list of every key, its type and its environment-variable name, see the
auto-generated [CONFIG_REFERENCE_en.md](CONFIG_REFERENCE_en.md) (or
[CONFIG_REFERENCE_ru.md](CONFIG_REFERENCE_ru.md)).

## Contents

- [How configuration works](#how-configuration-works)
- [Minimal config](#minimal-config)
- [Core settings](#core-settings)
- [Database](#database)
- [Chats & topics](#chats--topics)
- [Admin & moderation](#admin--moderation)
- [Message deletion](#message-deletion)
- [Database cleanup](#database-cleanup)
- [Server, webhook & web UI](#server-webhook--web-ui)
- [Scheduled events](#scheduled-events)
- [AI models](#ai-models)
- [AI content moderation](#ai-content-moderation)
- [Image moderation (vision & OCR)](#image-moderation-vision--ocr)
- [Creative replies](#creative-replies)
- [Morning greeting](#morning-greeting)
- [Daily summary](#daily-summary)
- [Message & link summaries](#message--link-summaries)
- [RSS feeds](#rss-feeds)
- [User profiles](#user-profiles)
- [Reactions & debug](#reactions--debug)
- [Prompt placeholders](#prompt-placeholders)

---

## How configuration works

Configuration comes from three sources, in increasing priority:

1. **YAML file** (`config.yaml`) - the usual source. Start from `config.example.yaml`.
2. **Database** (`config_values` table) - used automatically when no config file is found and a
   remote database is configured. Editable through the web UI.
3. **Environment variables** - always override both of the above.

**Environment variable naming:** take the YAML path, replace dots with underscores, and
upper-case it.

| YAML path | Environment variable |
|---|---|
| `bot_token` | `BOT_TOKEN` |
| `admin.chat_id` | `ADMIN_CHAT_ID` |
| `ai.content_moderation.enabled` | `AI_CONTENT_MODERATION_ENABLED` |

Dump every variable for your current config:

```bash
./gennadium -export-env
```

---

## Minimal config

The smallest config that runs (long-polling, local SQLite, no AI):

```yaml
bot_token: "123456:ABC-DEF..."
language: "en"

admin:
  chat_id: -1001234567890        # chat that receives moderation alerts

moderation:
  chat_id: -1009876543210        # chat to moderate (single ID or a list)

ai:
  enabled: false
```

---

## Core settings

```yaml
bot_token: "123456:ABC-DEF..."   # from @BotFather (required)
language: "en"                   # "en" or "ru" - UI and default-prompt language
# proxy_url: "socks5://127.0.0.1:1080"   # optional proxy for outgoing requests
```

---

## Database

```yaml
database:
  provider: "local"              # "local", "remote", or empty to auto-detect
  path: "db/moderation.db"       # local SQLite file (provider: local)
  # url: "libsql://your-db.turso.io"   # remote DB (provider: remote)
  # auth_token: ""                     # remote auth token
```

Auto-detection: if `provider` is empty/unknown and both `url` and `auth_token` are set, the bot
treats the DB as **remote**, otherwise **local**. See
[installation.md](installation.md#remote-database-configuration-turso--libsql-bunnynet) for
Turso setup. A remote database is the recommended choice for ephemeral cloud containers such as
Bunny.net Magic Containers.

---

## Chats & topics

Gennady can manage **several chats at once** and, inside each chat, can target individual
**forum topics**. Most chat-scoped settings therefore use a small, uniform `(chat, topic)`
shape. Read this section once - it applies to moderation, message deletion, creative replies,
summaries and RSS publishing.

### What is a "topic"?

A **topic** (also called a *forum topic* or *thread*) is a sub-channel inside a Telegram group
that has **Topics** enabled (Group Settings → *Topics*). Each topic has its own numeric ID.
The area you see in a non-forum group - or the default thread of a forum group - is the
**main area**.

- If your group does **not** use topics, you never need a topic ID: everything lives in the
  main area.
- If your group **does** use topics, you can point a feature at one specific topic, at the main
  area only, or at the whole chat regardless of topic.

### Topic values

Wherever a topic is expected, three values are special. Each accepts a numeric form **or** a
case-insensitive string alias:

| Value | Alias | Meaning |
|---|---|---|
| `-1` | `any`  | **Any topic** - the whole chat, including the main area |
| `0`  | `main` | **Main area only** - the part with no forum thread |
| `N`  | -      | A **specific** forum topic, where `N` is its numeric ID |

`topic: any` and `topic: main` are exactly equivalent to `topic: -1` and `topic: 0`. The web UI
and exported configs always write the numeric form, but you can type the aliases by hand:

```yaml
included_topics:
  - {chat: -1001112223334, topic: any}    # same as topic: -1
  - {chat: -1005556667778, topic: main}   # same as topic: 0
```

### How to get a topic ID

The easiest way: open the topic in **Telegram** (desktop or web), right-click any message in it
and choose **Copy Message Link**. The link looks like
`https://t.me/c/1234567890/<TOPIC_ID>/<MESSAGE_ID>` - the **middle** number is the topic ID.

Alternatively, turn on debug logging for a moment:

1. Set `debug.debug_telegram: true` (or env `DEBUG_TELEGRAM=true`).
2. Post or reply in the target topic - the bot logs each incoming update with its `chat_id` and
   the topic/thread message ID.
3. Turn debug logging back off when you're done.

### The `(chat, topic)` list shape

A `(chat, topic)` list is written as objects. **Both `chat` and `topic` are required** in the
object form:

```yaml
excluded_topics:
  - {chat: -1001112223334, topic: 42}    # one specific topic
  - {chat: -1005556667778, topic: -1}    # the whole chat
  - {chat: -1009998887776, topic: 0}     # main area only
```

If you moderate **exactly one** chat, you may use a bare topic ID as a shorthand and the bot
fills in the chat for you:

```yaml
excluded_topics: [42, 43]                # only valid with a single moderation chat
```

> With more than one moderation chat the bare-int shorthand is rejected at load time - be
> explicit and pass both `chat` and `topic`.

### Included vs. excluded

Feature scope is computed as **included minus excluded**:

- `included_topics` - where the feature is active. **An empty list means "every moderation
  chat, any topic"** (the common default).
- `excluded_topics` - carve-outs that win over the included set.

A message at `(chat, topic)` is in scope when its chat is a moderation chat **and** it matches
`included_topics` (or that list is empty) **and** it does **not** match `excluded_topics`. A
`topic: -1` entry matches every topic in that chat.

Common patterns:

```yaml
# Everywhere in every moderation chat (default)
included_topics: []

# Only one chat, only its main area
included_topics: [{chat: -1001112223334, topic: 0}]

# A whole chat except one noisy topic
included_topics: [{chat: -1001112223334, topic: -1}]
excluded_topics: [{chat: -1001112223334, topic: 555}]
```

### Where the bot posts (`post_to`)

Features that publish bot-authored messages (morning greeting, daily summary, RSS feeds) use a
`post_to` list with the same `(chat, topic)` shape. **An empty `post_to` posts to every
moderation chat's main area.** Use `topic: 0` to force the main area or `topic: N` to post into
a specific topic. (`topic: -1` makes no sense as a destination and should not be used here.)

---

## Admin & moderation

```yaml
admin:
  chat_id: -1001234567890        # where moderation alerts and action keyboards are sent
  reply_message_ids: [42]        # for forum chats: topic message IDs to post alerts into
  super_admin_user_id: 123456789 # your Telegram user ID (silent commands, OTP, debug DMs)
  notify_super_admin: false      # also DM moderation alerts to the super-admin
  notify_startup: false          # DM the super-admin on start/restart and remote-DB connection loss/recovery
  whitelist_user_ids: [777000]   # users who bypass filters and have admin access; 777000 = Telegram service account (keep it whitelisted)

moderation:
  chat_id: -1009876543210        # single ID, or a list: [-100111, -100222]
  excluded_topics: []            # (chat, topic) pairs excluded from content analysis
  mute_across_all_chats: false   # a mute applies to every moderated chat at once
```

`moderation.chat_id` accepts either a single integer or a list:

```yaml
moderation:
  chat_id: [-1001112223334, -1005556667778]
```

`excluded_topics` uses the `(chat, topic)` shape from [Chats & topics](#chats--topics). Leave it
empty to analyze everything; add an entry like `{chat: -100111, topic: 42}` to skip a topic, or
`{chat: -100111, topic: -1}` to skip a whole chat.

---

## Message deletion

Automatically delete messages, optionally restricted to specific topics. Useful for keeping an
announcements or media topic clean.

```yaml
message_deletion:
  enabled: true
  included_topics: []            # where deletion runs (empty = every chat, any topic)
  excluded_topics: []            # carve-outs that are never deleted
  excluded_user_ids: []          # users whose messages are never deleted
  chat_deletion_retention_hours: 3   # delete messages older than N hours
  cleanup_interval_hours: 3          # run the deletion sweep every N hours
```

Examples:

```yaml
# Delete everywhere (default)
included_topics: []

# Delete only in the main area of one chat
included_topics: [{chat: -1009876543210, topic: 0}]

# Delete in a whole chat but never in its "pinned" topic
included_topics: [{chat: -1009876543210, topic: -1}]
excluded_topics: [{chat: -1009876543210, topic: 777}]
```

> There is no separate "also delete in the main chat" switch anymore. The main area is just
> `topic: 0` - include it (or use `topic: -1` for the whole chat) when you want it covered.

---

## Database cleanup

Periodic purge of stored records to keep the database small.

```yaml
database_cleanup:
  cleanup_interval_hours: 24     # run cleanup every N hours (0 = disabled)
  message_retention_hours: 168   # keep message_info for 7 days
  warning_retention_hours: 168
  action_retention_hours: 168
  preserve_warned_muted_messages: false  # keep messages that caused a warn/active mute
```

---

## Server, webhook & web UI

Shared HTTP server settings used by both the webhook and the web UI:

```yaml
server:
  listen_addr: "0.0.0.0"
  listen_port: 8080
  # certificate_path: "/path/to/cert.pem"   # TLS cert for self-signed setups
```

**Webhook** (cloud/serverless; default is long-polling):

```yaml
webhook:
  enabled: false
  url: "https://bot.example.com/webhook"  # public HTTPS URL
  secret_token: ""                        # shared secret validated per request
  debug: false                            # log raw payloads (verbose)
```

**Web UI** (admin panel):

```yaml
web_ui:
  enabled: false
  path_prefix: "/admin"          # URL prefix - change to a custom value (see note below)
  password: ""                   # admin password or hashed:pbkdf2-sha256:... (empty = disabled)
  otp_enabled: true              # require Telegram OTP as 2FA (needs super_admin_user_id)
  moderator_path_prefix: "/mod"  # URL prefix for the isolated, limited moderator UI (must differ from path_prefix)
  public_url: ""                 # base URL without prefix (https://bot.example.com); falls back to the webhook host when empty
```

When config is stored in the database, `web_ui.password` is saved in the marked
`hashed:pbkdf2-sha256:...` format. YAML configs may use either plaintext or that marked hash,
so exporting DB-backed config to YAML does not lose the password.

> **Change the path prefix.** Don't leave `path_prefix` at the default `/admin` on a
> publicly reachable instance - set a custom, hard-to-guess value (e.g. `/manage-7f3a`) so the
> panel isn't trivially discovered by automated scanners.

> To start the bot without full config and finish setup in the browser, the web UI must have
> **usable authentication**: a password set, *or* OTP enabled with both
> `admin.super_admin_user_id` and a valid `bot_token`. See
> [installation.md](installation.md#minimal-cloud-instance-without-a-config-file).

### Moderator web UI (isolated, limited)

The super-admin panel above is the full UI. Moderators (anyone who is an admin in a
moderation/admin chat) can be given a **separate, restricted** web UI without sharing the
super-admin credentials:

- It is served under its own `moderator_path_prefix` (default `/mod`, must differ from
  `path_prefix`). Only moderation, messages, profiles and a **read-only** diagnostics page are
  mounted — configuration, logs, the system page and all test/debug actions don't exist under
  this prefix (they return 404), so they can't be reached even by URL.
- Access is granted on demand: a moderator opens the bot, taps **🛡️ Access Web UI** on the
  `/start` menu, and the bot sends a **one-time login link plus a separate OTP**. Opening the
  link and entering the matching OTP grants a moderator-scoped session. The link is single-use,
  expires after 5 minutes, and is useless without the OTP.
- `public_url` is the externally-reachable base URL used to build that link
  (e.g. `https://bot.example.com`), **without** the path prefix. If it's empty, it falls back to
  the scheme + host of `webhook.url` (handy for webhook deployments). If neither is set, the
  "Access Web UI" button is hidden from the keyboard and moderator login is unavailable.

Moderator sessions are role-scoped: a moderator token is rejected by the super-admin endpoints,
and its cookie is scoped to the moderator prefix.

---

## Scheduled events

Controls how daily/scheduled tasks (morning greeting, daily summary, RSS, profiles) are run.

```yaml
scheduled_events:
  missed_event_max_delay_minutes: 60   # fire a missed event only if ≤ N minutes late
  webhook_mode: false                  # run scheduled tasks only when triggered via webhook
  webhook_path: "/trigger-events-CHANGE-ME-LONG-RANDOM"  # trigger endpoint path
  lock_timeout_minutes: 15             # reclaim a stale lock after N minutes (multi-instance safe)
```

In `webhook_mode`, an external scheduler must periodically call `webhook_path`; the bot runs
only tasks that are due. Use a long, random, unguessable path for public deployments. See
[installation.md](installation.md#cloud-installation-webhook--web-ui).

---

## AI models

`light_model` and `full_model` each take a **list** of endpoints with automatic failover (the
bot rotates to the next entry on error). Use a cheaper/faster model for high-volume tasks and a
stronger model where quality matters.

```yaml
ai:
  enabled: true
  chat_rules: "Be respectful. No spam, no NSFW, English/Russian only."

  # Store per-message emoji→count reactions and fold them into the AI context for
  # daily summaries, creative replies and user profiles. Aggregate counts work in
  # any chat; per-user reaction events also require the bot to be a chat admin.
  track_reactions: false

  light_model:
    - provider: "azure"          # "azure" or "openai" (auto-detected if omitted)
      endpoint: "https://YOUR.openai.azure.com/"
      api_key: "KEY"
      deployment_name: "gpt-4o-mini"
      temperature: 0.5
      omit_max_tokens: false     # true for models that reject max_tokens (some gpt-5)

  full_model:
    - provider: "azure"
      endpoint: "https://PRIMARY.openai.azure.com/"
      api_key: "KEY"
      deployment_name: "gpt-4o"
      temperature: 0.7
    - provider: "openai"         # fallback on an OpenAI-compatible gateway
      endpoint: "https://api.openai.com/v1"
      api_key: "sk-..."
      deployment_name: "gpt-4o"
      temperature: 0.7
```

- **`provider: azure`** - `endpoint` is the resource base URL, `deployment_name` is the Azure
  deployment, auth via `api-key` header.
- **`provider: openai`** - works with OpenAI and any OpenAI-compatible gateway (OpenRouter,
  Groq, LiteLLM, local Ollama, …). `endpoint` is the API base, `deployment_name` is the model
  id, auth via Bearer token.

Most AI features have a `use_full_model` toggle to pick which model they use.

---

## AI content moderation

The heart of the bot: classify messages with a custom prompt, then route each verdict to an
action.

```yaml
ai:
  content_moderation:
    enabled: true
    skip_admin_users: true
    complaint_manual_moderation: true   # reply + @bot complaint handling (see below)
    default_mute_minutes: 60     # duration for the "mute" action (0 = forever)
    reply_context_max_chars: 500 # cap on quoted {{reply_to}} text (0 = no limit)

    # Stricter rules for brand-new users (first message within new_user_window_hours).
    new_user_window_hours: 24    # how long a user counts as "new"
    new_user_rules: ""           # injected via {{new_user_rules}}, only for new users

    # Double-check a user's first N messages with the full model even when the
    # light model found nothing (catches subtle spam new members slip past the
    # cheap model). Counted per user per chat. 0 = disabled.
    full_model_first_messages: 0

    # Map a substring of the model's verdict to an action.
    # Rules run in order; every matching rule fires (you can stack actions).
    rules:
      - trigger: "spam"
        action: delete
        description: "Spam / promo links"
        notify_admin: true
      - trigger: "profanity"
        action: warn
        notify_admin: true
      - trigger: "nsfw"
        action: mute
        notify_admin: true
      - trigger: "unsure"
        action: report          # forward to admin chat for a human decision
        notify_admin: true

    prompt:
      system: |
        You are a content-moderation classifier for a chat. Classify the message into one or
        more violation labels (lowercase, comma-separated): spam, profanity, nsfw, unsure, ok.
        Respond with labels only. Chat rules: {{chat_rules}}
      user: |
        {{user_profile}}
        {{new_user_rules}}
        {{reply_to}}
        Message: «{{message}}»
        Answer (labels only):

    warning_prompt:
      system: |
        You are a chat moderator. Write a short, personalized warning (1-2 sentences) for the
        user, explaining the problem. Include a ⚠️ emoji. Chat rules: {{chat_rules}}
      user: "Write a warning for {{username}}.{{user_message}}"
```

**Actions:**

| Action | Effect |
|---|---|
| `delete` | Delete the offending message. |
| `warn` | Issue an AI-generated warning (logged per user). |
| `mute` | Mute the user for `default_mute_minutes` (`0` = permanent). |
| `report` | Forward to the admin chat with an action keyboard for a manual decision. |

To re-tune the bot for a different community, change `chat_rules`, the labels in `prompt`, and
the `rules` mapping - no code changes needed.

### New-user rules

A user counts as **new** until `new_user_window_hours` (default 24) after their first observed
message. While they are new, the bot adds a prominent "new user" marker to the moderation
context and expands `{{new_user_rules}}` to the text of `new_user_rules` (empty by default). Use
it to apply stricter first-poster rules without affecting trusted regulars:

```yaml
ai:
  content_moderation:
    new_user_window_hours: 24
    new_user_rules: |-
      This user is new. Treat any link, promo, or "earn money" pitch as spam.
    prompt:
      user: |
        {{user_profile}}
        {{new_user_rules}}
        Message: «{{message}}»
```

### User complaints (reply + @bot mention)

When a member **replies to a message and mentions the bot** in a moderation chat, the bot treats
it as a complaint. It first re-runs AI moderation across **every distinct configured model** (the
same as the web UI **"Moderate again"** action) and acts automatically if any model flags the
message. Only when **every** model clears it does `complaint_manual_moderation` decide what
happens next:

- `true` (default) - post the admin decision card so a human can act.
- `false` - end the complaint silently without notifying admins.

---

## Image moderation (vision & OCR)

Catch violations hidden in images by extracting their text/content and running it through the
same moderation pipeline.

```yaml
ai:
  content_moderation:
    # Azure Vision - image captioning + OCR
    vision_enabled: true
    vision_endpoint: "https://YOUR.cognitiveservices.azure.com/"
    vision_api_key: "KEY"

    # Azure Content Safety - optional safety scoring
    content_safety_enabled: false
    content_safety_endpoint: "https://YOUR-SAFETY.cognitiveservices.azure.com/"
    content_safety_api_key: "KEY"

    # Screen a new member's WHOLE public profile on their first message: name,
    # bio and profile photo, plus their linked personal channel (name,
    # description, photo). Photos are screened with Content Safety first, and
    # only described via Vision / OCR.space when Content Safety is unavailable
    # or fails; all the text is judged by the new_user_profile_prompt below.
    # Works WITHOUT content_safety_enabled. The prompt MUST reply exactly CLEAN
    # for good profiles; any other reply is recorded as a finding.
    new_user_profile_check_enabled: false
    # Judge the gathered profile text with the FULL model instead of the light
    # model - better at subtle spam/scam/promo cues, at a higher per-call cost.
    # Only affects the AI text verdict; photo screening is unchanged. Default: false.
    new_user_profile_use_full_model: false
    new_user_profile_prompt:
      user: |
        Analyze the following profile:

        {{profile_text}}

    # OCR.space cloud OCR API - no self-hosting, free tier available
    ocrspace_enabled: false
    ocrspace_api_key: "YOUR_OCRSPACE_KEY"  # test key: helloworld
    ocrspace_url: "https://api.ocr.space/parse/image"
    ocrspace_language: "eng"               # 3-letter code; engines 2/3 accept "auto"
    ocrspace_engine: 2                     # 1=default, 2=best all-round, 3=highest accuracy
```

> **[OCR.space](https://ocr.space/ocrapi)** is a hosted alternative: set `ocrspace_enabled: true`
> and an `ocrspace_api_key` for a cloud OCR provider with no self-hosting (free tier: 25,000
> requests/month, 1 MB file limit). The fallback order is **Azure Vision → OCR.space**. Engine
> `2` works well for memes and noisy backgrounds; engine `3` is the most accurate.

> **First-message profile screening.** `new_user_profile_check_enabled` runs **once** on a
> user's first message in a moderation chat. When it fires, the bot stamps the unified marker
> `[moderation:suspicious-profile]` on the user's profile, which then surfaces in the
> `{{user_profile}}` and `{{user_reputation}}` moderation placeholders. Profile/channel photos
> are screened with Content Safety first; Vision/OCR.space describe them only when Content
> Safety is unavailable or fails. See [Cookbook Recipe 6](cookbook.md#recipe-6-new-member-profile-screening).


Occasional witty, contextual AI replies - rate-limited so the bot stays charming, not spammy.

```yaml
ai:
  creative_replies:
    enabled: true
    use_full_model: true
    max_messages: 3              # max replies per time window
    time_window: 3              # window length in hours
    included_topics: []          # where replies are allowed (empty = every chat, any topic)
    excluded_topics: []          # topics where replies are suppressed
    follow_up_only_same_user: false
    prompt:
      system: "You are a witty chat participant. Reply naturally and briefly."
      user: |
        Reply to: "{{message}}"
        {{context}}
```

Both lists use the `(chat, topic)` shape from [Chats & topics](#chats--topics). To enable
replies only in one topic, set `included_topics: [{chat: -100111, topic: 42}]`.

---

## Morning greeting

A daily greeting that can include weather, holidays and historical events.

```yaml
ai:
  morning_greeting:
    enabled: true
    use_ai: true                 # false = data only (weather/holidays/events, no AI text)
    use_full_model: true
    time: "08:00"                # HH:MM, local time (set TZ env var)
    post_to: []                  # where to post (empty = every moderation chat, main area)
    prompt:
      system: "You write a short, friendly morning greeting for the chat."
      user: "Create a greeting. Today is {{weekday}} {{date}}{{weather}}{{holidays}}{{events}}"

  external_data:
    weather_latitude: 50.088     # Prague by default
    weather_longitude: 14.4208
    holidays_country: "CZ"       # ISO country code (Open Holidays API)
    wikipedia_language: "cs"     # Wikipedia "on this day" language
    translate_wikipedia: true    # translate events via AI
```

Weather (Open-Meteo), holidays (OpenHolidays API) and Wikipedia events are all free and need no
API key.

---

## Daily summary

```yaml
ai:
  daily_summary:
    enabled: true
    time: "02:00"
    use_full_model: false
    post_to: []                  # where to post (empty = every moderation chat, main area)
    prompt:
      system: "You summarize the day's chat discussion in 4-5 sentences."
      user: |
        Summarize today's messages:
        {{messages}}
```

---

## Message & link summaries

**Message summaries** - auto-summarize long messages:

```yaml
ai:
  message_summaries:
    enabled: true
    use_full_model: false
    light_model_threshold: 4096  # force light model above N chars (0 = off)
    min_length: 1000             # only summarize messages longer than this
    included_topics: []          # where summarization runs (empty = every chat, any topic)
    excluded_topics: []          # topics to skip
    excluded_user_ids: []
    prompt:
      system: "You summarize messages briefly."
      user: |
        Summarize in 1-2 sentences:
        {{message}}
```

**Link summaries** - fetch shared links and post a summary. Uses a multi-provider extraction
fallback chain (Diffbot → ExtractorAPI → Cloudflare Browser Rendering → manual).

```yaml
ai:
  link_summaries:
    enabled: true
    use_full_model: false
    light_model_threshold: 8192
    excluded_domains: ["t.me", "telegram.org"]
    excluded_extensions: [".pdf", ".zip", ".mp4", ".exe"]
    excluded_user_ids: []
    included_topics: []          # where link summaries run (empty = every chat, any topic)
    excluded_topics: []          # topics to skip
    extractor_api_key: ""        # ExtractorAPI (optional)
    diffbot_api_key: ""          # Diffbot (optional)
    # cloudflare_account_id: ""  # Cloudflare Browser Rendering (optional, for JS-heavy pages)
    # cloudflare_api_token: ""
    # cookies: "name=value"      # cookies for extraction (bypass consent walls)
    # user_agent: "Mozilla/5.0 ..."
    content_language: "en"
    max_extracted_content_length: 4096
    max_download_size_bytes: 1048576   # 1 MB
    min_summary_length: 0        # discard summaries shorter than N chars (0 = off)
    prompt:
      system: "You summarize web pages briefly."
      user: |
        Title: {{title}}
        URL: {{url}}
        Content{{truncated_suffix}}:
        {{content}}
```

---

## RSS feeds

Publish new RSS items on a per-feed schedule, optionally translated and/or summarized by AI.

```yaml
ai:
  rss:
    use_full_model: true
    light_model_threshold: 8192
    feeds:
      - name: "Example Feed"
        url: "https://example.com/rss"
        time: "14:00"            # daily check time (HH:MM)
        enabled: true
        translate: true          # false = publish original text instead of AI-translated
        summarize_if_long: true  # when body exceeds max_message_length: true = AI summary, false = hard truncate
        post_to: []              # where to publish (empty = every moderation chat, main area)
        max_message_length: 0    # truncate/summarize threshold in chars (0 = Telegram limit, 4096)

    translation_prompt:          # optional; falls back to ai.translation_prompt
      system: "You are a professional translator. Keep the headline on the first line."
      user: |
        Translate this news text:
        {{text}}

    summary_prompt:
      system: "You summarize news articles."
      user: |
        Summarize in 7-8 sentences:
        {{text}}
```

The generic translation prompt (also used for links and Wikipedia events):

```yaml
ai:
  translation_prompt:
    system: "You are a professional translator."
    user: |
      Translate to the chat language. Keep the headline on the first line.
      {{text}}
```

---

## User profiles

Two independent systems:

**AI profiles** (`ai.user_profiles`) - daily-built behavior profiles with a reputation score,
injected into moderation via `{{user_profile}}`. A compact, token-cheap alternative is also
available as `{{user_reputation}}`, which contains only the reputation score plus a note when
the user's profile photo was flagged by the content-safety check:

```yaml
ai:
  user_profiles:
    enabled: false
    time: "03:00"
    skip_forever_muted_users: false
    prompt:
      system: |
        You are a behavioral analyst. Write a SHORT profile (2-3 sentences) and end with a line:
        "Reputation: good" / "neutral" / "bad".
      user: |
        Profile @{{username}} based on:
        {{messages}}
    update_prompt:
      system: "Update the existing profile based on new activity. Keep it short."
      user: |
        Current profile:
        {{existing_profile}}
        New activity:
        {{messages}}
```

**Tracking** (`user_profiles`) - non-AI tracking of username/display-name history, first-seen
timestamps, per-day activity counts, and impersonation alerts:

```yaml
user_profiles:
  enabled: false
  disable_username_reuse_alerts: false   # suppress "username reused by a new account" alerts
```

---

## Reactions & debug

Emoji reactions have sensible defaults; override only if you want to:

```yaml
reactions:
  suspicious_message: "🤔"
  bad_message: "🍌"
  content_filter: "🥴"
  creative_reply_limit: "🥱"
  extracting_link: "✍"
  extract_link_failed: "🌚"
  user_muted: "🤮"
  report_acknowledged: "👌"
  creative_reply_error: "😐"
```

> Use only emojis that Telegram allows **as message reactions**. Telegram accepts a fixed set of
> reaction emojis; if you set one outside that set, the bot fails to apply the reaction. Stick
> to the defaults or pick replacements from Telegram's supported reaction list.

Debug logging:

```yaml
debug:
  debug_telegram: false          # log updates, commands, callbacks (useful to find chat IDs)
  debug_external_apis: false     # log all external API requests/responses
  debug_api_errors: false        # log only failed requests
  dump_moderation_messages: false
  dump_admin_messages: false
  message_dump_path: "./logs"
  send_to_super_admin: false     # DM enabled debug logs to the super-admin
```

---

## Prompt placeholders

Each AI prompt supports placeholders that the bot fills in at runtime:

| Prompt | Placeholders |
|---|---|
| Content moderation | `{{message}}`, `{{chat_rules}}`, `{{user_profile}}`, `{{user_reputation}}`, `{{reply_to}}`, `{{new_user_rules}}` |
| Warning | `{{username}}`, `{{user_message}}`, `{{chat_rules}}`, `{{mute_info}}`, `{{reputation}}` |
| Creative reply | `{{message}}`, `{{context}}`, `{{quote}}` |
| Morning greeting | `{{weekday}}`, `{{date}}`, `{{weather}}`, `{{holidays}}`, `{{events}}` |
| Daily summary | `{{messages}}` |
| Message summary | `{{message}}` |
| Link summary | `{{title}}`, `{{url}}`, `{{content}}`, `{{truncated_suffix}}` |
| Translation / RSS translation | `{{text}}` |
| RSS summary | `{{text}}` |
| AI user profile (new) | `{{username}}`, `{{messages}}` |
| AI user profile (update) | `{{username}}`, `{{messages}}`, `{{existing_profile}}` |

---

## See also

- [installation.md](installation.md) - deploy locally, in Docker, or in the cloud.
- [CONFIG_REFERENCE_en.md](CONFIG_REFERENCE_en.md) / [CONFIG_REFERENCE_ru.md](CONFIG_REFERENCE_ru.md) - full auto-generated key + environment-variable reference.
