// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"errors"

	"gennadium/internal/config"
)

// classifier.go is the functional core of AI content moderation: the pure
// decision logic that maps the (light, full) model outcomes to a verdict. It is
// kept free of I/O so it can be unit-tested with table cases and no mocks. The
// imperative shell (containsBadWords / applyVerdictDecision in filters.go)
// performs the AI calls, reactions, placeholder writes and DB stats, then defers
// every branch decision to the pure functions here.

// modelOutcome is the side-effect-free summary of one AI moderation call: the
// rules it emitted, its explanatory details, and how it ended (clean, a provider
// content-filter trip, or a transient error).
type modelOutcome struct {
	rules         []config.ModerationRule
	details       string
	contentFilter bool   // provider content filter tripped (definitive-ish)
	cfDetails     string // content-filter details, when contentFilter is set
	transientErr  bool   // a non-content-filter error occurred
}

// newModelOutcome classifies an AI call's (rules, details, err) return into a
// modelOutcome. A ContentFilterError becomes a content-filter trip (carrying its
// details); any other error becomes a transient error.
func newModelOutcome(rules []config.ModerationRule, details string, err error) modelOutcome {
	o := modelOutcome{rules: rules, details: details}
	if err != nil {
		var cfErr *ContentFilterError
		if errors.As(err, &cfErr) {
			o.contentFilter = true
			o.cfDetails = cfErr.Details
		} else {
			o.transientErr = true
		}
	}
	return o
}

// flagged reports whether the model considers the message bad: it either matched
// at least one rule or tripped the provider content filter.
func (o modelOutcome) flagged() bool {
	return len(o.rules) > 0 || o.contentFilter
}

// verdictEffect names the terminal side effect a moderation decision requires.
// The shell (applyVerdictDecision) materialises each effect.
type verdictEffect int

const (
	// effectNone returns the decision's rules/isCF/details unchanged.
	effectNone verdictEffect = iota
	// effectClearReaction clears the "thinking" reaction and returns a clean
	// (no-action) verdict: the full model disagreed with the light model.
	effectClearReaction
	// effectPlaceholderRemoved replaces the message with the content-removed
	// placeholder and returns the content-security rules; cfDetails is appended
	// to the content-security details.
	effectPlaceholderRemoved
	// effectPlaceholderNotSaved replaces the message with the policy-violation
	// placeholder (cfDetails appended) but returns the decision's own
	// rules/isCF/details (the confirmed full-model verdict).
	effectPlaceholderNotSaved
)

// verdictDecision is the pure outcome of the moderation decision: the verdict
// tuple to return plus the terminal side effect the shell must apply.
type verdictDecision struct {
	effect    verdictEffect
	rules     []config.ModerationRule
	isCF      bool
	details   string
	cfDetails string // extra content-filter details for the placeholder effects
}

// decideConfirmedVerdict decides the verdict for the staged path where the light
// model flagged the message and the full model was consulted to confirm:
//
//   - full content-filter  → definitive content-security removal
//   - full transient error → trust the light model (its content-filter or rules)
//   - full confirmed rules → use the full model's rules (placeholder when the
//     light model had tripped the content filter)
//   - full disagreed       → clear the reaction, no action
func decideConfirmedVerdict(light, full modelOutcome) verdictDecision {
	switch {
	case full.contentFilter:
		return verdictDecision{effect: effectPlaceholderRemoved, isCF: true, cfDetails: full.cfDetails}
	case full.transientErr:
		if light.contentFilter {
			return verdictDecision{effect: effectPlaceholderRemoved, isCF: true, cfDetails: light.cfDetails}
		}
		return verdictDecision{effect: effectNone, rules: light.rules, details: light.details}
	case len(full.rules) > 0:
		if light.contentFilter {
			return verdictDecision{effect: effectPlaceholderNotSaved, isCF: true, rules: full.rules, details: full.details, cfDetails: light.cfDetails}
		}
		return verdictDecision{effect: effectNone, rules: full.rules, details: full.details}
	default:
		return verdictDecision{effect: effectClearReaction}
	}
}

// decideDoubleCheckVerdict decides the verdict for the new-user double-check
// path, where the light model already cleared the message and the full model is
// run as an extra safety net for a user's first N messages. There is no
// "thinking" reaction to clear here, so a clean (or transiently-failed) full
// model yields no action.
func decideDoubleCheckVerdict(full modelOutcome) verdictDecision {
	switch {
	case full.contentFilter:
		return verdictDecision{effect: effectPlaceholderRemoved, isCF: true, cfDetails: full.cfDetails}
	case len(full.rules) > 0:
		return verdictDecision{effect: effectNone, rules: full.rules, details: full.details}
	default:
		return verdictDecision{effect: effectNone}
	}
}
