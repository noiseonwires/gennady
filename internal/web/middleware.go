// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// requireMethod wraps a handler so it only accepts the given HTTP method(s).
// Returns 405 Method Not Allowed for any other method, with a uniform JSON
// error body so the SPA can render it consistently.
//
// Pass a single method for the common case:
//
//	mux.HandleFunc("/api/foo", requireMethod(http.MethodPost, h.doFoo))
//
// Or multiple methods when an endpoint accepts e.g. POST and PUT:
//
//	mux.HandleFunc("/api/foo", requireMethod([]string{http.MethodPut, http.MethodPost}, h.doFoo))
func requireMethod(method any, h http.HandlerFunc) http.HandlerFunc {
	var allowed []string
	switch v := method.(type) {
	case string:
		allowed = []string{v}
	case []string:
		allowed = v
	default:
		panic(fmt.Sprintf("requireMethod: unsupported method type %T", method))
	}

	return func(w http.ResponseWriter, r *http.Request) {
		for _, m := range allowed {
			if r.Method == m {
				h(w, r)
				return
			}
		}
		w.Header().Set("Allow", joinAllowedMethods(allowed))
		writeWebErr(w, errMethodNotAllowed)
	}
}

func joinAllowedMethods(methods []string) string {
	out := ""
	for i, m := range methods {
		if i > 0 {
			out += ", "
		}
		out += m
	}
	return out
}

// decodeJSON parses a JSON request body into a value of type T. Returns an
// empty T plus an error on parse failure; never panics. The caller is
// responsible for using respondDecodeError to surface the failure to the
// client.
func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		return v, err
	}
	return v, nil
}

// decodeJSONLimited is like decodeJSON but caps the request body at maxBytes
// to defend against oversized payloads on debug/diagnostics endpoints.
func decodeJSONLimited[T any](r *http.Request, maxBytes int64) (T, error) {
	var v T
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBytes)).Decode(&v); err != nil {
		return v, err
	}
	return v, nil
}

// respondDecodeError writes a uniform 400 Bad Request response for JSON
// decode failures, surfacing the parser's message (e.g. the offending token
// or the field whose type didn't match) as the error detail so the caller can
// see exactly what was malformed. Use after decodeJSON returns an error.
func respondDecodeError(w http.ResponseWriter, err error) {
	if err != nil {
		writeWebErrf(w, errInvalidRequestBody, "%v", err)
		return
	}
	writeWebErr(w, errInvalidRequestBody)
}
