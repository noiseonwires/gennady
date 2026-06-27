// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/i18n"
	tgbotapi "gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- describeImageWithFallback ------------------------------------------------

func TestDescribeImageWithFallback_Vision(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"captionResult":{"text":"a smiling face","confidence":0.9}}`))
	})
	b.config.AI.ContentModeration.VisionEnabled = true
	b.config.AI.ContentModeration.VisionEndpoint = "https://vision.example.com"

	desc, flagged, err := b.describeImageWithFallback([]byte("img"))
	require.NoError(t, err)
	assert.False(t, flagged)
	assert.Contains(t, desc, "a smiling face")
}

func TestDescribeImageWithFallback_OCRFallback(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		// OCR.space response shape.
		_, _ = w.Write([]byte(`{"ParsedResults":[{"ParsedText":"BUY NOW"}],"OCRExitCode":1,"IsErroredOnProcessing":false}`))
	})
	b.config.AI.ContentModeration.VisionEnabled = false
	b.config.AI.ContentModeration.OCRSpaceEnabled = true
	b.config.AI.ContentModeration.OCRSpaceAPIKey = "k"
	b.config.AI.ContentModeration.OCRSpaceURL = "https://ocr.example.com/parse"

	desc, _, err := b.describeImageWithFallback([]byte("img"))
	require.NoError(t, err)
	assert.Contains(t, desc, "BUY NOW")
}

func TestDescribeImageWithFallback_NeitherEnabled(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.ContentModeration.VisionEnabled = false
	b.config.AI.ContentModeration.OCRSpaceEnabled = false

	desc, flagged, err := b.describeImageWithFallback([]byte("img"))
	require.NoError(t, err)
	assert.Equal(t, "", desc)
	assert.False(t, flagged)
}

// --- analyzeUserProfileText ---------------------------------------------------

func newProfileScreeningBot(t *testing.T, aiContent string) (*Bot, *mockTelegram) {
	t.Helper()
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + aiContent + `"}}]}`))
	})
	b.config.AI.Enabled = true
	b.config.AI.LightModel = fullModelConfigs()
	b.config.AI.ContentModeration.NewUserProfileCheckEnabled = true
	b.config.AI.ContentModeration.NewUserProfilePrompt = config.PromptPair{
		System: "screen this profile",
		User:   "profile: {{profile_text}}",
	}
	return b, tg
}

func TestAnalyzeUserProfileText_Flagged(t *testing.T) {
	b, _ := newProfileScreeningBot(t, "Spam: selling followers")
	finding := b.analyzeUserProfileText(7, "Bio: buy cheap followers")
	assert.NotEqual(t, "", finding)
	assert.Contains(t, finding, "selling followers")
}

func TestAnalyzeUserProfileText_Clean(t *testing.T) {
	b, _ := newProfileScreeningBot(t, "CLEAN")
	finding := b.analyzeUserProfileText(7, "Bio: just a regular person")
	assert.Equal(t, "", finding)
}

func TestAnalyzeUserProfileText_Unconfigured(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.ContentModeration.NewUserProfilePrompt = config.PromptPair{}
	finding := b.analyzeUserProfileText(7, "anything")
	assert.Equal(t, "", finding)
}

// --- checkFirstMessageUserProfile (end-to-end via mock + transport) -----------

func TestCheckFirstMessageUserProfile_RecordsSuspiciousNotice(t *testing.T) {
	b, tg := newProfileScreeningBot(t, "Spam: promo channel for crypto")
	// No photo, no personal channel - keeps the flow to bio + AI only.
	tg.GetChatFullFunc = func(chatID int64) (tgbotapi.ChatFull, error) {
		return tgbotapi.ChatFull{
			Chat: tgbotapi.Chat{ID: chatID},
			Bio:  "DM me for crypto signals",
		}, nil
	}

	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat:      tgbotapi.Chat{ID: -100},
		From:      &tgbotapi.User{ID: 7, FirstName: "Spammy", Username: "spammer"},
	}
	b.checkFirstMessageUserProfile(msg)

	prof, err := b.db.GetUserProfile(7)
	require.NoError(t, err)
	require.NotNil(t, prof)
	assert.Contains(t, prof.TgProfileAnalysis, "promo channel for crypto")
	assert.Equal(t, "", prof.Profile)
}

func TestCheckFirstMessageUserProfile_CleanRecordsNothing(t *testing.T) {
	b, tg := newProfileScreeningBot(t, "CLEAN")
	tg.GetChatFullFunc = func(chatID int64) (tgbotapi.ChatFull, error) {
		return tgbotapi.ChatFull{Chat: tgbotapi.Chat{ID: chatID}, Bio: "hi"}, nil
	}

	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat:      tgbotapi.Chat{ID: -100},
		From:      &tgbotapi.User{ID: 8, FirstName: "Nice"},
	}
	b.checkFirstMessageUserProfile(msg)

	prof, err := b.db.GetUserProfile(8)
	require.NoError(t, err)
	assert.Nil(t, prof, "clean profile must not create a notice")
}

func TestCheckFirstMessageUserProfile_DisabledNoOp(t *testing.T) {
	b, _ := newProfileScreeningBot(t, "Spam")
	b.config.AI.ContentModeration.NewUserProfileCheckEnabled = false

	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat:      tgbotapi.Chat{ID: -100},
		From:      &tgbotapi.User{ID: 9},
	}
	b.checkFirstMessageUserProfile(msg)

	prof, err := b.db.GetUserProfile(9)
	require.NoError(t, err)
	assert.Nil(t, prof)
}

// --- screening findings recorded into tg_profile_analysis ---------------------

func TestRecordUserProfileFinding_StoresInTgProfileAnalysis(t *testing.T) {
	b, _ := newMockBot(t)
	b.recordUserProfileFinding(7, &tgbotapi.User{ID: 7, Username: "u"}, []string{"looks like spam"})

	prof, err := b.db.GetUserProfile(7)
	require.NoError(t, err)
	require.NotNil(t, prof)
	assert.Contains(t, prof.TgProfileAnalysis, "looks like spam")
	assert.Equal(t, "", prof.Profile)
}

// --- screening result surfacing into moderation prompts -----------------------

func TestSuspiciousProfileSurfacedInModerationBlocks(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.ContentModeration.NewUserProfileCheckEnabled = true

	// Record two distinct screening findings.
	b.recordUserProfileFinding(7, &tgbotapi.User{ID: 7, Username: "spammer"}, []string{"Spam: crypto promo", "Photo flagged"})

	// Full {{user_profile}} block surfaces each recorded finding.
	full := b.getUserProfileForModeration(7, -100)
	assert.Contains(t, full, "Spam: crypto promo")
	assert.Contains(t, full, "Photo flagged")

	// Compact {{user_reputation}} block surfaces the findings too.
	compact := b.getUserReputationForModeration(7)
	assert.Contains(t, compact, "Spam: crypto promo")
	assert.Contains(t, compact, "Photo flagged")
}

// Ensure the compact reputation block stays empty when nothing is recorded.
func TestGetUserReputationForModeration_EmptyWhenNoProfile(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.ContentModeration.NewUserProfileCheckEnabled = true
	assert.Equal(t, "", b.getUserReputationForModeration(123))
}

// --- new-user marker + {{new_user_rules}} placeholder ------------------------

// recordFirstSeen inserts a first-seen row for userID in chatID at ts via the
// general-profile tracking path (the same write the on-receive flow performs).
func recordFirstSeen(t *testing.T, b *Bot, userID, chatID int64, ts time.Time) {
	t.Helper()
	_, err := b.db.RecordIncomingMessage(
		&database.MessageInfo{MessageID: int(userID), ChatID: chatID, UserID: userID, Timestamp: ts},
		database.IncomingMessageOpts{TrackProfile: true, Username: "u", DisplayName: "U"},
	)
	require.NoError(t, err)
}

func TestNewUserMarkerInModerationBlocks(t *testing.T) {
	b, _ := newMockBot(t)

	// Recent first-seen → user counts as new; marker shows in both blocks.
	recordFirstSeen(t, b, 7, -100, time.Now().Add(-1*time.Hour))
	assert.Contains(t, b.getUserProfileForModeration(7, -100), i18n.T("mod.user_new"))
	assert.Contains(t, b.getUserReputationForModeration(7), i18n.T("mod.user_new"))

	// First-seen beyond the default 24h window → not new; marker absent.
	recordFirstSeen(t, b, 8, -100, time.Now().Add(-48*time.Hour))
	assert.NotContains(t, b.getUserProfileForModeration(8, -100), i18n.T("mod.user_new"))
	assert.Equal(t, "", b.getUserReputationForModeration(8))
}

func TestIsNewUserRespectsConfiguredWindow(t *testing.T) {
	b, _ := newMockBot(t)
	recordFirstSeen(t, b, 7, -100, time.Now().Add(-2*time.Hour))

	b.config.AI.ContentModeration.NewUserWindowHours = 1 // 2h ago, 1h window → not new
	assert.False(t, b.isNewUser(7))
	b.config.AI.ContentModeration.NewUserWindowHours = 6 // widen → new
	assert.True(t, b.isNewUser(7))

	// Unknown user (no first-seen) is never new.
	assert.False(t, b.isNewUser(999))
}

func TestNewUserRulesFor(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.AI.ContentModeration.NewUserRules = "Be extra strict with newcomers."
	recordFirstSeen(t, b, 7, -100, time.Now().Add(-1*time.Hour))  // new
	recordFirstSeen(t, b, 8, -100, time.Now().Add(-72*time.Hour)) // old

	assert.Equal(t, "Be extra strict with newcomers.", b.newUserRulesFor(7))
	assert.Equal(t, "", b.newUserRulesFor(8))

	// No rules configured → empty even for a new user.
	b.config.AI.ContentModeration.NewUserRules = ""
	assert.Equal(t, "", b.newUserRulesFor(7))
}

func TestDistinctModerationModels_Dedup(t *testing.T) {
	b, _ := newMockBot(t)
	shared := config.AIModelConfig{Endpoint: "https://a.example.com", DeploymentName: "shared"}
	lightOnly := config.AIModelConfig{Endpoint: "https://a.example.com", DeploymentName: "light-only"}
	fullOnly := config.AIModelConfig{Endpoint: "https://b.example.com", DeploymentName: "full-only"}
	b.config.AI.LightModel = config.AIModelConfigs{Configs: []config.AIModelConfig{shared, lightOnly}}
	b.config.AI.FullModel = config.AIModelConfigs{Configs: []config.AIModelConfig{shared, fullOnly}}

	models := b.distinctModerationModels()
	require.Len(t, models, 3) // shared (once), light-only, full-only
	// Light group is walked first, so the shared model appears once, first.
	assert.Equal(t, "shared", models[0].cfg.DeploymentName)
	assert.Equal(t, "light-only", models[1].cfg.DeploymentName)
	assert.Equal(t, "full-only", models[2].cfg.DeploymentName)
}
