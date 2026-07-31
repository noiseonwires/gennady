// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gennadium/internal/config"
)

// Target is a backup destination.
type Target interface {
	// Put uploads localPath under the plain file name produced by BaseName,
	// replacing any existing file with that name.
	Put(ctx context.Context, name, localPath string) error
	// Describe returns a human-readable destination for logs.
	Describe() string
}

// NewTarget builds the destination described by the backup config.
func NewTarget(cfg config.BackupConfig) (Target, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Target)) {
	case "", "local":
		return newLocalTarget(cfg.LocalPath)
	case "webdav":
		return newWebDAVTarget(cfg.WebDAV)
	case "bunny":
		return newBunnyTarget(cfg.Bunny)
	default:
		return nil, fmt.Errorf("unsupported backup target %q (supported: local, webdav, bunny)", cfg.Target)
	}
}

// ── Local folder ─────────────────────────────────────────────────────────

type localTarget struct{ dir string }

func newLocalTarget(dir string) (*localTarget, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("backup.local_path is not set")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create backup directory %s: %w", dir, err)
	}
	return &localTarget{dir: dir}, nil
}

func (t *localTarget) Describe() string { return "local folder " + t.dir }

func (t *localTarget) Put(ctx context.Context, name, localPath string) error {
	// Reject anything that is not a bare file name, so a crafted prefix cannot
	// escape the backup directory.
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
		return fmt.Errorf("invalid backup file name %q", name)
	}
	dst := filepath.Join(t.dir, name)

	in, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer in.Close()

	// Write to a sibling temp file and rename, so a crash never leaves a
	// truncated backup under the final name.
	tmp, err := os.CreateTemp(t.dir, ".upload_*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, err = io.Copy(tmp, in)
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
