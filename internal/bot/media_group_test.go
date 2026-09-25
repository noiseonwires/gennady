// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"testing"
	"time"

	tgbotapi "gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A captioned photo must keep its media marker in the stored history (the tag
// used to be a fallback that disappeared as soon as the message had any text),
// and album items must record the media group that ties them together.
func TestRecordMessage_StoresMediaTagAndAlbumID(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"нет"}}]}`))
	})
	moderationConfig(b)
	b.moderatedMsgs = make(map[string]time.Time)

	msg := testMessage(-100, 7, 55, "")
	msg.Caption = "look at this"
	msg.Photo = []tgbotapi.PhotoSize{{}}
	msg.MediaGroupID = "grp1"

	b.analyzeMessage(b.newInboundContext(msg, false))

	stored, err := b.db.GetMessageInfo(55, -100)
	require.NoError(t, err)
	assert.Equal(t, "<photo> look at this", stored.Text)
	assert.Equal(t, "grp1", stored.MediaGroupID)

	// A forwarded album item carries both markers, origin first.
	fwd := testMessage(-100, 7, 56, "")
	fwd.Photo = []tgbotapi.PhotoSize{{}}
	fwd.MediaGroupID = "grp1"
	fwd.ForwardFromChat = &tgbotapi.Chat{Title: "News", Username: "news"}

	b.analyzeMessage(b.newInboundContext(fwd, false))

	stored, err = b.db.GetMessageInfo(56, -100)
	require.NoError(t, err)
	assert.Equal(t, "<forwarded from: News (@news)> <photo>", stored.Text)

	siblings, err := b.db.GetAlbumSiblingIDs(55, -100)
	require.NoError(t, err)
	assert.Equal(t, []int{56}, siblings)
}
