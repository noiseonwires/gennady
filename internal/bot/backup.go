// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	"gennadium/internal/backup"
	"gennadium/internal/i18n"
)

// backupTimeout caps a single backup run (snapshot + upload).
const backupTimeout = 30 * time.Minute

// performDatabaseBackup snapshots the active database and copies it to the
// configured backup destination. Registered as the "database_backup" interval
// task. (The web UI's "Back up now" button calls backup.Run directly, so it
// reports failures in the response instead of notifying the super-admin.)
func (b *Bot) performDatabaseBackup() {
	start := time.Now()
	log.Printf("💾 Starting database backup...")

	ctx, cancel := context.WithTimeout(context.Background(), backupTimeout)
	defer cancel()

	res, err := backup.Run(ctx, b.db, b.config.Backup, b.config.Database.Path)
	if err != nil {
		log.Printf("💾 Database backup FAILED: %v", err)
		b.sendToAdminChat(fmt.Sprintf("⚠️ Database backup failed: %v", err))
		b.notifySuperAdminBackupFailed(err)
		return
	}

	log.Printf("💾 Database backup completed in %v: %s (%.1f MB) → %s",
		time.Since(start).Round(time.Second), res.FileName,
		float64(res.Bytes)/(1024*1024), res.Destination)
}

// notifySuperAdminBackupFailed DMs the super-admin about a failed scheduled
// backup when backup.notify_super_admin_on_failure is enabled. The error text is
// redacted (it can carry destination URLs) and truncated to fit one Telegram
// message.
func (b *Bot) notifySuperAdminBackupFailed(cause error) {
	if !b.config.Backup.NotifyOnFailure {
		return
	}
	details := redactSensitiveText(cause.Error())
	text := truncateMessage(i18n.Tf("backup.failed", details), MaxTelegramMessageLength)
	b.dmSuperAdmin(text, "")
}
