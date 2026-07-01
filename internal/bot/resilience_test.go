// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/telegram"

	tgbotapi "gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover graceful degradation when external services fail mid-pipeline.

// A transient (non-content-filter) AI error must not lose the message and must
// not trigger any moderation action.
func TestResilience_AITransientError_RecordedNotActioned(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)

	b.handleMessage(testMessage(-100, 7, 55, "anything"), false)

	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	require.NotNil(t, stored, "a transient AI error must not lose the message")
	assert.Empty(t, tg.DeletedIDs, "a transient AI error must not trigger an action")
	assert.Empty(t, tg.Restrictions)
}

// When the image can't be downloaded/analyzed, enhancement falls back to the raw
// caption so the message is still moderated on its actual text.
func TestResilience_ImageDownloadFailure_FallsBackToRawText(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.AI.Enabled = true
	b.config.AI.ContentModeration.VisionEnabled = true
	tg.GetFileFunc = func(fileID string) (telegram.File, error) {
		return telegram.File{}, fmt.Errorf("download boom")
	}

	msg := testMessage(-100, 7, 55, "")
	msg.Caption = "raw caption text"
	msg.Photo = []tgbotapi.PhotoSize{{FileID: "abc"}}
	mc := b.newInboundContext(msg, false)

	b.stageEnhance(mc)

	assert.Equal(t, "raw caption text", mc.Enhanced, "on image failure the raw caption is used for moderation")
	assert.False(t, mc.Flagged)
}

// A failed link extraction degrades quietly: no summary is posted and nothing
// panics.
func TestResilience_LinkExtractionFailure_NoSummary(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.AI.Enabled = true
	b.config.AI.LinkSummaries.Enabled = true

	msg := testMessage(-100, 7, 55, "check https://example.com/article")
	posted := b.extractAndStoreLinksContent(msg)

	assert.False(t, posted, "a failed link extraction must not post a summary")
	assert.Empty(t, tg.SentMessages)
}
