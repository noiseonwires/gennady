// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	_ "embed"
	"encoding/json"
	"log"
	"strings"
	"sync"

	"gennadium/internal/config"
)

//go:embed data/config_labels_en.json
var configLabelsENJSON []byte

//go:embed data/config_labels_ru.json
var configLabelsRUJSON []byte

//go:embed data/i18n_en.json
var i18nENJSON []byte

//go:embed data/i18n_ru.json
var i18nRUJSON []byte

// ConfigFieldUI is the combined metadata for a config field sent to the frontend.
type ConfigFieldUI struct {
	Key          string   `json:"key"`
	Section      string   `json:"section"`
	Type         string   `json:"type"`
	Sensitive    bool     `json:"sensitive,omitempty"`
	LabelEN      string   `json:"label_en"`
	LabelRU      string   `json:"label_ru"`
	DescEN       string   `json:"desc_en"`
	DescRU       string   `json:"desc_ru"`
	Placeholders []string `json:"placeholders,omitempty"`
}

// SectionUI is a section entry sent to the frontend.
type SectionUI struct {
	ID      string `json:"id"`
	LabelEN string `json:"label_en"`
	LabelRU string `json:"label_ru"`
}

// parsed caches
var (
	configMetaOnce sync.Once
	cachedFields   []ConfigFieldUI
	cachedSections []SectionUI
	i18nOnce       sync.Once
	i18nData       map[string]map[string]string
)

// parseLabel splits a label string in the format "Label // Description // ph1,ph2"
// into label, description, and optional placeholders.
func parseLabel(s string) (label, desc string, placeholders []string) {
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

// buildConfigMeta combines reflection metadata with i18n labels.
func buildConfigMeta() ([]ConfigFieldUI, []SectionUI) {
	reflectFields, reflectSections := config.ReflectConfigMeta()

	var labelsEN, labelsRU map[string]string
	if err := json.Unmarshal(configLabelsENJSON, &labelsEN); err != nil {
		log.Fatalf("failed to parse config_labels_en.json: %v", err)
	}
	if err := json.Unmarshal(configLabelsRUJSON, &labelsRU); err != nil {
		log.Fatalf("failed to parse config_labels_ru.json: %v", err)
	}

	// Build sections with labels
	sections := make([]SectionUI, len(reflectSections))
	for i, s := range reflectSections {
		sKey := "section:" + s.ID
		sections[i] = SectionUI{
			ID:      s.ID,
			LabelEN: labelsEN[sKey],
			LabelRU: labelsRU[sKey],
		}
		if sections[i].LabelEN == "" {
			sections[i].LabelEN = s.ID
		}
		if sections[i].LabelRU == "" {
			sections[i].LabelRU = sections[i].LabelEN
		}
	}

	// Build fields with labels
	fields := make([]ConfigFieldUI, len(reflectFields))
	for i, f := range reflectFields {
		labelEN, descEN, ph := parseLabel(labelsEN[f.Key])
		labelRU, descRU, _ := parseLabel(labelsRU[f.Key])
		if labelEN == "" {
			labelEN = f.Key
		}
		if labelRU == "" {
			labelRU = labelEN
		}
		fields[i] = ConfigFieldUI{
			Key:          f.Key,
			Section:      f.Section,
			Type:         f.Type,
			Sensitive:    f.Sensitive,
			LabelEN:      labelEN,
			LabelRU:      labelRU,
			DescEN:       descEN,
			DescRU:       descRU,
			Placeholders: ph,
		}
	}

	return fields, sections
}

// GetConfigMeta returns combined field metadata (reflection + labels).
func GetConfigMeta() ([]ConfigFieldUI, []SectionUI) {
	configMetaOnce.Do(func() {
		cachedFields, cachedSections = buildConfigMeta()
	})
	return cachedFields, cachedSections
}

// GetI18n returns the combined i18n map {"en":{...},"ru":{...}}.
func GetI18n() map[string]map[string]string {
	i18nOnce.Do(func() {
		var en, ru map[string]string
		if err := json.Unmarshal(i18nENJSON, &en); err != nil {
			log.Fatalf("failed to parse embedded i18n_en.json: %v", err)
		}
		if err := json.Unmarshal(i18nRUJSON, &ru); err != nil {
			log.Fatalf("failed to parse embedded i18n_ru.json: %v", err)
		}
		i18nData = map[string]map[string]string{"en": en, "ru": ru}
	})
	return i18nData
}
