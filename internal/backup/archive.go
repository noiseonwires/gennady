// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/yeka/zip"
)

// encryptedArchive streams the snapshot into a private temporary ZIP. The caller
// owns the completed archive; incomplete archives are removed here.
func encryptedArchive(ctx context.Context, snapshotPath, name, password string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	in, err := os.Open(snapshotPath)
	if err != nil {
		return "", err
	}
	defer in.Close()

	out, err := os.CreateTemp(filepath.Dir(snapshotPath), "moderation_backup_*.zip")
	if err != nil {
		return "", err
	}
	complete := false
	defer func() {
		out.Close()
		if !complete {
			os.Remove(out.Name())
		}
	}()

	archive := zip.NewWriter(out)
	entry, err := archive.Encrypt(name, password, zip.AES256Encryption)
	if err != nil {
		archive.Close()
		return "", err
	}
	_, copyErr := io.Copy(entry, backupContextReader{ctx: ctx, reader: in})
	closeErr := archive.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	complete = true
	return out.Name(), nil
}

type backupContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r backupContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
