# Changelog

This project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.2.0] - 2026-07-31

### Added

- **Website watch** for AI-generated change reports from pages without RSS feeds.
- **Night mode** with scheduled message cleanup, notices and optional auto-mute.
- **Server time and timezone** on the Diagnostics page.
- **Scheduled database backups** to local storage, WebDAV or bunny.net, plus
  manual backups from the Web UI.
- **Per-model `enabled` flag** for temporarily disabling AI models.
- **User profile and reputation placeholders** for creative-reply prompts.
- **Message reporting by `t.me` link** within the same chat.

### Fixed

- Moderation audit events now record the actual moderator.
- Duplicate Telegram deliveries no longer generate multiple creative replies.

## [1.1.0] - 2026-07-01

### Added

- **Isolated moderator Web UI** on its own URL prefix (`web_ui.moderator_path_prefix`,
  default `/mod`): moderation, messages, profiles and read-only diagnostics only.
  Moderators sign in with a one-time link + OTP from the bot's `/start` menu — no
  shared super-admin credentials. New `web_ui.public_url` sets the login base URL.
- **Moderation funnel stats** (received → light-flagged → full-confirmed →
  auto/cleared/manual) with today/yesterday/day-before/all-time columns, in the
  `/start` admin menu and Web UI diagnostics.
- **Full-model double-check for a user's first N messages**
  (`ai.content_moderation.full_model_first_messages`, default off) and
  **full-model new-user profile screening**
  (`ai.content_moderation.new_user_profile_use_full_model`, default off).
- **Tunable long-polling concurrency** via `update_processing.workers`, with
  periodic worker-pool utilization logging.

### Changed

- **Message-processing pipeline rebuilt under the hood** into a modular
  router/staged pipeline — same behavior, but more robust, flexible and
  maintainable.
- Refreshed Web UI with mobile optimizations; funnel percentages are now
  stage-relative; lower memory footprint.

### Fixed

- Duplicate Telegram deliveries no longer wipe a message's saved moderation verdict.
- Bot mentions/replies aimed at the bot's own messages now get a creative reply
  instead of the moderation path; caption mentions are detected.

[1.2.0]: https://github.com/noiseonwires/gennady/releases/tag/v1.2.0
[1.1.0]: https://github.com/noiseonwires/gennady/releases/tag/v1.1.0
