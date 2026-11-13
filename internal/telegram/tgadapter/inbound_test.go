// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package tgadapter

import (
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInboundMessage_AnalysisMediaMetadata(t *testing.T) {
	message := toInboundMessage(&models.Message{
		Animation: &models.Animation{FileID: "animation"},
		Document:  &models.Document{FileID: "gif-file", MimeType: "image/gif"},
		Sticker: &models.Sticker{
			FileID: "sticker", Emoji: "emoji", IsAnimated: true, IsVideo: true,
			Thumbnail: &models.PhotoSize{FileID: "thumbnail", Width: 100, Height: 80, FileSize: 123},
		},
	})
	require.NotNil(t, message.Animation)
	assert.Equal(t, "animation", message.Animation.FileID)
	require.NotNil(t, message.Document)
	assert.Equal(t, "gif-file", message.Document.FileID)
	assert.Equal(t, "image/gif", message.Document.MIMEType)
	require.NotNil(t, message.Sticker)
	assert.Equal(t, "sticker", message.Sticker.FileID)
	assert.Equal(t, "emoji", message.Sticker.Emoji)
	assert.True(t, message.Sticker.IsAnimated)
	assert.True(t, message.Sticker.IsVideo)
	require.NotNil(t, message.Sticker.Thumbnail)
	assert.Equal(t, "thumbnail", message.Sticker.Thumbnail.FileID)
	assert.Equal(t, 100, message.Sticker.Thumbnail.Width)
	assert.Equal(t, 80, message.Sticker.Thumbnail.Height)
	assert.Equal(t, 123, message.Sticker.Thumbnail.FileSize)

	withoutThumbnail := toInboundMessage(&models.Message{Sticker: &models.Sticker{FileID: "static"}})
	assert.Nil(t, withoutThumbnail.Sticker.Thumbnail)
	assert.Nil(t, withoutThumbnail.Animation)
}
