// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// --- types.go RSS feed optional-bool predicates -------------------------------

func TestRssFeedPredicates(t *testing.T) {
	f := &RssFeed{}
	assert.True(t, f.IsTranslate())       // defaults true
	assert.True(t, f.IsSummarizeIfLong()) // defaults true

	f.Translate = boolPtr(false)
	f.SummarizeIfLong = boolPtr(false)
	assert.False(t, f.IsTranslate())
	assert.False(t, f.IsSummarizeIfLong())
}

// --- types_custom.go YAML marshalers ------------------------------------------

func TestChatIDListYAMLMarshal(t *testing.T) {
	// Single element marshals as a scalar.
	single := ChatIDList{IDs: []int64{-100}}
	out, err := yaml.Marshal(single)
	require.NoError(t, err)
	assert.Contains(t, string(out), "-100")

	// Round-trip a multi-element list.
	multi := ChatIDList{IDs: []int64{-100, -200}}
	out, err = yaml.Marshal(multi)
	require.NoError(t, err)
	var back ChatIDList
	require.NoError(t, yaml.Unmarshal(out, &back))
	assert.Equal(t, []int64{-100, -200}, back.IDs)
}

func TestChatIDListYAMLUnmarshalScalar(t *testing.T) {
	var l ChatIDList
	require.NoError(t, yaml.Unmarshal([]byte("-100"), &l))
	assert.Equal(t, []int64{-100}, l.IDs)

	// Zero is dropped (treated as unset).
	var z ChatIDList
	require.NoError(t, yaml.Unmarshal([]byte("0"), &z))
	assert.Empty(t, z.IDs)
}

func TestAIModelConfigsYAML(t *testing.T) {
	// Single object form.
	var single AIModelConfigs
	require.NoError(t, yaml.Unmarshal([]byte("endpoint: https://x\ndeployment_name: dep\n"), &single))
	require.Equal(t, 1, single.Count())
	assert.Equal(t, "https://x", single.Get(0).Endpoint)

	// Marshal of a single config emits an object (round-trips).
	out, err := yaml.Marshal(single)
	require.NoError(t, err)
	var back AIModelConfigs
	require.NoError(t, yaml.Unmarshal(out, &back))
	assert.Equal(t, "https://x", back.Get(0).Endpoint)

	// Sequence form.
	var multi AIModelConfigs
	require.NoError(t, yaml.Unmarshal([]byte("- endpoint: https://a\n- endpoint: https://b\n"), &multi))
	assert.Equal(t, 2, multi.Count())

	out, err = yaml.Marshal(multi)
	require.NoError(t, err)
	assert.Contains(t, string(out), "https://a")
}

func TestChatTopicListYAMLMarshal(t *testing.T) {
	list := ChatTopicList{Refs: []ChatTopicRef{{Chat: -100, Topic: 5}}}
	out, err := yaml.Marshal(list)
	require.NoError(t, err)
	var back ChatTopicList
	require.NoError(t, yaml.Unmarshal(out, &back))
	require.Len(t, back.Refs, 1)
	assert.Equal(t, ChatTopicRef{Chat: -100, Topic: 5}, back.Refs[0])

	// Nil refs marshal to an empty sequence.
	out, err = yaml.Marshal(ChatTopicList{})
	require.NoError(t, err)
	assert.Contains(t, string(out), "[]")
}

// --- config_reflect.go setChatTopicList / refFromInterface --------------------

func TestSetConfigValue_ChatTopicList(t *testing.T) {
	c := &Config{}

	// Object array form (what the web UI sends).
	res := SetConfigValue(c, "moderation.excluded_topics", []interface{}{
		map[string]interface{}{"chat": float64(-100), "topic": float64(5)},
		float64(7), // bare topic
	})
	assert.Equal(t, "ok", res)
	require.Len(t, c.Moderation.ExcludedTopics.Refs, 2)
	assert.Equal(t, ChatTopicRef{Chat: -100, Topic: 5}, c.Moderation.ExcludedTopics.Refs[0])
	assert.Equal(t, ChatTopicRef{Chat: 0, Topic: 7}, c.Moderation.ExcludedTopics.Refs[1])

	// Compact string form.
	c2 := &Config{}
	assert.Equal(t, "ok", SetConfigValue(c2, "moderation.excluded_topics", "-100:5,-200:any"))
	require.Len(t, c2.Moderation.ExcludedTopics.Refs, 2)

	// nil clears.
	assert.Equal(t, "ok", SetConfigValue(c2, "moderation.excluded_topics", nil))
	assert.Empty(t, c2.Moderation.ExcludedTopics.Refs)

	// Invalid topic in string form.
	c3 := &Config{}
	assert.NotEqual(t, "ok", SetConfigValue(c3, "moderation.excluded_topics", "-100:notatopic"))
}

func TestSetConfigValue_SliceField(t *testing.T) {
	c := &Config{}

	// Int64 slice via array.
	assert.Equal(t, "ok", SetConfigValue(c, "admin.whitelist_user_ids", []interface{}{float64(1), float64(2)}))
	assert.Equal(t, []int64{1, 2}, c.Admin.WhitelistUserIDs)

	// Int slice via comma string.
	assert.Equal(t, "ok", SetConfigValue(c, "admin.reply_message_ids", "10,20,30"))
	assert.Equal(t, []int{10, 20, 30}, c.Admin.ReplyMessageIDs)
}

// --- config_db.go ConfigToDBStringMap (hashes the password) -------------------

func TestConfigToDBStringMap(t *testing.T) {
	cfg := &Config{}
	cfg.WebUI.Password = "plaintext"
	kv, err := ConfigToDBStringMap(cfg)
	require.NoError(t, err)
	assert.True(t, IsHashedWebUIPassword(kv["web_ui.password"]))
}

// --- password_hash.go edge cases ----------------------------------------------

func TestHashAndVerifyWebUIPassword_EdgeCases(t *testing.T) {
	// Empty password passes through unchanged.
	out, err := HashWebUIPasswordForStorage("")
	require.NoError(t, err)
	assert.Equal(t, "", out)

	hashed, err := HashWebUIPasswordForStorage("s3cret")
	require.NoError(t, err)
	assert.True(t, IsHashedWebUIPassword(hashed))

	// Re-hashing an already-hashed value is a no-op.
	again, err := HashWebUIPasswordForStorage(hashed)
	require.NoError(t, err)
	assert.Equal(t, hashed, again)

	// Verify against hashed.
	assert.True(t, VerifyWebUIPassword("s3cret", hashed))
	assert.False(t, VerifyWebUIPassword("wrong", hashed))

	// Plaintext expected (legacy) uses constant-time compare.
	assert.True(t, VerifyWebUIPassword("plain", "plain"))
	assert.False(t, VerifyWebUIPassword("plain", "other"))

	// Malformed hashed value is rejected.
	assert.False(t, VerifyWebUIPassword("x", "hashed:pbkdf2-sha256:bad"))
}
