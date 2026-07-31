// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

// Package backup implements scheduled database backups.
//
// One backup cycle does two things:
//
//  1. Snapshot - produce a single self-contained SQLite file. For a local
//     database that is a WAL-checkpointed copy of the live file; for a remote
//     database it is a full export (the same code path the web UI uses for
//     "download DB").
//  2. Upload - hand the file to a Target (local folder, WebDAV, or a bunny.net
//     Edge Storage zone), replacing the previous backup with the same name.
//
// Everything is built on the standard library; no extra dependencies.
package backup

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gennadium/internal/config"
)

// DefaultBaseName is the backup file name used when no prefix is configured.
const DefaultBaseName = "moderation.db"

// Source is the minimal database surface a backup needs. *database.DB
// satisfies it; tests use a stub.
type Source interface {
	IsLocal() bool
	WALCheckpoint() error
	ExportToLocalFile(dir string, includeConfig bool) (string, error)
}

// Result describes a completed backup.
type Result struct {
	FileName    string // final name at the destination
	Bytes       int64  // uploaded size
	Destination string // human-readable target description
}

// Run performs one complete backup cycle. dbPath is the local SQLite path from
// the database config; it is only used when the active database is local.
func Run(ctx context.Context, src Source, cfg config.BackupConfig, dbPath string) (Result, error) {
	tmpDir, err := resolveTempDir(cfg, dbPath)
	if err != nil {
		return Result{}, err
	}

	target, err := NewTarget(cfg)
	if err != nil {
		return Result{}, err
	}

	snapPath, err := snapshot(src, cfg, dbPath, tmpDir)
	if err != nil {
		return Result{}, err
	}
	// The intermediate snapshot is always removed, whether the upload succeeds
	// or not, so a remote destination leaves nothing behind on this machine.
	defer os.Remove(snapPath)

	info, err := os.Stat(snapPath)
	if err != nil {
		return Result{}, fmt.Errorf("failed to stat backup file: %w", err)
	}

	name := BaseName(cfg)
	if err := target.Put(ctx, name, snapPath); err != nil {
		return Result{}, fmt.Errorf("failed to upload backup to %s: %w", target.Describe(), err)
	}

	return Result{FileName: name, Bytes: info.Size(), Destination: target.Describe()}, nil
}

// resolveTempDir picks the scratch directory for the intermediate snapshot.
//
// An explicitly configured backup.temp_dir must work - failing loudly is better
// than silently writing somewhere the operator did not ask for. The implicit
// default (the local database's folder) falls back to the OS temp dir instead,
// because it is often unusable in containers: with a remote database the
// configured database.path may point at a directory that was never created and
// that an unprivileged process cannot create.
func resolveTempDir(cfg config.BackupConfig, dbPath string) (string, error) {
	if dir := strings.TrimSpace(cfg.TempDir); dir != "" {
		if err := ensureWritableDir(dir); err != nil {
			return "", fmt.Errorf("backup.temp_dir %s is not usable: %w", dir, err)
		}
		return dir, nil
	}

	if strings.TrimSpace(dbPath) != "" {
		dir := filepath.Dir(dbPath)
		if err := ensureWritableDir(dir); err == nil {
			return dir, nil
		} else {
			log.Printf("Backup: cannot use %s for the temporary snapshot (%v), falling back to %s - set backup.temp_dir to pick another location",
				dir, err, os.TempDir())
		}
	}

	fallback := os.TempDir()
	if err := ensureWritableDir(fallback); err != nil {
		return "", fmt.Errorf("no writable temp directory (%s: %w) - set backup.temp_dir to a writable path", fallback, err)
	}
	return fallback, nil
}

// ensureWritableDir creates dir if needed and verifies that files can actually
// be created in it: an existing directory can still be read-only, e.g. a
// container volume mounted without write access.
func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".probe_*")
	if err != nil {
		return err
	}
	probe.Close()
	return os.Remove(probe.Name())
}

// BaseName returns the destination file name for the current settings, i.e.
// "<prefix>moderation.db".
func BaseName(cfg config.BackupConfig) string {
	return SanitizeName(cfg.FilePrefix) + DefaultBaseName
}

// SanitizeName restricts a user-supplied file-name fragment to characters that
// are safe in a path and in a URL, so it can be used verbatim everywhere
// without escaping surprises.
func SanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// snapshot produces a standalone SQLite file to upload and returns its path.
// The caller owns the file and must delete it.
func snapshot(src Source, cfg config.BackupConfig, dbPath, tmpDir string) (string, error) {
	// A raw file copy is only possible for a local database that we want to
	// back up in full. Everything else goes through the table-by-table export.
	if !src.IsLocal() || !cfg.IncludeConfig {
		path, err := src.ExportToLocalFile(tmpDir, cfg.IncludeConfig)
		if err != nil {
			return "", fmt.Errorf("failed to export database: %w", err)
		}
		return path, nil
	}

	if strings.TrimSpace(dbPath) == "" {
		return "", fmt.Errorf("database.path is not configured")
	}
	if err := src.WALCheckpoint(); err != nil {
		log.Printf("Backup: WAL checkpoint failed (continuing anyway): %v", err)
	}
	dst, err := os.CreateTemp(tmpDir, "moderation_backup_*.db")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	dstPath := dst.Name()
	in, err := os.Open(dbPath)
	if err != nil {
		dst.Close()
		os.Remove(dstPath)
		return "", fmt.Errorf("failed to open database file %s: %w", dbPath, err)
	}
	_, err = io.Copy(dst, in)
	in.Close()
	closeErr := dst.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(dstPath)
		return "", fmt.Errorf("failed to copy database file: %w", err)
	}
	return dstPath, nil
}
