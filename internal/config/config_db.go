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

// ConfigToStringMap serializes a Config struct into a flat map of YAML dot-path
// keys to string values, suitable for storage in a key-value database table.
func ConfigToStringMap(cfg *Config) map[string]string {
	result := make(map[string]string)
	collectStringKV(reflect.ValueOf(*cfg), "", result)
	return result
}

func ConfigToDBStringMap(cfg *Config) (map[string]string, error) {
	result := ConfigToStringMap(cfg)
	if _, err := HashWebUIPasswordInConfigValues(result); err != nil {
		return nil, err
	}
	return result, nil
}

func ConfigForExport(cfg *Config) (*Config, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	RLock()
	exportCfg := *cfg
	RUnlock()
	if exportCfg.WebUI.Password != "" && !IsHashedWebUIPassword(exportCfg.WebUI.Password) {
		hashed, err := HashWebUIPasswordForStorage(exportCfg.WebUI.Password)
		if err != nil {
			return nil, err
		}
		exportCfg.WebUI.Password = hashed
	}
	return &exportCfg, nil
}

func collectStringKV(v reflect.Value, prefix string, result map[string]string) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		yamlTag := fieldType.Tag.Get("yaml")
		tagName := strings.Split(yamlTag, ",")[0]
		if tagName == "" || tagName == "-" {
			continue
		}

		key := tagName
		if prefix != "" {
			key = prefix + "." + tagName
		}

		// Handle custom aggregate types
		switch val := field.Interface().(type) {
		case ChatIDList:
			var parts []string
			for _, id := range val.IDs {
				parts = append(parts, strconv.FormatInt(id, 10))
			}
			result[key] = strings.Join(parts, ",")
			continue
		case ChatTopicList:
			result[key] = FormatChatTopicList(val)
			continue
		case AIModelConfigs:
			for j, mc := range val.Configs {
				collectStringKV(reflect.ValueOf(mc), fmt.Sprintf("%s.%d", key, j), result)
			}
			continue
		}

		switch field.Kind() {
		case reflect.Struct:
			collectStringKV(field, key, result)
		case reflect.String:
			result[key] = field.String()
		case reflect.Int, reflect.Int64:
			result[key] = strconv.FormatInt(field.Int(), 10)
		case reflect.Bool:
			result[key] = strconv.FormatBool(field.Bool())
		case reflect.Float64:
			result[key] = strconv.FormatFloat(field.Float(), 'g', -1, 64)
		case reflect.Ptr:
			if !field.IsNil() {
				switch field.Elem().Kind() {
				case reflect.Float64:
					result[key] = strconv.FormatFloat(field.Elem().Float(), 'g', -1, 64)
				case reflect.Bool:
					result[key] = strconv.FormatBool(field.Elem().Bool())
				}
			}
		case reflect.Slice:
			elemType := fieldType.Type.Elem()
			switch elemType.Kind() {
			case reflect.Int, reflect.Int64:
				var parts []string
				for j := 0; j < field.Len(); j++ {
					parts = append(parts, strconv.FormatInt(field.Index(j).Int(), 10))
				}
				result[key] = strings.Join(parts, ",")
			case reflect.String:
				var parts []string
				for j := 0; j < field.Len(); j++ {
					parts = append(parts, field.Index(j).String())
				}
				result[key] = strings.Join(parts, ",")
			case reflect.Struct:
				for j := 0; j < field.Len(); j++ {
					collectStringKV(field.Index(j), fmt.Sprintf("%s.%d", key, j), result)
				}
			}
		}
	}
}

// ApplyStringMap applies string values from a flat key-value map to a Config
// struct. Keys use YAML dot-path notation (e.g. "ai.content_moderation.enabled").
func ApplyStringMap(cfg *Config, values map[string]string) {
	applyMapRecursive(reflect.ValueOf(cfg).Elem(), "", values)
}

func applyMapRecursive(v reflect.Value, prefix string, values map[string]string) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		yamlTag := fieldType.Tag.Get("yaml")
		tagName := strings.Split(yamlTag, ",")[0]
		if tagName == "" || tagName == "-" {
			continue
		}

		key := tagName
		if prefix != "" {
			key = prefix + "." + tagName
		}

		// Handle custom aggregate types
		if field.CanAddr() {
			switch ptr := field.Addr().Interface().(type) {
			case *ChatIDList:
				if val, ok := values[key]; ok {
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
				if val, ok := values[key]; ok {
					if parsed, err := ParseChatTopicList(val); err == nil {
						*ptr = parsed
					}
				}
				continue
			case *AIModelConfigs:
				applyIndexedMapSlice(reflect.ValueOf(&ptr.Configs).Elem(), key+".", values)
				continue
			}
		}

		switch field.Kind() {
		case reflect.Struct:
			applyMapRecursive(field, key, values)

		case reflect.String:
			if val, ok := values[key]; ok {
				field.SetString(val)
			}

		case reflect.Int, reflect.Int64:
			if val, ok := values[key]; ok && val != "" {
				if n, err := strconv.ParseInt(val, 10, 64); err == nil {
					field.SetInt(n)
				}
			}

		case reflect.Bool:
			if val, ok := values[key]; ok && val != "" {
				field.SetBool(val == "true" || val == "1")
			}

		case reflect.Float64:
			if val, ok := values[key]; ok && val != "" {
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					field.SetFloat(f)
				}
			}

		case reflect.Ptr:
			switch field.Type().Elem().Kind() {
			case reflect.Float64:
				if val, ok := values[key]; ok && val != "" {
					if f, err := strconv.ParseFloat(val, 64); err == nil {
						field.Set(reflect.ValueOf(&f))
					}
				}
			case reflect.Bool:
				if val, ok := values[key]; ok && val != "" {
					b := strings.EqualFold(val, "true") || strings.EqualFold(val, "yes") || val == "1"
					field.Set(reflect.ValueOf(&b))
				}
			}

		case reflect.Slice:
			if fieldType.Type.Elem().Kind() == reflect.Struct {
				applyIndexedMapSlice(field, key+".", values)
			} else if val, ok := values[key]; ok && val != "" {
				setSliceFromEnv(field, fieldType.Type.Elem(), val)
			}
		}
	}
}

// applyIndexedMapSlice handles indexed dot-path keys for slices of structs,
// e.g. "ai.light_model.0.endpoint", "ai.rss_feeds.1.name".
func applyIndexedMapSlice(sliceVal reflect.Value, prefix string, values map[string]string) {
	// Find the highest index referenced
	maxIdx := -1
	for k := range values {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		parts := strings.SplitN(rest, ".", 2)
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
	// Apply values to each indexed element
	for i := 0; i < sliceVal.Len(); i++ {
		applyMapRecursive(sliceVal.Index(i), fmt.Sprintf("%s%d", prefix, i), values)
	}
}

// LoadFromStringMap creates a Config from a flat key-value string map (as stored
// in the database), applies environment variable overrides on top, fills in
// defaults, and runs validation. This mirrors the same pipeline as Load().
func LoadFromStringMap(values map[string]string) (*Config, error) {
	var cfg Config

	// Apply stored values from DB
	ApplyStringMap(&cfg, values)

	// Environment variables override DB values
	applyEnvOverrides(&cfg)

	// Fill in defaults for anything still missing
	setDefaults(&cfg)

	// Same post-processing as Load()
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		if _, ok := os.LookupEnv("SERVER_LISTEN_PORT"); !ok {
			if p, err := strconv.Atoi(port); err == nil {
				cfg.Server.ListenPort = p
			}
		}
	}

	if len(cfg.AI.LightModel.Configs) > 0 && len(cfg.AI.FullModel.Configs) > 0 {
		if cfg.AI.LightModel.Configs[0].Endpoint == "" {
			cfg.AI.LightModel.Configs[0].Endpoint = cfg.AI.FullModel.Configs[0].Endpoint
		}
		if cfg.AI.LightModel.Configs[0].APIKey == "" {
			cfg.AI.LightModel.Configs[0].APIKey = cfg.AI.FullModel.Configs[0].APIKey
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.AI.Enabled {
		cfg.AI.WarnMissingPrompts()
	}

	return &cfg, nil
}
