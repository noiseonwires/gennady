// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package i18n

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

//go:embed data/bot_en.json
var botENJSON []byte

//go:embed data/bot_ru.json
var botRUJSON []byte

var (
	once         sync.Once
	translations map[string]string
)

// Init initializes the i18n system with the given language code ("en" or "ru").
// Must be called once before any T/Tf calls.
func Init(lang string) {
	once.Do(func() {
		var en, ru map[string]string
		if err := json.Unmarshal(botENJSON, &en); err != nil {
			log.Fatalf("i18n: failed to parse bot_en.json: %v", err)
		}
		if err := json.Unmarshal(botRUJSON, &ru); err != nil {
			log.Fatalf("i18n: failed to parse bot_ru.json: %v", err)
		}
		switch lang {
		case "ru":
			translations = ru
		default:
			translations = en
		}
		log.Printf("i18n: loaded %d bot strings for language %q", len(translations), lang)
	})
}

// T returns the localized string for the given key.
// If the key is not found, it returns the key itself.
func T(key string) string {
	if translations == nil {
		return key
	}
	if s, ok := translations[key]; ok {
		return s
	}
	return key
}

// Tf returns the localized string formatted with the given arguments.
func Tf(key string, args ...interface{}) string {
	return fmt.Sprintf(T(key), args...)
}
