// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"strings"
	"time"
)

func reputationLookupTool() responsesFunctionTool {
	return responsesFunctionTool{
		Type: "function", Name: "get_user_reputation", Strict: true,
		Description: "Look up a user's reputation, returned as two separate values: general_reputation (calculated reputation score, grade and component breakdown in the current chat) and ai_reputation (stored AI grade and behavior profile). Identify the user by exact numeric ID, Telegram username, or known display name. Ambiguous names require a numeric ID. Unavailable values are returned as null, not neutral.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"user": map[string]any{"type": "string", "minLength": 1, "description": "Exact numeric user ID, Telegram username (with or without @), or known display name."},
			},
			"required":             []string{"user"},
			"additionalProperties": false,
		},
	}
}

type reputationToolAIProfile struct {
	Reputation string    `json:"reputation"`
	Profile    string    `json:"profile"`
	UpdatedAt  time.Time `json:"updated_at"`
	Truncated  bool      `json:"truncated,omitempty"`
}

type reputationToolGeneral struct {
	Score      float64            `json:"score"`
	Grade      string             `json:"grade"`
	Components map[string]float64 `json:"components,omitempty"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

// reputationToolOutput reports the general (event-based) reputation and the
// stored AI reputation as two separate values, as shown in the web UI.
type reputationToolOutput struct {
	UserID                   int64                    `json:"user_id"`
	ChatID                   int64                    `json:"chat_id"`
	GeneralReputationEnabled bool                     `json:"general_reputation_enabled"`
	GeneralReputation        *reputationToolGeneral   `json:"general_reputation"`
	AIReputation             *reputationToolAIProfile `json:"ai_reputation"`
}

func (b *Bot) executeReputationTool(rawArguments string, scope messageToolScope) string {
	if !b.config.AI.CreativeReplies.ReputationToolEnabled {
		return marshalMessageToolError("tool_disabled", "Reputation lookup is disabled")
	}
	var arguments struct {
		User string `json:"user"`
	}
	if err := json.Unmarshal([]byte(rawArguments), &arguments); err != nil {
		return marshalMessageToolError("invalid_arguments", err.Error())
	}
	if strings.TrimSpace(arguments.User) == "" {
		return marshalMessageToolError("invalid_arguments", "user is required")
	}
	ids, err := b.db.FindUserIDsForAI(scope.ChatID, arguments.User)
	if err != nil {
		return marshalMessageToolError("query_failed", err.Error())
	}
	if len(ids) == 0 {
		return marshalMessageToolError("user_not_found", "No matching user is known in the current chat")
	}
	if len(ids) > 1 {
		return marshalMessageToolError("ambiguous_user", "Multiple users match; retry with a numeric user ID")
	}
	output := reputationToolOutput{
		UserID: ids[0], ChatID: scope.ChatID, GeneralReputationEnabled: b.reputationEnabled(),
	}
	profile, err := b.db.GetUserProfile(ids[0])
	if err != nil {
		return marshalMessageToolError("query_failed", err.Error())
	}
	if profile != nil && (strings.TrimSpace(profile.Profile) != "" || strings.TrimSpace(profile.Reputation) != "") {
		text, truncated, _ := truncateMessageToolText(profile.Profile)
		output.AIReputation = &reputationToolAIProfile{
			Reputation: profile.Reputation, Profile: text, UpdatedAt: profile.UpdatedAt, Truncated: truncated,
		}
	}
	if output.GeneralReputationEnabled {
		score, err := b.reputationScore(ids[0], scope.ChatID)
		if err != nil {
			return marshalMessageToolError("query_failed", err.Error())
		}
		output.GeneralReputation = &reputationToolGeneral{
			Score: score.Score, Grade: score.Grade, Components: score.Components, UpdatedAt: score.UpdatedAt,
		}
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return marshalMessageToolError("encoding_failed", err.Error())
	}
	return string(encoded)
}
