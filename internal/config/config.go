// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

// Package config loads, validates and exposes the bot's runtime configuration.
//
// Configuration can come from any combination of:
//
//   - a YAML file (optional; the bot can run on env-vars alone)
//   - environment variables (named after YAML paths, uppercased; see types.go)
//   - the database (config_db.go - used when the YAML file is absent and a
//     remote DB is configured)
//
// Effective precedence is env > YAML > defaults.
//
// File layout (all in this package):
//
//	config.go         Load() entry point + package docs
//	types.go          all Config / sub-struct types and tiny accessor methods
//	types_custom.go   ChatIDList, AIModelConfig(s) - custom YAML marshalling
//	defaults.go       setDefaults() - fills zero values
//	env_apply.go      applyEnvOverrides() - env > YAML precedence
//	env_export.go     GetEnvOverrides() + ExportEnvVars() - introspection
//	unknown_keys.go   warnUnknownKeys() - typo detection on YAML load
//	validation.go     prompt validation + MissingConfigFields + moderation-action helpers
//	helpers.go        chat-id / reply-id predicates used across the bot
//	config_db.go      DB <-> Config flat-map conversion
//	config_reflect.go reflection-based field metadata for web UI
//	generate_docs.go  CLI subcommand that renders CONFIG_REFERENCE_*.md
package config

import (
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Load reads configuration from a YAML file, applies environment variable
// overrides, and fills in defaults. The config file is optional - the bot
// can be configured entirely through environment variables.
func Load(filename string) (*Config, error) {
	var config Config

	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("⚠️  Config file %s not found, using defaults + environment variables", filename)
		} else {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}

		// Check for unknown keys and warn about them
		var raw map[string]interface{}
		if err := yaml.Unmarshal(data, &raw); err == nil {
			warnUnknownKeys(raw, reflect.TypeOf(Config{}), "")
		}
	}

	// Apply environment variable overrides (take precedence over file values)
	applyEnvOverrides(&config)

	// Fill in defaults for unset values
	setDefaults(&config)

	// PORT env var fallback for server (common in container environments)
	if port := os.Getenv("PORT"); port != "" {
		if _, ok := os.LookupEnv("SERVER_LISTEN_PORT"); !ok {
			if p, err := strconv.Atoi(port); err == nil {
				config.Server.ListenPort = p
			}
		}
	}

	// Copy light model credentials from full model when not specified
	if len(config.AI.LightModel.Configs) > 0 && len(config.AI.FullModel.Configs) > 0 {
		if config.AI.LightModel.Configs[0].Endpoint == "" {
			config.AI.LightModel.Configs[0].Endpoint = config.AI.FullModel.Configs[0].Endpoint
		}
		if config.AI.LightModel.Configs[0].APIKey == "" {
			config.AI.LightModel.Configs[0].APIKey = config.AI.FullModel.Configs[0].APIKey
		}
	}

	// Validation
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if config.AI.Enabled {
		config.AI.WarnMissingPrompts()
	}

	return &config, nil
}
