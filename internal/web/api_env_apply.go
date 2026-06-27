// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"gennadium/internal/config"
)

// Reflection-based env-name → config-field mapping used by the env-file
// upload path (handleUploadEnv in api_files.go).
//
// Walks the config struct depth-first, matching the targetEnv name against
// the upper-cased dotted yaml tags. A couple of custom types (ChatIDList,
// AIModelConfigs) get special handling.

// validateAndApplyEnvUpdates validates types before applying env-based
// updates and returns a list of validation errors.
func validateAndApplyEnvUpdates(cfg *config.Config, updates map[string]string) []string {
	var errs []string
	for envName, val := range updates {
		setConfigFieldByEnv(reflect.ValueOf(cfg).Elem(), "", envName, val, &errs)
	}
	return errs
}

func setConfigFieldByEnv(v reflect.Value, prefix, targetEnv, val string, errs *[]string) bool {
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

		switch field.Kind() {
		case reflect.Struct:
			// Handle custom types first
			if field.CanAddr() {
				switch field.Addr().Interface().(type) {
				case *config.ChatIDList:
					if envName == targetEnv {
						ptr := field.Addr().Interface().(*config.ChatIDList)
						ptr.IDs = nil
						for _, p := range strings.Split(val, ",") {
							p = strings.TrimSpace(p)
							if p == "" {
								continue
							}
							id, err := strconv.ParseInt(p, 10, 64)
							if err != nil {
								addValidationError(errs, targetEnv, fmt.Sprintf("invalid integer %q in list", p))
								return true
							}
							ptr.IDs = append(ptr.IDs, id)
						}
						return true
					}
					continue
				case *config.AIModelConfigs:
					if strings.HasPrefix(targetEnv, envName+"_") {
						ptr := field.Addr().Interface().(*config.AIModelConfigs)
						rest := strings.TrimPrefix(targetEnv, envName+"_")
						parts := strings.SplitN(rest, "_", 2)
						if len(parts) == 2 {
							idx, err := strconv.Atoi(parts[0])
							if err == nil {
								for len(ptr.Configs) <= idx {
									ptr.Configs = append(ptr.Configs, config.AIModelConfig{})
								}
								setConfigFieldByEnv(reflect.ValueOf(&ptr.Configs[idx]).Elem(), "", parts[1], val, errs)
							}
						}
						return true
					}
					continue
				}
			}
			if setConfigFieldByEnv(field, envName+"_", targetEnv, val, errs) {
				return true
			}

		case reflect.String:
			if envName == targetEnv {
				field.SetString(val)
				return true
			}

		case reflect.Int, reflect.Int64:
			if envName == targetEnv {
				n, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					addValidationError(errs, targetEnv, fmt.Sprintf("expected integer, got %q", val))
					return true
				}
				field.SetInt(n)
				return true
			}

		case reflect.Bool:
			if envName == targetEnv {
				field.SetBool(strings.EqualFold(val, "true") || strings.EqualFold(val, "yes") || val == "1")
				return true
			}

		case reflect.Float64:
			if envName == targetEnv {
				f, err := strconv.ParseFloat(val, 64)
				if err != nil {
					addValidationError(errs, targetEnv, fmt.Sprintf("expected number, got %q", val))
					return true
				}
				field.SetFloat(f)
				return true
			}

		case reflect.Ptr:
			if envName == targetEnv && field.Type().Elem().Kind() == reflect.Float64 {
				f, err := strconv.ParseFloat(val, 64)
				if err != nil {
					addValidationError(errs, targetEnv, fmt.Sprintf("expected number, got %q", val))
					return true
				}
				field.Set(reflect.ValueOf(&f))
				return true
			}

		case reflect.Slice:
			if envName == targetEnv {
				elemType := fieldType.Type.Elem()
				switch elemType.Kind() {
				case reflect.Int:
					var slice []int
					for _, p := range strings.Split(val, ",") {
						p = strings.TrimSpace(p)
						if p == "" {
							continue
						}
						n, err := strconv.Atoi(p)
						if err != nil {
							addValidationError(errs, targetEnv, fmt.Sprintf("invalid integer %q in list", p))
							return true
						}
						slice = append(slice, n)
					}
					field.Set(reflect.ValueOf(slice))
				case reflect.Int64:
					var slice []int64
					for _, p := range strings.Split(val, ",") {
						p = strings.TrimSpace(p)
						if p == "" {
							continue
						}
						n, err := strconv.ParseInt(p, 10, 64)
						if err != nil {
							addValidationError(errs, targetEnv, fmt.Sprintf("invalid integer %q in list", p))
							return true
						}
						slice = append(slice, n)
					}
					field.Set(reflect.ValueOf(slice))
				case reflect.String:
					var slice []string
					for _, p := range strings.Split(val, ",") {
						slice = append(slice, strings.TrimSpace(p))
					}
					field.Set(reflect.ValueOf(slice))
				}
				return true
			}
		}
	}
	return false
}
