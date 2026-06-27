// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireMethod_SingleAllowed(t *testing.T) {
	called := false
	h := requireMethod(http.MethodPost, func(w http.ResponseWriter, r *http.Request) { called = true })

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRequireMethod_Rejected(t *testing.T) {
	h := requireMethod(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	assert.Equal(t, "POST", rr.Header().Get("Allow"))
}

func TestRequireMethod_MultipleAllowed(t *testing.T) {
	h := requireMethod([]string{http.MethodPut, http.MethodPost}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	for _, m := range []string{http.MethodPut, http.MethodPost} {
		rr := httptest.NewRecorder()
		h(rr, httptest.NewRequest(m, "/", nil))
		assert.Equal(t, http.StatusAccepted, rr.Code, m)
	}

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodDelete, "/", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	assert.Equal(t, "PUT, POST", rr.Header().Get("Allow"))
}

func TestRequireMethod_UnsupportedTypePanics(t *testing.T) {
	assert.Panics(t, func() {
		requireMethod(42, func(http.ResponseWriter, *http.Request) {})
	})
}

func TestJoinAllowedMethods(t *testing.T) {
	assert.Equal(t, "GET", joinAllowedMethods([]string{"GET"}))
	assert.Equal(t, "GET, POST", joinAllowedMethods([]string{"GET", "POST"}))
	assert.Equal(t, "", joinAllowedMethods(nil))
}

type decodePayload struct {
	Name string `json:"name"`
}

func TestDecodeJSON_Success(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"foo"}`))
	v, err := decodeJSON[decodePayload](r)
	require.NoError(t, err)
	assert.Equal(t, "foo", v.Name)
}

func TestDecodeJSON_Error(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`not json`))
	_, err := decodeJSON[decodePayload](r)
	require.Error(t, err)
}

func TestDecodeJSONLimited_Success(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"bar"}`))
	v, err := decodeJSONLimited[decodePayload](r, 1024)
	require.NoError(t, err)
	assert.Equal(t, "bar", v.Name)
}

func TestDecodeJSONLimited_TruncatedFails(t *testing.T) {
	// A body longer than the limit is truncated, producing invalid JSON.
	long := `{"name":"` + strings.Repeat("a", 100) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(long))
	_, err := decodeJSONLimited[decodePayload](r, 10)
	require.Error(t, err)
}

func TestRespondDecodeError(t *testing.T) {
	rr := httptest.NewRecorder()
	respondDecodeError(rr, nil)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errInvalidRequestBody.code, body["error_code"])
}

func TestRespondDecodeError_SurfacesDetail(t *testing.T) {
	rr := httptest.NewRecorder()
	respondDecodeError(rr, errors.New("invalid character 'x' looking for beginning of value"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeErrBody(t, rr)
	assert.Equal(t, errInvalidRequestBody.code, body["error_code"])
	assert.Contains(t, body["detail"], "invalid character")
}
