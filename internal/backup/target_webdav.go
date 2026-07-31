// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package backup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gennadium/internal/config"
)

// webdavTarget stores backups on a WebDAV server (Nextcloud, ownCloud, Apache
// mod_dav, Synology, ...). Only four verbs are needed: PUT, MOVE, DELETE, HEAD.
type webdavTarget struct {
	base   string // collection URL without trailing slash
	safe   string // same URL with any embedded credentials redacted, for logs
	user   string
	pass   string
	client *http.Client
}

func newWebDAVTarget(cfg config.WebDAVBackupConfig) (*webdavTarget, error) {
	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return nil, fmt.Errorf("backup.webdav.url is not set")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("backup.webdav.url must be an http(s) URL, got %q", raw)
	}
	return &webdavTarget{
		base:   strings.TrimRight(raw, "/"),
		safe:   strings.TrimRight(u.Redacted(), "/"),
		user:   cfg.Username,
		pass:   cfg.Password,
		client: &http.Client{Timeout: 10 * time.Minute},
	}, nil
}

// Describe returns the collection URL with any embedded credentials masked, so
// it is safe to put in logs and API error responses.
func (t *webdavTarget) Describe() string { return "WebDAV " + t.safe }

func (t *webdavTarget) url(name string) string { return t.base + "/" + url.PathEscape(name) }

// safeURL is url() with any credentials embedded in the configured URL masked;
// used in error messages, which reach the logs and the web UI.
func (t *webdavTarget) safeURL(name string) string { return t.safe + "/" + url.PathEscape(name) }

func (t *webdavTarget) do(req *http.Request) (*http.Response, error) {
	if t.user != "" || t.pass != "" {
		req.SetBasicAuth(t.user, t.pass)
	}
	return t.client.Do(req)
}

func (t *webdavTarget) Put(ctx context.Context, name, localPath string) error {
	status, err := t.put(ctx, name, localPath)
	if err == nil {
		return nil
	}
	// 409 Conflict means the parent collection does not exist yet; create it
	// once and retry. MKCOL only creates one level, which covers the common
	// "…/backups" case.
	if status != http.StatusConflict {
		return err
	}
	if mkErr := t.mkcol(ctx); mkErr != nil {
		return err
	}
	_, err = t.put(ctx, name, localPath)
	return err
}

// mkcol creates the backup collection. An already-existing collection answers
// 405 Method Not Allowed, which is success for our purposes.
func (t *webdavTarget) mkcol(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "MKCOL", t.base, nil)
	if err != nil {
		return err
	}
	resp, err := t.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK, http.StatusMethodNotAllowed:
		return nil
	default:
		return fmt.Errorf("MKCOL %s: unexpected status %s", t.safe, resp.Status)
	}
}

func (t *webdavTarget) put(ctx context.Context, name, localPath string) (int, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, t.url(name), f)
	if err != nil {
		return 0, err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := t.do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return resp.StatusCode, nil
	default:
		return resp.StatusCode, fmt.Errorf("PUT %s: unexpected status %s", t.safeURL(name), resp.Status)
	}
}
