// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- defaults.go --------------------------------------------------------------

func TestSetDefaults(t *testing.T) {
	c := &Config{}
	setDefaults(c)
	assert.Equal(t, "local", c.Database.Provider)
	assert.Equal(t, "./db/moderation.db", c.Database.Path)
	assert.Equal(t, "🤔", c.Reactions.SuspiciousMessage)
	assert.Equal(t, "🍌", c.Reactions.BadMessage)
	assert.NotZero(t, c.MessageDeletion.CleanupIntervalHours)
	assert.Equal(t, 24, c.AI.ContentModeration.NewUserWindowHours)
	assert.Equal(t, 500, c.AI.ContentModeration.ReplyContextMaxChars)
	// Update-processing defaults: in-order single worker, stats on every 10 min.
	assert.Equal(t, 1, c.UpdateProcessing.Workers)
	assert.Equal(t, 600, c.UpdateProcessing.StatsIntervalSeconds)

	// Remote auto-detection from URL + token.
	c2 := &Config{}
	c2.Database.URL = "libsql://x"
	c2.Database.AuthToken = "tok"
	setDefaults(c2)
	assert.Equal(t, "remote", c2.Database.Provider)

	// Explicit provider preserved (lower-cased).
	c3 := &Config{}
	c3.Database.Provider = "REMOTE"
	setDefaults(c3)
	assert.Equal(t, "remote", c3.Database.Provider)
}

// --- config_reflect.go --------------------------------------------------------

func TestReflectConfigMeta(t *testing.T) {
	fields, sections := ReflectConfigMeta()
	require.NotEmpty(t, fields)
	require.NotEmpty(t, sections)

	// A known scalar field is present with the right type.
	var found bool
	for _, f := range fields {
		if f.Key == "bot_token" {
			found = true
			assert.Equal(t, "string", f.Type)
		}
	}
	assert.True(t, found, "expected bot_token in field metadata")
}

func TestSensitiveConfigKeys(t *testing.T) {
	keys := SensitiveConfigKeys()
	require.NotEmpty(t, keys)
	// web_ui.password and bot_token are tagged web:"sensitive".
	assert.True(t, keys["web_ui.password"])
	assert.True(t, keys["bot_token"])
	// language is a plain non-sensitive field.
	assert.False(t, keys["language"])
}

func TestGetConfigValues(t *testing.T) {
	c := &Config{BotToken: "tok", Language: "ru"}
	c.Admin.ChatID = -111
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}
	c.Server.ListenPort = 9090

	vals := GetConfigValues(c)
	assert.Equal(t, "tok", vals["bot_token"])
	assert.Equal(t, int64(-111), vals["admin.chat_id"])
	assert.Equal(t, int64(9090), vals["server.listen_port"])
	assert.Equal(t, []int64{-100}, vals["moderation.chat_id"])
}

func TestSetConfigValue_Scalars(t *testing.T) {
	c := &Config{}

	assert.Equal(t, "ok", SetConfigValue(c, "bot_token", "newtoken"))
	assert.Equal(t, "newtoken", c.BotToken)

	assert.Equal(t, "ok", SetConfigValue(c, "admin.chat_id", float64(-555)))
	assert.Equal(t, int64(-555), c.Admin.ChatID)

	assert.Equal(t, "ok", SetConfigValue(c, "web_ui.enabled", true))
	assert.True(t, c.WebUI.Enabled)

	// String -> int coercion.
	assert.Equal(t, "ok", SetConfigValue(c, "server.listen_port", "8081"))
	assert.Equal(t, 8081, c.Server.ListenPort)

	// String -> bool coercion.
	c.WebUI.Enabled = false
	assert.Equal(t, "ok", SetConfigValue(c, "web_ui.enabled", "yes"))
	assert.True(t, c.WebUI.Enabled)

	// Unknown key -> empty result.
	assert.Equal(t, "", SetConfigValue(c, "no.such.key", "x"))

	// Wrong type for string field.
	assert.Equal(t, "expected string", SetConfigValue(c, "bot_token", 123))
}

func TestSetConfigValue_ChatIDList(t *testing.T) {
	c := &Config{}
	assert.Equal(t, "ok", SetConfigValue(c, "moderation.chat_id", "-100, -200"))
	assert.Equal(t, []int64{-100, -200}, c.Moderation.ChatIDs.IDs)

	c2 := &Config{}
	assert.Equal(t, "ok", SetConfigValue(c2, "moderation.chat_id", []interface{}{float64(-1), float64(-2)}))
	assert.Equal(t, []int64{-1, -2}, c2.Moderation.ChatIDs.IDs)

	// Invalid integer in string form.
	c3 := &Config{}
	assert.Contains(t, SetConfigValue(c3, "moderation.chat_id", "abc"), "invalid integer")
}

func TestSetConfigValue_PointerBool(t *testing.T) {
	c := &Config{}
	// web_ui.otp_enabled is *bool.
	assert.Equal(t, "ok", SetConfigValue(c, "web_ui.otp_enabled", true))
	require.NotNil(t, c.WebUI.OTPEnabled)
	assert.True(t, *c.WebUI.OTPEnabled)

	// Setting null clears the pointer.
	assert.Equal(t, "ok", SetConfigValue(c, "web_ui.otp_enabled", nil))
	assert.Nil(t, c.WebUI.OTPEnabled)
}

func TestParseIntTo(t *testing.T) {
	var n int64
	_, bad := parseIntTo(&n, "-123")
	assert.False(t, bad)
	assert.Equal(t, int64(-123), n)

	_, bad = parseIntTo(&n, "45")
	assert.False(t, bad)
	assert.Equal(t, int64(45), n)

	_, bad = parseIntTo(&n, "12x")
	assert.True(t, bad)
}

func TestSplitListValue(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, splitListValue("a,b;c"))
	assert.Empty(t, splitListValue(""))
}

// --- env_apply.go (via applyEnvOverrides) -------------------------------------

func TestApplyEnvOverrides(t *testing.T) {
	t.Setenv("BOT_TOKEN", "env-token")
	t.Setenv("SERVER_LISTEN_PORT", "7000")
	t.Setenv("WEB_UI_ENABLED", "true")
	t.Setenv("MODERATION_CHAT_ID", "-100,-200")

	c := &Config{}
	applyEnvOverrides(c)
	assert.Equal(t, "env-token", c.BotToken)
	assert.Equal(t, 7000, c.Server.ListenPort)
	assert.True(t, c.WebUI.Enabled)
	assert.Equal(t, []int64{-100, -200}, c.Moderation.ChatIDs.IDs)
}

func TestApplyEnvOverrides_IndexedModelSlice(t *testing.T) {
	t.Setenv("AI_FULL_MODEL_0_ENDPOINT", "https://e0")
	t.Setenv("AI_FULL_MODEL_0_DEPLOYMENT_NAME", "dep0")
	t.Setenv("AI_FULL_MODEL_1_ENDPOINT", "https://e1")

	c := &Config{}
	applyEnvOverrides(c)
	require.GreaterOrEqual(t, c.AI.FullModel.Count(), 2)
	assert.Equal(t, "https://e0", c.AI.FullModel.Get(0).Endpoint)
	assert.Equal(t, "dep0", c.AI.FullModel.Get(0).DeploymentName)
	assert.Equal(t, "https://e1", c.AI.FullModel.Get(1).Endpoint)
}

// --- config_db.go ApplyStringMap ----------------------------------------------

func TestApplyStringMap(t *testing.T) {
	c := &Config{}
	ApplyStringMap(c, map[string]string{
		"bot_token":          "sm-token",
		"server.listen_port": "8082",
		"web_ui.enabled":     "true",
		"moderation.chat_id": "-100,-200",
	})
	assert.Equal(t, "sm-token", c.BotToken)
	assert.Equal(t, 8082, c.Server.ListenPort)
	assert.True(t, c.WebUI.Enabled)
	assert.Equal(t, []int64{-100, -200}, c.Moderation.ChatIDs.IDs)
}

// --- unknown_keys.go (via Load) -----------------------------------------------

func TestLoad_WarnsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// "totally_unknown" should be warned about but not fail the load.
	yaml := "bot_token: t\ntotally_unknown: 1\nadmin:\n  chat_id: -1\n  bogus_nested: 2\n"
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "t", cfg.BotToken)
}

// --- generate_docs.go ---------------------------------------------------------

func TestGenerateConfigDocs(t *testing.T) {
	dataDir := t.TempDir()
	// Minimal label files for two languages.
	enLabels := map[string]string{
		"section:general": "General",
		"bot_token":       "Bot Token // The Telegram bot token",
	}
	ruLabels := map[string]string{
		"section:general": "Общее",
		"bot_token":       "Токен бота // Токен Telegram бота",
	}
	writeJSON(t, filepath.Join(dataDir, "config_labels_en.json"), enLabels)
	writeJSON(t, filepath.Join(dataDir, "config_labels_ru.json"), ruLabels)

	outBase := filepath.Join(t.TempDir(), "CONFIG_REFERENCE.md")
	require.NoError(t, GenerateConfigDocs(dataDir, outBase))

	enOut, err := os.ReadFile(filepath.Join(filepath.Dir(outBase), "CONFIG_REFERENCE_en.md"))
	require.NoError(t, err)
	assert.Contains(t, string(enOut), "Configuration Reference (EN)")
	assert.Contains(t, string(enOut), "BOT_TOKEN")

	ruOut, err := os.ReadFile(filepath.Join(filepath.Dir(outBase), "CONFIG_REFERENCE_ru.md"))
	require.NoError(t, err)
	assert.Contains(t, string(ruOut), "Справочник по конфигурации (RU)")
}

func TestGenerateConfigDocs_NoLabelFiles(t *testing.T) {
	err := GenerateConfigDocs(t.TempDir(), filepath.Join(t.TempDir(), "out.md"))
	require.Error(t, err)
}

func TestParseLabelEntry(t *testing.T) {
	label, desc, ph := parseLabelEntry("My Label // My description // a,b")
	assert.Equal(t, "My Label", label)
	assert.Equal(t, "My description", desc)
	assert.Equal(t, []string{"a", "b"}, ph)

	label, desc, ph = parseLabelEntry("OnlyLabel")
	assert.Equal(t, "OnlyLabel", label)
	assert.Empty(t, desc)
	assert.Empty(t, ph)
}

func writeJSON(t *testing.T, path string, v interface{}) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// --- types_custom.go parsing --------------------------------------------------

func TestParseTopicString(t *testing.T) {
	cases := map[string]int{"any": TopicAny, "ANY": TopicAny, "main": TopicMain, "0": 0, "5": 5, "-1": -1}
	for in, want := range cases {
		got, err := parseTopicString(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	_, err := parseTopicString("notanumber")
	assert.Error(t, err)
	_, err = parseTopicString("-5")
	assert.Error(t, err)
}

func TestParseChatTopicList(t *testing.T) {
	list, err := ParseChatTopicList("-100:5, -200:any, 7")
	require.NoError(t, err)
	require.Len(t, list.Refs, 3)
	assert.Equal(t, ChatTopicRef{Chat: -100, Topic: 5}, list.Refs[0])
	assert.Equal(t, ChatTopicRef{Chat: -200, Topic: TopicAny}, list.Refs[1])
	assert.Equal(t, ChatTopicRef{Chat: 0, Topic: 7}, list.Refs[2])

	// Empty string -> empty (non-nil) list.
	empty, err := ParseChatTopicList("  ")
	require.NoError(t, err)
	assert.Empty(t, empty.Refs)

	// Invalid token.
	_, err = ParseChatTopicList("-100:")
	assert.Error(t, err)
	_, err = ParseChatTopicList("notanint:5")
	assert.Error(t, err)
}

func TestFormatChatTopicList(t *testing.T) {
	list := ChatTopicList{Refs: []ChatTopicRef{{Chat: -100, Topic: 5}, {Chat: 0, Topic: 7}}}
	assert.Equal(t, "-100:5,7", FormatChatTopicList(list))
	assert.Equal(t, "", FormatChatTopicList(ChatTopicList{}))
}

func TestChatTopicRefJSONRoundTrip(t *testing.T) {
	ref := ChatTopicRef{Chat: -100, Topic: 5}
	data, err := json.Marshal(ref)
	require.NoError(t, err)

	var back ChatTopicRef
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, ref, back)

	// Missing topic is rejected.
	err = json.Unmarshal([]byte(`{"chat":-100}`), &back)
	assert.Error(t, err)
	// Missing chat is rejected.
	err = json.Unmarshal([]byte(`{"topic":5}`), &back)
	assert.Error(t, err)
	// "any" alias accepted.
	require.NoError(t, json.Unmarshal([]byte(`{"chat":-100,"topic":"any"}`), &back))
	assert.Equal(t, TopicAny, back.Topic)
}

func TestChatTopicListJSON(t *testing.T) {
	var list ChatTopicList
	require.NoError(t, json.Unmarshal([]byte(`[{"chat":-100,"topic":5}, 7]`), &list))
	require.Len(t, list.Refs, 2)
	assert.Equal(t, ChatTopicRef{Chat: -100, Topic: 5}, list.Refs[0])
	assert.Equal(t, ChatTopicRef{Chat: 0, Topic: 7}, list.Refs[1])

	// Marshal back to array form.
	out, err := json.Marshal(list)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"chat":-100`)

	// Empty list marshals to [].
	out, err = json.Marshal(ChatTopicList{})
	require.NoError(t, err)
	assert.Equal(t, "[]", string(out))

	// null unmarshals to empty.
	var n ChatTopicList
	require.NoError(t, json.Unmarshal([]byte("null"), &n))
	assert.Empty(t, n.Refs)
}
