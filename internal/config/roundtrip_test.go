// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin down the contract of the configuration dump/restore path:
// converting a Config to the flat key/value form stored in the database (or an
// exported file) and back MUST NOT lose or corrupt any value. They are written
// as behavior specifications - "a saved config restores to the same config" -
// so a future refactor of the reflection-based (de)serializer cannot silently
// drop a field.

func floatPtr(f float64) *float64 { return &f }

// fullySpecifiedConfig builds a Config that exercises every value category the
// serializer must handle: scalars (string/int/bool/float), pointer scalars
// (*bool/*float64), the custom aggregate types (ChatIDList, ChatTopicList,
// AIModelConfigs) and slices of structs (RSS feeds, moderation rules,
// per-chat rule overrides).
func fullySpecifiedConfig() *Config {
	cfg := &Config{
		BotToken: "12345:SECRET-TOKEN",
		ProxyURL: "http://proxy.local:8080",
		Language: "en",
	}

	cfg.Database.Provider = "remote"
	cfg.Database.URL = "libsql://example.turso.io"
	cfg.Database.AuthToken = "db-auth-token"

	cfg.Admin.ChatID = -1001234567890
	cfg.Admin.SuperAdminUserID = 42
	cfg.Admin.NotifySuperAdmin = true
	cfg.Admin.WhitelistUserIDs = []int64{7, 8, 9}

	cfg.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100111, -100222}}
	cfg.Moderation.ExcludedTopics = ChatTopicList{Refs: []ChatTopicRef{
		{Chat: -100111, Topic: 5},
		{Chat: -100222, Topic: TopicMain},
	}}
	cfg.Moderation.MuteAcrossAllChats = true

	cfg.MessageDeletion.Enabled = true
	cfg.MessageDeletion.ChatDeletionRetentionHours = 48
	cfg.MessageDeletion.ExcludedUserIDs = []int64{111, 222}

	cfg.WebUI.Enabled = true
	cfg.WebUI.Password = "hashed:pbkdf2-sha256:1000:c2FsdA:a2V5" // pre-hashed
	cfg.WebUI.OTPEnabled = boolPtr(false)

	cfg.AI.Enabled = true
	cfg.AI.ChatRules = "Be nice."
	cfg.AI.ChatRulesOverrides = []ChatRulesOverride{
		{Chat: -100111, Rules: "No links in chat 111."},
		{Chat: -100222, Rules: "English only in chat 222."},
	}
	cfg.AI.LightModel = AIModelConfigs{Configs: []AIModelConfig{
		{Endpoint: "https://light.example", APIKey: "light-key", DeploymentName: "gpt-light", Temperature: floatPtr(0.2)},
	}}
	cfg.AI.FullModel = AIModelConfigs{Configs: []AIModelConfig{
		{Endpoint: "https://full.example", APIKey: "full-key", DeploymentName: "gpt-full", Temperature: floatPtr(0.7), OmitMaxTokens: true},
		{Endpoint: "https://full2.example", APIKey: "full-key-2", DeploymentName: "gpt-full-2"},
	}}
	cfg.AI.ContentModeration.Enabled = true
	cfg.AI.ContentModeration.DefaultMuteMinutes = 30
	cfg.AI.ContentModeration.Rules = []ModerationRule{
		{Trigger: "spam", Action: "delete", Description: "obvious spam"},
		{Trigger: "insult", Action: "warn", NotifyAdmin: boolPtr(false)},
	}
	cfg.AI.Rss.Feeds = []RssFeed{
		{
			Name: "News", URL: "https://news.example/rss", Time: "08:00", Enabled: true,
			Translate: boolPtr(true), MaxMessageLength: 4000,
			PostTo: ChatTopicList{Refs: []ChatTopicRef{{Chat: -100111, Topic: TopicMain}}},
		},
	}

	cfg.UserProfiles.Enabled = true

	cfg.Topics = []TopicNameRef{
		{Chat: -100111, Topic: 7, Name: "Support"},
		{Chat: -100222, Topic: 12, Name: "Off-Topic"},
	}

	return cfg
}

// TestConfigDump_StringMapRoundTripIsLossless is the core "no config is lost"
// guarantee for the database-backed config source: Config → string map →
// Config must reproduce an identical string map. If the round-trip dropped or
// mangled any field, the second serialization would differ.
func TestConfigDump_StringMapRoundTripIsLossless(t *testing.T) {
	original := fullySpecifiedConfig()

	dump := ConfigToStringMap(original)
	require.NotEmpty(t, dump)

	var restored Config
	ApplyStringMap(&restored, dump)

	redump := ConfigToStringMap(&restored)

	// Every key/value that survived the first serialization must survive the
	// restore identically. Comparing the two maps catches any field that fails
	// to round-trip without having to enumerate them by hand.
	assert.Equal(t, dump, redump)
}

// TestConfigDump_PreservesEveryValueCategory spot-checks the restored struct so
// a regression points at the exact field type that broke, not just "the maps
// differ".
func TestConfigDump_PreservesEveryValueCategory(t *testing.T) {
	original := fullySpecifiedConfig()

	var restored Config
	ApplyStringMap(&restored, ConfigToStringMap(original))

	// Scalars
	assert.Equal(t, "12345:SECRET-TOKEN", restored.BotToken, "sensitive string")
	assert.Equal(t, "en", restored.Language)
	assert.Equal(t, int64(-1001234567890), restored.Admin.ChatID, "int64")
	assert.True(t, restored.Admin.NotifySuperAdmin, "bool")
	assert.Equal(t, 48, restored.MessageDeletion.ChatDeletionRetentionHours, "int")

	// Pointer scalars
	require.NotNil(t, restored.WebUI.OTPEnabled)
	assert.False(t, *restored.WebUI.OTPEnabled, "*bool")
	require.Len(t, restored.AI.LightModel.Configs, 1)
	require.NotNil(t, restored.AI.LightModel.Configs[0].Temperature)
	assert.InDelta(t, 0.2, *restored.AI.LightModel.Configs[0].Temperature, 1e-9, "*float64")

	// Custom aggregate: ChatIDList
	assert.Equal(t, []int64{-100111, -100222}, restored.Moderation.ChatIDs.IDs)

	// Custom aggregate: ChatTopicList
	require.Len(t, restored.Moderation.ExcludedTopics.Refs, 2)
	assert.Equal(t, int64(-100111), restored.Moderation.ExcludedTopics.Refs[0].Chat)
	assert.Equal(t, 5, restored.Moderation.ExcludedTopics.Refs[0].Topic)

	// Custom aggregate: AIModelConfigs (multiple entries with mixed pointers)
	require.Len(t, restored.AI.FullModel.Configs, 2)
	assert.Equal(t, "gpt-full", restored.AI.FullModel.Configs[0].DeploymentName)
	assert.True(t, restored.AI.FullModel.Configs[0].OmitMaxTokens)
	assert.Equal(t, "full-key-2", restored.AI.FullModel.Configs[1].APIKey)

	// Slice of structs: moderation rules (incl. *bool field)
	require.Len(t, restored.AI.ContentModeration.Rules, 2)
	assert.Equal(t, "spam", restored.AI.ContentModeration.Rules[0].Trigger)
	assert.Equal(t, "delete", restored.AI.ContentModeration.Rules[0].Action)
	require.NotNil(t, restored.AI.ContentModeration.Rules[1].NotifyAdmin)
	assert.False(t, *restored.AI.ContentModeration.Rules[1].NotifyAdmin)

	// Slice of structs: per-chat rule overrides
	require.Len(t, restored.AI.ChatRulesOverrides, 2)
	assert.Equal(t, int64(-100222), restored.AI.ChatRulesOverrides[1].Chat)
	assert.Equal(t, "English only in chat 222.", restored.AI.ChatRulesOverrides[1].Rules)

	// Slice of structs: RSS feeds (with nested ChatTopicList + *bool)
	require.Len(t, restored.AI.Rss.Feeds, 1)
	assert.Equal(t, "News", restored.AI.Rss.Feeds[0].Name)
	require.Len(t, restored.AI.Rss.Feeds[0].PostTo.Refs, 1)
	assert.Equal(t, int64(-100111), restored.AI.Rss.Feeds[0].PostTo.Refs[0].Chat)
}

// TestConfigRestore_ThroughLoadFromStringMap verifies the production restore
// entry point (LoadFromStringMap, used when the database is the config source)
// preserves the stored values after running the full defaults+validate
// pipeline on top.
func TestConfigRestore_ThroughLoadFromStringMap(t *testing.T) {
	original := fullySpecifiedConfig()
	dump, err := ConfigToDBStringMap(original)
	require.NoError(t, err)

	restored, err := LoadFromStringMap(dump)
	require.NoError(t, err)

	assert.Equal(t, "12345:SECRET-TOKEN", restored.BotToken)
	assert.Equal(t, []int64{-100111, -100222}, restored.Moderation.ChatIDs.IDs)
	require.Len(t, restored.AI.FullModel.Configs, 2)
	assert.Equal(t, "gpt-full", restored.AI.FullModel.Configs[0].DeploymentName)
	require.Len(t, restored.AI.ContentModeration.Rules, 2)
	assert.Equal(t, "spam", restored.AI.ContentModeration.Rules[0].Trigger)
}

// TestConfigDump_ToDBHashesPlaintextPassword documents that the database dump
// path never persists a plaintext web UI password: a bare password is replaced
// with its PBKDF2 hash, while everything else is left untouched.
func TestConfigDump_ToDBHashesPlaintextPassword(t *testing.T) {
	cfg := fullySpecifiedConfig()
	cfg.WebUI.Password = "plaintext-admin-pw"

	dump, err := ConfigToDBStringMap(cfg)
	require.NoError(t, err)

	stored := dump["web_ui.password"]
	assert.NotEqual(t, "plaintext-admin-pw", stored)
	assert.True(t, IsHashedWebUIPassword(stored), "password must be hashed in the DB dump")
	assert.True(t, VerifyWebUIPassword("plaintext-admin-pw", stored), "hash must still verify the original password")
}

// TestConfigExport_HashesPlaintextPassword documents the same guarantee for the
// file/YAML export path used by the "download config" admin action.
func TestConfigExport_HashesPlaintextPassword(t *testing.T) {
	cfg := fullySpecifiedConfig()
	cfg.WebUI.Password = "plaintext-admin-pw"

	exported, err := ConfigForExport(cfg)
	require.NoError(t, err)
	assert.True(t, IsHashedWebUIPassword(exported.WebUI.Password))
	// The original in-memory config must be left unchanged (export works on a copy).
	assert.Equal(t, "plaintext-admin-pw", cfg.WebUI.Password)
}

// TestConfigExport_LeavesAlreadyHashedPasswordUnchanged ensures repeated
// export/restore cycles are idempotent and never double-hash.
func TestConfigExport_LeavesAlreadyHashedPasswordUnchanged(t *testing.T) {
	cfg := fullySpecifiedConfig()
	hashed, err := HashWebUIPasswordForStorage("admin-pw")
	require.NoError(t, err)
	cfg.WebUI.Password = hashed

	exported, err := ConfigForExport(cfg)
	require.NoError(t, err)
	assert.Equal(t, hashed, exported.WebUI.Password)
}
