<p align="center">
  <img src="assets/logo.png" alt="Gennady logo" width="220">
</p>

# 🤖 Gennady

[![Tests](https://img.shields.io/github/actions/workflow/status/noiseonwires/gennady/test.yml?branch=main&label=tests)](https://github.com/noiseonwires/gennady/actions/workflows/test.yml)
[![Build and Publish Docker Image](https://img.shields.io/github/actions/workflow/status/noiseonwires/gennady/docker-image.yml?branch=main&label=docker%20build)](https://github.com/noiseonwires/gennady/actions/workflows/docker-image.yml)
[![Check i18n & assets](https://img.shields.io/github/actions/workflow/status/noiseonwires/gennady/check-i18n.yml?branch=main&label=i18n%20%26%20assets)](https://github.com/noiseonwires/gennady/actions/workflows/check-i18n.yml)
[![Latest release](https://img.shields.io/github/v/release/noiseonwires/gennady?sort=semver)](https://github.com/noiseonwires/gennady/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/noiseonwires/gennady)](https://github.com/noiseonwires/gennady/blob/main/go.mod)
[![Last commit](https://img.shields.io/github/last-commit/noiseonwires/gennady)](https://github.com/noiseonwires/gennady/commits/main)
[![Issues](https://img.shields.io/github/issues/noiseonwires/gennady)](https://github.com/noiseonwires/gennady/issues)
[![License: AGPL v3](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](LICENSE)

*Languages: **English** | [Русский](README_ru.md)*

**The noble guardian of your Telegram community.**

Gennady is a prompt-driven AI moderation and community intelligence bot for Telegram.

Gennady watches your chat, understands text and images in context, and acts on the rules you
define in plain language. Route each AI verdict to the action you choose: delete, mute, warn,
report, summarize, or hand off to an admin. He has seen every spam scheme, can write a warning
that lands with uncomfortable precision, and still turns around to greet the chat in the
morning with the weather, holidays, and a history fact.

He runs a tight room. But it is still a warm room.

Built in Go. Runs as a single static binary, a Docker container, or a cloud service. Uses
SQLite locally or a remote database in the cloud. Free and open source under the AGPL-3.

> **Battle-tested in production.** Gennady has been running since August 2025 in a Telegram
> chat with 1,000+ members, and since March 2026 it moderates the comments chat of a major
> Czech news channel on Telegram.

---

## Why Gennady?

Most moderation bots make you pick from a fixed menu of rules. Gennady lets you *describe* the
behavior you want in natural language and routes the AI's verdict to the action you choose.
That means the same bot can run a strict legal-news channel and a relaxed hobby group - you
just change the prompt.

---

## Features

### 🛡️ AI moderation on your terms

- **Custom moderation prompts.** Define what counts as a violation in plain language. Tune the
  bot for spam, hate speech, off-topic chatter, NSFW, scams, or anything specific to your
  community. The chat rules are injected into the prompt so the AI judges messages by *your*
  standards.
- **Rule-to-action routing.** Map each verdict the AI returns (e.g. `spam`, `profanity`,
  `nsfw`, `unsure`) to a concrete action. Stack multiple actions on one trigger.
- **Configurable actions:** auto-delete, auto-mute (with a configurable duration or
  permanent), auto-warn, or report to the admin chat for a human decision - per rule.
- **AI-generated warnings.** When a user crosses a line, the bot can write a personalized,
  context-aware warning instead of a canned message.
- **Light and full AI models.** Route cheap, high-volume checks to a fast/cheap AI model and
  reserve a stronger AI model for nuanced tasks - independently per feature, with automatic
  length-based switching.
- **Reputation-aware profiles.** Optional AI user profiles let the moderator weigh a message
  against the user's known style and history, reducing false positives for regulars and
  tightening up on repeat offenders.
- **Admin override.** Optionally skip moderation for admins and a whitelist of trusted users.

### 🖼️ Content security beyond text

- **Image moderation via OCR & vision.** Violations hidden in screenshots and images are
  caught too - not just in text. Run OCR (Azure Vision or the OCR.space cloud API) to read
  text out of images and feed it through the same moderation rules.
- **Azure Vision & Content Safety** integration for image analysis and safety scoring.
- **Profile-photo screening.** Optionally screen a new user's profile photos through Azure
  Content Safety on their first message; a flagged photo adds a note to their profile that the
  moderator weighs on subsequent messages.

### 👮 Moderation toolkit

- **Mute system** with preset durations and automatic expiry; optionally apply a mute across
  all moderated chats at once.
- **"Cruel mute"** for repeat offenders - the user is **not** officially restricted by Telegram
  (their account shows no mute, and they can keep typing and sending), but every message they
  post is silently deleted the instant it arrives. With no "you are muted" notice and no error,
  a persistent troublemaker keeps talking to an empty room - which tends to be far more
  frustrating than a normal mute.
- **Warning system** with full per-user history.
- **Manual actions** from the admin chat (inline keyboard) or the web UI.
- **Topic-aware auto-deletion.** Telegram only supports auto-deletion for an entire chat; the
  bot instead deletes messages only in the specific topics/threads you choose, with a
  configurable message lifetime - keep an announcements topic clean while leaving the main chat
  untouched.
- **Pinned-message & service-notification cleanup.**
- **Action log** of every mute, unmute, warn and delete - attributed to the bot or the admin.

### 👤 User intelligence

- **User profiles by AI.** Daily-built behavior profiles with a reputation score, used to make
  smarter moderation decisions.
- **User tracking.** Username / display-name change history, first-seen timestamps per chat,
  and per-day activity plots.
- **Fake / impersonation detection.** Get alerted when a new account starts using a `@username`
  previously held by a different account ("deleted and re-registered with the same handle").

### 💬 Community engagement

- **Morning greetings.** A daily good-morning message that can include the weather, public
  holidays, and "on this day" historical events - generated by AI or assembled from data only.
- **Daily summaries.** An AI recap of what was discussed in the chat during the day.
- **Message summaries.** Long messages get an automatic short summary.
- **Link summaries.** When someone shares a link, the bot fetches the page and posts a concise
  summary - and can translate the page into your chat's language - with a multi-provider
  extraction fallback chain for tough pages.
- **Creative replies.** Witty, contextual AI responses, rate-limited so the bot stays charming
  instead of spammy.

### 📰 RSS publishing

- **Scheduled RSS feeds.** Publish new items from any RSS feed on a per-feed schedule.
- **AI translation & summarization.** Optionally translate feed items into your chat's language
  and/or summarize them before posting.

### 🖥️ Web UI

A built-in admin panel (optional, password + Telegram OTP protected) to run everything without
SSH:

- **Visual configuration** of every setting, AI model, moderation rule and RSS feed.
- **Diagnostics:** test external API connectivity, inspect rendered moderation prompts, check
  webhook status, view live logs, database stats and AI token/cost usage per service.
- **Moderation from the browser:** mute, unmute, warn, browse messages (filter by chat/user, follow reply chains), view user profiles.
- **Config & data transfer:** download/upload the config file, environment variables, or the
  SQLite database; migrate config from a file into the database and back.
- **One-click restart.**

### ⚙️ Deployment & data

- **Run anywhere.** Single static Go binary, systemd service, Docker / Docker Compose, or a
  cloud container.
- **Long-polling or webhook.** Use simple long-polling for local runs, or webhook mode for
  cloud/serverless deployments.
- **Local SQLite or remote database.** Store everything in a local SQLite file, or point the
  bot at a remote libSQL/Turso database - ideal for ephemeral cloud containers (e.g. Bunny.net
  Magic Containers).
- **Config without files.** Boot a minimal cloud instance (e.g. on Bunny.net) with no mounted
  config, then configure everything through the web UI; the bot can seed and persist its config
  in the database.
- **Localized** interface and config docs in English and Russian.
- **Multi-instance safe.** Database-level locking prevents duplicate scheduled runs when more
  than one instance is live in the cloud - for example during a redeployment when the old and
  new containers briefly overlap.

---

## External services

Gennady works on its own for basic moderation, and integrates optional external
services to unlock its full feature set. Everything below is optional and configurable.

| Service | Used for | Notes |
|---|---|---|
| **Azure OpenAI** | All AI features | Or any OpenAI-compatible endpoint. |
| **OpenAI / OpenAI-compatible** | All AI features | OpenAI, OpenRouter, Groq, LiteLLM, local Ollama, etc. |
| **Azure Vision** | Image analysis & OCR | For image moderation. |
| **Azure Content Safety** | Safety scoring of content | Optional safety layer. |
| **OCR.space** | Text extraction from images | Cloud OCR API; free tier, no self-hosting. |
| **Diffbot** | Link content extraction | First in the link-extraction fallback chain. |
| **ExtractorAPI** | Link content extraction | Fallback extractor. |
| **Cloudflare Browser Rendering** | Link content extraction | Renders JS-heavy pages. |
| **Open-Meteo** | Weather in morning greetings | Free, no API key. |
| **OpenHolidays API** | Public holidays in greetings | Free, no API key. |
| **Wikipedia (On This Day)** | Historical events in greetings | Free, no API key. |
| **Turso / BunnyNet /libSQL** | Remote database | For cloud deployments. |

---

## Quick start

```bash
# 1. Get the code
git clone https://github.com/noiseonwires/gennady.git
cd gennady

# 2. Create your config
cp config.example.yaml config.yaml
#    edit config.yaml - set bot_token, admin.chat_id, moderation.chat_id, AI keys

# 3. Build and run
go build -o gennadium .
./gennadium
```

You need at minimum a **Telegram bot token** from [@BotFather](https://t.me/BotFather). AI keys
are optional but required for the AI features.

➡️ Full instructions for local, Docker, and cloud deployments are in
[installation.md](docs/installation.md).

➡️ Every configuration option is documented in [config.md](docs/config.md), with auto-generated
references in [CONFIG_REFERENCE_en.md](docs/CONFIG_REFERENCE_en.md) and
[CONFIG_REFERENCE_ru.md](docs/CONFIG_REFERENCE_ru.md).

---

## Command-line flags

| Flag | Description |
|---|---|
| `-config <path>` | Path to the YAML config file (default `config.yaml`). |
| `-version` | Print version, commit and build time, then exit. |
| `-export-env` | Print the effective configuration as environment variables and exit. |
| `-generate-config-docs <path>` | Regenerate the localized config reference files and exit. |

---

## Documentation

- [installation.md](docs/installation.md) - local, Docker and cloud installation, plus database/config migration.
- [config.md](docs/config.md) - configuration guide with examples.
- [cookbook.md](docs/cookbook.md) - recipes combining AI prompts with config options for moderation and more.
- [CONFIG_REFERENCE_en.md](docs/CONFIG_REFERENCE_en.md) / [CONFIG_REFERENCE_ru.md](docs/CONFIG_REFERENCE_ru.md) - auto-generated full reference of every key and its environment variable.

---

## License

Gennady is licensed under the **GNU Affero General Public License v3 (AGPL-3)** or a
commercial license. See [LICENSE](LICENSE) for details.

Contributions are welcome - see [CLA.md](CLA.md) before submitting a pull request.
