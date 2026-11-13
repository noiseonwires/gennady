// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/telegram"
)

type analysisMedia struct {
	fileID      string
	kind        string
	thumbnailID string
}

func messageAnalysisMedia(message *telegram.Message) (analysisMedia, bool) {
	if len(message.Photo) > 0 {
		return analysisMedia{fileID: message.Photo[len(message.Photo)-1].FileID, kind: "photo"}, true
	}
	if message.Animation != nil {
		return analysisMedia{fileID: message.Animation.FileID, kind: "animation"}, true
	}
	if document := message.Document; document != nil && strings.EqualFold(document.MIMEType, "image/gif") {
		return analysisMedia{fileID: document.FileID, kind: "animation"}, true
	}
	if sticker := message.Sticker; sticker != nil {
		media := analysisMedia{fileID: sticker.FileID, kind: "sticker"}
		if sticker.IsVideo {
			media.kind = "video_sticker"
		} else if sticker.IsAnimated {
			media.kind = "tgs_sticker"
			if sticker.Thumbnail != nil {
				media.thumbnailID = sticker.Thumbnail.FileID
			}
		}
		return media, true
	}
	return analysisMedia{}, false
}

func (b *Bot) mediaAnalysisKey(data []byte, kind string) (string, error) {
	cm := b.config.AI.ContentModeration
	settings := struct {
		Version               int
		Kind                  string
		Language              string
		AIEnabled             bool
		AIPrompt              config.PromptPair
		Models                config.AIModelConfigs
		VisionEnabled         bool
		VisionEndpoint        string
		VisionAPIKey          string
		ContentSafetyEnabled  bool
		ContentSafetyEndpoint string
		ContentSafetyAPIKey   string
		OCRSpaceEnabled       bool
		OCRSpaceAPIKey        string
		OCRSpaceURL           string
		OCRSpaceLanguage      string
		OCRSpaceEngine        int
		FFmpegPath            string `json:",omitempty"`
	}{
		Version: 1, Kind: kind, Language: b.config.Language,
		AIEnabled: b.config.AI.Enabled && cm.AIImageAnalysisEnabled,
		AIPrompt:  cm.AIImageAnalysisPrompt, Models: b.imageAnalysisModels(),
		VisionEnabled: cm.VisionEnabled, VisionEndpoint: cm.VisionEndpoint, VisionAPIKey: cm.VisionAPIKey,
		ContentSafetyEnabled: cm.ContentSafetyEnabled, ContentSafetyEndpoint: cm.ContentSafetyEndpoint,
		ContentSafetyAPIKey: cm.ContentSafetyAPIKey,
		OCRSpaceEnabled:     cm.OCRSpaceEnabled, OCRSpaceAPIKey: cm.OCRSpaceAPIKey, OCRSpaceURL: cm.OCRSpaceURL,
		OCRSpaceLanguage: cm.OCRSpaceLanguage, OCRSpaceEngine: cm.OCRSpaceEngine,
	}
	if kind != "photo" {
		settings.FFmpegPath = strings.TrimSpace(cm.FFmpegPath)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("encoding image-analysis cache settings: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write(encoded)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type mediaAnalysisCall struct {
	done   chan struct{}
	result database.MediaAnalysis
	err    error
}

func (b *Bot) analyzeMessageMedia(media analysisMedia) (database.MediaAnalysis, error) {
	var result database.MediaAnalysis
	if media.kind != "photo" && !b.config.AI.ContentModeration.FFmpegEnabled {
		log.Printf("Skipping %s image analysis: ffmpeg_enabled is false", media.kind)
		return result, nil
	}
	data, err := b.downloadImage(media.fileID)
	if err != nil {
		return result, err
	}
	key, err := b.mediaAnalysisKey(data, media.kind)
	if err != nil {
		return result, err
	}

	// Coalesce simultaneous reposts without serializing unrelated media.
	b.mediaAnalysisMu.Lock()
	if pending := b.mediaAnalysisPending[key]; pending != nil {
		b.mediaAnalysisMu.Unlock()
		<-pending.done
		return pending.result, pending.err
	}
	call := &mediaAnalysisCall{done: make(chan struct{})}
	if b.mediaAnalysisPending == nil {
		b.mediaAnalysisPending = make(map[string]*mediaAnalysisCall)
	}
	b.mediaAnalysisPending[key] = call
	b.mediaAnalysisMu.Unlock()
	defer func() {
		b.mediaAnalysisMu.Lock()
		delete(b.mediaAnalysisPending, key)
		close(call.done)
		b.mediaAnalysisMu.Unlock()
	}()

	call.result, call.err = b.analyzeMediaWithCache(key, media, data)
	return call.result, call.err
}

func (b *Bot) analyzeMediaWithCache(key string, media analysisMedia, data []byte) (database.MediaAnalysis, error) {
	var result database.MediaAnalysis
	if b.db != nil {
		cached, err := b.db.GetMediaAnalysis(key)
		if err != nil {
			log.Printf("Error reading media analysis cache: %v", err)
		} else if cached != nil {
			return *cached, nil
		}
	}
	ctx := b.botCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if media.kind == "tgs_sticker" {
		if media.thumbnailID == "" {
			return result, fmt.Errorf("animated TGS sticker has no Telegram thumbnail")
		}
		var err error
		data, err = b.downloadImage(media.thumbnailID)
		if err != nil {
			return result, fmt.Errorf("downloading TGS sticker thumbnail: %w", err)
		}
	}
	if media.kind != "photo" {
		var err error
		data, err = prepareMediaImage(ctx, b.config.AI.ContentModeration.FFmpegPath, data, media.kind == "animation" || media.kind == "video_sticker")
		if err != nil {
			return result, fmt.Errorf("preparing %s for image analysis: %w", media.kind, err)
		}
	}
	var err error
	result.Description, result.ContentSafetyFlagged, err = b.describeImageWithFallback(data)
	if err == nil && result.Description != "" && b.db != nil {
		if cacheErr := b.db.SaveMediaAnalysis(key, result); cacheErr != nil {
			log.Printf("Error saving media analysis cache: %v", cacheErr)
		}
	}
	return result, err
}
