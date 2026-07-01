# Installation Guide

*Languages: **English** | [Русский](installation_ru.md)*

This guide covers every way to run **Gennady**:

- [Prerequisites](#prerequisites)
- [Local installation (binary)](#local-installation-binary)
- [Local systemd service](#local-systemd-service)
- [Docker - local, no web UI](#docker--local-no-web-ui)
- [Docker - local, with web UI](#docker--local-with-web-ui)
- [Cloud installation (webhook + web UI)](#cloud-installation-webhook--web-ui)
- [Minimal cloud instance without a config file](#minimal-cloud-instance-without-a-config-file)
- [Moving config and database between files and the database](#moving-config-and-database-between-files-and-the-database)
- [Remote database configuration (Turso / libSQL, BunnyNet)](#remote-database-configuration-turso--libsql-bunnynet)

Every configuration key can be set three ways, in increasing priority:

1. The YAML config file (`config.yaml`).
2. The database (`config_values` table) when running without a config file.
3. **Environment variables** (always win). The variable name is the YAML path,
   upper-cased, with dots replaced by underscores - e.g. `ai.content_moderation.enabled`
  → `AI_CONTENT_MODERATION_ENABLED`. Run `./gennadium -export-env` to dump the full
   list for your current config.

See [config.md](config.md) for what each key means.

---

## Prerequisites

- **Telegram bot token** from [@BotFather](https://t.me/BotFather) - required.
- The bot must be **added to your chat as an administrator** (with delete/restrict rights).
- For **topic-based (forum) chats**, note the topic/thread message IDs you want to moderate.
- **Go 1.26+** if you build from source (not needed for Docker).
- *Optional:* Azure OpenAI / OpenAI-compatible API keys for AI features; Azure Vision or
  OCR.space for image moderation.

> **Tip - finding chat IDs:** add the bot to your chat, send a message, and enable
> `debug.debug_telegram: true` to see chat and topic IDs in the logs. Group/supergroup IDs are
> negative (e.g. `-1001234567890`).

> **Tip - finding topic IDs:** in a forum-style chat, open a message in the target topic, copy
> its link (right-click / long-press → *Copy Message Link*). It looks like
> `https://t.me/c/1234567890/55/1001` - the **middle number** (`55`) is the topic ID. For
> public chats the link is `https://t.me/yourchat/55/1001` with the topic ID in the same
> position.

---

## Local installation (binary)

```bash
git clone https://github.com/noiseonwires/gennady.git
cd gennady

# Create your config from the template
cp config.example.yaml config.yaml
# Edit config.yaml: bot_token, admin.chat_id, moderation.chat_id, and AI keys

# Build
go build -o gennadium .

# Run (uses ./config.yaml and a local SQLite DB at db/moderation.db)
./gennadium

# Or point at a custom config
./gennadium -config /etc/gennadium/config.yaml
```

The minimum working config needs `bot_token`, `admin.chat_id`, and `moderation.chat_id`. This
runs in **long-polling** mode (no inbound ports required) with a local SQLite database.

### Build with version info

```bash
go build -ldflags="-X 'main.version=1.0.0' \
  -X 'main.gitCommit=$(git rev-parse HEAD)' \
  -X 'main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
  -o gennadium .

./gennadium -version
```

---

## Local systemd service

For an always-on Linux host, install as a systemd service:

```bash
# Build first, then:
sudo ./install.sh
```

The script creates a `telegram-bot` system user, copies the binary and config to
`/opt/gennadium`, and installs the `gennadium.service` unit. Then:

```bash
sudo systemctl start gennadium
sudo systemctl status gennadium
sudo journalctl -u gennadium -f      # follow logs
```

> Set the `TZ` environment variable in the service unit (e.g. `Environment=TZ=Europe/Prague`)
> so scheduled events (morning greeting, daily summary, RSS) fire at the right local time.

---

## Docker - local, no web UI

Run the bot in a container with long-polling and a mounted config + database. No ports need to
be exposed.

**1. Prepare files** in your working directory:

```
config.yaml      # your configuration (web_ui.enabled: false, webhook.enabled: false)
db/              # directory for the SQLite database (will be created)
```

**2. `docker-compose.yml`:**

```yaml
services:
  gennadium:
    image: ghcr.io/noiseonwires/gennady:latest
    container_name: gennadium
    restart: unless-stopped
    environment:
      - TZ=Europe/Prague
    volumes:
      - ./db:/app/db:rw                       # persistent database
      - ./config.yaml:/app/config.yaml:ro     # configuration (read-only)
```

**3. Run:**

```bash
docker compose up -d
docker compose logs -f
```

---

## Docker - local, with web UI

Same as above, but enable the web UI and publish its port so you can configure and moderate
from the browser.

**1. In `config.yaml`:**

```yaml
server:
  listen_addr: "0.0.0.0"
  listen_port: 8080

web_ui:
  enabled: true
  path_prefix: "/manage-7f3a"   # change from the default "/admin" to a custom, hard-to-guess value
  password: "choose-a-strong-password"
  otp_enabled: true          # also require a Telegram OTP code (needs super_admin_user_id)

admin:
  super_admin_user_id: 123456789   # your Telegram user ID - receives OTP codes
```

**2. `docker-compose.yml`** - publish the port:

```yaml
services:
  gennadium:
    image: ghcr.io/noiseonwires/gennady:latest
    container_name: gennadium
    restart: unless-stopped
    environment:
      - TZ=Europe/Prague
    ports:
      - "8080:8080"
    volumes:
      - ./db:/app/db:rw
      - ./config.yaml:/app/config.yaml:ro
```

**3. Run and open** `http://localhost:8080/manage-7f3a`.

> The web UI is protected by a password and (recommended) a Telegram one-time code. If you
> expose it to the internet, put it behind HTTPS (reverse proxy) and keep `otp_enabled: true`.
> Also change `web_ui.path_prefix` from the default `/admin` to a custom value - it makes the
> panel harder to discover by automated scanners.

> **Moderators?** To also offer an isolated, limited panel for moderators, set
> `web_ui.public_url` (e.g. `http://localhost:8080` locally, or your public HTTPS URL) and keep
> `web_ui.moderator_path_prefix` (default `/mod`). In webhook mode the public URL is taken from
> `webhook.url` automatically. Moderators then request a one-time login link from the bot's
> `/start` menu. See [Moderator web UI](config.md#moderator-web-ui-isolated-limited).

> Note: the config file is mounted **read-only** here. Edits made in the web UI cannot be
> written back to a read-only file. To make the web UI the source of truth, run without a
> mounted config file and store config in the database - see
> [the next section](#cloud-installation-webhook--web-ui) and
> [Moving config…](#moving-config-and-database-between-files-and-the-database).

---

## Cloud installation (webhook + web UI)

For a cloud container (Bunny.net Magic Containers, Fly.io, Render, a VPS, etc.), use **webhook
mode** instead of long-polling, expose the HTTP server, and serve the web UI behind HTTPS.

**1. Configuration** (file or environment variables):

```yaml
bot_token: "123456:ABC..."

server:
  listen_addr: "0.0.0.0"
  listen_port: 8080

webhook:
  enabled: true
  url: "https://bot.example.com/webhook"   # public HTTPS URL routed to this container
  secret_token: "a-random-shared-secret"   # validated on every Telegram request

web_ui:
  enabled: true
  path_prefix: "/manage-7f3a"   # custom prefix instead of the default "/admin"
  password: "strong-password"
  otp_enabled: true
  # Optional: hand moderators an isolated, limited panel (no config/logs/system).
  moderator_path_prefix: "/mod"            # must differ from path_prefix
  public_url: "https://bot.example.com"    # base URL for moderator login links (auto-derived from webhook.url if omitted)

admin:
  super_admin_user_id: 123456789
```

> **Moderator access (optional).** Moderators can request a one-time login link from the bot's
> `/start` menu (**🛡️ Access Web UI**). They land on a restricted panel under
> `moderator_path_prefix` (moderation, messages, profiles, read-only diagnostics) — configuration,
> logs and the system page are not served there. The link is built from `web_ui.public_url`; in
> webhook mode it falls back to the host of `webhook.url`, so you usually don't need to set it.
> If no public URL can be resolved, the button is hidden and moderator login stays disabled.

**2. Requirements:**

- A **public HTTPS endpoint** that routes to the container's `listen_port`. Telegram only
  delivers webhooks over HTTPS. Terminate TLS at your platform's load balancer / reverse proxy,
  or provide a certificate via `server.certificate_path`.
- The bot registers the webhook with Telegram automatically on startup using `webhook.url`.

**3. Scheduled events in webhook/serverless environments.**
In long-polling mode the bot fires scheduled tasks (morning greeting, daily summary, RSS,
profiles, cleanup) on its own timer. If your platform suspends the container when idle, enable
**webhook-triggered scheduling** and call the trigger endpoint from an external cron / uptime
monitor:

```yaml
scheduled_events:
  webhook_mode: true
  webhook_path: "/trigger-events-9e4b8f2d7c1a4b6f91c0d3e5a8b7c2d4"
  lock_timeout_minutes: 15      # prevents duplicate runs across multiple instances
```

Use a long, random, unguessable `webhook_path` and keep it out of public docs and logs. Then
have an external scheduler hit that URL periodically (e.g. every 5–15 minutes). The bot runs only
the tasks that are due.

> Leaving the **internal scheduler** on (`webhook_mode: false`) is also fine for many cloud
> setups: even if the platform suspends/stops the container when idle, incoming Telegram webhook
> events periodically wake it back up, and on each wake-up it runs any recently missed events
> (subject to `scheduled_events.missed_event_max_delay_minutes`).

---

## Minimal cloud instance without a config file

If your platform makes it hard to mount a `config.yaml`, you can boot a **minimal instance**
with just a few environment variables and a remote database, then configure everything else
through the web UI. This works because:

- When **no config file is found** and a **remote database** is configured, the bot loads its
  configuration from the database.
- If the database is **empty**, the bot seeds it from `config.example.yaml` (with environment
  overrides applied) on first boot.
- If required values (bot token / chat IDs) are still missing but the **web UI is enabled with
  usable authentication**, the bot starts in a degraded mode so you can finish setup in the
  browser.

**Essential environment variables for a minimal instance:**

```bash
# Remote database (so config can be stored without a file)
DATABASE_URL=libsql://your-db.turso.io
DATABASE_AUTH_TOKEN=your-db-token

# Telegram + how to reach you for OTP delivery
BOT_TOKEN=123456:ABC...
ADMIN_SUPER_ADMIN_USER_ID=123456789

# Web UI with usable authentication (required to start without full config)
WEB_UI_ENABLED=true
WEB_UI_PASSWORD=strong-password
WEB_UI_OTP_ENABLED=true

# HTTP server
SERVER_LISTEN_ADDR=0.0.0.0
SERVER_LISTEN_PORT=8080
```

The Web UI accepts plaintext `WEB_UI_PASSWORD`; when that config is persisted in the database,
the stored value is converted to `hashed:pbkdf2-sha256:...`. Large database uploads are capped at
128 MB by default; set `WEB_UI_MAX_DB_UPLOAD_BYTES` to override that limit.

> "Usable authentication" means **either** a web UI password is set, **or** OTP is enabled with
> both `admin.super_admin_user_id` and a valid `bot_token`. Without it, the bot refuses to start
> when required values are missing - so it never sits exposed and unconfigured.

Boot the container, open `https://your-host/admin`, log in, and fill in the rest (moderation
chat IDs, AI models, rules, RSS feeds). The web UI writes directly to the database, so your
configuration persists across restarts and redeploys without any mounted file.

---

## Moving config and database between files and the database

Gennady can move both **configuration** and **stored data** between a local file and a
(local or remote) database. This is the bridge between "develop locally with files" and "run in
the cloud with a database".

All of these operations are available in the **web UI** (Files / Diagnostics sections):

| Action | What it does |
|---|---|
| **Download config** | Export the current configuration as a `config.yaml` file. |
| **Upload config** | Replace the running configuration from an uploaded `config.yaml`. |
| **Download env** | Export the effective configuration as environment variables (same as `-export-env`). |
| **Copy config to DB** | Write the current file-based config into the database's `config_values` table. |
| **Download database** | Export the SQLite database (optionally without the config values) to a file. |
| **Upload database** | Import a database file, **merging** data into the current store. |

### Typical workflows

**Local file → cloud database.** Configure locally with `config.yaml`, then either:

- in the web UI choose **Copy config to DB** (writes config into the remote DB), or
- run the bot once locally pointed at the remote DB so it seeds, or
- export your env with `./gennadium -export-env` and set those variables in the cloud.

**Cloud database → local file.** In the web UI choose **Download config** to get a
`config.yaml` you can commit or keep as a backup, and **Download database** to pull the data
down to a local SQLite file.

### Data import/merge behavior

Database import is non-destructive where it matters. Per-table strategies:

- **Replaced:** `config_values`, `muted_users`.
- **Merged (insert missing):** `message_info`, `messages_for_deletion`.
- **Merged by natural key (skip duplicates):** `actions`, `warnings`, `user_names_history`.
- **Merged keeping newest:** `scheduled_events`, `user_profiles`.
- **Merged by summing counts:** `user_daily_activity`.

This means you can pull a production database down, work locally, and push back without losing
recent activity.

> The command-line `-export-env` flag is handy for migrating a file-based setup into any
> platform's environment-variable configuration:
> ```bash
> ./gennadium -config config.yaml -export-env > bot.env
> ```

---

## Remote database configuration (Turso / libSQL, BunnyNet)

By default the bot uses a **local SQLite** file. For cloud deployments you have **two options**,
and you can switch between them later (see [Switching between local and remote](#switching-between-local-and-remote)):

- **Remote libSQL / Turso database** - the right choice for **ephemeral containers** with no
  persistent storage, so config and data survive restarts and redeploys.
- **Local SQLite on a persistent volume** - if your cloud provider offers a persistent
  disk/volume, you can keep the simple local SQLite file (`db/moderation.db`) on it instead of
  running a separate database service.

```yaml
database:
  provider: "remote"                       # "local", "remote", or empty to auto-detect
  url: "libsql://your-db.turso.io"
  auth_token: "your-turso-auth-token"
```

Or via environment variables:

```bash
DATABASE_PROVIDER=remote
DATABASE_URL=libsql://your-db.turso.io
DATABASE_AUTH_TOKEN=your-turso-auth-token
```

**Provider auto-detection.** If `provider` is empty or unrecognized, the bot auto-detects:
when both `url` and `auth_token` are set it uses the **remote** provider, otherwise **local**.

**Local (default):**

```yaml
database:
  provider: "local"
  path: "db/moderation.db"
```

**Creating a Turso database:**

```bash
turso db create gennadium
turso db show gennadium --url            # → libsql://...
turso db tokens create gennadium         # → auth token
```

**BunnyNet.** When running on BunnyNet Magic Containers, the platform's environment variables
(`BUNNYNET_MC_APPID`, `BUNNYNET_MC_PODID`, `BUNNYNET_MC_REGION`) are detected automatically and
shown in the web UI diagnostics / startup logs. Combine a BunnyNet container with a remote
Turso database and the [minimal-instance setup](#minimal-cloud-instance-without-a-config-file)
for a fully file-less cloud deployment.

### Switching between local and remote

You can move an existing deployment between a **local SQLite** file and a **remote** database in
either direction - **local → remote** or **remote → local** - without losing data:

1. In the web UI **Files** section, **Download database** from the current instance to get a
   SQLite file.
2. Point a new instance at the destination backend and **Upload database** to import/merge that
   file - see [Moving config and database…](#moving-config-and-database-between-files-and-the-database)
   for the per-table merge behavior.
3. **Adjust your environment variables** so the bot talks to the new backend, then restart:
   - To **remote**: set `DATABASE_PROVIDER=remote`, `DATABASE_URL`, `DATABASE_AUTH_TOKEN`.
   - To **local**: set `DATABASE_PROVIDER=local` and `DATABASE_PATH`, and remove the
     `DATABASE_URL` / `DATABASE_AUTH_TOKEN` variables.

> **Don't forget the env change.** Environment variables override the config file, so if the old
> `DATABASE_*` variables are left in place they win and the bot keeps using the old backend.

---

## Next steps

- [config.md](config.md) - understand and tune every setting.
- [CONFIG_REFERENCE_en.md](CONFIG_REFERENCE_en.md) / [CONFIG_REFERENCE_ru.md](CONFIG_REFERENCE_ru.md) - complete auto-generated key + environment-variable reference.
