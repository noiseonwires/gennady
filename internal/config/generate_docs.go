// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// YAMLPathToEnv converts a YAML dot-path to an ENV variable name.
// e.g. "ai.content_moderation.enabled" → "AI_CONTENT_MODERATION_ENABLED"
func YAMLPathToEnv(yamlPath string) string {
	return strings.ToUpper(strings.ReplaceAll(yamlPath, ".", "_"))
}

// parseLabelEntry splits "Label // Description // ph1,ph2" into parts.
func parseLabelEntry(s string) (label, desc string, placeholders []string) {
	parts := strings.SplitN(s, " // ", 3)
	label = strings.TrimSpace(parts[0])
	if len(parts) >= 2 {
		desc = strings.TrimSpace(parts[1])
	}
	if len(parts) >= 3 {
		for _, p := range strings.Split(parts[2], ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				placeholders = append(placeholders, p)
			}
		}
	}
	return
}

// GenerateConfigDocs generates one markdown file per language documenting all
// config options with descriptions and their ENV variable names.
// dataDir should point to the directory containing config_labels_*.json files.
// outputPath is a base path like "CONFIG_REFERENCE.md"; files will be named
// "CONFIG_REFERENCE_en.md", "CONFIG_REFERENCE_ru.md", etc.
func GenerateConfigDocs(dataDir, outputPath string) error {
	// Discover available languages by scanning for config_labels_*.json
	matches, err := filepath.Glob(filepath.Join(dataDir, "config_labels_*.json"))
	if err != nil {
		return fmt.Errorf("scanning label files: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("no config_labels_*.json files found in %s", dataDir)
	}

	// Load all language label maps
	type langData struct {
		code   string
		labels map[string]string
	}
	var langs []langData
	for _, path := range matches {
		base := filepath.Base(path)
		code := strings.TrimSuffix(strings.TrimPrefix(base, "config_labels_"), ".json")

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		var labels map[string]string
		if err := json.Unmarshal(data, &labels); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		langs = append(langs, langData{code: code, labels: labels})
	}

	// Get reflection metadata
	fields, sections := ReflectConfigMeta()

	// Group fields by section
	fieldsBySection := make(map[string][]FieldMeta)
	for _, f := range fields {
		fieldsBySection[f.Section] = append(fieldsBySection[f.Section], f)
	}

	// Derive base name and extension for per-language output files
	ext := filepath.Ext(outputPath)
	base := strings.TrimSuffix(outputPath, ext)

	// Generate one file per language
	for _, lang := range langs {
		outFile := fmt.Sprintf("%s_%s%s", base, lang.code, ext)
		if err := generateSingleLangDoc(outFile, lang.code, lang.labels, fields, sections, fieldsBySection); err != nil {
			return err
		}
	}

	return nil
}

func generateSingleLangDoc(outFile, langCode string, labels map[string]string, fields []FieldMeta, sections []SectionMeta, fieldsBySection map[string][]FieldMeta) error {
	var b strings.Builder
	title := fmt.Sprintf("# Configuration Reference (%s)\n\n", strings.ToUpper(langCode))
	intro := "This file is auto-generated. Do not edit manually.\n\n"
	yamlHeader := "YAML Key"
	typeHeader := "Type"
	descHeader := "Description"
	placeholdersLabel := "placeholders"
	if langCode == "ru" {
		title = "# Справочник по конфигурации (RU)\n\n"
		intro = "Этот файл сформирован автоматически. Не редактируйте его вручную.\n\n"
		yamlHeader = "Ключ YAML"
		typeHeader = "Тип"
		descHeader = "Описание"
		placeholdersLabel = "плейсхолдеры"
	}
	b.WriteString(title)
	b.WriteString(intro)

	for _, sec := range sections {
		sFields := fieldsBySection[sec.ID]
		if len(sFields) == 0 {
			continue
		}

		// Section header
		sKey := "section:" + sec.ID
		heading := sec.ID
		if lbl, ok := labels[sKey]; ok && lbl != "" {
			heading = lbl
		}
		b.WriteString(fmt.Sprintf("## %s\n\n", heading))

		// Fields table
		b.WriteString(fmt.Sprintf("| %s | ENV | %s | %s |\n", yamlHeader, typeHeader, descHeader))
		b.WriteString("|---|---|---|---|\n")

		for _, f := range sFields {
			envName := YAMLPathToEnv(f.Key)
			typeName := f.Type
			if f.Sensitive {
				typeName += " 🔒"
			}

			raw := labels[f.Key]
			label, desc, ph := parseLabelEntry(raw)
			cell := label
			if desc != "" {
				cell += " - " + desc
			}
			if len(ph) > 0 {
				phStrs := make([]string, len(ph))
				for i, p := range ph {
					phStrs[i] = "`{{" + p + "}}`"
				}
				cell += " (" + placeholdersLabel + ": " + strings.Join(phStrs, ", ") + ")"
			}

			b.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n", f.Key, envName, typeName, cell))
		}

		b.WriteString("\n")
	}

	if err := os.WriteFile(outFile, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", outFile, err)
	}
	return nil
}
