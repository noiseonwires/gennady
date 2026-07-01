// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"strings"
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin down the pipeline's structural contracts so an accidental
// reorder of stages or features (which can silently change behavior even when
// the individual stage/feature tests still pass) fails loudly here.

// The moderation pipeline must keep its stage order: recording and moderation
// run before the side features, and deletion is finalized last.
func TestModerationStages_Order(t *testing.T) {
	b, _ := newMockBot(t)
	var names []string
	for _, s := range b.moderationStages() {
		names = append(names, s.name)
	}
	assert.Equal(t, []string{
		"dump", "cruel_mute", "prepare_deletion", "enhance", "moderate", "features", "finalize_deletion",
	}, names)
}

// link_summary must precede creative_reply so the link content the link feature
// stores on the message's extra_info is available to the creative reply's
// reply-chain context.
func TestFeatures_Order_LinkBeforeCreative(t *testing.T) {
	b, _ := newMockBot(t)
	var names []string
	for _, f := range b.features() {
		names = append(names, f.name)
	}
	assert.Equal(t, []string{"message_summary", "link_summary", "creative_reply"}, names)

	li, ci := indexOf(names, "link_summary"), indexOf(names, "creative_reply")
	require.GreaterOrEqual(t, li, 0)
	require.GreaterOrEqual(t, ci, 0)
	assert.Less(t, li, ci, "link_summary must come before creative_reply")
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// resolveScope computes each per-(chat, topic)/per-user flag independently for a
// single message: moderation off, summaries on, links off, creative on, and
// whitelist all coexist on the same Scope.
func TestResolveScope_FeatureScopesAreIndependent(t *testing.T) {
	b, _ := newMockBot(t)
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}
	// Moderation is excluded for the whole chat...
	b.config.Moderation.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}
	// ...link summaries are excluded too, but summaries and creative are not...
	b.config.AI.LinkSummaries.ExcludedTopics = config.ChatTopicList{
		Refs: []config.ChatTopicRef{{Chat: -100, Topic: config.TopicAny}},
	}
	// ...and the sender is whitelisted.
	b.config.Admin.WhitelistUserIDs = []int64{7}

	sc := b.resolveScope(testMessage(-100, 7, 55, "x"))

	assert.True(t, sc.InModerationChat)
	assert.False(t, sc.Moderate, "moderation excluded")
	assert.True(t, sc.Summarize, "summaries independently active")
	assert.False(t, sc.LinkSummary, "link summaries independently excluded")
	assert.True(t, sc.Creative, "creative replies independently active")
	assert.True(t, sc.Whitelisted)
	assert.False(t, sc.IsService)
	assert.Equal(t, 0, sc.Topic)
}

// Edited messages must not run the async side features. link_summary and
// creative_reply gate on !IsEdited at the registry level; the summary feature's
// per-message gate also skips edits.
func TestPipeline_EditedMessage_SkipsAsyncFeatures(t *testing.T) {
	b, tg := newMockBot(t)
	b.config.AI.Enabled = true
	b.config.AI.MessageSummaries.Enabled = true
	b.config.AI.LinkSummaries.Enabled = true
	b.config.AI.CreativeReplies.Enabled = true
	b.config.Moderation.ChatIDs = config.ChatIDList{IDs: []int64{-100}}

	mc := b.newInboundContext(testMessage(-100, 7, 55, strings.Repeat("a", 50)), true)
	mc.Enhanced = strings.Repeat("a", 50)

	for _, f := range b.features() {
		switch f.name {
		case "link_summary", "creative_reply":
			assert.False(t, f.applies(mc), "an edited message must not run the %s feature", f.name)
		}
	}

	// The summary feature's per-message gate also skips edits.
	b.runMessageSummary(mc)
	assert.Equal(t, 0, tg.sentCount(), "an edited message must not be summarized")
}
