// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package bot

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureMediaVision(b *Bot, tg *mockTelegram) {
	b.config.AI.Enabled = true
	b.config.AI.ContentModeration.VisionEnabled = true
	b.config.AI.ContentModeration.VisionEndpoint = "https://vision.example.com"
	tg.GetFileFunc = func(id string) (telegram.File, error) {
		return telegram.File{FileID: id, DownloadURL: "https://files.telegram.example/" + id}, nil
	}
}

func TestMediaAnalysis_ReusesContentNotFileIDOrCaption(t *testing.T) {
	var descriptions, safety atomic.Int32
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write(testPNG())
		case strings.Contains(r.URL.Path, "contentsafety"):
			safety.Add(1)
			_, _ = w.Write([]byte(`{"categoriesAnalysis":[{"category":"Violence","severity":4}]}`))
		default:
			descriptions.Add(1)
			_, _ = w.Write([]byte(`{"captionResult":{"text":"cached scene"}}`))
		}
	})
	configureMediaVision(b, tg)
	b.config.AI.ContentModeration.ContentSafetyEnabled = true
	b.config.AI.ContentModeration.ContentSafetyEndpoint = "https://safety.example.com"
	first, flagged := b.processMessageEnhancements(&telegram.Message{
		Photo: []telegram.PhotoSize{{FileID: "one"}}, Caption: "first caption",
	})
	assert.Contains(t, first, "first caption")
	assert.True(t, flagged)

	restarted := &Bot{config: b.config, db: b.db, tg: tg, httpTransport: b.httpTransport}
	second, flagged := restarted.processMessageEnhancements(&telegram.Message{
		Photo: []telegram.PhotoSize{{FileID: "different-id"}}, Caption: "second caption",
	})
	assert.Contains(t, second, "second caption")
	assert.NotContains(t, second, "first caption")
	assert.Contains(t, second, "cached scene")
	assert.True(t, flagged)
	assert.EqualValues(t, 1, descriptions.Load())
	assert.EqualValues(t, 1, safety.Load())
}

func TestMediaAnalysis_KeyInvalidatesSettingsAndContent(t *testing.T) {
	mutations := map[string]func(*Bot){
		"prompt": func(b *Bot) { b.config.AI.ContentModeration.AIImageAnalysisPrompt.User = "new prompt" },
		"model": func(b *Bot) {
			model := fullModelConfig()
			model.UseForImageAnalysis = true
			b.config.AI.LightModel = config.AIModelConfigs{Configs: []config.AIModelConfig{model}}
		},
		"vision": func(b *Bot) { b.config.AI.ContentModeration.VisionEndpoint = "new endpoint" },
		"safety": func(b *Bot) { b.config.AI.ContentModeration.ContentSafetyEnabled = true },
		"ocr":    func(b *Bot) { b.config.AI.ContentModeration.OCRSpaceLanguage = "rus" },
		"locale": func(b *Bot) { b.config.Language = "ru" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			b, _ := newMockBot(t)
			before, err := b.mediaAnalysisKey([]byte("same"), "sticker")
			require.NoError(t, err)
			mutate(b)
			after, err := b.mediaAnalysisKey([]byte("same"), "sticker")
			require.NoError(t, err)
			assert.NotEqual(t, before, after)
		})
	}
	b, _ := newMockBot(t)
	first, err := b.mediaAnalysisKey([]byte("one"), "sticker")
	require.NoError(t, err)
	second, err := b.mediaAnalysisKey([]byte("two"), "sticker")
	require.NoError(t, err)
	third, err := b.mediaAnalysisKey([]byte("one"), "animation")
	require.NoError(t, err)
	assert.Len(t, first, 64)
	assert.NotEqual(t, first, second)
	assert.NotEqual(t, first, third)
}

func TestMediaAnalysis_DoesNotCachePartialSafetyFailure(t *testing.T) {
	var descriptions, safety atomic.Int32
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write(testPNG())
		case strings.Contains(r.URL.Path, "contentsafety"):
			if safety.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"categoriesAnalysis":[]}`))
		default:
			descriptions.Add(1)
			_, _ = w.Write([]byte(`{"captionResult":{"text":"usable description"}}`))
		}
	})
	configureMediaVision(b, tg)
	b.config.AI.ContentModeration.ContentSafetyEnabled = true
	b.config.AI.ContentModeration.ContentSafetyEndpoint = "https://safety.example.com"
	media := analysisMedia{fileID: "photo", kind: "photo"}
	partial, err := b.analyzeMessageMedia(media)
	require.Error(t, err)
	assert.Contains(t, partial.Description, "usable description")
	for range 2 {
		result, err := b.analyzeMessageMedia(media)
		require.NoError(t, err)
		assert.Contains(t, result.Description, "usable description")
	}
	assert.EqualValues(t, 2, descriptions.Load())
	assert.EqualValues(t, 2, safety.Load())
}

func TestMediaAnalysis_FailedDescriptionRetried(t *testing.T) {
	var descriptions atomic.Int32
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write(testPNG())
			return
		}
		if descriptions.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"captionResult":{"text":"recovered"}}`))
	})
	configureMediaVision(b, tg)
	media := analysisMedia{fileID: "photo", kind: "photo"}
	_, err := b.analyzeMessageMedia(media)
	require.Error(t, err)
	for range 2 {
		result, err := b.analyzeMessageMedia(media)
		require.NoError(t, err)
		assert.Contains(t, result.Description, "recovered")
	}
	assert.EqualValues(t, 2, descriptions.Load())
}

func TestMediaAnalysis_ConcurrentRepostsCoalesced(t *testing.T) {
	var descriptions atomic.Int32
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write(testPNG())
			return
		}
		descriptions.Add(1)
		_, _ = w.Write([]byte(`{"captionResult":{"text":"once"}}`))
	})
	configureMediaVision(b, tg)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := b.analyzeMessageMedia(analysisMedia{fileID: "photo", kind: "photo"})
			assert.NoError(t, err)
			assert.Contains(t, result.Description, "once")
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, descriptions.Load())
}

func TestMediaAnalysis_AnimationAndStickersUsePNGAndCache(t *testing.T) {
	ffmpeg := requireMediaTools(t)
	t.Setenv("PATH", t.TempDir())
	for _, kind := range []string{"animation", "gif_document", "sticker", "video_sticker", "tgs_sticker"} {
		t.Run(kind, func(t *testing.T) {
			var descriptions, thumbnails atomic.Int32
			source := mediaTestGIF(t, 5)
			still := mediaTestPNG(t)
			b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if strings.HasSuffix(r.URL.Path, "/thumbnail") {
						thumbnails.Add(1)
						_, _ = w.Write(still)
					} else if kind == "tgs_sticker" {
						_, _ = w.Write([]byte("original TGS bytes"))
					} else {
						_, _ = w.Write(source)
					}
					return
				}
				descriptions.Add(1)
				var request struct {
					Messages []struct {
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				var parts []AIMessageContentPart
				assert.NoError(t, json.Unmarshal(request.Messages[1].Content, &parts))
				imageURL := parts[1].ImageURL.URL
				assert.True(t, strings.HasPrefix(imageURL, "data:image/png;base64,"))
				data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(imageURL, "data:image/png;base64,"))
				assert.NoError(t, err)
				img, err := png.Decode(bytes.NewReader(data))
				if assert.NoError(t, err) {
					width := mediaFrameSize
					if kind == "animation" || kind == "gif_document" || kind == "video_sticker" {
						width *= 3
					}
					assert.Equal(t, width, img.Bounds().Dx())
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"visible text"}}]}`))
			})
			configureMediaVision(b, tg)
			b.config.AI.ContentModeration.FFmpegPath = ffmpeg
			b.config.AI.ContentModeration.FFmpegEnabled = true
			b.config.AI.ContentModeration.VisionEnabled = false
			b.config.AI.ContentModeration.AIImageAnalysisEnabled = true
			model := fullModelConfig()
			model.UseForImageAnalysis = true
			b.config.AI.LightModel = config.AIModelConfigs{Configs: []config.AIModelConfig{model}}
			message := &telegram.Message{Caption: "caption"}
			switch kind {
			case "animation":
				message.Animation = &telegram.Animation{FileID: "source"}
			case "gif_document":
				message.Document = &telegram.Document{FileID: "source", MIMEType: "image/gif"}
			default:
				message.Sticker = &telegram.Sticker{
					FileID: "source", IsVideo: kind == "video_sticker", IsAnimated: kind == "tgs_sticker",
					Thumbnail: &telegram.PhotoSize{FileID: "thumbnail"},
				}
			}
			for range 2 {
				text, flagged := b.processMessageEnhancements(message)
				assert.Contains(t, text, "visible text")
				assert.Contains(t, text, "caption")
				assert.False(t, flagged)
			}
			assert.EqualValues(t, 1, descriptions.Load())
			if kind == "tgs_sticker" {
				assert.EqualValues(t, 1, thumbnails.Load(), "cached TGS analysis must not redownload its thumbnail")
			}
		})
	}
}

func TestMediaAnalysis_MissingTGSThumbnailAndDisabledAnalysis(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("TGS"))
	})
	configureMediaVision(b, tg)
	b.config.AI.ContentModeration.FFmpegPath = "ffmpeg"
	b.config.AI.ContentModeration.FFmpegEnabled = true
	_, err := b.analyzeMessageMedia(analysisMedia{fileID: "tgs", kind: "tgs_sticker"})
	require.ErrorContains(t, err, "has no Telegram thumbnail")
	b.config.AI.ContentModeration.VisionEnabled = false
	text, flagged := b.processMessageEnhancements(&telegram.Message{
		Caption: "unchanged", Sticker: &telegram.Sticker{FileID: "sticker"},
	})
	assert.Equal(t, "unchanged", text)
	assert.False(t, flagged)
	assert.Equal(t, 1, rt.count())
}

func TestMediaAnalysis_ExcludedScopeIgnoresCachedSafetyVerdict(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(testPNG())
	})
	configureMediaVision(b, tg)
	b.config.AI.ContentModeration.FFmpegPath = "ffmpeg"
	b.config.AI.ContentModeration.FFmpegEnabled = true
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	b.config.Moderation.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}
	b.config.AI.ContentModeration.VisionOutsideModeration = true
	key, err := b.mediaAnalysisKey(testPNG(), "sticker")
	require.NoError(t, err)
	require.NoError(t, b.db.SaveMediaAnalysis(key, database.MediaAnalysis{
		Description: "flagged image", ContentSafetyFlagged: true,
	}))
	msg := testMessage(-100, 7, 55, "")
	msg.Sticker = &telegram.Sticker{FileID: "sticker"}
	mc := b.newInboundContext(msg, false)
	b.stageEnhance(mc)
	assert.Contains(t, mc.Enhanced, "flagged image")
	assert.False(t, mc.Flagged)
}

func TestMediaAnalysis_SettingChangeTriggersNewRequest(t *testing.T) {
	var descriptions atomic.Int32
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write(testPNG())
			return
		}
		descriptions.Add(1)
		_, _ = w.Write([]byte(`{"captionResult":{"text":"description"}}`))
	})
	configureMediaVision(b, tg)
	media := analysisMedia{fileID: "source", kind: "photo"}
	_, err := b.analyzeMessageMedia(media)
	require.NoError(t, err)
	b.config.AI.ContentModeration.VisionEndpoint = "https://new-vision.example.com"
	_, err = b.analyzeMessageMedia(media)
	require.NoError(t, err)
	assert.EqualValues(t, 2, descriptions.Load())
}

func TestDownloadMedia_RejectsOversizeAndEmpty(t *testing.T) {
	for _, mode := range []string{"metadata", "header", "stream", "empty"} {
		t.Run(mode, func(t *testing.T) {
			b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "header" {
					w.Header().Set("Content-Length", "20971521")
				} else if mode == "stream" {
					w.(http.Flusher).Flush()
					_, _ = w.Write(make([]byte, maxMediaBytes+1))
				}
			})
			configureMediaVision(b, tg)
			if mode == "metadata" {
				tg.GetFileFunc = func(id string) (telegram.File, error) {
					return telegram.File{FileID: id, FileSize: maxMediaBytes + 1}, nil
				}
			}

			_, err := b.downloadImage("source")
			require.Error(t, err)
			if mode == "metadata" {
				assert.Zero(t, rt.count())
			}
		})
	}
}

func TestMediaAnalysis_SafetyFailureNotHiddenByOCRFallback(t *testing.T) {
	var descriptions atomic.Int32
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write(testPNG())
		case strings.Contains(r.URL.Path, "chat/completions"):
			descriptions.Add(1)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}]}`))
		case strings.Contains(r.URL.Path, "contentsafety"):
			w.WriteHeader(http.StatusBadRequest)
		default:
			_, _ = w.Write([]byte(`{"ParsedResults":[{"ParsedText":"fallback text"}],"OCRExitCode":1,"IsErroredOnProcessing":false}`))
		}
	})
	configureMediaVision(b, tg)
	model := fullModelConfig()
	model.UseForImageAnalysis = true
	b.config.AI.LightModel = config.AIModelConfigs{Configs: []config.AIModelConfig{model}}
	cm := &b.config.AI.ContentModeration
	cm.AIImageAnalysisEnabled = true
	cm.VisionEnabled = false
	cm.ContentSafetyEnabled = true
	cm.ContentSafetyEndpoint = "https://safety.example.com"
	cm.OCRSpaceEnabled = true
	cm.OCRSpaceAPIKey = "key"
	for range 2 {
		result, err := b.analyzeMessageMedia(analysisMedia{fileID: "source", kind: "photo"})
		require.ErrorIs(t, err, errImageSafetyIncomplete)
		assert.Contains(t, result.Description, "fallback text")
	}
	assert.EqualValues(t, 2, descriptions.Load())
}

func TestMessageAnalysisMedia_NonGIFDocumentIgnored(t *testing.T) {
	_, supported := messageAnalysisMedia(&telegram.Message{
		Document: &telegram.Document{FileID: "pdf", MIMEType: "application/pdf"},
	})
	assert.False(t, supported)
}

func TestMediaAnalysis_DisabledFFmpegSkipsDependentChecksIncludingCache(t *testing.T) {
	for _, path := range []string{"/usr/bin/ffmpeg", "ffmpeg", "", " \t "} {
		for _, kind := range []string{"animation", "gif_document", "sticker", "video_sticker", "tgs_sticker"} {
			t.Run(path+"/"+kind, func(t *testing.T) {
				b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
					t.Error("disabled conversion must not download media or call image analysis")
				})
				configureMediaVision(b, tg)
				b.config.AI.ContentModeration.FFmpegPath = path
				b.config.AI.ContentModeration.FFmpegEnabled = false
				tg.GetFileFunc = func(id string) (telegram.File, error) {
					t.Error("disabled conversion must not request Telegram file metadata")
					return telegram.File{}, nil
				}
				message := &telegram.Message{Caption: "unchanged caption"}
				switch kind {
				case "animation":
					message.Animation = &telegram.Animation{FileID: "source"}
				case "gif_document":
					message.Document = &telegram.Document{FileID: "source", MIMEType: "image/gif"}
				default:
					message.Sticker = &telegram.Sticker{
						FileID: "source", IsVideo: kind == "video_sticker", IsAnimated: kind == "tgs_sticker",
						Thumbnail: &telegram.PhotoSize{FileID: "thumbnail"},
					}
				}
				media, ok := messageAnalysisMedia(message)
				require.True(t, ok)
				key, err := b.mediaAnalysisKey(testPNG(), media.kind)
				require.NoError(t, err)
				require.NoError(t, b.db.SaveMediaAnalysis(key, database.MediaAnalysis{
					Description: "old cached verdict", ContentSafetyFlagged: true,
				}))
				text, flagged := b.processMessageEnhancements(message)
				assert.Equal(t, "unchanged caption", text)
				assert.False(t, flagged)
				assert.Zero(t, rt.count())
			})
		}
	}
}

func TestMediaAnalysis_InvalidFFmpegPathReportsErrorWithoutCaching(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write(testPNG())
	})
	configureMediaVision(b, tg)
	b.config.AI.ContentModeration.FFmpegPath = filepath.Join(t.TempDir(), "missing-ffmpeg")
	b.config.AI.ContentModeration.FFmpegEnabled = true
	result, err := b.analyzeMessageMedia(analysisMedia{fileID: "source", kind: "sticker"})
	require.ErrorContains(t, err, "resolving configured FFmpeg")
	assert.Empty(t, result.Description)
	text, flagged := b.processMessageEnhancements(&telegram.Message{
		Caption: "keep caption", Sticker: &telegram.Sticker{FileID: "source"},
	})
	assert.Equal(t, "keep caption", text)
	assert.False(t, flagged)
	assert.Equal(t, 2, rt.count())
	key, err := b.mediaAnalysisKey(testPNG(), "sticker")
	require.NoError(t, err)
	cached, err := b.db.GetMediaAnalysis(key)
	require.NoError(t, err)
	assert.Nil(t, cached)
}

func TestMediaAnalysis_FFmpegPathOnlyInvalidatesConversionCache(t *testing.T) {
	b, _ := newMockBot(t)
	photoBefore, err := b.mediaAnalysisKey(testPNG(), "photo")
	require.NoError(t, err)
	stickerBefore, err := b.mediaAnalysisKey(testPNG(), "sticker")
	require.NoError(t, err)
	b.config.AI.ContentModeration.FFmpegPath = "ffmpeg"
	photoAfter, err := b.mediaAnalysisKey(testPNG(), "photo")
	require.NoError(t, err)
	stickerAfter, err := b.mediaAnalysisKey(testPNG(), "sticker")
	require.NoError(t, err)
	assert.Equal(t, photoBefore, photoAfter)
	assert.NotEqual(t, stickerBefore, stickerAfter)
}

func TestMediaAnalysis_FFmpegTogglePreservesCachedVerdicts(t *testing.T) {
	b, tg, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write(testPNG())
	})
	configureMediaVision(b, tg)
	b.config.AI.ContentModeration.FFmpegPath = "/usr/bin/ffmpeg"
	key, err := b.mediaAnalysisKey(testPNG(), "sticker")
	require.NoError(t, err)
	require.NoError(t, b.db.SaveMediaAnalysis(key, database.MediaAnalysis{
		Description: "cached sticker", ContentSafetyFlagged: true,
	}))
	message := &telegram.Message{Caption: "caption", Sticker: &telegram.Sticker{FileID: "source"}}
	for _, enabled := range []bool{true, false, true} {
		b.config.AI.ContentModeration.FFmpegEnabled = enabled
		text, flagged := b.processMessageEnhancements(message)
		assert.Equal(t, enabled, flagged)
		if enabled {
			assert.Contains(t, text, "cached sticker")
		} else {
			assert.Equal(t, "caption", text)
		}
	}
	assert.Equal(t, 2, rt.count(), "only enabled checks may download media; cache hits avoid conversion/API calls")
}
