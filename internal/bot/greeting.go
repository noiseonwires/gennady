// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/i18n"
	"gennadium/internal/telegram"
)

// sendMorningGreeting sends the morning greeting to all moderation chats.
func (b *Bot) sendMorningGreeting() {
	if !b.config.AI.MorningGreeting.Enabled {
		return
	}

	greeting, replacements, rawWeatherJSON, err := b.generateMorningGreeting()
	if err != nil {
		log.Printf("Error generating morning greeting: %v", err)
		return
	}

	var extraParts []string
	if len(replacements) > 0 {
		// Exclude post-processed weather strings from extra_info: the raw weather
		// JSON is included below and is more useful for downstream consumers.
		filtered := make(map[string]string, len(replacements))
		for k, v := range replacements {
			if k == "weather" || k == "weather_ru" {
				continue
			}
			filtered[k] = v
		}
		if len(filtered) > 0 {
			if data, err := json.MarshalIndent(filtered, "", "  "); err == nil {
				extraParts = append(extraParts, "Processed data (replacements):\n"+string(data))
			}
		}
	}
	if rawWeatherJSON != "" {
		extraParts = append(extraParts, "Raw weather API response:\n"+rawWeatherJSON)
	}
	extraInfo := strings.Join(extraParts, "\n\n")

	for _, ref := range b.config.EffectivePostTo(b.config.AI.MorningGreeting.PostTo) {
		sentMsg := b.sendToModerationChatTopic(ref.Chat, fmt.Sprintf("🌅 %s", greeting), ref.Topic)
		if sentMsg != nil {
			b.storeBotMessageInfo(sentMsg)
			if extraInfo != "" {
				if err := b.db.UpdateMessageExtraInfo(sentMsg.MessageID, sentMsg.Chat.ID, extraInfo); err != nil {
					log.Printf("Error storing morning greeting extra info: %v", err)
				}
			}
			log.Printf("Morning greeting sent to chat %d (topic %d) with ID %d, now trackable for replies", ref.Chat, ref.Topic, sentMsg.MessageID)
		}
	}
}

// generateMorningGreeting generates a morning greeting using Azure AI.
// It returns the generated greeting text, the replacements map used for prompt
// substitution, a human-formatted raw weather API JSON response (or empty
// string if unavailable), and any error encountered.
func (b *Bot) generateMorningGreeting() (string, map[string]string, string, error) {
	now := time.Now()

	weatherPart := ""
	weatherPartEN := ""
	holidaysPart := ""
	eventsPart := ""
	rawWeatherJSON := ""

	if w, _, err := b.fetchCurrentWeather(); err == nil && w != nil {
		c := w.Current
		currentWeather := formatWeatherValues(c.Temperature2m, c.WindSpeed10m, c.Rain, c.Snowfall, c.Showers, c.Precipitation, c.WeatherCode, "ru")
		currentWeatherEN := formatWeatherValues(c.Temperature2m, c.WindSpeed10m, c.Rain, c.Snowfall, c.Showers, c.Precipitation, c.WeatherCode, "en")
		weatherPart = fmt.Sprintf("\n\nCейчас: %s.", currentWeather)
		weatherPartEN = fmt.Sprintf("\n\nCurrent: %s.", currentWeatherEN)

		// Track which hourly forecast points we actually reference so the raw
		// JSON stored in extra_info contains only the data we use, not the
		// whole-day response.
		var usedHourIndices []int
		if idx := findHourIndex(w.Hourly.Time, 13); idx >= 0 {
			usedHourIndices = append(usedHourIndices, idx)
			if forecast := formatHourlyWeather(w, idx, "ru"); forecast != "" {
				weatherPart += fmt.Sprintf("\nПрогноз на день (13:00): %s.", forecast)
			}
			if forecastEN := formatHourlyWeather(w, idx, "en"); forecastEN != "" {
				weatherPartEN += fmt.Sprintf("\nMidday forecast (13:00): %s.", forecastEN)
			}
		}

		if idx := findHourIndex(w.Hourly.Time, 19); idx >= 0 {
			usedHourIndices = append(usedHourIndices, idx)
			if forecast := formatHourlyWeather(w, idx, "ru"); forecast != "" {
				weatherPart += fmt.Sprintf("\nПрогноз на вечер (19:00): %s.", forecast)
			}
			if forecastEN := formatHourlyWeather(w, idx, "en"); forecastEN != "" {
				weatherPartEN += fmt.Sprintf("\nEvening forecast (19:00): %s.", forecastEN)
			}
		}

		if usedJSON, jsonErr := buildUsedWeatherJSON(w, usedHourIndices); jsonErr == nil {
			rawWeatherJSON = usedJSON
		} else {
			log.Printf("Could not build used weather JSON: %v", jsonErr)
		}
	} else if err != nil {
		log.Printf("Could not fetch weather: %v", err)
	}

	holidays, err := b.fetchHolidays(b.config.AI.ExternalData.HolidaysCountry)
	hasHoliday := false
	if err == nil && len(holidays) > 0 {
		names := make([]string, 0, len(holidays))
		for _, h := range holidays {
			if len(h.Name) > 0 {
				names = append(names, h.Name[0].Text)
			} else {
				names = append(names, h.Type)
			}
		}
		holidaysPart = strings.Join(names, ", ")
		hasHoliday = true
	} else if err != nil {
		log.Printf("Could not fetch holidays: %v", err)
	}

	if !hasHoliday {
		wikiEvents, err := b.fetchWikipediaOnThisDay()
		if err == nil && len(wikiEvents) > 0 {
			maxEvents := 10
			if len(wikiEvents) > maxEvents {
				wikiEvents = wikiEvents[:maxEvents]
			}

			translatedEvents, err := b.translateWikipediaEvents(wikiEvents)
			if err != nil {
				log.Printf("Could not translate Wikipedia events: %v", err)
				eventsPart = strings.Join(wikiEvents, "\n")
			} else {
				eventsPart = strings.Join(translatedEvents, "\n")
			}
		} else if err != nil {
			log.Printf("Could not fetch Wikipedia events: %v", err)
		}
	} else {
		log.Printf("Skipping Wikipedia events because today is a holiday")
	}

	replacements := map[string]string{
		"weekday":    now.Weekday().String(),
		"date":       now.Format("02.01.2006"),
		"weather":    weatherPartEN,
		"weather_ru": weatherPart,
		"holidays":   holidaysPart,
		"events":     eventsPart,
	}

	systemPrompt := applyReplacements(b.config.AI.MorningGreeting.Prompt.System, replacements)
	prompt := applyReplacements(b.config.AI.MorningGreeting.Prompt.User, replacements)

	b.dumpPromptToLog("morning_greeting", systemPrompt, prompt)

	if !b.config.AI.Enabled || !b.config.AI.MorningGreeting.IsUseAI() {
		return b.composePlainMorningGreeting(weatherPart, holidaysPart, eventsPart), replacements, rawWeatherJSON, nil
	}

	var modelConfigs config.AIModelConfigs
	if b.config.AI.MorningGreeting.UseFullModel {
		modelConfigs = b.config.AI.FullModel
	} else {
		modelConfigs = b.config.AI.LightModel
	}

	result, err := b.callAzureOpenAIWithRetriesAndBackoff("morning_greeting", prompt, systemPrompt, modelConfigs, 0, 4, scheduledTaskBackoff)
	return result, replacements, rawWeatherJSON, err
}

// composePlainMorningGreeting composes a simple morning greeting without AI from available data.
func (b *Bot) composePlainMorningGreeting(weather, holidays, events string) string {
	now := time.Now()
	var parts []string

	parts = append(parts, fmt.Sprintf("%s, %s", now.Weekday().String(), now.Format("02.01.2006")))

	if holidays != "" {
		parts = append(parts, fmt.Sprintf("🎉 %s", holidays))
	}

	if events != "" {
		parts = append(parts, fmt.Sprintf("📅 %s", i18n.T("greeting.events_today")))
		parts = append(parts, events)
	}

	if weather != "" {
		parts = append(parts, fmt.Sprintf("🌤 %s", strings.TrimSpace(weather)))
	}

	return strings.Join(parts, "\n\n")
}

// sendDailySummary sends a daily summary of chat discussions to all moderation chats.
func (b *Bot) sendDailySummary() {
	if !b.config.AI.DailySummary.Enabled {
		return
	}

	// Group post destinations by chat so we generate one summary per chat even
	// when PostTo lists the same chat with multiple topics.
	postTargets := b.config.EffectivePostTo(b.config.AI.DailySummary.PostTo)
	byChat := make(map[int64][]int, len(postTargets))
	order := make([]int64, 0, len(postTargets))
	for _, ref := range postTargets {
		if _, seen := byChat[ref.Chat]; !seen {
			order = append(order, ref.Chat)
		}
		byChat[ref.Chat] = append(byChat[ref.Chat], ref.Topic)
	}

	for _, chatID := range order {
		summary, err := b.generateDailySummaryForChat(chatID)
		if err != nil {
			log.Printf("Error generating daily summary for chat %d: %v", chatID, err)
			continue
		}

		chatName := b.getChatNameShort(chatID)

		b.dumpPromptToLog("daily_summary_result", chatName, summary)

		var sentMsg *telegram.Message
		for _, topic := range byChat[chatID] {
			msg := b.sendToModerationChatPermanent(chatID, fmt.Sprintf("📊 %s\n\n%s", chatName, summary), topic)
			log.Printf("Daily summary sent to chat %d (topic %d, %s)", chatID, topic, chatName)
			if sentMsg == nil {
				sentMsg = msg
			}
		}

		// Store daily summary in message_info with adjusted timestamp so it won't be
		// included in the next summary's dataset: next_run_time - period - 1h
		if sentMsg != nil {
			adjustedTS := b.dailySummaryAdjustedTimestamp()
			info := &database.MessageInfo{
				MessageID:       sentMsg.MessageID,
				ChatID:          sentMsg.Chat.ID,
				UserID:          b.botSelf.ID,
				Username:        b.botSelf.Username,
				Text:            sentMsg.Text,
				MessageThreadID: messageTopic(sentMsg),
				Timestamp:       adjustedTS,
			}
			if err := b.db.StoreMessageInfo(info); err != nil {
				log.Printf("Error storing daily summary message info: %v", err)
			}
		}
	}
}

// dailySummaryAdjustedTimestamp returns a timestamp for storing the daily summary
// in message_info such that it falls outside the next summary's collection window.
// Formula: next_run_time - summary_period(23h) - 1h.
func (b *Bot) dailySummaryAdjustedTimestamp() time.Time {
	now := time.Now()
	targetTime, err := time.Parse("15:04", b.config.AI.DailySummary.Time)
	if err != nil {
		// Fallback: just subtract 24h from now
		return now.Add(-25 * time.Hour)
	}
	nextRun := time.Date(now.Year(), now.Month(), now.Day(),
		targetTime.Hour(), targetTime.Minute(), 0, 0, now.Location())
	if !nextRun.After(now) {
		nextRun = nextRun.Add(24 * time.Hour)
	}
	return nextRun.Add(-23 * time.Hour).Add(-1 * time.Hour)
}

// generateDailySummaryForChat generates a daily summary for a specific chat.
func (b *Bot) generateDailySummaryForChat(chatID int64) (string, error) {
	if !b.config.AI.Enabled {
		return "", fmt.Errorf("Azure AI is not enabled")
	}

	messages, err := b.db.GetRecentMessagesWithUsernames(chatID, 23, 0)
	if err != nil {
		return "", fmt.Errorf("failed to get recent messages: %v", err)
	}

	if len(messages) == 0 {
		return "", fmt.Errorf("No messages to process")
	}

	processedMsgs := 0
	skippedMsgs := 0
	var messageTexts []string
	for _, msg := range messages {
		if msg != "" && len(msg) > 10 {
			if len(msg) > 1500 {
				msg = msg[:1500] + "..."
			}
			messageTexts = append(messageTexts, msg+"\n ")
			processedMsgs++
		} else {
			skippedMsgs++
		}

		if processedMsgs >= 1500 {
			break
		}
	}

	log.Printf("Daily summary for chat %d: %d messages retrieved, %d processed, %d skipped (too short)", chatID, len(messages), processedMsgs, skippedMsgs)

	if len(messageTexts) < 10 {
		return i18n.T("ai.daily_summary_quiet"), nil
	}

	combinedText := strings.Join(messageTexts, "\n")

	replacements := map[string]string{"messages": combinedText}
	systemPrompt := applyReplacements(b.config.AI.DailySummary.Prompt.System, replacements)
	prompt := applyReplacements(b.config.AI.DailySummary.Prompt.User, replacements)

	b.dumpPromptToLog("daily_summary", systemPrompt, prompt)

	if b.config.AI.DailySummary.UseFullModel {
		result, err := b.callAzureOpenAIWithRetriesAndBackoff("daily_summary", prompt, systemPrompt, b.config.AI.FullModel, 1000, 4, scheduledTaskBackoff)
		if err != nil {
			if isRetryableError(err) {
				log.Printf("Full model failed for daily summary after retries (%v), trying fallback to light model", err)
				return b.callAzureOpenAIWithRetriesAndBackoff("daily_summary", prompt, systemPrompt, b.config.AI.LightModel, 1000, 3, scheduledTaskBackoff)
			}
			return "", err
		}
		return result, nil
	}

	return b.callAzureOpenAIWithRetriesAndBackoff("daily_summary", prompt, systemPrompt, b.config.AI.LightModel, 1000, 4, scheduledTaskBackoff)
}
