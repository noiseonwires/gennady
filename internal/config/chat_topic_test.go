// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestChatTopicListUnmarshalYAML(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		want    []ChatTopicRef
		wantErr bool
	}{
		{"empty", "[]", nil, false},
		{"bare-int", "42", []ChatTopicRef{{Chat: 0, Topic: 42}}, false},
		{"bare-int-any", "-1", []ChatTopicRef{{Chat: 0, Topic: TopicAny}}, false},
		{"bare-int-main", "0", []ChatTopicRef{{Chat: 0, Topic: TopicMain}}, false},
		{"list-of-ints", "[42, 43]", []ChatTopicRef{{Chat: 0, Topic: 42}, {Chat: 0, Topic: 43}}, false},
		{"object", "{chat: -100111, topic: 7}", []ChatTopicRef{{Chat: -100111, Topic: 7}}, false},
		{"object-any-topic", "{chat: -100111, topic: -1}", []ChatTopicRef{{Chat: -100111, Topic: TopicAny}}, false},
		{"object-main-topic", "{chat: -100111, topic: 0}", []ChatTopicRef{{Chat: -100111, Topic: TopicMain}}, false},
		// string aliases
		{"alias-any-bare", "any", []ChatTopicRef{{Chat: 0, Topic: TopicAny}}, false},
		{"alias-main-bare", "main", []ChatTopicRef{{Chat: 0, Topic: TopicMain}}, false},
		{"alias-any-object", "{chat: -100111, topic: any}", []ChatTopicRef{{Chat: -100111, Topic: TopicAny}}, false},
		{"alias-main-object", "{chat: -100222, topic: main}", []ChatTopicRef{{Chat: -100222, Topic: TopicMain}}, false},
		{"alias-uppercase", "{chat: -100333, topic: ANY}", []ChatTopicRef{{Chat: -100333, Topic: TopicAny}}, false},
		{
			"list-of-objects",
			"[{chat: -100111, topic: 7}, {chat: -100222, topic: -1}]",
			[]ChatTopicRef{{Chat: -100111, Topic: 7}, {Chat: -100222, Topic: TopicAny}},
			false,
		},
		{
			"mixed",
			"[42, {chat: -100222, topic: 99}]",
			[]ChatTopicRef{{Chat: 0, Topic: 42}, {Chat: -100222, Topic: 99}},
			false,
		},
		// strict-form failures
		{"object-missing-topic", "{chat: -100111}", nil, true},
		{"object-missing-chat", "{topic: 7}", nil, true},
		{"object-unknown-key", "{chat: -100, topic: 7, extra: 1}", nil, true},
		{"object-invalid-topic", "{chat: -100, topic: -5}", nil, true},
		{"bare-int-invalid", "-7", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got ChatTopicList
			err := yaml.Unmarshal([]byte(tc.yaml), &got)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if !equalRefs(got.Refs, tc.want) {
				t.Fatalf("refs = %+v, want %+v", got.Refs, tc.want)
			}
		})
	}
}

func TestChatTopicListMarshalYAMLObjectForm(t *testing.T) {
	list := ChatTopicList{Refs: []ChatTopicRef{{Chat: -100111, Topic: 7}, {Chat: -100222, Topic: 0}}}
	out, err := yaml.Marshal(list)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ChatTopicList
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !equalRefs(back.Refs, list.Refs) {
		t.Fatalf("round-trip refs = %+v, want %+v\nyaml:\n%s", back.Refs, list.Refs, out)
	}
}

func TestChatTopicListJSONRoundTrip(t *testing.T) {
	src := ChatTopicList{Refs: []ChatTopicRef{{Chat: -100111, Topic: 7}, {Chat: -100222, Topic: TopicAny}}}
	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ChatTopicList
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !equalRefs(back.Refs, src.Refs) {
		t.Fatalf("refs = %+v, want %+v", back.Refs, src.Refs)
	}
}

func TestChatTopicListJSONStrict(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"missing-topic", `[{"chat": -100111}]`, "missing required 'topic'"},
		{"missing-chat", `[{"topic": 7}]`, "missing required 'chat'"},
		{"invalid-topic", `[{"chat": -100, "topic": -5}]`, "invalid topic"},
		{"bare-int-invalid", `[-7]`, "invalid topic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var back ChatTopicList
			err := json.Unmarshal([]byte(tc.body), &back)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err %v does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestChatTopicListEnvFormat(t *testing.T) {
	src := ChatTopicList{Refs: []ChatTopicRef{
		{Chat: -100111, Topic: 7},
		{Chat: -100222, Topic: TopicMain},
		{Chat: -100333, Topic: TopicAny},
		{Chat: 0, Topic: 99},
	}}
	enc := FormatChatTopicList(src)
	want := "-100111:7,-100222:0,-100333:-1,99"
	if enc != want {
		t.Fatalf("encode = %q, want %q", enc, want)
	}
	back, err := ParseChatTopicList(enc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !equalRefs(back.Refs, src.Refs) {
		t.Fatalf("decode = %+v, want %+v", back.Refs, src.Refs)
	}
	empty, err := ParseChatTopicList("")
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if len(empty.Refs) != 0 {
		t.Fatalf("expected empty list, got %+v", empty.Refs)
	}
	if _, err := ParseChatTopicList("-100111:"); err == nil {
		t.Fatal("expected error for empty topic, got nil")
	}
	if _, err := ParseChatTopicList("-100111:-5"); err == nil {
		t.Fatal("expected error for invalid topic, got nil")
	}

	// String aliases in env/DB compact form and JSON.
	aliasEnv, err := ParseChatTopicList("-100111:any,-100222:main,any")
	if err != nil {
		t.Fatalf("parse aliases: %v", err)
	}
	wantAlias := []ChatTopicRef{
		{Chat: -100111, Topic: TopicAny},
		{Chat: -100222, Topic: TopicMain},
		{Chat: 0, Topic: TopicAny},
	}
	if !equalRefs(aliasEnv.Refs, wantAlias) {
		t.Fatalf("alias env decode = %+v, want %+v", aliasEnv.Refs, wantAlias)
	}

	var jsonAlias ChatTopicList
	if err := json.Unmarshal([]byte(`[{"chat":-100111,"topic":"any"},{"chat":-100222,"topic":"main"}]`), &jsonAlias); err != nil {
		t.Fatalf("json aliases: %v", err)
	}
	wantJSONAlias := []ChatTopicRef{
		{Chat: -100111, Topic: TopicAny},
		{Chat: -100222, Topic: TopicMain},
	}
	if !equalRefs(jsonAlias.Refs, wantJSONAlias) {
		t.Fatalf("alias json decode = %+v, want %+v", jsonAlias.Refs, wantJSONAlias)
	}
}

func TestChatTopicListMatches(t *testing.T) {
	list := ChatTopicList{Refs: []ChatTopicRef{
		{Chat: -100111, Topic: 7},
		{Chat: -100222, Topic: TopicMain},
		{Chat: -100333, Topic: TopicAny},
	}}
	cases := []struct {
		chat  int64
		topic int
		want  bool
	}{
		{-100111, 7, true},  // exact match
		{-100111, 8, false}, // wrong topic
		{-100222, 0, true},  // main match
		{-100222, 7, false}, // topic 7 not in chat 222
		{-100333, 7, true},  // wildcard topic
		{-100333, 0, true},  // wildcard includes main
		{-100333, 999, true},
		{-100444, 7, false}, // unknown chat
	}
	for _, c := range cases {
		if got := list.Matches(c.chat, c.topic); got != c.want {
			t.Errorf("Matches(%d,%d) = %v, want %v", c.chat, c.topic, got, c.want)
		}
	}
}

func TestChatTopicListAppliesTo(t *testing.T) {
	// Empty list matches everything.
	var empty ChatTopicList
	if !empty.AppliesTo(-100, 7) {
		t.Fatal("empty list should apply to anything")
	}
	// Non-empty behaves like Matches.
	list := ChatTopicList{Refs: []ChatTopicRef{{Chat: -100111, Topic: 7}}}
	if !list.AppliesTo(-100111, 7) {
		t.Fatal("should apply to configured pair")
	}
	if list.AppliesTo(-100111, 8) {
		t.Fatal("should not apply to other topic")
	}
}

func TestConfigInScope(t *testing.T) {
	cfg := &Config{}
	cfg.Moderation.ChatIDs.IDs = []int64{-100111, -100222}
	included := ChatTopicList{Refs: []ChatTopicRef{{Chat: -100111, Topic: TopicAny}}}
	excluded := ChatTopicList{Refs: []ChatTopicRef{{Chat: -100111, Topic: 42}}}

	cases := []struct {
		chat  int64
		topic int
		want  bool
	}{
		{-100111, 7, true},   // in included (wildcard), not excluded
		{-100111, 42, false}, // excluded
		{-100222, 0, false},  // not in included list
		{-100333, 0, false},  // not a moderation chat
	}
	for _, c := range cases {
		if got := cfg.InScope(included, excluded, c.chat, c.topic); got != c.want {
			t.Errorf("InScope(%d,%d) = %v, want %v", c.chat, c.topic, got, c.want)
		}
	}
}

func TestChatTopicListNormalize(t *testing.T) {
	t.Run("single-chat-resolves-bare", func(t *testing.T) {
		list := ChatTopicList{Refs: []ChatTopicRef{{Chat: 0, Topic: 42}}}
		if err := list.Normalize("test", []int64{-100111}); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		want := []ChatTopicRef{{Chat: -100111, Topic: 42}}
		if !equalRefs(list.Refs, want) {
			t.Fatalf("refs = %+v, want %+v", list.Refs, want)
		}
	})
	t.Run("multi-chat-bare-fails", func(t *testing.T) {
		list := ChatTopicList{Refs: []ChatTopicRef{{Chat: 0, Topic: 42}}}
		if err := list.Normalize("test", []int64{-100111, -100222}); err == nil {
			t.Fatal("expected ambiguity error, got nil")
		}
	})
	t.Run("unknown-chat-fails", func(t *testing.T) {
		list := ChatTopicList{Refs: []ChatTopicRef{{Chat: -999, Topic: 42}}}
		if err := list.Normalize("test", []int64{-100111}); err == nil {
			t.Fatal("expected unknown-chat error, got nil")
		}
	})
}

func TestConfigChatRulesFor(t *testing.T) {
	cfg := &Config{}
	cfg.AI.ChatRules = "base"
	cfg.AI.ChatRulesOverrides = []ChatRulesOverride{
		{Chat: -100111, Rules: "extra"},
	}
	if got := cfg.ChatRulesFor(-100111); got != "base\n\nextra" {
		t.Fatalf("with override: %q", got)
	}
	if got := cfg.ChatRulesFor(-100222); got != "base" {
		t.Fatalf("without override: %q", got)
	}
	if got := cfg.ChatRulesFor(0); got != "base" {
		t.Fatalf("chatID=0: %q", got)
	}
}

func TestValidatePostToDestinations(t *testing.T) {
	mk := func(refs ...ChatTopicRef) ChatTopicList { return ChatTopicList{Refs: refs} }

	t.Run("any-in-greeting-rejected", func(t *testing.T) {
		cfg := &Config{}
		cfg.AI.MorningGreeting.PostTo = mk(ChatTopicRef{Chat: -100111, Topic: TopicAny})
		if err := cfg.validatePostToDestinations(); err == nil {
			t.Fatal("expected error for topic:any in morning_greeting.post_to")
		}
	})
	t.Run("any-in-summary-rejected", func(t *testing.T) {
		cfg := &Config{}
		cfg.AI.DailySummary.PostTo = mk(ChatTopicRef{Chat: -100111, Topic: TopicAny})
		if err := cfg.validatePostToDestinations(); err == nil {
			t.Fatal("expected error for topic:any in daily_summary.post_to")
		}
	})
	t.Run("any-in-rss-feed-rejected", func(t *testing.T) {
		cfg := &Config{}
		cfg.AI.Rss.Feeds = []RssFeed{{Name: "f", PostTo: mk(ChatTopicRef{Chat: -100111, Topic: TopicAny})}}
		if err := cfg.validatePostToDestinations(); err == nil {
			t.Fatal("expected error for topic:any in rss feed post_to")
		}
	})
	t.Run("main-and-specific-ok", func(t *testing.T) {
		cfg := &Config{}
		cfg.AI.MorningGreeting.PostTo = mk(ChatTopicRef{Chat: -100111, Topic: TopicMain})
		cfg.AI.DailySummary.PostTo = mk(ChatTopicRef{Chat: -100111, Topic: 42})
		cfg.AI.Rss.Feeds = []RssFeed{{Name: "f", PostTo: mk(ChatTopicRef{Chat: -100111, Topic: 7})}}
		if err := cfg.validatePostToDestinations(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func equalRefs(a, b []ChatTopicRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
