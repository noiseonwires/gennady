// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gennadium/internal/config"
)

// bunnyTarget stores backups in a bunny.net Edge Storage zone.
//
// The API is a plain REST interface authenticated with the storage zone
// password in the AccessKey header; uploading is a single request:
//
//	PUT https://<region-host>/<zone>/<path>/<file>   (201 on success)
//
// Missing directories are created automatically. See
// https://docs.bunny.net/api-reference/storage.
type bunnyTarget struct {
	base      string // https://<host>/<zone>[/<path>] without trailing slash
	accessKey string
	client    *http.Client
}

// DefaultBunnyEndpoint is the hostname of the Falkenstein/Frankfurt region,
// which bunny.net uses for storage zones created without a region prefix.
const DefaultBunnyEndpoint = "storage.bunnycdn.com"

func newBunnyTarget(cfg config.BunnyBackupConfig) (*bunnyTarget, error) {
	zone := strings.Trim(strings.TrimSpace(cfg.StorageZone), "/")
	if zone == "" {
		return nil, fmt.Errorf("backup.bunny.storage_zone is not set")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" {
		return nil, fmt.Errorf("backup.bunny.access_key is not set (use the storage zone password, not the account API key)")
	}

	host := strings.TrimSpace(cfg.Endpoint)
	if host == "" {
		host = DefaultBunnyEndpoint
	}
	// Accept a bare hostname (the documented form) as well as a full URL; a
	// bare hostname always means https.
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("backup.bunny.endpoint must be a storage hostname such as %q, got %q", DefaultBunnyEndpoint, cfg.Endpoint)
	}

	base := u.Scheme + "://" + u.Host + "/" + url.PathEscape(zone)
	if p := sanitizeKeyPath(cfg.Path); p != "" {
		base += "/" + p
	}
	return &bunnyTarget{
		base:      base,
		accessKey: cfg.AccessKey,
		client:    &http.Client{Timeout: 10 * time.Minute},
	}, nil
}

// sanitizeKeyPath restricts a folder path inside the storage zone to safe
// characters, keeping "/" as the separator.
func sanitizeKeyPath(s string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(s), "/"), "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = SanitizeName(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

func (t *bunnyTarget) Describe() string { return "Bunny Storage " + t.base }

func (t *bunnyTarget) url(name string) string { return t.base + "/" + url.PathEscape(name) }

func (t *bunnyTarget) newRequest(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("AccessKey", t.accessKey)
	return req, nil
}

func (t *bunnyTarget) Put(ctx context.Context, name, localPath string) error {
	sum, err := sha256File(localPath)
	if err != nil {
		return err
	}
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	req, err := t.newRequest(ctx, http.MethodPut, t.url(name), f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	// Bunny verifies this and rejects the upload if the stored bytes differ.
	req.Header.Set("Checksum", strings.ToUpper(sum))

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT %s: %s: %s", t.url(name), resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// sha256File returns the hex SHA-256 of a file, streaming it so large backups
// do not have to fit in memory.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
