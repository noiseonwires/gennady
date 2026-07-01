// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"errors"
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
)

func ruleFor(trigger string) config.ModerationRule {
	return config.ModerationRule{Trigger: trigger, Action: "delete"}
}

// newModelOutcome classifies an AI call's (rules, details, err) return.
func TestNewModelOutcome(t *testing.T) {
	rules := []config.ModerationRule{ruleFor("spam")}

	t.Run("clean", func(t *testing.T) {
		o := newModelOutcome(rules, "why", nil)
		assert.Equal(t, modelOutcome{rules: rules, details: "why"}, o)
		assert.True(t, o.flagged())
	})

	t.Run("clean empty is not flagged", func(t *testing.T) {
		o := newModelOutcome(nil, "", nil)
		assert.False(t, o.flagged())
	})

	t.Run("content filter error", func(t *testing.T) {
		o := newModelOutcome(nil, "", &ContentFilterError{Details: "azure"})
		assert.Equal(t, modelOutcome{contentFilter: true, cfDetails: "azure"}, o)
		assert.True(t, o.flagged())
	})

	t.Run("transient error", func(t *testing.T) {
		o := newModelOutcome(nil, "", errors.New("timeout"))
		assert.Equal(t, modelOutcome{transientErr: true}, o)
		assert.False(t, o.flagged())
	})
}

// decideConfirmedVerdict: the light model flagged the message and the full model
// was consulted to confirm.
func TestDecideConfirmedVerdict(t *testing.T) {
	lightRule := []config.ModerationRule{ruleFor("light")}
	fullRule := []config.ModerationRule{ruleFor("full")}

	cases := []struct {
		name  string
		light modelOutcome
		full  modelOutcome
		want  verdictDecision
	}{
		{
			name:  "full content-filter is definitive removal",
			light: modelOutcome{rules: lightRule},
			full:  modelOutcome{contentFilter: true, cfDetails: "full-cf"},
			want:  verdictDecision{effect: effectPlaceholderRemoved, isCF: true, cfDetails: "full-cf"},
		},
		{
			name:  "full transient error trusts light content-filter",
			light: modelOutcome{contentFilter: true, cfDetails: "light-cf"},
			full:  modelOutcome{transientErr: true},
			want:  verdictDecision{effect: effectPlaceholderRemoved, isCF: true, cfDetails: "light-cf"},
		},
		{
			name:  "full transient error trusts light rules",
			light: modelOutcome{rules: lightRule, details: "light-d"},
			full:  modelOutcome{transientErr: true},
			want:  verdictDecision{effect: effectNone, rules: lightRule, details: "light-d"},
		},
		{
			name:  "full confirms with light content-filter keeps full verdict",
			light: modelOutcome{contentFilter: true, cfDetails: "light-cf"},
			full:  modelOutcome{rules: fullRule, details: "full-d"},
			want:  verdictDecision{effect: effectPlaceholderNotSaved, isCF: true, rules: fullRule, details: "full-d", cfDetails: "light-cf"},
		},
		{
			name:  "full confirms with plain rules",
			light: modelOutcome{rules: lightRule, details: "light-d"},
			full:  modelOutcome{rules: fullRule, details: "full-d"},
			want:  verdictDecision{effect: effectNone, rules: fullRule, details: "full-d"},
		},
		{
			name:  "full disagrees clears the reaction",
			light: modelOutcome{rules: lightRule},
			full:  modelOutcome{},
			want:  verdictDecision{effect: effectClearReaction},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decideConfirmedVerdict(tc.light, tc.full))
		})
	}
}

// decideDoubleCheckVerdict: the light model already cleared the message; the full
// model runs as a safety net with no "thinking" reaction to clear.
func TestDecideDoubleCheckVerdict(t *testing.T) {
	fullRule := []config.ModerationRule{ruleFor("full")}

	cases := []struct {
		name string
		full modelOutcome
		want verdictDecision
	}{
		{
			name: "full content-filter is definitive removal",
			full: modelOutcome{contentFilter: true, cfDetails: "cf"},
			want: verdictDecision{effect: effectPlaceholderRemoved, isCF: true, cfDetails: "cf"},
		},
		{
			name: "full flags with rules",
			full: modelOutcome{rules: fullRule, details: "d"},
			want: verdictDecision{effect: effectNone, rules: fullRule, details: "d"},
		},
		{
			name: "full clean yields no action",
			full: modelOutcome{},
			want: verdictDecision{effect: effectNone},
		},
		{
			name: "full transient error yields no action",
			full: modelOutcome{transientErr: true},
			want: verdictDecision{effect: effectNone},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decideDoubleCheckVerdict(tc.full))
		})
	}
}
