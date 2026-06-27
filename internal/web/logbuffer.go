// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"strings"
	"sync"
	"time"
)

// LogEntry represents a single log line with a timestamp.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// LogBuffer is a thread-safe circular buffer that captures log output.
type LogBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
	pos     int // next write position
	full    bool
	maxSize int
}

// NewLogBuffer creates a log buffer with the given capacity (number of lines).
func NewLogBuffer(maxSize int) *LogBuffer {
	return &LogBuffer{
		entries: make([]LogEntry, maxSize),
		maxSize: maxSize,
	}
}

// Write implements io.Writer so the buffer can be used with log.SetOutput.
// It splits input on newlines and stores each non-empty line as an entry.
func (lb *LogBuffer) Write(p []byte) (n int, err error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	now := time.Now()
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		lb.entries[lb.pos] = LogEntry{Time: now, Message: line}
		lb.pos++
		if lb.pos >= lb.maxSize {
			lb.pos = 0
			lb.full = true
		}
	}

	return len(p), nil
}

// Lines returns all buffered log entries in chronological order.
func (lb *LogBuffer) Lines() []LogEntry {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if !lb.full {
		result := make([]LogEntry, lb.pos)
		copy(result, lb.entries[:lb.pos])
		return result
	}

	result := make([]LogEntry, lb.maxSize)
	copy(result, lb.entries[lb.pos:])
	copy(result[lb.maxSize-lb.pos:], lb.entries[:lb.pos])
	return result
}
