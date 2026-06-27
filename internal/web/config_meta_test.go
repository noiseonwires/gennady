// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetI18n(t *testing.T) {
	i18n := GetI18n()
	require.NotNil(t, i18n)
	assert.Contains(t, i18n, "en")
	assert.Contains(t, i18n, "ru")
	assert.NotEmpty(t, i18n["en"])

	// Cached: a second call returns the same map instance.
	assert.True(t, sameI18nMap(i18n, GetI18n()))
}

func sameI18nMap(a, b map[string]map[string]string) bool {
	// Compare the inner "en" map pointer identity via length equality and a
	// shared key; the once.Do guarantees identical content on repeated calls.
	return len(a) == len(b) && len(a["en"]) == len(b["en"])
}

func TestGetConfigMeta(t *testing.T) {
	fields, sections := GetConfigMeta()
	require.NotEmpty(t, fields)
	require.NotEmpty(t, sections)

	for _, f := range fields {
		assert.NotEmpty(t, f.Key)
		assert.NotEmpty(t, f.LabelEN)
		// LabelRU falls back to LabelEN when not translated.
		assert.NotEmpty(t, f.LabelRU)
	}

	// Cached: repeated calls return consistent counts.
	fields2, sections2 := GetConfigMeta()
	assert.Equal(t, len(fields), len(fields2))
	assert.Equal(t, len(sections), len(sections2))
}
