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

// Env-variable inspection & export.
//
//   - GetEnvOverrides reports which config keys are currently being shadowed
//     by an environment variable. Used by the Web UI to flag "this field is
//     read-only because $VAR is set".
//   - ExportEnvVars renders the effective config as KEY=VALUE lines suitable
//     for `docker --env-file` or `source`.

// GetEnvOverrides returns a list of YAML dot-path keys whose values are
// overridden by environment variables.
func GetEnvOverrides() []string {
	var keys []string
	collectEnvOverrides(reflect.TypeOf(Config{}), "", "", &keys)
	return keys
}

func collectEnvOverrides(t reflect.Type, dotPrefix, envPrefix string, keys *[]string) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		yamlTag := sf.Tag.Get("yaml")
		tagName := strings.Split(yamlTag, ",")[0]
		if tagName == "" || tagName == "-" {
			continue
		}

		dotKey := tagName
		if dotPrefix != "" {
			dotKey = dotPrefix + "." + tagName
		}
		envName := envPrefix + strings.ToUpper(tagName)

		ft := sf.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		switch ft.Kind() {
		case reflect.Struct:
			// Skip custom aggregate types that are handled as leaf values
			switch ft.Name() {
			case "ChatIDList", "ChatTopicList":
				if _, ok := os.LookupEnv(envName); ok {
					*keys = append(*keys, dotKey)
				}
			case "AIModelConfigs":
				// Check indexed env vars (PREFIX_0_FIELD, etc.)
				for _, env := range os.Environ() {
					eqIdx := strings.Index(env, "=")
					if eqIdx > 0 && strings.HasPrefix(env[:eqIdx], envName+"_") {
						*keys = append(*keys, dotKey)
						break
					}
				}
			default:
				collectEnvOverrides(ft, dotKey, envName+"_", keys)
			}
		default:
			if _, ok := os.LookupEnv(envName); ok {
				*keys = append(*keys, dotKey)
			}
		}
	}
}

// ExportEnvVars returns all effective configuration values formatted as
// KEY=VALUE lines suitable for use with docker --env-file or shell source.
func ExportEnvVars(cfg *Config) string {
	var lines []string
	collectEnvVars(reflect.ValueOf(*cfg), "", &lines)
	return strings.Join(lines, "\n") + "\n"
}

func collectEnvVars(v reflect.Value, prefix string, lines *[]string) {
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
		switch val := field.Interface().(type) {
		case ChatIDList:
			var parts []string
			for _, id := range val.IDs {
				parts = append(parts, strconv.FormatInt(id, 10))
			}
			*lines = append(*lines, fmt.Sprintf("%s=%s", envName, strings.Join(parts, ",")))
			continue
		case ChatTopicList:
			*lines = append(*lines, fmt.Sprintf("%s=%s", envName, FormatChatTopicList(val)))
			continue
		case AIModelConfigs:
			for j, mc := range val.Configs {
				collectEnvVars(reflect.ValueOf(mc), fmt.Sprintf("%s_%d_", envName, j), lines)
			}
			continue
		}

		switch field.Kind() {
		case reflect.Struct:
			collectEnvVars(field, envName+"_", lines)

		case reflect.String:
			val := field.String()
			if strings.ContainsAny(val, "\n\r") {
				val = strconv.Quote(val)
			}
			*lines = append(*lines, fmt.Sprintf("%s=%s", envName, val))

		case reflect.Int, reflect.Int64:
			*lines = append(*lines, fmt.Sprintf("%s=%d", envName, field.Int()))

		case reflect.Bool:
			*lines = append(*lines, fmt.Sprintf("%s=%v", envName, field.Bool()))

		case reflect.Float64:
			*lines = append(*lines, fmt.Sprintf("%s=%g", envName, field.Float()))

		case reflect.Ptr:
			if !field.IsNil() {
				switch field.Elem().Kind() {
				case reflect.Float64:
					*lines = append(*lines, fmt.Sprintf("%s=%g", envName, field.Elem().Float()))
				case reflect.Bool:
					*lines = append(*lines, fmt.Sprintf("%s=%v", envName, field.Elem().Bool()))
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
				*lines = append(*lines, fmt.Sprintf("%s=%s", envName, strings.Join(parts, ",")))
			case reflect.String:
				var parts []string
				for j := 0; j < field.Len(); j++ {
					parts = append(parts, field.Index(j).String())
				}
				*lines = append(*lines, fmt.Sprintf("%s=%s", envName, strings.Join(parts, ",")))
			case reflect.Struct:
				for j := 0; j < field.Len(); j++ {
					collectEnvVars(field.Index(j), fmt.Sprintf("%s_%d_", envName, j), lines)
				}
			}
		}
	}
}
