// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeErrBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body
}

func TestWebError_Error(t *testing.T) {
	assert.Equal(t, "method not allowed", errMethodNotAllowed.Error())
	withDetail := errMethodNotAllowed.Format("custom %d", 7)
	assert.Equal(t, "custom 7", withDetail.Error())
}

func TestWebError_FormatPreservesCodeStatus(t *testing.T) {
	f := errAuthInvalidCredentials.Format("only %d left", 2)
	assert.Equal(t, errAuthInvalidCredentials.code, f.code)
	assert.Equal(t, errAuthInvalidCredentials.status, f.status)
	assert.Equal(t, "only 2 left", f.detail)
	// Original is unchanged.
	assert.Equal(t, "", errAuthInvalidCredentials.detail)
}

func TestWriteWebErr(t *testing.T) {
	rr := httptest.NewRecorder()
	writeWebErr(rr, errUnauthorized)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	body := decodeErrBody(t, rr)
	assert.Equal(t, errUnauthorized.code, body["error_code"])
	assert.Equal(t, "unauthorized", body["error"])
}

func TestWriteWebErr_UsesDetail(t *testing.T) {
	rr := httptest.NewRecorder()
	writeWebErr(rr, errAuthLockedOut.Format("try again in %d min", 3))
	body := decodeErrBody(t, rr)
	assert.Equal(t, "try again in 3 min", body["error"])
	// The dynamic specifics are also exposed as a discrete `detail` field so the
	// SPA can pair the localized headline with the exact failure.
	assert.Equal(t, "try again in 3 min", body["detail"])
	assert.Equal(t, errAuthLockedOut.code, body["error_code"])
}

func TestWriteWebErr_NoDetailOmitsField(t *testing.T) {
	rr := httptest.NewRecorder()
	writeWebErr(rr, errUnauthorized)
	body := decodeErrBody(t, rr)
	_, has := body["detail"]
	assert.False(t, has, "detail must be omitted when no dynamic context is set")
}

func TestWriteWebErrf(t *testing.T) {
	rr := httptest.NewRecorder()
	writeWebErrf(rr, errConfigValidation, "bad field %s", "x")
	body := decodeErrBody(t, rr)
	assert.Equal(t, "bad field x", body["error"])
	assert.Equal(t, errConfigValidation.code, body["error_code"])
}

func TestWriteWebErrFromErr_WebError(t *testing.T) {
	rr := httptest.NewRecorder()
	writeWebErrFromErr(rr, errAuthPasswordFirst)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errAuthPasswordFirst.code, body["error_code"])
}

func TestWriteWebErrFromErr_PlainError(t *testing.T) {
	rr := httptest.NewRecorder()
	writeWebErrFromErr(rr, errors.New("boom"))
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errInternal.code, body["error_code"])
	assert.Contains(t, body["error"], "boom")
}

func TestWriteWebErrFromErr_WrappedWebError(t *testing.T) {
	rr := httptest.NewRecorder()
	wrapped := fmt.Errorf("context: %w", errAuthLockedOut)
	writeWebErrFromErr(rr, wrapped)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errAuthLockedOut.code, body["error_code"])
}

func TestWriteWebErrLogged(t *testing.T) {
	rr := httptest.NewRecorder()
	writeWebErrLogged(rr, errInternal, errors.New("secret detail"))
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errInternal.code, body["error_code"])
	// The internal error detail must not leak to the client.
	assert.NotContains(t, body["error"], "secret detail")
}

func TestWriteWebErrLogged_NilErr(t *testing.T) {
	rr := httptest.NewRecorder()
	assert.NotPanics(t, func() { writeWebErrLogged(rr, errInternal, nil) })
}
