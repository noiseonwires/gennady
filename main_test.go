// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gennadium/internal/database"
)

func TestPrintBanner(t *testing.T) {
	var buf bytes.Buffer
	printBanner(&buf)
	out := buf.String()
	assert.Contains(t, out, "Telegram Bot")
	assert.Contains(t, out, "Version:")
	assert.Contains(t, out, version)
	assert.Contains(t, out, "Git Commit:")
	assert.Contains(t, out, "AGPL-3")
}

func TestPrintBanner_IncludesBunnyEnv(t *testing.T) {
	t.Setenv("BUNNYNET_MC_REGION", "eu-west")
	var buf bytes.Buffer
	printBanner(&buf)
	assert.Contains(t, buf.String(), "BUNNYNET_MC_REGION: eu-west")
}

func TestRun_Version(t *testing.T) {
	var buf bytes.Buffer
	code := run([]string{"-version"}, &buf)
	assert.Equal(t, 0, code)
	assert.Contains(t, buf.String(), "Version:")
}

func TestRun_BadFlag(t *testing.T) {
	var buf bytes.Buffer
	code := run([]string{"-this-flag-does-not-exist"}, &buf)
	assert.Equal(t, 2, code)
}

func TestRun_GenerateConfigDocs(t *testing.T) {
	out := filepath.Join(t.TempDir(), "config_reference.md")
	var buf bytes.Buffer
	code := run([]string{"-generate-config-docs", out}, &buf)
	assert.Equal(t, 0, code)
	assert.Contains(t, buf.String(), "Config reference generated")

	// At least one per-language file should have been written.
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(out), "config_reference_*.md"))
	require.NoError(t, err)
	assert.NotEmpty(t, matches)
}

func TestRun_GenerateConfigDocs_Failure(t *testing.T) {
	// An output path inside a non-existent directory makes the write fail.
	bad := filepath.Join(t.TempDir(), "no_such_dir", "out.md")
	var buf bytes.Buffer
	code := run([]string{"-generate-config-docs", bad}, &buf)
	assert.Equal(t, 1, code)
	assert.Contains(t, buf.String(), "Failed to generate config docs")
}

func TestRun_ExportEnv(t *testing.T) {
	// Minimal valid config file so config.Load + Validate succeed.
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("language: en\n"), 0600))

	var buf bytes.Buffer
	code := run([]string{"-config", cfgPath, "-export-env"}, &buf)
	assert.Equal(t, 0, code)
	// Export output should contain env-style assignments.
	assert.Contains(t, buf.String(), "=")
}

func TestRun_ExportEnv_BadConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	// Invalid YAML triggers a load error.
	require.NoError(t, os.WriteFile(cfgPath, []byte("language: [unterminated\n"), 0600))

	var buf bytes.Buffer
	code := run([]string{"-config", cfgPath, "-export-env"}, &buf)
	assert.Equal(t, 1, code)
	assert.Contains(t, buf.String(), "Failed to load config")
}

func TestHashDBWebUIPasswordAndReload_NoChangeWhenAlreadyHashed(t *testing.T) {
	db := newMainTestDB(t)
	// A plaintext password gets hashed on the first pass.
	values := map[string]string{"web_ui.password": "plaintext-secret"}
	reloaded, changed, err := hashDBWebUIPasswordAndReload(db, values)
	require.NoError(t, err)
	assert.True(t, changed)
	require.Contains(t, reloaded, "web_ui.password")
	assert.NotEqual(t, "plaintext-secret", reloaded["web_ui.password"])
	assert.True(t, strings.HasPrefix(reloaded["web_ui.password"], "hashed:"))
}

func TestHashDBWebUIPasswordAndReload_EmptyNoChange(t *testing.T) {
	db := newMainTestDB(t)
	values := map[string]string{"web_ui.password": ""}
	out, changed, err := hashDBWebUIPasswordAndReload(db, values)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, "", out["web_ui.password"])
}

func newMainTestDB(t *testing.T) *database.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main_test.db")
	db, err := database.InitLocal(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
