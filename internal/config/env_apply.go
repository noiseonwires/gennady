// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
)

// Environment-variable override application.
//
// Walks the Config struct via reflection and replaces field values with the
// corresponding env var when one is set. Env names are derived from YAML tag
// paths joined with underscores and uppercased
// (ai.content_moderation.enabled → AI_CONTENT_MODERATION_ENABLED).
//
// Lives in Load() between YAML parsing and setDefaults(): env values take
// precedence over the file, and defaults only fill what's still unset.

// applyEnvOverrides walks the Config struct via reflection and overrides each
// field whose corresponding environment variable is set. Env var names are
// derived from YAML tag paths, uppercased with underscores.
func applyEnvOverrides(cfg *Config) {
	applyEnvRecursive(reflect.ValueOf(cfg).Elem(), "")
}

func applyEnvRecursive(v reflect.Value, prefix string) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		yamlTag := fieldType.Tag.Get("yaml")
		tagName := strings.Split(yamlTag, ",")[0]
		if tagName == "" || tagName == "-" {
			continue
		}

		envName := prefix + strings.ToUpper(tagName)

		// Handle custom aggregate types
		if field.CanAddr() {
			switch ptr := field.Addr().Interface().(type) {
			case *ChatIDList:
				if val, ok := os.LookupEnv(envName); ok {
					ptr.IDs = nil
					if val != "" {
						for _, p := range strings.Split(val, ",") {
							if id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil {
								ptr.IDs = append(ptr.IDs, id)
							}
						}
					}
				}
				continue
			case *ChatTopicList:
				if val, ok := os.LookupEnv(envName); ok {
					if parsed, err := ParseChatTopicList(val); err == nil {
						*ptr = parsed
					}
				}
				continue
			case *AIModelConfigs:
				applyIndexedStructSliceEnv(reflect.ValueOf(&ptr.Configs).Elem(), envName+"_")
				continue
			}
		}

		switch field.Kind() {
		case reflect.Struct:
			applyEnvRecursive(field, envName+"_")

		case reflect.String:
			if val, ok := os.LookupEnv(envName); ok {
				field.SetString(val)
			}

		case reflect.Int, reflect.Int64:
			if val, ok := os.LookupEnv(envName); ok && val != "" {
				if n, err := strconv.ParseInt(val, 10, 64); err == nil {
					field.SetInt(n)
				}
			}

		case reflect.Bool:
			if val, ok := os.LookupEnv(envName); ok && val != "" {
				field.SetBool(strings.EqualFold(val, "true") || strings.EqualFold(val, "yes") || val == "1")
			}

		case reflect.Float64:
			if val, ok := os.LookupEnv(envName); ok && val != "" {
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					field.SetFloat(f)
				}
			}

		case reflect.Ptr:
			switch field.Type().Elem().Kind() {
			case reflect.Float64:
				if val, ok := os.LookupEnv(envName); ok && val != "" {
					if f, err := strconv.ParseFloat(val, 64); err == nil {
						field.Set(reflect.ValueOf(&f))
					}
				}
			case reflect.Bool:
				if val, ok := os.LookupEnv(envName); ok && val != "" {
					b := strings.EqualFold(val, "true") || strings.EqualFold(val, "yes") || val == "1"
					field.Set(reflect.ValueOf(&b))
				}
			}

		case reflect.Slice:
			if fieldType.Type.Elem().Kind() == reflect.Struct {
				applyIndexedStructSliceEnv(field, envName+"_")
			} else if val, ok := os.LookupEnv(envName); ok && val != "" {
				setSliceFromEnv(field, fieldType.Type.Elem(), val)
			}
		}
	}
}

// applyIndexedStructSliceEnv handles env overrides for slices of structs using
// indexed env var names: PREFIX_0_FIELD, PREFIX_1_FIELD, etc.
func applyIndexedStructSliceEnv(sliceVal reflect.Value, prefix string) {
	// Scan environment for the highest index referenced
	maxIdx := -1
	for _, env := range os.Environ() {
		eqIdx := strings.Index(env, "=")
		if eqIdx < 0 {
			continue
		}
		key := env[:eqIdx]
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		parts := strings.SplitN(rest, "_", 2)
		if idx, err := strconv.Atoi(parts[0]); err == nil && idx > maxIdx {
			maxIdx = idx
		}
	}
	if maxIdx < 0 {
		return
	}
	// Grow the slice if needed
	for sliceVal.Len() <= maxIdx {
		sliceVal.Set(reflect.Append(sliceVal, reflect.New(sliceVal.Type().Elem()).Elem()))
	}
	// Apply env overrides to each indexed element
	for i := 0; i < sliceVal.Len(); i++ {
		applyEnvRecursive(sliceVal.Index(i), fmt.Sprintf("%s%d_", prefix, i))
	}
}

// setSliceFromEnv parses a comma-separated env var value into a typed slice.
func setSliceFromEnv(field reflect.Value, elemType reflect.Type, val string) {
	parts := strings.Split(val, ",")
	switch elemType.Kind() {
	case reflect.Int:
		slice := reflect.MakeSlice(field.Type(), 0, len(parts))
		for _, p := range parts {
			if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
				slice = reflect.Append(slice, reflect.ValueOf(n))
			}
		}
		field.Set(slice)
	case reflect.Int64:
		slice := reflect.MakeSlice(field.Type(), 0, len(parts))
		for _, p := range parts {
			if n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil {
				slice = reflect.Append(slice, reflect.ValueOf(n))
			}
		}
		field.Set(slice)
	case reflect.String:
		slice := reflect.MakeSlice(field.Type(), 0, len(parts))
		for _, p := range parts {
			slice = reflect.Append(slice, reflect.ValueOf(strings.TrimSpace(p)))
		}
		field.Set(slice)
	}
	// Complex element types (structs) are silently skipped
}
