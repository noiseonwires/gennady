// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"gennadium/internal/i18n"
	"log"
	"time"
)

// taskKind distinguishes daily (time-of-day) tasks from interval-based tasks.
type taskKind int

const (
	taskDaily    taskKind = iota // runs at a specific HH:MM each day
	taskInterval                 // runs every N duration
)

// scheduledTaskDef describes a single scheduled task.
type scheduledTaskDef struct {
	Kind           taskKind
	TimeStr        string        // "HH:MM" for daily tasks
	Interval       time.Duration // for interval tasks
	Task           func()
	SeedOnFirstRun bool // seed fake record on first startup so it doesn't fire immediately
}

// lockTimeout returns the configured stale-lock timeout for scheduled events.
func (b *Bot) lockTimeout() time.Duration {
	return time.Duration(b.config.ScheduledEvents.LockTimeoutMinutes) * time.Minute
}

// executeWithLock tries to atomically claim a scheduled event, execute the task,
// and record completion. Returns true if the task was executed.
// If the lock cannot be acquired (another instance holds it), the task is skipped.
func (b *Bot) executeWithLock(name string, timeLabel string, task func()) bool {
	claimed, err := b.db.TryClaimScheduledEvent(name, b.lockTimeout())
	if err != nil {
		log.Printf("⏰ %s: failed to claim lock: %v, skipping", name, err)
		return false
	}
	if !claimed {
		log.Printf("⏰ %s: another instance is already executing this task, skipping", name)
		return false
	}

	log.Printf("⏰ %s: lock acquired, executing task", name)
	success := true
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("⏰ %s: task panicked: %v", name, r)
				success = false
			}
		}()
		task()
	}()

	if success {
		if err := b.db.RecordEventFired(name, timeLabel); err != nil {
			log.Printf("⏰ %s: failed to record event fired: %v", name, err)
		}
	} else {
		// Release the lock so the task can be retried
		if err := b.db.ReleaseScheduledEvent(name); err != nil {
			log.Printf("⏰ %s: failed to release lock after failure: %v", name, err)
		}
	}
	return success
}

// scheduledTaskBackoff provides long backoff periods for scheduled tasks.
func scheduledTaskBackoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 5 * time.Minute
	case 2:
		return 10 * time.Minute
	case 3:
		return 15 * time.Minute
	default:
		return 20 * time.Minute
	}
}

// buildAllTasks returns every enabled scheduled task: AI, RSS, message cleanup, DB cleanup.
func (b *Bot) buildAllTasks() map[string]scheduledTaskDef {
	tasks := make(map[string]scheduledTaskDef)

	// ── AI tasks ──
	if b.config.AI.Enabled {
		if b.config.AI.MorningGreeting.Enabled {
			tasks["morning_greeting"] = scheduledTaskDef{
				Kind: taskDaily, TimeStr: b.config.AI.MorningGreeting.Time,
				Task: b.sendMorningGreeting,
			}
		}
		if b.config.AI.DailySummary.Enabled {
			tasks["daily_summary"] = scheduledTaskDef{
				Kind: taskDaily, TimeStr: b.config.AI.DailySummary.Time,
				Task: b.sendDailySummary, SeedOnFirstRun: true,
			}
		}
		// ── RSS feeds ──
		for _, feed := range b.config.AI.Rss.Feeds {
			if !feed.Enabled || feed.URL == "" || feed.Time == "" {
				continue
			}
			feedCopy := feed
			tasks[rssTaskName(feedCopy.URL)] = scheduledTaskDef{
				Kind: taskDaily, TimeStr: feedCopy.Time,
				Task: func() { b.processRssFeed(feedCopy) },
			}
		}
	}

	// ── AI user profiles (daily behavior-profile update) ──
	// Only the AI subsystem needs a daily schedule. The general user-tracking
	// subsystem runs entirely on the message hot path; its only periodic work
	// (pruning user_daily_activity) is folded into database_cleanup below.
	if b.config.AI.Enabled && b.config.AI.UserProfiles.Enabled && b.config.AI.UserProfiles.Time != "" {
		tasks["user_profiles"] = scheduledTaskDef{
			Kind: taskDaily, TimeStr: b.config.AI.UserProfiles.Time,
			Task: b.runUserProfilesUpdate,
		}
	}

	// ── Message cleanup ──
	if b.config.MessageDeletion.Enabled && b.config.MessageDeletion.CleanupIntervalHours > 0 {
		tasks["message_cleanup"] = scheduledTaskDef{
			Kind:     taskInterval,
			Interval: time.Duration(b.config.MessageDeletion.CleanupIntervalHours) * time.Hour,
			Task:     b.cleanupOldMessages,
		}
	}

	// ── Database cleanup ──
	if b.config.DatabaseCleanup.CleanupIntervalHours > 0 {
		tasks["database_cleanup"] = scheduledTaskDef{
			Kind:     taskInterval,
			Interval: time.Duration(b.config.DatabaseCleanup.CleanupIntervalHours) * time.Hour,
			Task:     b.performDatabaseCleanup,
		}
	}

	return tasks
}

// startScheduledTasks registers all tasks, checks for missed events, and starts
// scheduler goroutines (unless webhook mode is enabled).
func (b *Bot) startScheduledTasks() {
	allTasks := b.buildAllTasks()
	if len(allTasks) == 0 {
		log.Printf("No scheduled tasks enabled")
		return
	}

	log.Printf("⏰ Registering %d scheduled task(s)...", len(allTasks))

	// Ensure all tasks have a DB record so they appear in the UI
	for name, def := range allTasks {
		timeLabel := def.TimeStr
		if def.Kind == taskInterval {
			timeLabel = fmt.Sprintf("every %v", def.Interval)
		}
		if err := b.db.EnsureScheduledEventExists(name, timeLabel); err != nil {
			log.Printf("⏰ Failed to ensure event %s exists in DB: %v", name, err)
		}
	}

	if b.config.ScheduledEvents.WebhookMode {
		log.Printf("⏰ Webhook mode: all scheduled events will only run on webhook trigger")
	} else {
		// Check for missed events (daily + interval)
		b.checkMissedEvents(allTasks)

		// Start goroutines
		for name, def := range allTasks {
			switch def.Kind {
			case taskDaily:
				go b.runScheduledTask(name, def.TimeStr, def.Task)
			case taskInterval:
				go b.runIntervalTask(name, def.Interval, def.Task)
			}
		}
	}

	// Prune orphaned DB records
	allActive := make([]string, 0, len(allTasks))
	for name := range allTasks {
		allActive = append(allActive, name)
	}
	if pruned, err := b.db.PruneScheduledEvents(allActive); err != nil {
		log.Printf("⏰ Failed to prune orphaned scheduled events: %v", err)
	} else if pruned > 0 {
		log.Printf("⏰ Pruned %d orphaned scheduled event(s) from database", pruned)
	}

	log.Printf("⏰ All scheduled tasks registered")
}

// checkMissedEvents checks for events that were missed while the bot was offline.
// Daily tasks are subject to the maxDelay filter; interval tasks always run if overdue.
func (b *Bot) checkMissedEvents(allTasks map[string]scheduledTaskDef) {
	maxDelay := time.Duration(b.config.ScheduledEvents.MissedEventMaxDelayMinutes) * time.Minute
	now := time.Now()

	log.Printf("⏰ Checking for missed scheduled events (max delay for daily tasks: %v)...", maxDelay)

	for name, def := range allTasks {
		if def.Kind == taskInterval {
			b.checkMissedIntervalEvent(name, def)
			continue
		}

		targetTime, err := time.Parse("15:04", def.TimeStr)
		if err != nil {
			log.Printf("⏰ Missed-event check: invalid time %q for %s, skipping", def.TimeStr, name)
			continue
		}

		event, err := b.db.GetScheduledEvent(name)
		if err != nil {
			log.Printf("⏰ Missed-event check: DB error for %s: %v", name, err)
			continue
		}

		if event == nil {
			if def.SeedOnFirstRun {
				todayOccurrence := time.Date(now.Year(), now.Month(), now.Day(),
					targetTime.Hour(), targetTime.Minute(), 0, 0, now.Location())
				log.Printf("⏰ Missed-event check: %s has no history, seeding fake record at %s (will start tomorrow)",
					name, todayOccurrence.Format("2006-01-02 15:04"))
				if err := b.db.RecordEventFiredAt(name, def.TimeStr, todayOccurrence); err != nil {
					log.Printf("⏰ Missed-event check: failed to seed %s: %v", name, err)
				}
			} else {
				log.Printf("⏰ Missed-event check: %s has no history, will run at next scheduled time", name)
			}
			continue
		}

		mostRecent := time.Date(now.Year(), now.Month(), now.Day(),
			targetTime.Hour(), targetTime.Minute(), 0, 0, now.Location())
		if mostRecent.After(now) {
			mostRecent = mostRecent.Add(-24 * time.Hour)
		}

		if !event.LastFiredAt.Before(mostRecent) {
			log.Printf("⏰ Missed-event check: %s is up to date (last fired: %s)",
				name, event.LastFiredAt.Format("2006-01-02 15:04:05"))
			continue
		}

		delay := now.Sub(mostRecent)
		if delay > maxDelay {
			log.Printf("⏰ Missed-event check: %s was missed at %s but delay %v exceeds max %v, skipping",
				name, mostRecent.Format("2006-01-02 15:04"), delay, maxDelay)
			continue
		}

		log.Printf("⏰ Missed-event check: %s was missed at %s (delay: %v), firing now!",
			name, mostRecent.Format("2006-01-02 15:04"), delay)

		b.taskWg.Add(1)
		taskName := name
		taskDef := def
		func() {
			defer b.taskWg.Done()
			b.executeWithLock(taskName, taskDef.TimeStr, taskDef.Task)
		}()
	}

	log.Printf("⏰ Missed scheduled events check complete")
}

// checkMissedIntervalEvent checks a single interval task and runs it if overdue.
// No maxDelay filter is applied - interval tasks always run if they are past due.
func (b *Bot) checkMissedIntervalEvent(name string, def scheduledTaskDef) {
	event, err := b.db.GetScheduledEvent(name)
	if err != nil {
		log.Printf("⏰ Missed-event check: %s DB error: %v", name, err)
		return
	}

	if event == nil {
		log.Printf("⏰ Missed-event check: %s has no history, will run at next interval", name)
		return
	}

	elapsed := time.Since(event.LastFiredAt)
	if elapsed < def.Interval {
		log.Printf("⏰ Missed-event check: %s is up to date (last ran %v ago, interval %v)",
			name, elapsed, def.Interval)
		return
	}

	log.Printf("⏰ Missed-event check: %s is overdue (last ran %v ago, interval %v), running now",
		name, elapsed, def.Interval)

	timeLabel := fmt.Sprintf("every %v", def.Interval)
	b.taskWg.Add(1)
	taskName := name
	taskDef := def
	func() {
		defer b.taskWg.Done()
		b.executeWithLock(taskName, timeLabel, taskDef.Task)
	}()
}

// checkIntervalTaskDue checks if an interval-based task is due and runs it once.
func (b *Bot) checkIntervalTaskDue(name string, def scheduledTaskDef) {
	event, err := b.db.GetScheduledEvent(name)
	if err != nil {
		log.Printf("⏰ Webhook trigger: %s DB error: %v", name, err)
		return
	}

	if event == nil {
		log.Printf("⏰ Webhook trigger: %s has no history, running now", name)
	} else {
		elapsed := time.Since(event.LastFiredAt)
		if elapsed < def.Interval {
			log.Printf("⏰ Webhook trigger: %s last ran %v ago (interval %v), skipping", name, elapsed, def.Interval)
			return
		}
		log.Printf("⏰ Webhook trigger: %s last ran %v ago (>= %v), running now", name, elapsed, def.Interval)
	}

	timeLabel := fmt.Sprintf("every %v", def.Interval)
	b.taskWg.Add(1)
	func() {
		defer b.taskWg.Done()
		b.executeWithLock(name, timeLabel, def.Task)
	}()
}

// TriggerScheduledEvents runs all due scheduled events.
// Used in webhook mode where nothing runs automatically.
func (b *Bot) TriggerScheduledEvents() {
	log.Printf("⏰ Webhook trigger: checking and running scheduled events...")

	b.db.InvalidateMuteCache()

	allTasks := b.buildAllTasks()

	// Fire any missed events (daily + interval)
	b.checkMissedEvents(allTasks)

	// Check for expired mutes
	b.checkExpiredMutes()

	log.Printf("⏰ Webhook trigger: finished")
}

// runScheduledTask runs a task at the specified time each day.
func (b *Bot) runScheduledTask(name string, timeStr string, task func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Scheduler %s panicked: %v, restarting...", name, r)
			go b.runScheduledTask(name, timeStr, task)
		}
	}()

	targetTime, err := time.Parse("15:04", timeStr)
	if err != nil {
		log.Printf("Error parsing time %s for task %s: %v", timeStr, name, err)
		return
	}

	log.Printf("Scheduler %s: will run at %s daily", name, timeStr)

	for {
		now := time.Now()

		next := time.Date(now.Year(), now.Month(), now.Day(),
			targetTime.Hour(), targetTime.Minute(), 0, 0, now.Location())

		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}

		duration := next.Sub(now)
		log.Printf("Scheduler %s: next run at %s (in %v)", name, next.Format("2006-01-02 15:04:05"), duration)

		select {
		case <-time.After(duration):
			if ev, err := b.db.GetScheduledEvent(name); err == nil && ev != nil && !ev.LastFiredAt.Before(next) {
				log.Printf("Scheduler %s: occurrence at %s already handled (last_fired_at: %s), skipping",
					name, next.Format("2006-01-02 15:04"), ev.LastFiredAt.Format("2006-01-02 15:04:05"))
				continue
			}

			log.Printf("Scheduler %s: executing task at %s", name, time.Now().Format("15:04:05"))

			b.db.InvalidateMuteCache()
			b.taskWg.Add(1)
			func() {
				defer b.taskWg.Done()
				start := time.Now()
				if b.executeWithLock(name, timeStr, task) {
					log.Printf("Scheduler %s: task completed in %v", name, time.Since(start))
					b.maybeForceGC()
				}
			}()

		case <-b.stopCh:
			log.Printf("Scheduler %s: stopping", name)
			return
		}
	}
}

// runIntervalTask runs a periodic task with DB-tracked execution times.
func (b *Bot) runIntervalTask(eventName string, interval time.Duration, task func()) {
	log.Printf("⏰ Interval task %s: checking last run (interval: %v)", eventName, interval)

	event, err := b.db.GetScheduledEvent(eventName)
	if err != nil {
		log.Printf("⏰ Interval task %s: DB error: %v, will retry on next ticker cycle", eventName, err)
	} else if event == nil {
		log.Printf("⏰ Interval task %s: first run ever, seeding record (will not execute now)", eventName)
		_ = b.db.RecordEventFired(eventName, fmt.Sprintf("every %v", interval))
	} else {
		elapsed := time.Since(event.LastFiredAt)
		if elapsed >= interval {
			log.Printf("⏰ Interval task %s: last run %v ago (>= %v), running now", eventName, elapsed, interval)
			b.db.InvalidateMuteCache()
			timeLabel := fmt.Sprintf("every %v", interval)
			b.taskWg.Add(1)
			func() {
				defer b.taskWg.Done()
				b.executeWithLock(eventName, timeLabel, task)
			}()
		} else {
			remaining := interval - elapsed
			log.Printf("⏰ Interval task %s: last run %v ago, waiting %v before next run", eventName, elapsed, remaining)
			select {
			case <-time.After(remaining):
				log.Printf("⏰ Interval task %s: wait complete, running now", eventName)
				b.db.InvalidateMuteCache()
				timeLabel := fmt.Sprintf("every %v", interval)
				b.taskWg.Add(1)
				func() {
					defer b.taskWg.Done()
					b.executeWithLock(eventName, timeLabel, task)
				}()
			case <-b.stopCh:
				log.Printf("⏰ Interval task %s: stopping during initial wait", eventName)
				return
			}
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Printf("⏰ Interval task %s: ticker fired, running now", eventName)
			b.db.InvalidateMuteCache()
			timeLabel := fmt.Sprintf("every %v", interval)
			b.taskWg.Add(1)
			func() {
				defer b.taskWg.Done()
				b.executeWithLock(eventName, timeLabel, task)
			}()
			b.maybeForceGC()
		case <-b.stopCh:
			log.Printf("⏰ Interval task %s: stopping", eventName)
			return
		}
	}
}

// cleanupOldMessages deletes old messages from the moderation chat.
func (b *Bot) cleanupOldMessages() {
	log.Println("Starting message cleanup...")

	cutoffTime := time.Now().Add(-time.Duration(b.config.MessageDeletion.ChatDeletionRetentionHours) * time.Hour)
	log.Printf("Cleanup cutoff time: %v (messages older than %d hours will be deleted)", cutoffTime, b.config.MessageDeletion.ChatDeletionRetentionHours)

	messages, err := b.db.GetOldMessages(cutoffTime)
	if err != nil {
		log.Printf("Error getting old messages: %v", err)
		return
	}

	log.Printf("Found %d messages to delete", len(messages))

	deletedCount := 0
	for _, msg := range messages {
		err := b.tg.DeleteMessage(msg.ChatID, msg.MessageID)
		if err != nil {
			log.Printf("Error deleting message %d: %v", msg.MessageID, err)
		} else {
			deletedCount++
		}

		err = b.db.RemoveMessageFromDeletion(msg.MessageID, msg.ChatID)
		if err != nil {
			log.Printf("Error removing message from deletion queue: %v", err)
		}

		time.Sleep(250 * time.Millisecond)
	}

	log.Printf("Cleanup completed: deleted %d messages", deletedCount)

	if deletedCount > 0 {
		b.sendToAdminChat(i18n.Tf("cleanup.notification", deletedCount))
	}
}

// startMuteExpirationChecker starts checking for expired mutes.
func (b *Bot) startMuteExpirationChecker() {
	interval := 1 * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.checkExpiredMutes()
		case <-b.stopCh:
			return
		}
	}
}

// checkExpiredMutes checks for and processes expired mutes.
func (b *Bot) checkExpiredMutes() {
	expiredMutes, err := b.db.GetExpiredMutes()
	if err != nil {
		log.Printf("Error getting expired mutes: %v", err)
		return
	}

	for _, mute := range expiredMutes {
		if !mute.IsCruel {
			b.unrestrictUserInChats(mute.UserID, mute.ChatID)
		}

		err = b.db.UnmuteUser(mute.UserID, mute.ChatID)
		if err != nil {
			log.Printf("Error updating mute status in database: %v", err)
		}

		b.sendToAdminChat(fmt.Sprintf("🔓 %s был автоматически разглушен", mute.Username))

		log.Printf("User %d automatically unmuted", mute.UserID)
	}
}

// performDatabaseCleanup executes the actual cleanup operations.
func (b *Bot) performDatabaseCleanup() {
	start := time.Now()
	log.Printf("Starting database cleanup...")

	results, err := b.db.PerformDatabaseCleanup(
		b.config.DatabaseCleanup.MessageRetentionHours,
		b.config.DatabaseCleanup.WarningRetentionHours,
		b.config.DatabaseCleanup.ActionRetentionHours,
		b.config.DatabaseCleanup.PreserveWarnedMutedMessages,
	)

	if err != nil {
		log.Printf("Database cleanup encountered errors: %v", err)
	}

	var totalCleaned int64
	for table, count := range results {
		totalCleaned += count
		if count > 0 {
			log.Printf("Cleaned up %d records from %s", count, table)
		}
	}

	if totalCleaned > 10 {
		log.Printf("Running VACUUM to reclaim disk space after cleaning %d records...", totalCleaned)
		if vacuumErr := b.db.VacuumDatabase(); vacuumErr != nil {
			log.Printf("VACUUM operation failed: %v", vacuumErr)
		}
	}

	duration := time.Since(start)
	log.Printf("Database cleanup completed in %v. Total records cleaned: %d", duration, totalCleaned)

	if totalCleaned > 100 && b.config.Admin.ChatID != 0 && b.config.Debug.DebugTelegram {
		summary := "🧹 Database cleanup completed:\n"
		for table, count := range results {
			if count > 0 {
				summary += fmt.Sprintf("• %s: %d records\n", table, count)
			}
		}
		summary += fmt.Sprintf("Total: %d records cleaned in %v", totalCleaned, duration)

		b.sendToAdminChat(summary)
	}
}
