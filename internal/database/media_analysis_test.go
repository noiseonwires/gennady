// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaAnalysis_PersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	db, err := InitLocal(path)
	require.NoError(t, err)
	missing, err := db.GetMediaAnalysis("missing")
	require.NoError(t, err)
	assert.Nil(t, missing)
	want := MediaAnalysis{Description: "flagged sticker", ContentSafetyFlagged: true}
	require.NoError(t, db.SaveMediaAnalysis("hash", want))
	require.NoError(t, db.Close())

	db, err = InitLocal(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	got, err := db.GetMediaAnalysis("hash")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want, *got)
	want = MediaAnalysis{Description: "updated", ContentSafetyFlagged: false}
	require.NoError(t, db.SaveMediaAnalysis("hash", want))
	got, err = db.GetMediaAnalysis("hash")
	require.NoError(t, err)
	assert.Equal(t, want, *got)
}

func TestMediaAnalysis_UpgradesVersionOneDatabase(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.SaveWebsiteSnapshot("url", "keep me", "hash"))
	_, err := db.conn.Exec("DROP TABLE media_analysis_cache")
	require.NoError(t, err)
	require.NoError(t, db.setSchemaVersion(1))
	require.NoError(t, db.createTables())
	require.NoError(t, db.SaveMediaAnalysis("hash", MediaAnalysis{Description: "new"}))
	snapshot, err := db.GetWebsiteSnapshot("url")
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, "keep me", snapshot.Content)
}

func TestMediaAnalysis_BackupImportMergesEntries(t *testing.T) {
	source, destination := newTestDB(t), newTestDB(t)
	require.NoError(t, source.SaveMediaAnalysis("shared", MediaAnalysis{Description: "source"}))
	require.NoError(t, source.SaveMediaAnalysis("source-only", MediaAnalysis{Description: "import me"}))
	require.NoError(t, destination.SaveMediaAnalysis("shared", MediaAnalysis{Description: "keep destination"}))
	require.NoError(t, destination.SaveMediaAnalysis("destination-only", MediaAnalysis{Description: "keep me"}))
	path, err := source.ExportToLocalFile(t.TempDir(), false)
	require.NoError(t, err)
	require.NoError(t, destination.ImportFromLocalFile(path, false))
	legacy, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = legacy.Exec("DROP TABLE media_analysis_cache")
	require.NoError(t, err)
	require.NoError(t, legacy.Close())
	require.NoError(t, destination.ImportFromLocalFile(path, false), "legacy backups without a cache must remain importable")
	for key, want := range map[string]string{
		"shared": "keep destination", "source-only": "import me", "destination-only": "keep me",
	} {
		got, err := destination.GetMediaAnalysis(key)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, want, got.Description)
	}
}
