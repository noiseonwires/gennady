// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"reflect"
	"strings"
)

// FieldMeta describes a single config field for the web UI, generated via reflection.
type FieldMeta struct {
	Key       string `json:"key"`     // YAML dot-path, e.g. "ai.content_moderation.enabled"
	Section   string `json:"section"` // first-level section ID, e.g. "ai_content_moderation"
	Type      string `json:"type"`    // "string","int","int64","bool","float64","[]int","[]int64","[]string"
	Sensitive bool   `json:"sensitive,omitempty"`
}

// SectionMeta describes a config section derived from struct nesting.
type SectionMeta struct {
	ID    string `json:"id"`    // e.g. "ai_content_moderation"
	Depth int    `json:"depth"` // nesting depth for ordering (0 = top-level fields)
}

// ReflectConfigMeta walks the Config struct via reflection and returns
// field metadata and ordered section list. Fields that are complex types
// handled separately (AIModelConfigs, RssFeed list) are skipped.
func ReflectConfigMeta() ([]FieldMeta, []SectionMeta) {
	var fields []FieldMeta
	sectionOrder := []SectionMeta{}
	sectionSeen := map[string]bool{}

	walkStruct(reflect.TypeOf(Config{}), "", "", &fields, &sectionOrder, &sectionSeen, 0)
	return fields, sectionOrder
}

func walkStruct(t reflect.Type, prefix, sectionPrefix string, fields *[]FieldMeta, sections *[]SectionMeta, seen *map[string]bool, depth int) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		yamlTag := sf.Tag.Get("yaml")
		tagName := strings.Split(yamlTag, ",")[0]
		if tagName == "" || tagName == "-" {
			continue
		}

		// Skip fields explicitly hidden from web UI
		if sf.Tag.Get("web") == "-" {
			continue
		}

		key := tagName
		if prefix != "" {
			key = prefix + "." + tagName
		}

		ft := sf.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		// Skip complex types handled by dedicated editors
		if isSkippedType(ft) {
			continue
		}

		// Struct → check if it's a "flat" type that shouldn't create a subsection
		if ft.Kind() == reflect.Struct {
			// ChatIDList is handled as a single field, not a section
			if ft.Name() == "ChatIDList" {
				section := strings.TrimSuffix(sectionPrefix, "_")
				if section == "" {
					section = "general"
					if !(*seen)[section] {
						(*seen)[section] = true
						*sections = append(*sections, SectionMeta{ID: section, Depth: 0})
					}
				}
				fm := FieldMeta{
					Key:     key,
					Section: section,
					Type:    "[]int64",
				}
				*fields = append(*fields, fm)
				continue
			}

			// ChatTopicList is handled as a single field, not a section
			if ft.Name() == "ChatTopicList" {
				section := strings.TrimSuffix(sectionPrefix, "_")
				if section == "" {
					section = "general"
					if !(*seen)[section] {
						(*seen)[section] = true
						*sections = append(*sections, SectionMeta{ID: section, Depth: 0})
					}
				}
				fm := FieldMeta{
					Key:     key,
					Section: section,
					Type:    "[]chat_topic",
				}
				*fields = append(*fields, fm)
				continue
			}

			// PromptPair: flatten system/user into parent section instead of creating subsection
			if ft.Name() == "PromptPair" {
				section := strings.TrimSuffix(sectionPrefix, "_")
				if section == "" {
					section = "general"
				}
				if !(*seen)[section] {
					(*seen)[section] = true
					*sections = append(*sections, SectionMeta{ID: section, Depth: depth})
				}
				for j := 0; j < ft.NumField(); j++ {
					psf := ft.Field(j)
					pTag := strings.Split(psf.Tag.Get("yaml"), ",")[0]
					if pTag == "" || pTag == "-" {
						continue
					}
					fm := FieldMeta{
						Key:     key + "." + pTag,
						Section: section,
						Type:    goTypeToString(psf.Type),
					}
					*fields = append(*fields, fm)
				}
				continue
			}

			// Regular struct → recurse, treating it as a section
			secID := sectionPrefix + tagName
			if !(*seen)[secID] {
				(*seen)[secID] = true
				*sections = append(*sections, SectionMeta{ID: secID, Depth: depth})
			}
			walkStruct(ft, key, secID+"_", fields, sections, seen, depth+1)
			continue
		}

		// Determine section for this field
		section := strings.TrimSuffix(sectionPrefix, "_")
		if section == "" {
			section = "general"
			if !(*seen)[section] {
				(*seen)[section] = true
				*sections = append(*sections, SectionMeta{ID: section, Depth: 0})
			}
		}

		fm := FieldMeta{
			Key:     key,
			Section: section,
			Type:    goTypeToString(sf.Type),
		}

		// Check web tag for sensitive
		webTag := sf.Tag.Get("web")
		if strings.Contains(webTag, "sensitive") {
			fm.Sensitive = true
		}

		*fields = append(*fields, fm)
	}
}

func isSkippedType(t reflect.Type) bool {
	name := t.Name()
	// Skip wrapper types that have dedicated UI editors
	switch name {
	case "AIModelConfigs", "ChatIDList", "ChatTopicList":
		return false // handled as a flat field in walkStruct
	}
	// Skip slice-of-struct types (RSS feeds handled separately)
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Struct {
		return true
	}
	return false
}

func goTypeToString(t reflect.Type) string {
	if t.Kind() == reflect.Ptr {
		// *float64 → "float64"
		return t.Elem().Kind().String()
	}
	if t.Kind() == reflect.Slice {
		elem := t.Elem()
		return "[]" + elem.Kind().String()
	}
	// Handle custom types
	switch t.Name() {
	case "ChatIDList":
		return "[]int64"
	case "ChatTopicList":
		return "[]chat_topic"
	}
	return t.Kind().String()
}

// GetConfigValues returns the current config as a map of YAML dot-path → string value.
// It uses the same reflection walk as ExportEnvVars but produces dot-path keys.
func GetConfigValues(cfg *Config) map[string]interface{} {
	result := make(map[string]interface{})
	collectValues(reflect.ValueOf(*cfg), "", result)
	return result
}

// SensitiveConfigKeys returns the set of dot-path config keys whose values are
// marked sensitive (secrets such as tokens, API keys and passwords) and must
// not be exposed in plaintext over the API.
func SensitiveConfigKeys() map[string]bool {
	fields, _ := ReflectConfigMeta()
	keys := make(map[string]bool, len(fields))
	for _, f := range fields {
		if f.Sensitive {
			keys[f.Key] = true
		}
	}
	return keys
}

func collectValues(v reflect.Value, prefix string, result map[string]interface{}) {
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

		ft := field.Type()
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		// Handle custom aggregate types
		switch val := field.Interface().(type) {
		case ChatIDList:
			result[key] = val.IDs
			continue
		case ChatTopicList:
			result[key] = val.Refs
			continue
		case AIModelConfigs:
			// Handled separately by dedicated editor
			continue
		}

		switch field.Kind() {
		case reflect.Struct:
			if ft.Kind() == reflect.Struct {
				collectValues(field, key, result)
			}
		case reflect.String:
			result[key] = field.String()
		case reflect.Int, reflect.Int64:
			result[key] = field.Int()
		case reflect.Bool:
			result[key] = field.Bool()
		case reflect.Float64:
			result[key] = field.Float()
		case reflect.Ptr:
			if !field.IsNil() {
				switch field.Elem().Kind() {
				case reflect.Float64:
					result[key] = field.Elem().Float()
				case reflect.Bool:
					result[key] = field.Elem().Bool()
				default:
					result[key] = nil
				}
			} else {
				result[key] = nil
			}
		case reflect.Slice:
			elemType := fieldType.Type.Elem()
			switch elemType.Kind() {
			case reflect.Int:
				s := make([]int, field.Len())
				for j := 0; j < field.Len(); j++ {
					s[j] = int(field.Index(j).Int())
				}
				result[key] = s
			case reflect.Int64:
				s := make([]int64, field.Len())
				for j := 0; j < field.Len(); j++ {
					s[j] = field.Index(j).Int()
				}
				result[key] = s
			case reflect.String:
				s := make([]string, field.Len())
				for j := 0; j < field.Len(); j++ {
					s[j] = field.Index(j).String()
				}
				result[key] = s
			case reflect.Struct:
				// Skip slice-of-struct (rss_feeds handled separately)
			}
		}
	}
}

// SetConfigValue sets a single config field by its YAML dot-path.
// Returns an error message if the field is not found or the value is invalid.
func SetConfigValue(cfg *Config, key string, value interface{}) string {
	return setValueRecursive(reflect.ValueOf(cfg).Elem(), "", key, value)
}

func setValueRecursive(v reflect.Value, prefix, targetKey string, value interface{}) string {
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

		// Handle ChatIDList
		if field.CanAddr() {
			if ptr, ok := field.Addr().Interface().(*ChatIDList); ok {
				if key == targetKey {
					return setChatIDList(ptr, value)
				}
				continue
			}
			if ptr, ok := field.Addr().Interface().(*ChatTopicList); ok {
				if key == targetKey {
					return setChatTopicList(ptr, value)
				}
				continue
			}
			// Skip AIModelConfigs (handled by dedicated endpoint)
			if _, ok := field.Addr().Interface().(*AIModelConfigs); ok {
				continue
			}
		}

		if field.Kind() == reflect.Struct {
			if result := setValueRecursive(field, key, targetKey, value); result != "" {
				return result
			}
			continue
		}

		if key != targetKey {
			continue
		}

		// Found the field - set it
		return setFieldValue(field, fieldType, value)
	}
	return ""
}

func setChatIDList(ptr *ChatIDList, value interface{}) string {
	switch v := value.(type) {
	case []interface{}:
		ptr.IDs = nil
		for _, item := range v {
			switch n := item.(type) {
			case float64:
				ptr.IDs = append(ptr.IDs, int64(n))
			case int64:
				ptr.IDs = append(ptr.IDs, n)
			default:
				return "invalid value in chat ID list"
			}
		}
	case string:
		ptr.IDs = nil
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			var n int64
			_, err := parseIntTo(&n, p)
			if err {
				return "invalid integer in chat ID list: " + p
			}
			ptr.IDs = append(ptr.IDs, n)
		}
	default:
		return "unexpected type for chat ID list"
	}
	return "ok"
}

// setChatTopicList accepts either:
//   - a JSON array of {chat, topic} objects ([]interface{} of map[string]interface{})
//   - a JSON array of bare numbers (treated as topic IDs with Chat=0)
//   - the compact "chat:topic,chat:topic" string form
func setChatTopicList(ptr *ChatTopicList, value interface{}) string {
	switch v := value.(type) {
	case []interface{}:
		refs := make([]ChatTopicRef, 0, len(v))
		for _, item := range v {
			ref, err := refFromInterface(item)
			if err != "" {
				return err
			}
			refs = append(refs, ref)
		}
		ptr.Refs = refs
	case string:
		parsed, err := ParseChatTopicList(v)
		if err != nil {
			return err.Error()
		}
		*ptr = parsed
	case nil:
		ptr.Refs = nil
	default:
		return "unexpected type for chat_topic list"
	}
	return "ok"
}

func refFromInterface(item interface{}) (ChatTopicRef, string) {
	switch n := item.(type) {
	case map[string]interface{}:
		ref := ChatTopicRef{}
		if v, ok := n["chat"]; ok {
			switch c := v.(type) {
			case float64:
				ref.Chat = int64(c)
			case int64:
				ref.Chat = c
			case string:
				var parsed int64
				_, bad := parseIntTo(&parsed, c)
				if bad {
					return ChatTopicRef{}, "invalid chat in chat_topic entry: " + c
				}
				ref.Chat = parsed
			default:
				return ChatTopicRef{}, "invalid chat in chat_topic entry"
			}
		}
		if v, ok := n["topic"]; ok && v != nil {
			switch t := v.(type) {
			case float64:
				ref.Topic = int(t)
			case int64:
				ref.Topic = int(t)
			case string:
				var parsed int64
				_, bad := parseIntTo(&parsed, t)
				if bad {
					return ChatTopicRef{}, "invalid topic in chat_topic entry: " + t
				}
				ref.Topic = int(parsed)
			default:
				return ChatTopicRef{}, "invalid topic in chat_topic entry"
			}
		}
		return ref, ""
	case float64:
		return ChatTopicRef{Chat: 0, Topic: int(n)}, ""
	case int64:
		return ChatTopicRef{Chat: 0, Topic: int(n)}, ""
	case string:
		ref, err := parseChatTopicToken(n)
		if err != nil {
			return ChatTopicRef{}, err.Error()
		}
		return ref, ""
	default:
		return ChatTopicRef{}, "invalid element in chat_topic list"
	}
}

func parseIntTo(dst *int64, s string) (int64, bool) {
	for _, c := range s {
		if c == '-' || (c >= '0' && c <= '9') {
			continue
		}
		return 0, true
	}
	var n int64
	for i, c := range s {
		if c == '-' && i == 0 {
			continue
		}
		n = n*10 + int64(c-'0')
	}
	if len(s) > 0 && s[0] == '-' {
		n = -n
	}
	*dst = n
	return n, false
}

func setFieldValue(field reflect.Value, fieldType reflect.StructField, value interface{}) string {
	// JSON numbers come as float64
	switch field.Kind() {
	case reflect.String:
		s, ok := value.(string)
		if !ok {
			return "expected string"
		}
		field.SetString(s)

	case reflect.Bool:
		switch v := value.(type) {
		case bool:
			field.SetBool(v)
		case string:
			field.SetBool(strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || v == "1")
		default:
			return "expected boolean"
		}

	case reflect.Int, reflect.Int64:
		switch v := value.(type) {
		case float64:
			field.SetInt(int64(v))
		case int64:
			field.SetInt(v)
		case string:
			// Try parsing
			var n int64
			_, bad := parseIntTo(&n, v)
			if bad {
				return "expected integer"
			}
			field.SetInt(n)
		default:
			return "expected integer"
		}

	case reflect.Float64:
		switch v := value.(type) {
		case float64:
			field.SetFloat(v)
		case string:
			return "expected number"
		default:
			return "expected number"
		}

	case reflect.Ptr:
		switch field.Type().Elem().Kind() {
		case reflect.Float64:
			if value == nil {
				field.Set(reflect.Zero(field.Type()))
				return "ok"
			}
			switch v := value.(type) {
			case float64:
				field.Set(reflect.ValueOf(&v))
			default:
				return "expected number or null"
			}
		case reflect.Bool:
			if value == nil {
				field.Set(reflect.Zero(field.Type()))
				return "ok"
			}
			switch v := value.(type) {
			case bool:
				field.Set(reflect.ValueOf(&v))
			default:
				return "expected boolean or null"
			}
		}

	case reflect.Slice:
		return setSliceValue(field, fieldType, value)
	}
	return "ok"
}

// splitListValue splits a string by commas or semicolons.
func splitListValue(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' })
}

func setSliceValue(field reflect.Value, fieldType reflect.StructField, value interface{}) string {
	elemType := fieldType.Type.Elem()

	// Accept arrays from JSON
	if arr, ok := value.([]interface{}); ok {
		switch elemType.Kind() {
		case reflect.Int:
			slice := reflect.MakeSlice(fieldType.Type, 0, len(arr))
			for _, item := range arr {
				if n, ok := item.(float64); ok {
					slice = reflect.Append(slice, reflect.ValueOf(int(n)))
				}
			}
			field.Set(slice)
		case reflect.Int64:
			slice := reflect.MakeSlice(fieldType.Type, 0, len(arr))
			for _, item := range arr {
				if n, ok := item.(float64); ok {
					slice = reflect.Append(slice, reflect.ValueOf(int64(n)))
				}
			}
			field.Set(slice)
		case reflect.String:
			slice := reflect.MakeSlice(fieldType.Type, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					slice = reflect.Append(slice, reflect.ValueOf(s))
				}
			}
			field.Set(slice)
		}
		return "ok"
	}

	// Accept comma- or semicolon-separated string
	if s, ok := value.(string); ok {
		parts := splitListValue(s)
		switch elemType.Kind() {
		case reflect.Int:
			slice := reflect.MakeSlice(fieldType.Type, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				var n int64
				_, bad := parseIntTo(&n, p)
				if bad {
					return "invalid integer in list: " + p
				}
				slice = reflect.Append(slice, reflect.ValueOf(int(n)))
			}
			field.Set(slice)
		case reflect.Int64:
			slice := reflect.MakeSlice(fieldType.Type, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				var n int64
				_, bad := parseIntTo(&n, p)
				if bad {
					return "invalid integer in list: " + p
				}
				slice = reflect.Append(slice, reflect.ValueOf(n))
			}
			field.Set(slice)
		case reflect.String:
			slice := reflect.MakeSlice(fieldType.Type, 0, len(parts))
			for _, p := range parts {
				slice = reflect.Append(slice, reflect.ValueOf(strings.TrimSpace(p)))
			}
			field.Set(slice)
		}
		return "ok"
	}

	return "expected array or comma-separated string"
}
