// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yeka/zip"
)

func assertEncryptedBackup(t *testing.T, data []byte, name, password, body string) {
	t.Helper()
	assert.False(t, bytes.Contains(data, []byte(body)), "uploaded file must not contain the plaintext payload")
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, r.File, 1)
	f := r.File[0]
	assert.Equal(t, name, f.Name)
	assert.True(t, f.IsEncrypted())

	for _, wrong := range []string{"", "wrong-password"} {
		f.SetPassword(wrong)
		reader, err := f.Open()
		if err == nil {
			_, err = io.ReadAll(reader)
			reader.Close()
		}
		require.Error(t, err, "reading without the correct password must fail")
	}

	f.SetPassword(password)
	reader, err := f.Open()
	require.NoError(t, err)
	defer reader.Close()
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, body, string(got))
}

func TestRun_EncryptedLocalBackup(t *testing.T) {
	for _, local := range []bool{true, false} {
		for _, includeConfig := range []bool{true, false} {
			t.Run(fmt.Sprintf("local=%t/include_config=%t", local, includeConfig), func(t *testing.T) {
				dir := t.TempDir()
				scratch := filepath.Join(dir, "scratch")
				dbPath := filepath.Join(dir, "live.db")
				dest := filepath.Join(dir, "backups")
				src := &fakeSource{local: local}
				cfg := config.BackupConfig{
					Target: "local", LocalPath: dest, TempDir: scratch,
					FilePrefix: "chat1_", IncludeConfig: includeConfig,
					Encrypt: true, EncryptionPassword: "  a long backup passphrase  ",
				}
				assert.Equal(t, "chat1_moderation.db.zip", BaseName(cfg))

				var previous []byte
				for range 2 {
					body := strings.Repeat("private database contents\n", 100)
					writeDB(t, dbPath, body)
					src.exportBody = body
					res, err := Run(context.Background(), src, cfg, dbPath)
					require.NoError(t, err)
					assert.Equal(t, "chat1_moderation.db.zip", res.FileName)
					data, err := os.ReadFile(filepath.Join(dest, res.FileName))
					require.NoError(t, err)
					assert.Equal(t, int64(len(data)), res.Bytes)
					assert.Less(t, len(data), len(body), "archive must be compressed")
					assertEncryptedBackup(t, data, "chat1_moderation.db", cfg.EncryptionPassword, body)
					if previous != nil {
						assert.NotEqual(t, previous, data, "each archive must use fresh random encryption salt")
					}
					previous = data
				}
				if local && includeConfig {
					assert.Equal(t, 2, src.checkpoints)
					assert.Zero(t, src.exportCalls)
				} else {
					assert.Equal(t, 2, src.exportCalls)
					assert.Equal(t, includeConfig, src.lastIncluded)
				}
				entries, err := os.ReadDir(scratch)
				require.NoError(t, err)
				assert.Empty(t, entries)
				entries, err = os.ReadDir(dest)
				require.NoError(t, err)
				require.Len(t, entries, 1, "overwrites must not leave extra files")
			})
		}
	}
}

func TestRun_EncryptedRemoteTargets(t *testing.T) {
	for _, target := range []string{"webdav", "bunny"} {
		for _, failUpload := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail=%t", target, failUpload), func(t *testing.T) {
				var uploaded []byte
				var uploadedPath, checksum string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodPut, r.Method)
					var err error
					uploaded, err = io.ReadAll(r.Body)
					assert.NoError(t, err)
					assert.Equal(t, int64(len(uploaded)), r.ContentLength)
					uploadedPath, checksum = r.URL.Path, r.Header.Get("Checksum")
					if failUpload {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusCreated)
				}))
				defer srv.Close()

				scratch := t.TempDir()
				cfg := config.BackupConfig{
					Target: target, TempDir: scratch, FilePrefix: "a/b_",
					Encrypt: true, EncryptionPassword: "a strong backup password",
					WebDAV: config.WebDAVBackupConfig{URL: srv.URL + "/backups"},
					Bunny:  config.BunnyBackupConfig{Endpoint: srv.URL, StorageZone: "zone", AccessKey: "key"},
				}
				body := "SQLite database payload for encrypted remote backup"
				res, err := Run(context.Background(), &fakeSource{exportBody: body}, cfg, "")
				if failUpload {
					require.ErrorContains(t, err, "failed to upload backup")
				} else {
					require.NoError(t, err)
					assert.Equal(t, int64(len(uploaded)), res.Bytes)
				}
				assert.True(t, strings.HasSuffix(uploadedPath, "/a_b_moderation.db.zip"))
				assertEncryptedBackup(t, uploaded, "a_b_moderation.db", cfg.EncryptionPassword, body)
				if target == "bunny" {
					assert.Equal(t, fmt.Sprintf("%X", sha256.Sum256(uploaded)), checksum)
				}
				entries, err := os.ReadDir(scratch)
				require.NoError(t, err)
				assert.Empty(t, entries, "both plaintext snapshot and ZIP must be removed")
			})
		}
	}
}

func TestRun_EncryptionRequiresPasswordEvenWhenSchedulingDisabled(t *testing.T) {
	for _, password := range []string{"", " \t\n"} {
		src := &fakeSource{}
		cfg := config.BackupConfig{Encrypt: true, EncryptionPassword: password}
		_, err := Run(context.Background(), src, cfg, "")
		require.ErrorContains(t, err, "backup.encryption_password")
		assert.Zero(t, src.exportCalls, "invalid encryption settings must fail before snapshotting")
	}
}

func TestRun_EncryptionFailureDoesNotUploadAndCleansSnapshot(t *testing.T) {
	scratch := t.TempDir()
	dest := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := config.BackupConfig{
		Target: "local", LocalPath: dest, TempDir: scratch,
		Encrypt: true, EncryptionPassword: "a backup password",
	}
	_, err := Run(ctx, &fakeSource{exportBody: "private"}, cfg, "")
	require.ErrorIs(t, err, context.Canceled)
	for _, dir := range []string{scratch, dest} {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	}
}

func TestEncryptedArchive_ReadFailureRemovesIncompleteArchive(t *testing.T) {
	dir := t.TempDir()
	_, err := encryptedArchive(context.Background(), dir, "moderation.db", "password")
	require.Error(t, err, "a directory cannot be read as a database snapshot")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestBackupContextReader_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := backupContextReader{ctx: ctx, reader: strings.NewReader("payload")}
	buf := make([]byte, 1)
	_, err := r.Read(buf)
	require.NoError(t, err)
	cancel()
	_, err = r.Read(buf)
	require.ErrorIs(t, err, context.Canceled)
}

func TestEncryptedArchive_SevenZipCompatibility(t *testing.T) {
	sevenZip, err := exec.LookPath("7z")
	if err != nil {
		t.Skip("7z is not installed; Go ZIP round-trip tests still run")
	}
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "snapshot.db")
	body := "database payload for independent AES-256 ZIP extraction"
	writeDB(t, snapshot, body)
	archive, err := encryptedArchive(context.Background(), snapshot, "moderation.db", "test-password")
	require.NoError(t, err)

	listing, err := exec.Command(sevenZip, "l", "-slt", archive).CombinedOutput()
	require.NoError(t, err, "%s", listing)
	assert.Contains(t, string(listing), "AES-256")
	got, err := exec.Command(sevenZip, "x", "-so", "-ptest-password", archive).Output()
	require.NoError(t, err)
	assert.Equal(t, body, string(got))
}
