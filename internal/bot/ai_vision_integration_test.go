// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"strings"
	"testing"

	"gennadium/internal/telegram"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeImageWithVision_Disabled(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.ContentModeration.VisionEnabled = false
	_, _, err := b.analyzeImageWithVision([]byte("img"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not enabled")
}

func TestAnalyzeImageWithVision_OCRAndCaption(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"modelVersion":"2024-02-01",
			"captionResult":{"text":"a cat on a sofa","confidence":0.9},
			"readResult":{"blocks":[{"lines":[{"text":"HELLO"},{"text":"WORLD"}]}]}
		}`))
	})
	b.config.AI.ContentModeration.VisionEnabled = true
	b.config.AI.ContentModeration.VisionEndpoint = "https://vision.example.com"
	b.config.AI.ContentModeration.ContentSafetyEnabled = false

	out, flagged, err := b.analyzeImageWithVision([]byte("img"))
	require.NoError(t, err)
	assert.False(t, flagged)
	assert.Contains(t, out, "HELLO")
	assert.Contains(t, out, "WORLD")
	assert.Contains(t, out, "a cat on a sofa")
	assert.Contains(t, rt.last().Path, "imageanalysis:analyze")
}

func TestAnalyzeImageWithVision_NoDescription(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"modelVersion":"x"}`))
	})
	b.config.AI.ContentModeration.VisionEnabled = true
	b.config.AI.ContentModeration.VisionEndpoint = "https://vision.example.com"

	out, _, err := b.analyzeImageWithVision([]byte("img"))
	require.NoError(t, err)
	assert.Equal(t, "Image cannot be described and contains no text", out)
}

func TestAnalyzeImageWithVision_ErrorStatus(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`bad`))
	})
	b.config.AI.ContentModeration.VisionEnabled = true
	b.config.AI.ContentModeration.VisionEndpoint = "https://vision.example.com"

	_, _, err := b.analyzeImageWithVision([]byte("img"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestAnalyzeImageContentSafety_Flagged(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"categoriesAnalysis":[
			{"category":"Hate","severity":4},
			{"category":"Violence","severity":0}
		]}`))
	})
	b.config.AI.ContentModeration.ContentSafetyEnabled = true
	b.config.AI.ContentModeration.ContentSafetyEndpoint = "https://safety.example.com"

	flagged, details, err := b.analyzeImageContentSafety([]byte("img"))
	require.NoError(t, err)
	assert.True(t, flagged)
	assert.Contains(t, details, "Hate=4")
	// Zero-severity categories are skipped - only non-zero parameters surface.
	assert.NotContains(t, details, "Violence")
	assert.Contains(t, rt.last().Path, "image:analyze")
}

func TestAnalyzeImageContentSafety_Clean(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"categoriesAnalysis":[{"category":"Hate","severity":1}]}`))
	})
	b.config.AI.ContentModeration.ContentSafetyEnabled = true
	b.config.AI.ContentModeration.ContentSafetyEndpoint = "https://safety.example.com"

	flagged, _, err := b.analyzeImageContentSafety([]byte("img"))
	require.NoError(t, err)
	assert.False(t, flagged)
}

func TestAnalyzeImageContentSafety_Disabled(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.ContentModeration.ContentSafetyEnabled = false
	_, _, err := b.analyzeImageContentSafety([]byte("img"))
	require.Error(t, err)
}

func TestAnalyzeImageWithOCRSpace(t *testing.T) {
	b, _, rt := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ParsedResults":[{"ParsedText":"scanned text"}],
			"IsErroredOnProcessing":false,"OCRExitCode":1}`))
	})
	b.config.AI.ContentModeration.OCRSpaceAPIKey = "k"
	b.config.AI.ContentModeration.OCRSpaceURL = "https://ocr.example.com/parse"

	out, err := b.analyzeImageWithOCRSpace([]byte("img"))
	require.NoError(t, err)
	assert.Contains(t, out, "scanned text")
	assert.Contains(t, rt.last().Path, "/parse")
}

func TestAnalyzeImageWithOCRSpace_NoKey(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.ContentModeration.OCRSpaceAPIKey = ""
	_, err := b.analyzeImageWithOCRSpace([]byte("img"))
	require.Error(t, err)
}

func TestAnalyzeImageWithOCRSpace_ProcessingError(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"IsErroredOnProcessing":true,"OCRExitCode":3,"ErrorMessage":"bad key"}`))
	})
	b.config.AI.ContentModeration.OCRSpaceAPIKey = "k"
	b.config.AI.ContentModeration.OCRSpaceURL = "https://ocr.example.com/parse"

	_, err := b.analyzeImageWithOCRSpace([]byte("img"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "processing error")
}

func TestTestOCRSpace_HealthOK(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"OCRExitCode":1,"IsErroredOnProcessing":false}`))
	})
	b.config.AI.ContentModeration.OCRSpaceAPIKey = "k"
	b.config.AI.ContentModeration.OCRSpaceURL = "https://ocr.example.com/parse"

	status, err := b.testOCRSpace()
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestTestOCRSpace_NoKey(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {})
	b.config.AI.ContentModeration.OCRSpaceAPIKey = ""
	status, err := b.testOCRSpace()
	require.Error(t, err)
	assert.Equal(t, 0, status)
}

func TestDownloadImage_Success(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("binary-image-data"))
	})
	tg.GetFileFunc = func(fileID string) (telegram.File, error) {
		return telegram.File{FileID: fileID, DownloadURL: "https://files.telegram.example/x.jpg"}, nil
	}

	data, err := b.downloadImage("file123")
	require.NoError(t, err)
	assert.Equal(t, "binary-image-data", string(data))
}

func TestDownloadImage_BadStatus(t *testing.T) {
	b, tg, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	tg.GetFileFunc = func(fileID string) (telegram.File, error) {
		return telegram.File{FileID: fileID, DownloadURL: "https://files.telegram.example/x.jpg"}, nil
	}

	_, err := b.downloadImage("file123")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "status")
}
