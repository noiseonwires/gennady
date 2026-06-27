// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"gennadium/internal/config"
	"gennadium/internal/database"
)

// newTestDB spins up a fresh local SQLite database backed by a temp file and
// registers cleanup. A file (rather than :memory:) is used because the
// database/sql connection pool can otherwise hand out independent in-memory
// databases per connection.
func newTestDB(t *testing.T) *database.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "web_test.db")
	db, err := database.InitLocal(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newTestConfig returns a minimal config suitable for web handler tests.
func newTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.WebUI.PathPrefix = "/admin"
	return cfg
}
