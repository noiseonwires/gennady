// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type telegramDiagnosticTransport struct {
	base http.RoundTripper
}

func (t telegramDiagnosticTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	resp.Body = &telegramDiagnosticBody{
		ReadCloser: resp.Body,
		response:   resp,
		elapsed:    time.Since(started),
	}
	return resp, nil
}

type telegramDiagnosticBody struct {
	io.ReadCloser
	response *http.Response
	elapsed  time.Duration
	read     int64
	reported bool
}

func (b *telegramDiagnosticBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	if b.read == 0 && err == io.EOF && !b.reported {
		b.reported = true
		return 0, fmt.Errorf("empty Telegram HTTP response (%s)", telegramResponseMetadata(b.response, b.elapsed))
	}
	return n, err
}

func telegramResponseMetadata(resp *http.Response, elapsed time.Duration) string {
	fields := []string{
		"status=" + resp.Status,
		"protocol=" + resp.Proto,
		fmt.Sprintf("content_length=%d", resp.ContentLength),
		fmt.Sprintf("content_type=%q", resp.Header.Get("Content-Type")),
		fmt.Sprintf("server=%q", resp.Header.Get("Server")),
		fmt.Sprintf("date=%q", resp.Header.Get("Date")),
		fmt.Sprintf("elapsed=%s", elapsed.Round(time.Millisecond)),
	}
	for _, header := range []string{"Via", "X-Request-ID", "CF-Ray"} {
		if value := resp.Header.Get(header); value != "" {
			fields = append(fields, strings.ToLower(strings.ReplaceAll(header, "-", "_"))+"="+fmt.Sprintf("%q", value))
		}
	}
	return strings.Join(fields, ", ")
}
