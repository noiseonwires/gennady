// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package i18n

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reset returns the i18n package to its pristine, uninitialised state so that
// each test can exercise Init() from scratch. It is safe because tests in this
// file do not run in parallel.
func reset() {
	once = sync.Once{}
	translations = nil
}

func TestT_BeforeInit_ReturnsKey(t *testing.T) {
	reset()
	assert.Equal(t, "about.version", T("about.version"))
	assert.Equal(t, "totally.unknown.key", T("totally.unknown.key"))
}

func TestInit_English(t *testing.T) {
	reset()
	Init("en")
	assert.Equal(t, "Version", T("about.version"))
	assert.Equal(t, "Commit", T("about.commit"))
}

func TestInit_Russian(t *testing.T) {
	reset()
	Init("ru")
	assert.Equal(t, "Версия", T("about.version"))
	assert.Equal(t, "Коммит", T("about.commit"))
}

func TestInit_UnknownLanguage_DefaultsToEnglish(t *testing.T) {
	reset()
	Init("fr")
	assert.Equal(t, "Version", T("about.version"))
}

func TestInit_EmptyLanguage_DefaultsToEnglish(t *testing.T) {
	reset()
	Init("")
	assert.Equal(t, "Version", T("about.version"))
}

func TestInit_IsIdempotent(t *testing.T) {
	reset()
	Init("ru")
	// once.Do means the second call is a no-op; language stays Russian.
	Init("en")
	assert.Equal(t, "Версия", T("about.version"))
}

func TestT_UnknownKey_ReturnsKey(t *testing.T) {
	reset()
	Init("en")
	assert.Equal(t, "no.such.key", T("no.such.key"))
}

func TestTf_FormatsArguments(t *testing.T) {
	reset()
	Init("en")
	// "about.version" => "Version" (no verbs); Tf with a key that is a plain
	// string simply returns it, ignoring args is not valid - use a key with a verb.
	// Fall back to formatting the key itself for an unknown key containing a verb.
	require.Equal(t, "count=42", Tf("count=%d", 42))
}

func TestTf_KnownKeyWithoutVerbs(t *testing.T) {
	reset()
	Init("en")
	// No format verbs in the resolved string => Sprintf returns it unchanged
	// (with extra-args notice appended only when args are passed). With no args
	// the result is the plain translation.
	assert.Equal(t, "Version", Tf("about.version"))
}
