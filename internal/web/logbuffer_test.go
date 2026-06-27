// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogBuffer_WriteAndLines(t *testing.T) {
	lb := NewLogBuffer(5)
	n, err := lb.Write([]byte("line one\nline two\n"))
	require.NoError(t, err)
	assert.Equal(t, len("line one\nline two\n"), n)

	lines := lb.Lines()
	require.Len(t, lines, 2)
	assert.Equal(t, "line one", lines[0].Message)
	assert.Equal(t, "line two", lines[1].Message)
}

func TestLogBuffer_SkipsEmptyLines(t *testing.T) {
	lb := NewLogBuffer(5)
	lb.Write([]byte("\n\nhello\n\n"))
	lines := lb.Lines()
	require.Len(t, lines, 1)
	assert.Equal(t, "hello", lines[0].Message)
}

func TestLogBuffer_TrimsCarriageReturn(t *testing.T) {
	lb := NewLogBuffer(5)
	lb.Write([]byte("windows\r\n"))
	lines := lb.Lines()
	require.Len(t, lines, 1)
	assert.Equal(t, "windows", lines[0].Message)
}

func TestLogBuffer_WrapsWhenFull(t *testing.T) {
	lb := NewLogBuffer(3)
	for _, m := range []string{"a", "b", "c", "d", "e"} {
		lb.Write([]byte(m + "\n"))
	}
	lines := lb.Lines()
	// Capacity is 3, so only the last 3 lines remain, in order.
	require.Len(t, lines, 3)
	assert.Equal(t, "c", lines[0].Message)
	assert.Equal(t, "d", lines[1].Message)
	assert.Equal(t, "e", lines[2].Message)
}

func TestLogBuffer_EmptyLines(t *testing.T) {
	lb := NewLogBuffer(3)
	assert.Empty(t, lb.Lines())
}
