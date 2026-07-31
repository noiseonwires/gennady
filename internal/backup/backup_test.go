// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gennadium/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSource stands in for *database.DB.
type fakeSource struct {
	local        bool
	exportBody   string
	exportCalls  int
	lastIncluded bool
	checkpoints  int
}

func (f *fakeSource) IsLocal() bool         { return f.local }
func (f *fakeSource) WALCheckpoint() error  { f.checkpoints++; return nil }
func (f *fakeSource) ExportToLocalFile(dir string, includeConfig bool) (string, error) {
	f.exportCalls++
	f.lastIncluded = includeConfig
	tmp, err := os.CreateTemp(dir, "export_*.db")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := tmp.WriteString(f.exportBody); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

func writeDB(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestSanitizeName(t *testing.T) {
	assert.Equal(t, "chat1_", SanitizeName("chat1_"))
	assert.Equal(t, "a-b.c_D9", SanitizeName("a-b.c_D9"))
	// Separators are stripped, so a prefix can never escape the backup folder.
	assert.Equal(t, ".._", SanitizeName("../"))
	assert.Equal(t, "a_b_c", SanitizeName(`a/b\c`))
	assert.Equal(t, "", SanitizeName("   "))
}

func TestBaseName(t *testing.T) {
	assert.Equal(t, "moderation.db", BaseName(config.BackupConfig{}))
	assert.Equal(t, "chat1_moderation.db", BaseName(config.BackupConfig{FilePrefix: "chat1_"}))
	// Separators in the prefix are neutralised before it reaches any path or URL.
	assert.Equal(t, "a_bmoderation.db", BaseName(config.BackupConfig{FilePrefix: "a/b "}))
}

func TestRun_LocalDBCopiesFileAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db", "moderation.db")
	dest := filepath.Join(dir, "backups")

	src := &fakeSource{local: true}
	cfg := config.BackupConfig{
		FilePrefix:    "chat1_",
		IncludeConfig: true,
		Target:        "local",
		LocalPath:     dest,
	}

	for _, body := range []string{"gen-1", "gen-2", "gen-3"} {
		writeDB(t, dbPath, body)
		res, err := Run(context.Background(), src, cfg, dbPath)
		require.NoError(t, err)
		assert.Equal(t, "chat1_moderation.db", res.FileName)
	}

	// A full local backup is a raw file copy - no export round-trip.
	assert.Zero(t, src.exportCalls)
	assert.Equal(t, 3, src.checkpoints)

	// Each run replaces the previous backup; nothing else is left behind.
	b, err := os.ReadFile(filepath.Join(dest, "chat1_moderation.db"))
	require.NoError(t, err)
	assert.Equal(t, "gen-3", string(b))

	entries, err := os.ReadDir(dest)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestRun_RemoteDBUsesExport(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "backups")
	src := &fakeSource{local: false, exportBody: "exported"}
	cfg := config.BackupConfig{IncludeConfig: true, Target: "local", LocalPath: dest, TempDir: dir}

	_, err := Run(context.Background(), src, cfg, "")
	require.NoError(t, err)
	assert.Equal(t, 1, src.exportCalls)
	assert.True(t, src.lastIncluded)

	b, err := os.ReadFile(filepath.Join(dest, "moderation.db"))
	require.NoError(t, err)
	assert.Equal(t, "exported", string(b))

	// The intermediate snapshot must not be left behind.
	matches, _ := filepath.Glob(filepath.Join(dir, "export_*.db"))
	assert.Empty(t, matches)
}

func TestRun_LocalDBWithoutConfigUsesExport(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "moderation.db")
	writeDB(t, dbPath, "raw")
	src := &fakeSource{local: true, exportBody: "no-config"}
	cfg := config.BackupConfig{Target: "local", LocalPath: filepath.Join(dir, "backups")}

	_, err := Run(context.Background(), src, cfg, dbPath)
	require.NoError(t, err)
	assert.Equal(t, 1, src.exportCalls)
	assert.False(t, src.lastIncluded)
}

func TestRun_UnknownTarget(t *testing.T) {
	_, err := Run(context.Background(), &fakeSource{local: true}, config.BackupConfig{Target: "ftp"}, "x.db")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported backup target")
}

func TestResolveTempDir(t *testing.T) {
	// An explicit temp_dir is created on demand and used as-is.
	explicit := filepath.Join(t.TempDir(), "scratch")
	got, err := resolveTempDir(config.BackupConfig{TempDir: explicit}, "/db/moderation.db")
	require.NoError(t, err)
	assert.Equal(t, explicit, got)
	assert.DirExists(t, explicit)

	// Without temp_dir the database folder is used.
	dbDir := t.TempDir()
	got, err = resolveTempDir(config.BackupConfig{}, filepath.Join(dbDir, "moderation.db"))
	require.NoError(t, err)
	assert.Equal(t, dbDir, got)

	// With neither, fall back to the OS temp dir.
	got, err = resolveTempDir(config.BackupConfig{}, "")
	require.NoError(t, err)
	assert.Equal(t, os.TempDir(), got)

	// An unusable database folder must not fail the backup - this is the
	// container case where database.path points at a directory the process
	// cannot create (e.g. /db when running unprivileged).
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	got, err = resolveTempDir(config.BackupConfig{}, filepath.Join(blocker, "sub", "moderation.db"))
	require.NoError(t, err)
	assert.Equal(t, os.TempDir(), got)

	// An explicitly configured but unusable temp_dir is a hard error, and the
	// message points at the setting to fix.
	_, err = resolveTempDir(config.BackupConfig{TempDir: filepath.Join(blocker, "sub")}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backup.temp_dir")
}

func TestRun_TempDirFallbackStillBacksUp(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

	dest := filepath.Join(dir, "backups")
	src := &fakeSource{local: false, exportBody: "exported"}
	cfg := config.BackupConfig{IncludeConfig: true, Target: "local", LocalPath: dest}

	// database.path is unusable, as in a container with a remote database.
	_, err := Run(context.Background(), src, cfg, filepath.Join(blocker, "sub", "moderation.db"))
	require.NoError(t, err)

	b, err := os.ReadFile(filepath.Join(dest, "moderation.db"))
	require.NoError(t, err)
	assert.Equal(t, "exported", string(b))
}

// A failed upload must not leave the intermediate snapshot on disk.
func TestRun_RemovesSnapshotWhenUploadFails(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "moderation.db")
	writeDB(t, dbPath, "payload")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.BackupConfig{
		IncludeConfig: true, Target: "bunny", TempDir: dir,
		Bunny: config.BunnyBackupConfig{Endpoint: srv.URL, StorageZone: "z", AccessKey: "pw"},
	}
	_, err := Run(context.Background(), &fakeSource{local: true}, cfg, dbPath)
	require.Error(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "moderation.db", entries[0].Name(), "only the live database may remain")
}

// ── WebDAV ───────────────────────────────────────────────────────────────

// davServer is a tiny in-memory WebDAV implementation covering the two verbs
// the backup target uses: PUT and MKCOL.
func davServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "u" || pass != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			files[r.URL.Path] = string(b)
			w.WriteHeader(http.StatusCreated)
		case "MKCOL":
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func TestWebDAVTargetOverwrites(t *testing.T) {
	files := map[string]string{}
	srv := davServer(t, files)
	defer srv.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "moderation.db")
	cfg := config.BackupConfig{
		IncludeConfig: true, Target: "webdav",
		WebDAV: config.WebDAVBackupConfig{URL: srv.URL + "/dav/backups", Username: "u", Password: "p"},
	}

	writeDB(t, dbPath, "one")
	_, err := Run(context.Background(), &fakeSource{local: true}, cfg, dbPath)
	require.NoError(t, err)
	writeDB(t, dbPath, "two")
	_, err = Run(context.Background(), &fakeSource{local: true}, cfg, dbPath)
	require.NoError(t, err)

	assert.Equal(t, "two", files["/dav/backups/moderation.db"])
	assert.Len(t, files, 1)
}

func TestNewWebDAVTargetValidation(t *testing.T) {
	_, err := newWebDAVTarget(config.WebDAVBackupConfig{})
	require.Error(t, err)
	_, err = newWebDAVTarget(config.WebDAVBackupConfig{URL: "ftp://host/x"})
	require.Error(t, err)
}

// ── Bunny Storage ────────────────────────────────────────────────────────

// bunnyServer is a tiny in-memory stand-in for the bunny.net Edge Storage
// upload endpoint.
func bunnyServer(t *testing.T, objects map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("AccessKey") != "zone-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		// Bunny verifies the Checksum header; so do we.
		if want := r.Header.Get("Checksum"); want != "" {
			sum := sha256.Sum256(body)
			if !strings.EqualFold(want, hex.EncodeToString(sum[:])) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if want != strings.ToUpper(want) {
				t.Errorf("Checksum header must be uppercase, got %q", want)
			}
		}
		objects[r.URL.Path] = string(body)
		w.WriteHeader(http.StatusCreated)
	}))
}

func TestBunnyTargetOverwrites(t *testing.T) {
	objects := map[string]string{}
	srv := bunnyServer(t, objects)
	defer srv.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "moderation.db")
	cfg := config.BackupConfig{
		FilePrefix: "bot2_", IncludeConfig: true, Target: "bunny",
		Bunny: config.BunnyBackupConfig{
			Endpoint: srv.URL, StorageZone: "my-zone", Path: "gennady/backups", AccessKey: "zone-password",
		},
	}

	for _, body := range []string{"v1", "v2", "v3"} {
		writeDB(t, dbPath, body)
		_, err := Run(context.Background(), &fakeSource{local: true}, cfg, dbPath)
		require.NoError(t, err)
	}

	assert.Equal(t, "v3", objects["/my-zone/gennady/backups/bot2_moderation.db"])
	assert.Len(t, objects, 1)
}

func TestBunnyTargetRejectsWrongAccessKey(t *testing.T) {
	objects := map[string]string{}
	srv := bunnyServer(t, objects)
	defer srv.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "moderation.db")
	writeDB(t, dbPath, "x")
	cfg := config.BackupConfig{
		IncludeConfig: true, Target: "bunny",
		Bunny: config.BunnyBackupConfig{Endpoint: srv.URL, StorageZone: "z", AccessKey: "wrong"},
	}
	_, err := Run(context.Background(), &fakeSource{local: true}, cfg, dbPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestNewBunnyTargetValidation(t *testing.T) {
	_, err := newBunnyTarget(config.BunnyBackupConfig{})
	require.Error(t, err)
	_, err = newBunnyTarget(config.BunnyBackupConfig{StorageZone: "z"})
	require.Error(t, err)

	// A bare hostname is accepted and defaults to https; the region host and an
	// optional folder become the URL prefix.
	tgt, err := newBunnyTarget(config.BunnyBackupConfig{
		StorageZone: "my-zone", AccessKey: "pw", Endpoint: "ny.storage.bunnycdn.com", Path: "/a b/c/",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://ny.storage.bunnycdn.com/my-zone/a_b/c", tgt.base)
	assert.Equal(t, "https://ny.storage.bunnycdn.com/my-zone/a_b/c/moderation.db", tgt.url("moderation.db"))

	// Empty endpoint falls back to the default Falkenstein/Frankfurt host.
	tgt, err = newBunnyTarget(config.BunnyBackupConfig{StorageZone: "z", AccessKey: "pw"})
	require.NoError(t, err)
	assert.Equal(t, "https://"+DefaultBunnyEndpoint+"/z", tgt.base)
}

func TestLocalTargetRejectsPathTraversal(t *testing.T) {
	tgt, err := newLocalTarget(t.TempDir())
	require.NoError(t, err)
	require.Error(t, tgt.Put(context.Background(), "../escape.db", "x"))
	require.Error(t, tgt.Put(context.Background(), "sub/dir.db", "x"))
}
