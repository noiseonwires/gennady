// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// When ai.translation_prompt is empty, translation must be skipped (no network
// call) and the original text returned untouched - guarding against the model
// being handed an empty prompt and replying with a generic greeting.
func TestTranslateWikipediaEvents_SkipsWhenPromptEmpty(t *testing.T) {
	b := newTestBot() // empty config => empty TranslationPrompt

	events := []string{"1990 - událost A", "2001 - událost B"}
	out, err := b.translateWikipediaEvents(events)
	require.NoError(t, err)
	assert.Equal(t, events, out, "events should be returned untranslated")
}

func TestTranslateWikipediaEvents_DisabledFlag(t *testing.T) {
	b := newTestBot()
	disabled := false
	b.config.AI.ExternalData.TranslateWikipedia = &disabled
	// Even with a prompt configured, the explicit opt-out wins.
	b.config.AI.TranslationPrompt = config.PromptPair{System: "sys", User: "translate {{text}}"}

	events := []string{"x", "y"}
	out, err := b.translateWikipediaEvents(events)
	require.NoError(t, err)
	assert.Equal(t, events, out)
}

func TestTranslateExtractedContent_SkipsWhenPromptEmpty(t *testing.T) {
	b := newTestBot() // empty config => empty TranslationPrompt

	const content = "Nějaký český text k překladu."
	out, err := b.translateExtractedContent(content)
	require.NoError(t, err)
	assert.Equal(t, content, out, "content should be returned untranslated")
}
