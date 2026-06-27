// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"os"
	"testing"

	"gennadium/internal/i18n"
)

// TestMain initializes the i18n catalog (English) once for the whole bot test
// package so that handlers/formatters that call i18n.T / i18n.Tf produce real
// localized strings instead of bare keys.
func TestMain(m *testing.M) {
	i18n.Init("en")
	os.Exit(m.Run())
}
