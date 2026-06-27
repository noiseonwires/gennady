// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- extractReputation (pure) -------------------------------------------------

func TestExtractReputation(t *testing.T) {
	assert.Equal(t, "good", extractReputation("Nice user.\nReputation: good"))
	assert.Equal(t, "bad", extractReputation("Toxic.\nReputation: bad"))
	assert.Equal(t, "neutral", extractReputation("Ordinary.\nReputation: neutral"))
	// Russian markers.
	assert.Equal(t, "good", extractReputation("Хороший.\nРепутация: хороший"))
	assert.Equal(t, "bad", extractReputation("Плохой.\nРепутация: плохой"))
	// No marker -> neutral.
	assert.Equal(t, "neutral", extractReputation("just some text"))
}

// --- buildMessageDigest (pure) ------------------------------------------------

func TestBuildMessageDigest(t *testing.T) {
	b, _ := newMockBot(t)
	msgs := []database.MessageInfo{
		{MessageID: 1, ChatID: -100, UserID: 7, Text: "hello", Timestamp: time.Now()},
		{MessageID: 2, ChatID: -100, UserID: 7, Text: "world", Timestamp: time.Now()},
	}
	actions := map[int][]database.MessageAction{
		2: {{Type: "warn", Reason: "rude"}},
	}
	digest := b.buildMessageDigest(msgs, 1, 0, 0, actions, time.Now().Add(-24*time.Hour))
	assert.Contains(t, digest, "hello")
	assert.Contains(t, digest, "world")
	// Annotation for the warned message is present.
	assert.Contains(t, digest, "rude")
}

// --- creative replies (via transport) -----------------------------------------

func creativeConfig(b *Bot) {
	b.config.AI.Enabled = true
	b.config.AI.CreativeReplies.Enabled = true
	b.config.AI.CreativeReplies.UseFullModel = true
	b.config.AI.CreativeReplies.Prompt = config.PromptPair{
		System: "be witty",
		User:   "reply to {{message}} {{context}}",
	}
	b.config.AI.FullModel = fullModelConfigs()
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
}

func TestGenerateCreativeReplyWithQuote(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a witty reply"}}]}`))
	})
	creativeConfig(b)

	out, err := b.generateCreativeReplyWithQuote("hi there", "", 10, 7, -100, nil)
	require.NoError(t, err)
	assert.Equal(t, "a witty reply", out)
	assert.Contains(t, rt.last().Path, "/chat/completions")
}

func TestGenerateCreativeReplyWithQuote_LightModel(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"light reply"}}]}`))
	})
	creativeConfig(b)
	b.config.AI.CreativeReplies.UseFullModel = false
	b.config.AI.LightModel = fullModelConfigs()

	out, err := b.generateCreativeReplyWithQuote("hello", "quoted", 10, 7, -100, nil)
	require.NoError(t, err)
	assert.Equal(t, "light reply", out)
}

// --- user profile generation (via transport) ----------------------------------

func TestGenerateUserProfileWithAI(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"A helpful user.\nReputation: good"}}]}`))
	})
	b.config.AI.LightModel = fullModelConfigs()
	b.config.AI.UserProfiles.Prompt = config.PromptPair{
		System: "analyze {{username}}",
		User:   "messages: {{messages}}",
	}

	out, err := b.generateUserProfileWithAI("digest text", "alice")
	require.NoError(t, err)
	assert.Contains(t, out, "helpful user")
	assert.Equal(t, "good", extractReputation(out))
}

func TestGenerateUserProfileWithAI_Unconfigured(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := b.generateUserProfileWithAI("digest", "alice")
	require.Error(t, err)
}

func TestUpdateUserProfileWithAI(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Now toxic.\nReputation: bad"}}]}`))
	})
	b.config.AI.LightModel = fullModelConfigs()
	b.config.AI.UserProfiles.UpdatePrompt = config.PromptPair{
		System: "update {{username}}",
		User:   "old: {{existing_profile}} new: {{messages}}",
	}

	existing := &database.UserProfile{UserID: 7, Username: "alice", Profile: "Was nice.", Reputation: "good"}
	out, err := b.updateUserProfileWithAI(existing, "new digest", "alice")
	require.NoError(t, err)
	assert.Equal(t, "bad", extractReputation(out))
}

func TestGenerateOrUpdateUserProfile_NewProfile(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Brand new.\nReputation: neutral"}}]}`))
	})
	b.config.AI.LightModel = fullModelConfigs()
	b.config.AI.UserProfiles.Prompt = config.PromptPair{System: "s {{username}}", User: "u {{messages}}"}

	pd := &database.ProfileData{
		UserID:   7,
		Username: "alice",
		Messages: []database.MessageInfo{{MessageID: 1, ChatID: -100, UserID: 7, Text: "hi", Timestamp: time.Now()}},
	}
	err := b.generateOrUpdateUserProfile(pd, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)

	// Profile persisted with parsed reputation.
	saved, err := b.db.GetUserProfile(7)
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, "neutral", saved.Reputation)
	assert.Contains(t, saved.Profile, "Brand new")
}

func TestGenerateOrUpdateUserProfile_NoMessages(t *testing.T) {
	b, _ := newMockBot(t)
	// No messages -> no-op, no error.
	err := b.generateOrUpdateUserProfile(&database.ProfileData{UserID: 7}, time.Now())
	require.NoError(t, err)
}

// --- link content summary (via transport) -------------------------------------

func TestGenerateLinkContentSummary(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"This article is about X."}}]}`))
	})
	b.config.AI.LinkSummaries.UseFullModel = true
	b.config.AI.FullModel = fullModelConfigs()
	b.config.AI.LinkSummaries.Prompt = config.PromptPair{
		System: "summarize",
		User:   "title {{title}} url {{url}} content {{content}} {{truncated_suffix}}",
	}

	out, err := b.generateLinkContentSummary("Some page content", "Page Title", "https://x.com/a")
	require.NoError(t, err)
	assert.Equal(t, "This article is about X.", out)
}

func TestGenerateLinkContentSummary_AllModelsFail(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
	})
	b.config.AI.LinkSummaries.UseFullModel = true
	b.config.AI.FullModel = fullModelConfigs()
	b.config.AI.LinkSummaries.Prompt = config.PromptPair{System: "s", User: "u {{content}}"}

	_, err := b.generateLinkContentSummary("content", "title", "https://x.com/a")
	require.Error(t, err)
}

// --- runUserProfilesUpdate (scheduled entry point) ----------------------------

func TestRunUserProfilesUpdate_Disabled(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.UserProfiles.Enabled = false
	// Returns early without panic / DB writes.
	b.runUserProfilesUpdate()
}

func TestRunUserProfilesUpdate_EndToEnd(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Active user.\nReputation: good"}}]}`))
	})
	b.config.AI.Enabled = true
	b.config.AI.UserProfiles.Enabled = true
	b.config.AI.LightModel = fullModelConfigs()
	b.config.AI.UserProfiles.Prompt = config.PromptPair{System: "s {{username}}", User: "u {{messages}}"}
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.DatabaseCleanup.MessageRetentionHours = 168

	// Seed a recent message from a real user so GetAllProfileData returns them.
	require.NoError(t, b.db.StoreMessageInfo(&database.MessageInfo{
		MessageID: 1, ChatID: -100, UserID: 7, Username: "alice", Text: "hi", Timestamp: time.Now(),
	}))

	b.runUserProfilesUpdate()

	saved, err := b.db.GetUserProfile(7)
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, "good", saved.Reputation)
}

func TestIsUserForeverMuted(t *testing.T) {
	b, _ := newMockBot(t)
	chatIDs := []int64{-100}
	// No mute -> false.
	assert.False(t, b.isUserForeverMuted(7, chatIDs))

	// A near-permanent mute (far future) -> true.
	require.NoError(t, b.db.AddMutedUserSafely(&database.MutedUser{
		UserID: 7, ChatID: -100, MutedAt: time.Now(),
		UnmuteAt: time.Now().Add(ForeverMuteDuration), Reason: "x", IsActive: true,
	}))
	assert.True(t, b.isUserForeverMuted(7, chatIDs))

	// A short mute -> not forever.
	require.NoError(t, b.db.AddMutedUserSafely(&database.MutedUser{
		UserID: 8, ChatID: -100, MutedAt: time.Now(),
		UnmuteAt: time.Now().Add(time.Hour), Reason: "x", IsActive: true,
	}))
	assert.False(t, b.isUserForeverMuted(8, chatIDs))
}
