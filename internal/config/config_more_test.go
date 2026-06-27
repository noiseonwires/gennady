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

// boolPtr is a small helper for optional bool fields.
func boolPtr(b bool) *bool { return &b }

// --- helpers.go ---------------------------------------------------------------

func TestHasUsableWebUIAuth(t *testing.T) {
	c := &Config{}
	assert.False(t, c.HasUsableWebUIAuth())

	c.WebUI.Password = "secret"
	assert.True(t, c.HasUsableWebUIAuth())

	// OTP path requires OTP enabled + super-admin + bot token.
	c = &Config{}
	c.WebUI.OTPEnabled = boolPtr(true)
	c.Admin.SuperAdminUserID = 42
	c.BotToken = "token"
	assert.True(t, c.HasUsableWebUIAuth())

	c.BotToken = ""
	assert.False(t, c.HasUsableWebUIAuth())
}

func TestServerBindIsLoopbackOnly(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"*", false},
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"192.168.1.5", false},
		{"not-an-ip", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			c := &Config{}
			c.Server.ListenAddr = tt.addr
			assert.Equal(t, tt.want, c.ServerBindIsLoopbackOnly())
		})
	}
}

func TestModerationChatHelpers(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100, -200}}
	assert.True(t, c.IsModerationChat(-100))
	assert.False(t, c.IsModerationChat(-999))
	assert.Equal(t, []int64{-100, -200}, c.GetModerationChatIDs())
	assert.Equal(t, int64(-100), c.GetFirstModerationChatID())

	empty := &Config{}
	assert.Equal(t, int64(0), empty.GetFirstModerationChatID())
}

func TestIsAdminReplyMessage(t *testing.T) {
	c := &Config{}
	c.Admin.ReplyMessageIDs = []int{10, 20}
	assert.True(t, c.IsAdminReplyMessage(20))
	assert.False(t, c.IsAdminReplyMessage(99))
}

func TestScopePredicates(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}

	// IsModerationActive: chat in scope, topic not excluded.
	assert.True(t, c.IsModerationActive(-100, 5))
	assert.False(t, c.IsModerationActive(-999, 5))
	c.Moderation.ExcludedTopics = ChatTopicList{Refs: []ChatTopicRef{{Chat: -100, Topic: 5}}}
	assert.False(t, c.IsModerationActive(-100, 5))
	assert.True(t, c.IsModerationActive(-100, 6))

	// InScope with included list.
	included := ChatTopicList{Refs: []ChatTopicRef{{Chat: -100, Topic: 7}}}
	assert.True(t, c.InScope(included, ChatTopicList{}, -100, 7))
	assert.False(t, c.InScope(included, ChatTopicList{}, -100, 8))
	// Empty included means any topic.
	assert.True(t, c.InScope(ChatTopicList{}, ChatTopicList{}, -100, 123))
	// Not a moderation chat.
	assert.False(t, c.InScope(ChatTopicList{}, ChatTopicList{}, -999, 0))
}

func TestFeatureActivePredicates(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}

	// Deletion requires Enabled.
	assert.False(t, c.IsDeletionActive(-100, 0))
	c.MessageDeletion.Enabled = true
	assert.True(t, c.IsDeletionActive(-100, 0))

	assert.True(t, c.IsCreativeReplyActive(-100, 0))
	assert.True(t, c.IsMessageSummaryActive(-100, 0))
	assert.True(t, c.IsLinkSummaryActive(-100, 0))
	assert.False(t, c.IsCreativeReplyActive(-999, 0))
}

func TestChatRulesFor(t *testing.T) {
	c := &Config{}
	c.AI.ChatRules = "base rules"
	assert.Equal(t, "base rules", c.ChatRulesFor(0))
	assert.Equal(t, "base rules", c.ChatRulesFor(-100))

	c.AI.ChatRulesOverrides = []ChatRulesOverride{{Chat: -100, Rules: "extra"}}
	assert.Equal(t, "base rules\n\nextra", c.ChatRulesFor(-100))
	assert.Equal(t, "base rules", c.ChatRulesFor(-200))

	c.AI.ChatRules = ""
	assert.Equal(t, "extra", c.ChatRulesFor(-100))
}

func TestEffectivePostTo(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100, -200}}

	// Empty post-to expands to every moderation chat / main area.
	out := c.EffectivePostTo(ChatTopicList{})
	require.Len(t, out, 2)
	assert.Equal(t, ChatTopicRef{Chat: -100, Topic: TopicMain}, out[0])

	// Explicit refs are returned verbatim.
	explicit := ChatTopicList{Refs: []ChatTopicRef{{Chat: -100, Topic: 9}}}
	assert.Equal(t, explicit.Refs, c.EffectivePostTo(explicit))
}

// --- validation.go ------------------------------------------------------------

func TestValidate(t *testing.T) {
	// Webhook enabled without URL is rejected.
	c := &Config{}
	c.Webhook.Enabled = true
	require.Error(t, c.Validate())

	c.Webhook.URL = "https://example.com/hook"
	require.NoError(t, c.Validate())

	// post_to 'any' wildcard is rejected.
	c = &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}
	c.AI.DailySummary.PostTo = ChatTopicList{Refs: []ChatTopicRef{{Chat: -100, Topic: TopicAny}}}
	require.Error(t, c.Validate())
}

func TestValidateChatRulesOverrides(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}

	c.AI.ChatRulesOverrides = []ChatRulesOverride{{Chat: 0, Rules: "x"}}
	require.Error(t, c.Validate())

	c.AI.ChatRulesOverrides = []ChatRulesOverride{{Chat: -999, Rules: "x"}}
	require.Error(t, c.Validate())

	c.AI.ChatRulesOverrides = []ChatRulesOverride{{Chat: -100, Rules: "x"}, {Chat: -100, Rules: "y"}}
	require.Error(t, c.Validate())

	c.AI.ChatRulesOverrides = []ChatRulesOverride{{Chat: -100, Rules: "x"}}
	require.NoError(t, c.Validate())
}

// TestValidateAggregatesAllErrors verifies Validate() does not stop at the
// first problem: every violation across independent rules must surface so the
// operator can fix them all in one pass.
func TestValidateAggregatesAllErrors(t *testing.T) {
	c := &Config{}
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}
	// Two creative-reply refs to chats that aren't moderated …
	c.AI.CreativeReplies.IncludedTopics = ChatTopicList{Refs: []ChatTopicRef{
		{Chat: -999, Topic: 0},
		{Chat: -888, Topic: 0},
	}}
	// … a chat-rules override for yet another unknown chat …
	c.AI.ChatRulesOverrides = []ChatRulesOverride{{Chat: -777, Rules: "x"}}
	// … and an invalid "any" destination.
	c.AI.DailySummary.PostTo = ChatTopicList{Refs: []ChatTopicRef{{Chat: -100, Topic: TopicAny}}}

	err := c.Validate()
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "-999")
	assert.Contains(t, msg, "-888")
	assert.Contains(t, msg, "-777")
	assert.Contains(t, msg, "daily_summary.post_to")
}

func TestMissingConfigFields(t *testing.T) {
	c := &Config{}
	missing := c.MissingConfigFields()
	assert.ElementsMatch(t, []string{"bot_token", "admin.chat_id", "moderation.chat_ids"}, missing)
	assert.True(t, c.HasMissingConfig())

	c.BotToken = "t"
	c.Admin.ChatID = -1
	c.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}
	assert.Empty(t, c.MissingConfigFields())
	assert.False(t, c.HasMissingConfig())
}

func TestNormalizeAndValidateModerationAction(t *testing.T) {
	assert.Equal(t, ModerationActionMute, NormalizeModerationAction(" BAN "))
	assert.Equal(t, "warn", NormalizeModerationAction("WARN"))
	assert.Equal(t, "report", NormalizeModerationAction("report"))

	for _, a := range []string{ModerationActionReport, ModerationActionWarn, ModerationActionMute, ModerationActionDelete} {
		assert.True(t, IsValidModerationAction(a))
	}
	assert.False(t, IsValidModerationAction("nonsense"))
	assert.False(t, IsValidModerationAction("ban"))
}

func TestCollectPromptWarnings(t *testing.T) {
	c := &AzureAIConfig{}
	// No features enabled -> no warnings.
	assert.Empty(t, c.CollectPromptWarnings())

	// Enabled feature with missing prompt -> warning.
	c.ContentModeration.Enabled = true
	warnings := c.CollectPromptWarnings()
	assert.NotEmpty(t, warnings)

	// ValidatePrompts returns the first warning as error.
	require.Error(t, c.ValidatePrompts())

	// WarnMissingPrompts must not panic.
	c.WarnMissingPrompts()

	// Fully configured prompts -> no warning for that feature.
	c2 := &AzureAIConfig{}
	c2.ContentModeration.Enabled = true
	c2.ContentModeration.Prompt = PromptPair{System: "s", User: "u"}
	c2.ContentModeration.WarningPrompt = PromptPair{System: "s", User: "u"}
	assert.Empty(t, c2.CollectPromptWarnings())
}

// --- guard.go -----------------------------------------------------------------

func TestGuardLockUnlock(t *testing.T) {
	// Exercise the lock/unlock primitives (no deadlock, no panic).
	Lock()
	Unlock()
	RLock()
	RUnlock()

	c := &Config{}
	c.AI.ContentModeration.Rules = []ModerationRule{{Trigger: "x", Action: "warn"}}
	rules := c.ModerationRules()
	require.Len(t, rules, 1)
	assert.Equal(t, "x", rules[0].Trigger)
}

func TestReplaceContents(t *testing.T) {
	dst := &Config{BotToken: "old"}
	src := &Config{BotToken: "new"}
	dst.ReplaceContents(src)
	assert.Equal(t, "new", dst.BotToken)
}

// --- types.go optional-bool predicates ----------------------------------------

func TestOptionalBoolPredicates(t *testing.T) {
	w := &WebUIConfig{}
	assert.True(t, w.IsOTPEnabled()) // defaults true
	w.OTPEnabled = boolPtr(false)
	assert.False(t, w.IsOTPEnabled())

	r := ModerationRule{}
	assert.True(t, r.IsNotifyAdmin()) // defaults true
	r.NotifyAdmin = boolPtr(false)
	assert.False(t, r.IsNotifyAdmin())

	mg := &MorningGreetingConfig{}
	assert.True(t, mg.IsUseAI()) // defaults true
	mg.UseAI = boolPtr(false)
	assert.False(t, mg.IsUseAI())

	cm := &ContentModerationConfig{}
	assert.True(t, cm.IsComplaintManualModeration()) // defaults true
	cm.ComplaintManualModeration = boolPtr(false)
	assert.False(t, cm.IsComplaintManualModeration())
}

// --- types_custom.go ----------------------------------------------------------

func TestChatIDListAccessors(t *testing.T) {
	l := ChatIDList{IDs: []int64{1, 2, 3}}
	assert.True(t, l.Contains(2))
	assert.False(t, l.Contains(9))
	assert.Equal(t, int64(1), l.First())
	assert.Equal(t, 3, l.Count())
	assert.Equal(t, []int64{1, 2, 3}, l.All())

	empty := ChatIDList{}
	assert.Equal(t, int64(0), empty.First())
	assert.Equal(t, 0, empty.Count())
}

func TestAIModelConfigResolveProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		endpoint string
		want     string
	}{
		{"explicit azure", "Azure", "https://x.openai.com", AIProviderAzure},
		{"explicit openai", "OpenAI", "https://x.azure.com", AIProviderOpenAI},
		{"auto azure host", "", "https://x.openai.azure.com", AIProviderAzure},
		{"auto openai host", "", "https://api.openai.com", AIProviderOpenAI},
		{"empty endpoint defaults azure", "", "", AIProviderAzure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := AIModelConfig{Provider: tt.provider, Endpoint: tt.endpoint}
			assert.Equal(t, tt.want, m.ResolveProvider())
		})
	}
}

func TestAIModelConfigsGetCount(t *testing.T) {
	a := AIModelConfigs{Configs: []AIModelConfig{{DeploymentName: "a"}, {DeploymentName: "b"}}}
	assert.Equal(t, 2, a.Count())
	assert.Equal(t, "a", a.Get(0).DeploymentName)
	assert.Equal(t, "b", a.Get(1).DeploymentName)
	// Index wraps modulo length.
	assert.Equal(t, "a", a.Get(2).DeploymentName)

	empty := AIModelConfigs{}
	assert.Equal(t, AIModelConfig{}, empty.Get(0))
	assert.Equal(t, 0, empty.Count())
}

// --- config_db.go round-trip --------------------------------------------------

func TestConfigStringMapRoundTrip(t *testing.T) {
	cfg := &Config{
		BotToken: "tok",
		Language: "ru",
	}
	cfg.Admin.ChatID = -111
	cfg.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100}}

	kv := ConfigToStringMap(cfg)
	require.NotEmpty(t, kv)

	loaded, err := LoadFromStringMap(kv)
	require.NoError(t, err)
	assert.Equal(t, "tok", loaded.BotToken)
	assert.Equal(t, "ru", loaded.Language)
	assert.Equal(t, int64(-111), loaded.Admin.ChatID)
	assert.True(t, loaded.IsModerationChat(-100))
}

func TestConfigForExport(t *testing.T) {
	_, err := ConfigForExport(nil)
	require.Error(t, err)

	cfg := &Config{}
	cfg.WebUI.Password = "plaintext"
	exported, err := ConfigForExport(cfg)
	require.NoError(t, err)
	assert.True(t, IsHashedWebUIPassword(exported.WebUI.Password))
	// Original config is untouched.
	assert.Equal(t, "plaintext", cfg.WebUI.Password)
}

// --- env_export.go & generate_docs.go ----------------------------------------

func TestExportEnvVars(t *testing.T) {
	cfg := &Config{BotToken: "tok"}
	cfg.Moderation.ChatIDs = ChatIDList{IDs: []int64{-100, -200}}
	out := ExportEnvVars(cfg)
	assert.Contains(t, out, "BOT_TOKEN=tok")
	assert.Contains(t, out, "-100,-200")
}

func TestGetEnvOverrides(t *testing.T) {
	t.Setenv("BOT_TOKEN", "from-env")
	keys := GetEnvOverrides()
	assert.Contains(t, keys, "bot_token")
}

func TestYAMLPathToEnv(t *testing.T) {
	assert.Equal(t, "AI_CONTENT_MODERATION_ENABLED", YAMLPathToEnv("ai.content_moderation.enabled"))
	assert.Equal(t, "BOT_TOKEN", YAMLPathToEnv("bot_token"))
}

// --- config.go Load integration ----------------------------------------------

func TestLoad_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := "" +
		"bot_token: file-token\n" +
		"language: en\n" +
		"admin:\n" +
		"  chat_id: -111\n" +
		"moderation:\n" +
		"  chat_id: -100\n"
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "file-token", cfg.BotToken)
	assert.Equal(t, "en", cfg.Language)
	assert.Equal(t, int64(-111), cfg.Admin.ChatID)
	assert.True(t, cfg.IsModerationChat(-100))
}

func TestLoad_MissingFileUsesDefaults(t *testing.T) {
	// A non-existent file is not an error; defaults + env are used.
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("bot_token: [unterminated"), 0o600))
	_, err := Load(path)
	require.Error(t, err)
}
