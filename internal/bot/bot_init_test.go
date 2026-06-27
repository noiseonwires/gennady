// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tgbot "github.com/go-telegram/bot"
)

// TestIsRetryableInitError verifies that transient startup failures (timeouts,
// transport errors) are retried while definitive API errors (bad/revoked token)
// fail fast. The errors are wrapped the way go-telegram/bot wraps them: the
// library returns `error call getMe, %w` around a sentinel from errors.go.
func TestIsRetryableInitError(t *testing.T) {
	// wrapGetMe mirrors how tgbot.New wraps the underlying getMe error.
	wrapGetMe := func(inner error) error {
		return fmt.Errorf("error call getMe, %w", inner)
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context deadline exceeded is retryable",
			err:  wrapGetMe(fmt.Errorf("Post \"https://api.telegram.org/bot***/getMe\": %w", context.DeadlineExceeded)),
			want: true,
		},
		{
			name: "generic transport error is retryable",
			err:  wrapGetMe(errors.New("dial tcp: lookup api.telegram.org: no such host")),
			want: true,
		},
		{
			name: "unauthorized (bad token) is not retryable",
			err:  wrapGetMe(fmt.Errorf("%w, %s", tgbot.ErrorUnauthorized, "Unauthorized")),
			want: false,
		},
		{
			name: "forbidden is not retryable",
			err:  wrapGetMe(fmt.Errorf("%w, %s", tgbot.ErrorForbidden, "Forbidden")),
			want: false,
		},
		{
			name: "bad request is not retryable",
			err:  wrapGetMe(fmt.Errorf("%w, %s", tgbot.ErrorBadRequest, "Bad Request")),
			want: false,
		},
		{
			name: "not found is not retryable",
			err:  wrapGetMe(fmt.Errorf("%w, %s", tgbot.ErrorNotFound, "Not Found")),
			want: false,
		},
		{
			name: "conflict is not retryable",
			err:  wrapGetMe(fmt.Errorf("%w, %s", tgbot.ErrorConflict, "Conflict")),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableInitError(tt.err); got != tt.want {
				t.Errorf("isRetryableInitError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
