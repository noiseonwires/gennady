// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"fmt"
	"log"
	"reflect"
	"strings"
)

// Unknown-key detection. Run once at Load() time against the parsed YAML
// document to surface typos as warnings before they silently fall off the
// strongly-typed Config struct.

// findInnerSliceElem looks for a single exported slice field inside a struct
// and returns the element type of that slice. This handles wrapper types like
// AIModelConfigs (which has Configs []AIModelConfig) and ChatIDList.
// If no suitable field is found, the original type is returned unchanged.
func findInnerSliceElem(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return t
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.IsExported() && f.Type.Kind() == reflect.Slice {
			return f.Type.Elem()
		}
	}
	return t
}

// warnUnknownKeys recursively compares a raw YAML map against the yaml tags
// of a Go struct type and logs a warning for every key that has no matching
// struct field. The prefix is used to build a dotted path for readability.
func warnUnknownKeys(raw map[string]interface{}, t reflect.Type, prefix string) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}

	// Build a map: yaml tag name → field type
	known := make(map[string]reflect.Type, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		known[name] = field.Type
	}

	for key, val := range raw {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		fieldType, ok := known[key]
		if !ok {
			log.Printf("⚠️  Unknown config key: %s", fullKey)
			continue
		}

		// Recurse into nested maps if the field is a struct
		if sub, isMap := val.(map[string]interface{}); isMap {
			ft := fieldType
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				warnUnknownKeys(sub, ft, fullKey)
			}
		}

		// Recurse into slices of maps (e.g. rss feeds, light_model, full_model)
		if items, isList := val.([]interface{}); isList {
			ft := fieldType
			if ft.Kind() == reflect.Slice {
				ft = ft.Elem()
			} else if ft.Kind() == reflect.Struct {
				// Handle wrapper structs with custom UnmarshalYAML that wrap a slice
				// (e.g. AIModelConfigs has Configs []AIModelConfig)
				ft = findInnerSliceElem(ft)
			}
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				for i, item := range items {
					if m, ok := item.(map[string]interface{}); ok {
						warnUnknownKeys(m, ft, fmt.Sprintf("%s[%d]", fullKey, i))
					}
				}
			}
		}
	}
}
