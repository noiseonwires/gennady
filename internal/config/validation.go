// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
)

// Configuration validation.
//
// Two flavors today:
//
//   - Hard rejection via (*Config).Validate(): a small set of rules that
//     make the config unusable. Called from Load() and may be re-used by any
//     other caller that builds a Config out-of-band (e.g. LoadFromStringMap
//     for DB-as-config-source mode).
//
//   - Prompt validation: walks the AI config and reports system/user prompts
//     that are empty even though the owning feature is enabled. Surfaced as
//     log warnings at boot (WarnMissingPrompts), as an error response on web
//     UI save (ValidatePrompts), and as a list on the diagnostics page
//     (CollectPromptWarnings).
//
//   - Required-field checks: MissingConfigFields / HasMissingConfig flag the
//     handful of values the bot literally cannot start without (bot token,
//     admin chat ID, at least one moderation chat). These are advisory -
//     the web UI uses them to render a setup banner rather than refusing to
//     start, because we want the operator to be able to log in and fix things.

// Validate runs every hard-rejection rule and returns all violations found,
// joined into a single error (one per line). "Hard" means the config is
// structurally unusable; missing optional features (e.g. AI prompts) are
// reported via WarnMissingPrompts instead so the bot can still start in a
// degraded mode.
//
// Validation does not stop at the first problem: every rule runs so the
// operator sees the complete list of things to fix in one pass.
func (c *Config) Validate() error {
	var errs []error
	if c.Webhook.Enabled && c.Webhook.URL == "" {
		errs = append(errs, fmt.Errorf("webhook.url is required when webhook is enabled"))
	}
	if err := c.NormalizeChatTopicLists(); err != nil {
		errs = append(errs, err)
	}
	if err := c.validateChatRulesOverrides(); err != nil {
		errs = append(errs, err)
	}
	if err := c.validatePostToDestinations(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// validatePostToDestinations rejects the "any topic" wildcard (-1) in fields
// that publish bot-authored messages: a destination must be a concrete place
// (the main area = 0, or a specific forum thread). The wildcard is only
// meaningful for scope filters (included/excluded topics).
func (c *Config) validatePostToDestinations() error {
	var errs []error
	check := func(path string, list ChatTopicList) {
		for i, ref := range list.Refs {
			if ref.Topic == TopicAny {
				errs = append(errs, fmt.Errorf("%s[%d]: topic 'any' (-1) is not a valid destination; use 'main' (0) or a specific topic id", path, i))
			}
		}
	}
	check("ai.morning_greeting.post_to", c.AI.MorningGreeting.PostTo)
	check("ai.daily_summary.post_to", c.AI.DailySummary.PostTo)
	for i := range c.AI.Rss.Feeds {
		check(fmt.Sprintf("ai.rss.feeds[%d].post_to", i), c.AI.Rss.Feeds[i].PostTo)
	}
	return errors.Join(errs...)
}

// NormalizeChatTopicLists walks every ChatTopicList field on Config and
// resolves bare-int topic refs (Chat==0) against moderation.chat_id. Fails
// when a bare ref appears alongside multiple configured moderation chats, or
// when an explicit chat ref points at a chat that isn't being moderated.
//
// Skipped when moderation.chat_id is empty (early-init / setup mode).
func (c *Config) NormalizeChatTopicLists() error {
	allowed := c.Moderation.ChatIDs.All()
	if len(allowed) == 0 {
		return nil
	}
	var errs []error
	// Walk every ChatTopicList field. The callback never returns an error so the
	// walk visits all fields; per-field violations are collected and joined.
	_ = walkChatTopicLists(reflect.ValueOf(c).Elem(), "", func(path string, list *ChatTopicList) error {
		if err := list.Normalize(path, allowed); err != nil {
			errs = append(errs, err)
		}
		return nil
	})
	return errors.Join(errs...)
}

func walkChatTopicLists(v reflect.Value, prefix string, fn func(path string, list *ChatTopicList) error) error {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		ft := t.Field(i)
		yamlTag := strings.Split(ft.Tag.Get("yaml"), ",")[0]
		if yamlTag == "" || yamlTag == "-" {
			continue
		}
		path := yamlTag
		if prefix != "" {
			path = prefix + "." + yamlTag
		}
		if field.CanAddr() {
			if ptr, ok := field.Addr().Interface().(*ChatTopicList); ok {
				if err := fn(path, ptr); err != nil {
					return err
				}
				continue
			}
		}
		switch field.Kind() {
		case reflect.Struct:
			if err := walkChatTopicLists(field, path, fn); err != nil {
				return err
			}
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.Struct {
				for j := 0; j < field.Len(); j++ {
					if err := walkChatTopicLists(field.Index(j), fmt.Sprintf("%s[%d]", path, j), fn); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (c *Config) validateChatRulesOverrides() error {
	if len(c.AI.ChatRulesOverrides) == 0 {
		return nil
	}
	allowed := c.Moderation.ChatIDs.All()
	if len(allowed) == 0 {
		return nil
	}
	var errs []error
	seen := make(map[int64]bool, len(c.AI.ChatRulesOverrides))
	for i, ovr := range c.AI.ChatRulesOverrides {
		if ovr.Chat == 0 {
			errs = append(errs, fmt.Errorf("ai.chat_rules_overrides[%d]: chat is required", i))
			continue
		}
		known := false
		for _, id := range allowed {
			if id == ovr.Chat {
				known = true
				break
			}
		}
		if !known {
			errs = append(errs, fmt.Errorf("ai.chat_rules_overrides[%d]: chat %d is not listed in moderation.chat_id", i, ovr.Chat))
			continue
		}
		if seen[ovr.Chat] {
			errs = append(errs, fmt.Errorf("ai.chat_rules_overrides[%d]: chat %d appears more than once", i, ovr.Chat))
			continue
		}
		seen[ovr.Chat] = true
	}
	return errors.Join(errs...)
}

// ValidatePrompts checks that all enabled AI features have their prompts
// properly configured. Returns an error on the first issue found (used by web API).
func (c *AzureAIConfig) ValidatePrompts() error {
	if warnings := c.CollectPromptWarnings(); len(warnings) > 0 {
		return fmt.Errorf("%s", warnings[0])
	}
	return nil
}

// WarnMissingPrompts logs warnings for any AI prompts that are incomplete
// when the corresponding feature is enabled. The bot still starts, but
// affected features may not work correctly.
func (c *AzureAIConfig) WarnMissingPrompts() {
	for _, w := range c.CollectPromptWarnings() {
		log.Printf("⚠️  %s", w)
	}
}

// CollectPromptWarnings returns a list of human-readable warning strings for
// any enabled features whose prompts are incomplete or missing.
func (c *AzureAIConfig) CollectPromptWarnings() []string {
	var warnings []string

	// Check all PromptPair fields via reflection
	collectPromptsRecursive(reflect.ValueOf(*c), "ai", &warnings)

	// Check enabled features have their prompts configured
	type check struct {
		enabled bool
		name    string
		prompts []PromptPair
	}

	anyRssEnabled := false
	for _, f := range c.Rss.Feeds {
		if f.Enabled {
			anyRssEnabled = true
			break
		}
	}

	checks := []check{
		{c.ContentModeration.Enabled, "content_moderation", []PromptPair{c.ContentModeration.Prompt, c.ContentModeration.WarningPrompt}},
		{c.CreativeReplies.Enabled, "creative_replies", []PromptPair{c.CreativeReplies.Prompt}},
		{c.MorningGreeting.Enabled, "morning_greeting", []PromptPair{c.MorningGreeting.Prompt}},
		{c.DailySummary.Enabled, "daily_summary", []PromptPair{c.DailySummary.Prompt}},
		{c.MessageSummaries.Enabled, "message_summaries", []PromptPair{c.MessageSummaries.Prompt}},
		{c.LinkSummaries.Enabled, "link_summaries", []PromptPair{c.LinkSummaries.Prompt}},
		{c.LinkSummaries.Enabled || c.MorningGreeting.Enabled, "translation_prompt", []PromptPair{c.TranslationPrompt}},
		{anyRssEnabled, "rss (translation_prompt)", []PromptPair{c.Rss.TranslationPrompt}},
		{anyRssEnabled, "rss (summary_prompt)", []PromptPair{c.Rss.SummaryPrompt}},
	}

	for _, ch := range checks {
		if !ch.enabled {
			continue
		}
		for _, pp := range ch.prompts {
			if pp.System == "" || pp.User == "" {
				warnings = append(warnings, fmt.Sprintf("Feature '%s' is enabled but its prompts are not configured", ch.name))
			}
		}
	}
	return warnings
}

func collectPromptsRecursive(v reflect.Value, prefix string, warnings *[]string) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := v.Field(i)
		ft := t.Field(i)
		yamlTag := ft.Tag.Get("yaml")
		tagName := strings.Split(yamlTag, ",")[0]
		if tagName == "" || tagName == "-" {
			continue
		}
		fullKey := prefix + "." + tagName

		fieldType := ft.Type
		if fieldType.Kind() == reflect.Ptr {
			if field.IsNil() {
				continue
			}
			field = field.Elem()
			fieldType = fieldType.Elem()
		}

		if fieldType == reflect.TypeOf(PromptPair{}) {
			pp := field.Interface().(PromptPair)
			// Skip completely empty prompts - feature-specific checks handle enabled features.
			if pp.System == "" && pp.User == "" {
				continue
			}
			if pp.System == "" {
				*warnings = append(*warnings, fmt.Sprintf("Prompt '%s' is missing 'system' field", fullKey))
			}
			if pp.User == "" {
				*warnings = append(*warnings, fmt.Sprintf("Prompt '%s' is missing 'user' field", fullKey))
			}
			continue
		}

		if fieldType.Kind() == reflect.Struct {
			collectPromptsRecursive(field, fullKey, warnings)
		}
	}
}

// HasMissingConfig returns true if required configuration values are missing.
func (c *Config) HasMissingConfig() bool {
	return len(c.MissingConfigFields()) > 0
}

// MissingConfigFields returns the list of required configuration fields that are missing or empty.
func (c *Config) MissingConfigFields() []string {
	var missing []string
	if c.BotToken == "" {
		missing = append(missing, "bot_token")
	}
	if c.Admin.ChatID == 0 {
		missing = append(missing, "admin.chat_id")
	}
	if c.Moderation.ChatIDs.Count() == 0 {
		missing = append(missing, "moderation.chat_ids")
	}
	return missing
}

// NormalizeModerationAction lower-cases and trims s and rewrites legacy values
// for backward compatibility. The historical "ban" action was removed and is
// now silently treated as "mute" so existing configs keep working.
func NormalizeModerationAction(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "ban" {
		return ModerationActionMute
	}
	return s
}

// IsValidModerationAction reports whether s is a recognized auto-action.
func IsValidModerationAction(s string) bool {
	switch s {
	case ModerationActionReport, ModerationActionWarn, ModerationActionMute, ModerationActionDelete:
		return true
	}
	return false
}
