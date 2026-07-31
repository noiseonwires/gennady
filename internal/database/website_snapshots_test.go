// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebsiteSnapshotRoundTrip(t *testing.T) {
	db := newTestDB(t)

	// Unknown URL -> no snapshot, no error.
	got, err := db.GetWebsiteSnapshot("https://example.com/rules")
	require.NoError(t, err)
	assert.Nil(t, got)

	require.NoError(t, db.SaveWebsiteSnapshot("https://example.com/rules", "first version", "hash1"))
	got, err = db.GetWebsiteSnapshot("https://example.com/rules")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "first version", got.Content)
	assert.Equal(t, "hash1", got.ContentHash)
	assert.False(t, got.ChangedAt.IsZero())

	// Saving again replaces the stored baseline.
	require.NoError(t, db.SaveWebsiteSnapshot("https://example.com/rules", "second version", "hash2"))
	got, err = db.GetWebsiteSnapshot("https://example.com/rules")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "second version", got.Content)
	assert.Equal(t, "hash2", got.ContentHash)
}

func TestTouchWebsiteSnapshotKeepsContent(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.SaveWebsiteSnapshot("https://example.com/x", "body", "h"))

	require.NoError(t, db.TouchWebsiteSnapshot("https://example.com/x"))

	got, err := db.GetWebsiteSnapshot("https://example.com/x")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "body", got.Content)
	assert.Equal(t, "h", got.ContentHash)

	// Touching an unknown URL is a no-op, not an error.
	require.NoError(t, db.TouchWebsiteSnapshot("https://example.com/missing"))
}
