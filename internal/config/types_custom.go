// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Custom YAML aggregate types. These exist mostly to accept "one or many"
// shorthands in user-written config files (a single chat ID or a list, a
// single AI model entry or a list) while presenting a typed Go value to the
// rest of the codebase.

// AI model provider identifiers. "azure" targets the Azure OpenAI REST surface
// (deployment in the URL path, api-key header); "openai" targets the standard
// OpenAI REST surface and any OpenAI-compatible gateway (model in the request
// body, Bearer auth).
const (
	AIProviderAzure  = "azure"
	AIProviderOpenAI = "openai"
)

// ChatIDList supports both single int64 and array of int64 in YAML.
type ChatIDList struct {
	IDs []int64
}

func (c *ChatIDList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.SequenceNode {
		return value.Decode(&c.IDs)
	}
	var id int64
	if err := value.Decode(&id); err != nil {
		return err
	}
	if id != 0 {
		c.IDs = []int64{id}
	}
	return nil
}

func (c ChatIDList) MarshalYAML() (interface{}, error) {
	if len(c.IDs) == 1 {
		return c.IDs[0], nil
	}
	return c.IDs, nil
}

func (c *ChatIDList) Contains(chatID int64) bool {
	for _, id := range c.IDs {
		if id == chatID {
			return true
		}
	}
	return false
}

func (c *ChatIDList) First() int64 {
	if len(c.IDs) > 0 {
		return c.IDs[0]
	}
	return 0
}

func (c *ChatIDList) Count() int {
	return len(c.IDs)
}

func (c *ChatIDList) All() []int64 {
	return c.IDs
}

// AIModelConfig represents configuration for an AI model endpoint.
type AIModelConfig struct {
	Provider       string   `yaml:"provider,omitempty" json:"provider,omitempty"` // "azure" or "openai" (auto-detected from endpoint when empty)
	Endpoint       string   `yaml:"endpoint" json:"endpoint"`
	APIKey         string   `yaml:"api_key" json:"api_key" web:"sensitive"`
	DeploymentName string   `yaml:"deployment_name" json:"deployment_name"`
	Temperature    *float64 `yaml:"temperature,omitempty" json:"temperature"`
	OmitMaxTokens  bool     `yaml:"omit_max_tokens" json:"omit_max_tokens"`
}

// ResolveProvider returns the effective provider for this model. When Provider
// is set explicitly it is honored (case-insensitively); otherwise it is
// auto-detected from the endpoint: Azure endpoints contain "azure" in the host
// (e.g. *.openai.azure.com or *.services.ai.azure.com), everything else is
// treated as a standard OpenAI / OpenAI-compatible endpoint. An empty endpoint
// defaults to Azure to preserve historical behavior.
func (m AIModelConfig) ResolveProvider() string {
	switch strings.ToLower(strings.TrimSpace(m.Provider)) {
	case AIProviderAzure:
		return AIProviderAzure
	case AIProviderOpenAI:
		return AIProviderOpenAI
	}
	if m.Endpoint == "" || strings.Contains(strings.ToLower(m.Endpoint), "azure") {
		return AIProviderAzure
	}
	return AIProviderOpenAI
}

// AIModelConfigs wraps one or more model configs (supports both single object and array in YAML).
type AIModelConfigs struct {
	Configs []AIModelConfig
}

func (a *AIModelConfigs) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.SequenceNode {
		return value.Decode(&a.Configs)
	}
	var single AIModelConfig
	if err := value.Decode(&single); err != nil {
		return err
	}
	a.Configs = []AIModelConfig{single}
	return nil
}

func (a AIModelConfigs) MarshalYAML() (interface{}, error) {
	if len(a.Configs) == 1 {
		return a.Configs[0], nil
	}
	return a.Configs, nil
}

func (a *AIModelConfigs) Get(index int) AIModelConfig {
	if len(a.Configs) == 0 {
		return AIModelConfig{}
	}
	return a.Configs[index%len(a.Configs)]
}

func (a *AIModelConfigs) Count() int {
	return len(a.Configs)
}

// ChatTopicRef identifies a specific (chat, topic) pair.
//
// Topic uses two sentinel values plus regular forum thread IDs:
//
//	TopicAny  (-1) - wildcard: any topic in this chat (incl. main area)
//	TopicMain ( 0) - the chat's main area (no forum thread)
//	N      (> 0)   - a specific forum thread ID
//
// In YAML/JSON object form both `chat` and `topic` are REQUIRED and the topic
// must be ≥ -1; loading fails otherwise. Bare integers in YAML/JSON sequences
// remain a convenience shorthand for single-chat setups (Chat is resolved to
// the lone moderation chat at validation time).
type ChatTopicRef struct {
	Chat  int64 `yaml:"chat" json:"chat"`
	Topic int   `yaml:"topic" json:"topic"`
}

// Topic sentinel values.
const (
	TopicAny  = -1
	TopicMain = 0
)

// parseTopicString converts a topic token to its int value, accepting the
// case-insensitive aliases "any" (→ TopicAny) and "main" (→ TopicMain) in
// addition to plain integers (-1, 0, or a positive thread id). Out-of-range
// values are rejected.
func parseTopicString(s string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "any":
		return TopicAny, nil
	case "main":
		return TopicMain, nil
	}
	t, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("invalid topic %q (use -1/any, 0/main, or a positive thread id)", s)
	}
	if t < TopicAny {
		return 0, fmt.Errorf("invalid topic %d (must be -1, 0, or a positive thread id)", t)
	}
	return t, nil
}

// topicFromJSON decodes a JSON topic value, accepting a number or one of the
// string aliases "any"/"main".
func topicFromJSON(raw json.RawMessage) (int, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, err
		}
		return parseTopicString(s)
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("invalid topic %s (use -1/any, 0/main, or a positive thread id)", trimmed)
	}
	if n < TopicAny {
		return 0, fmt.Errorf("invalid topic %d (must be -1, 0, or a positive thread id)", n)
	}
	return n, nil
}

// UnmarshalYAML enforces that object-form entries provide both `chat` and
// `topic` and that the topic value is within range. Scalar entries are left
// for ChatTopicList to handle.
func (r *ChatTopicRef) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("chat_topic: expected mapping object at line %d", value.Line)
	}
	var seenChat, seenTopic bool
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i]
		val := value.Content[i+1]
		switch key.Value {
		case "chat":
			if err := val.Decode(&r.Chat); err != nil {
				return fmt.Errorf("chat_topic: invalid chat at line %d: %w", val.Line, err)
			}
			seenChat = true
		case "topic":
			t, err := parseTopicString(val.Value)
			if err != nil {
				return fmt.Errorf("chat_topic: %v at line %d", err, val.Line)
			}
			r.Topic = t
			seenTopic = true
		default:
			return fmt.Errorf("chat_topic: unknown key %q at line %d (allowed: chat, topic)", key.Value, key.Line)
		}
	}
	if !seenChat {
		return fmt.Errorf("chat_topic: missing required 'chat' field at line %d", value.Line)
	}
	if !seenTopic {
		return fmt.Errorf("chat_topic: missing required 'topic' field at line %d (use -1/any for any topic, 0/main for main area, or a specific thread id)", value.Line)
	}
	return nil
}

// UnmarshalJSON applies the same strictness as UnmarshalYAML: both `chat` and
// `topic` must be present and topic must be ≥ -1 (number or "any"/"main").
func (r *ChatTopicRef) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("chat_topic: %w", err)
	}
	chatRaw, hasChat := raw["chat"]
	topicRaw, hasTopic := raw["topic"]
	if !hasChat {
		return fmt.Errorf("chat_topic: missing required 'chat' field")
	}
	if !hasTopic {
		return fmt.Errorf("chat_topic: missing required 'topic' field (use -1/any for any topic, 0/main for main area, or a specific thread id)")
	}
	if err := json.Unmarshal(chatRaw, &r.Chat); err != nil {
		return fmt.Errorf("chat_topic: invalid chat: %w", err)
	}
	t, err := topicFromJSON(topicRaw)
	if err != nil {
		return fmt.Errorf("chat_topic: %v", err)
	}
	r.Topic = t
	return nil
}

// ChatTopicList is a list of (chat, topic) pairs accepted in YAML/JSON in
// these forms:
//
//   - bare int            → {Chat: 0, Topic: <int>}  (single-chat shorthand;
//     Chat is resolved to the lone moderation chat at validation time)
//   - {chat: -100, topic: 7}
//   - list of either of the above (mixed)
//
// Always marshals back to the object-list form so files round-trip into the
// canonical shape after the first save.
type ChatTopicList struct {
	Refs []ChatTopicRef
}

// UnmarshalYAML accepts an int, a {chat,topic} mapping, or a sequence of either.
func (c *ChatTopicList) UnmarshalYAML(value *yaml.Node) error {
	c.Refs = nil
	switch value.Kind {
	case yaml.ScalarNode:
		ref, err := decodeChatTopicScalar(value)
		if err != nil {
			return err
		}
		c.Refs = []ChatTopicRef{ref}
		return nil
	case yaml.MappingNode:
		var ref ChatTopicRef
		if err := ref.UnmarshalYAML(value); err != nil {
			return err
		}
		c.Refs = []ChatTopicRef{ref}
		return nil
	case yaml.SequenceNode:
		for _, item := range value.Content {
			switch item.Kind {
			case yaml.ScalarNode:
				ref, err := decodeChatTopicScalar(item)
				if err != nil {
					return err
				}
				c.Refs = append(c.Refs, ref)
			case yaml.MappingNode:
				var ref ChatTopicRef
				if err := ref.UnmarshalYAML(item); err != nil {
					return err
				}
				c.Refs = append(c.Refs, ref)
			default:
				return fmt.Errorf("chat_topic list: unsupported element kind %d at line %d", item.Kind, item.Line)
			}
		}
		return nil
	case 0:
		return nil
	default:
		return fmt.Errorf("chat_topic list: unsupported node kind %d at line %d", value.Kind, value.Line)
	}
}

func decodeChatTopicScalar(node *yaml.Node) (ChatTopicRef, error) {
	t, err := parseTopicString(node.Value)
	if err != nil {
		return ChatTopicRef{}, fmt.Errorf("chat_topic: %v at line %d", err, node.Line)
	}
	// Chat=0 is a placeholder; Normalize resolves it later.
	return ChatTopicRef{Chat: 0, Topic: t}, nil
}

// MarshalYAML always emits the canonical object-list form.
func (c ChatTopicList) MarshalYAML() (interface{}, error) {
	if c.Refs == nil {
		return []ChatTopicRef{}, nil
	}
	return c.Refs, nil
}

// MarshalJSON emits a JSON array of {chat, topic} objects.
func (c ChatTopicList) MarshalJSON() ([]byte, error) {
	if c.Refs == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(c.Refs)
}

// UnmarshalJSON accepts a JSON array of {chat, topic} objects (strict form,
// what the web UI sends) or - for env/file-backed configs - bare integers in
// the array, which become single-chat shorthand refs.
func (c *ChatTopicList) UnmarshalJSON(data []byte) error {
	c.Refs = nil
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var raw []json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		for _, item := range raw {
			ref, err := unmarshalChatTopicJSONItem(item)
			if err != nil {
				return err
			}
			c.Refs = append(c.Refs, ref)
		}
		return nil
	}
	ref, err := unmarshalChatTopicJSONItem(data)
	if err != nil {
		return err
	}
	c.Refs = []ChatTopicRef{ref}
	return nil
}

func unmarshalChatTopicJSONItem(data []byte) (ChatTopicRef, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var ref ChatTopicRef
		if err := ref.UnmarshalJSON(data); err != nil {
			return ChatTopicRef{}, err
		}
		return ref, nil
	}
	// Bare number or "any"/"main" alias → placeholder topic, chat resolved later.
	t, err := topicFromJSON(data)
	if err != nil {
		return ChatTopicRef{}, fmt.Errorf("chat_topic: %v", err)
	}
	return ChatTopicRef{Chat: 0, Topic: t}, nil
}

// ParseChatTopicList parses the compact "chat:topic,chat:topic" string form
// used in env vars and the flat DB key/value store. Bare integers (no colon)
// are accepted and stored with Chat=0 (resolved later by Normalize).
//
// An empty string yields an empty list (not nil); whitespace around tokens is
// trimmed; empty tokens are skipped.
func ParseChatTopicList(s string) (ChatTopicList, error) {
	out := ChatTopicList{Refs: []ChatTopicRef{}}
	s = strings.TrimSpace(s)
	if s == "" {
		return out, nil
	}
	for _, raw := range strings.Split(s, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ref, err := parseChatTopicToken(raw)
		if err != nil {
			return ChatTopicList{}, err
		}
		out.Refs = append(out.Refs, ref)
	}
	return out, nil
}

func parseChatTopicToken(tok string) (ChatTopicRef, error) {
	if idx := strings.Index(tok, ":"); idx >= 0 {
		chatStr := strings.TrimSpace(tok[:idx])
		topicStr := strings.TrimSpace(tok[idx+1:])
		chat, err := strconv.ParseInt(chatStr, 10, 64)
		if err != nil {
			return ChatTopicRef{}, fmt.Errorf("chat_topic: invalid chat id %q: %w", chatStr, err)
		}
		if topicStr == "" {
			return ChatTopicRef{}, fmt.Errorf("chat_topic: missing topic after %q (use -1/any for any topic, 0/main for main area, or a specific thread id)", chatStr)
		}
		t, err := parseTopicString(topicStr)
		if err != nil {
			return ChatTopicRef{}, fmt.Errorf("chat_topic: %v", err)
		}
		return ChatTopicRef{Chat: chat, Topic: t}, nil
	}
	// Bare integer or "any"/"main" alias = topic, chat resolved later.
	t, err := parseTopicString(tok)
	if err != nil {
		return ChatTopicRef{}, fmt.Errorf("chat_topic: %v", err)
	}
	return ChatTopicRef{Chat: 0, Topic: t}, nil
}

// FormatChatTopicList returns the canonical compact form used for env/DB
// serialization: "chat:topic,chat:topic". Refs whose Chat is still 0 are
// emitted as bare topic ints for round-tripping fidelity.
func FormatChatTopicList(list ChatTopicList) string {
	if len(list.Refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(list.Refs))
	for _, ref := range list.Refs {
		if ref.Chat == 0 {
			parts = append(parts, strconv.Itoa(ref.Topic))
		} else {
			parts = append(parts, fmt.Sprintf("%d:%d", ref.Chat, ref.Topic))
		}
	}
	return strings.Join(parts, ",")
}

// Matches reports whether (chatID, topic) is in this list. A ref with
// Topic == TopicAny matches every topic in that chat.
func (c *ChatTopicList) Matches(chatID int64, topic int) bool {
	if c == nil {
		return false
	}
	for _, ref := range c.Refs {
		if ref.Chat != chatID {
			continue
		}
		if ref.Topic == TopicAny || ref.Topic == topic {
			return true
		}
	}
	return false
}

// AppliesTo reports whether this list - used as an "included" filter -
// considers (chatID, topic) in scope. An empty list matches everything; a
// non-empty list matches iff there's a corresponding ref (Matches semantics).
func (c *ChatTopicList) AppliesTo(chatID int64, topic int) bool {
	if c == nil || len(c.Refs) == 0 {
		return true
	}
	return c.Matches(chatID, topic)
}

// All returns the underlying refs.
func (c *ChatTopicList) All() []ChatTopicRef {
	if c == nil {
		return nil
	}
	return c.Refs
}

// Count returns the number of refs.
func (c *ChatTopicList) Count() int {
	if c == nil {
		return 0
	}
	return len(c.Refs)
}

// Normalize resolves any refs with Chat==0 by attaching the single configured
// moderation chat. Returns an error if the list contains chat-less refs but
// more than one moderation chat is configured (ambiguous), or if any explicit
// chat is not in the allowed set.
//
// When the allowed list is empty, no chat validation is performed (used during
// early-init paths where the moderation chat list is also being loaded).
func (c *ChatTopicList) Normalize(fieldPath string, allowedChats []int64) error {
	if c == nil {
		return nil
	}
	var defaultChat int64
	if len(allowedChats) == 1 {
		defaultChat = allowedChats[0]
	}
	// Collect every violation rather than bailing on the first one, so the
	// operator sees the full list of bad refs in a single validation pass.
	// Valid bare refs are still resolved against the single moderation chat.
	var errs []error
	for i, ref := range c.Refs {
		if ref.Topic < TopicAny {
			errs = append(errs, fmt.Errorf("%s[%d]: invalid topic %d (must be -1, 0, or a positive thread id)", fieldPath, i, ref.Topic))
			continue
		}
		if ref.Chat == 0 {
			if defaultChat == 0 {
				errs = append(errs, fmt.Errorf("%s: bare topic id %d requires exactly one moderation.chat_id to be configured (got %d); rewrite as {chat: <id>, topic: %d}", fieldPath, ref.Topic, len(allowedChats), ref.Topic))
				continue
			}
			c.Refs[i].Chat = defaultChat
			continue
		}
		if len(allowedChats) == 0 {
			continue
		}
		known := false
		for _, id := range allowedChats {
			if id == ref.Chat {
				known = true
				break
			}
		}
		if !known {
			errs = append(errs, fmt.Errorf("%s: chat id %d is not listed in moderation.chat_id", fieldPath, ref.Chat))
		}
	}
	return errors.Join(errs...)
}
