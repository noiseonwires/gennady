// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- env_apply.go setSliceFromEnv (scalar slices via env) ---------------------

func TestApplyEnvOverrides_ScalarSlices(t *testing.T) {
	t.Setenv("ADMIN_REPLY_MESSAGE_IDS", "10,20,30") // []int
	t.Setenv("ADMIN_WHITELIST_USER_IDS", "1,2,3")   // []int64

	c := &Config{}
	applyEnvOverrides(c)
	assert.Equal(t, []int{10, 20, 30}, c.Admin.ReplyMessageIDs)
	assert.Equal(t, []int64{1, 2, 3}, c.Admin.WhitelistUserIDs)
}

// --- validation.go NormalizeChatTopicLists ------------------------------------

func TestNormalizeChatTopicLists_ResolvesBareTopic(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}
	// A bare topic (Chat==0) resolves to the single moderation chat.
	c.MessageDeletion.ExcludedTopics = ChatTopicList{Refs: []ChatTopicRef{{Chat: 0, Topic: 5}}}
	require.NoError(t, c.NormalizeChatTopicLists())
	assert.Equal(t, int64(-100), c.MessageDeletion.ExcludedTopics.Refs[0].Chat)
}

func TestNormalizeChatTopicLists_AmbiguousBareTopic(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100, -200}}
	// Bare topic with multiple moderation chats is ambiguous -> error.
	c.MessageDeletion.ExcludedTopics = ChatTopicList{Refs: []ChatTopicRef{{Chat: 0, Topic: 5}}}
	require.Error(t, c.NormalizeChatTopicLists())
}

func TestNormalizeChatTopicLists_UnknownChat(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}
	// Explicit chat not in moderation list -> error.
	c.MessageDeletion.ExcludedTopics = ChatTopicList{Refs: []ChatTopicRef{{Chat: -999, Topic: 5}}}
	require.Error(t, c.NormalizeChatTopicLists())
}

func TestNormalizeChatTopicLists_NoModerationChat(t *testing.T) {
	c := &Config{}
	// No moderation chat configured -> normalization is skipped (no error).
	c.MessageDeletion.ExcludedTopics = ChatTopicList{Refs: []ChatTopicRef{{Chat: 0, Topic: 5}}}
	require.NoError(t, c.NormalizeChatTopicLists())
}

// --- config_db.go LoadFromStringMap error path --------------------------------

func TestLoadFromStringMap_ValidationError(t *testing.T) {
	// webhook enabled without a URL must fail validation.
	_, err := LoadFromStringMap(map[string]string{
		"webhook.enabled": "true",
	})
	require.Error(t, err)
}

func TestLoadFromStringMap_OK(t *testing.T) {
	cfg, err := LoadFromStringMap(map[string]string{
		"bot_token":          "t",
		"language":           "en",
		"moderation.chat_id": "-100",
	})
	require.NoError(t, err)
	assert.Equal(t, "t", cfg.BotToken)
	assert.True(t, cfg.IsModerationChat(-100))
}

// --- types_custom.go nil-receiver branches ------------------------------------

func TestChatTopicListNilReceiver(t *testing.T) {
	var c *ChatTopicList
	assert.Nil(t, c.All())
	assert.Equal(t, 0, c.Count())
	assert.False(t, c.Matches(-100, 1))
	assert.True(t, c.AppliesTo(-100, 1)) // nil/empty included matches everything
}

// --- unknown_keys.go slice-of-struct path (findInnerSliceElem) ----------------

func TestLoad_UnknownKeyInModelSlice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// An unknown key nested inside a full_model entry exercises the
	// slice-of-struct branch of warnUnknownKeys / findInnerSliceElem.
	yaml := "" +
		"bot_token: t\n" +
		"ai:\n" +
		"  full_model:\n" +
		"    - endpoint: https://x\n" +
		"      bogus_model_key: 1\n"
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "https://x", cfg.AI.FullModel.Get(0).Endpoint)
}
