// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOperation_StartReturnsImmediatelyAndStatusCanBePolled(t *testing.T) {
	h := &apiHandler{
		pathPrefix: "/admin",
		operations: newOperationStore(),
	}
	release := make(chan struct{})

	startedAt := time.Now()
	startRecorder := httptest.NewRecorder()
	h.startOperation(startRecorder, "test", func() (string, error) {
		<-release
		return "finished", nil
	})

	require.Equal(t, http.StatusAccepted, startRecorder.Code)
	assert.Less(t, time.Since(startedAt), time.Second)

	var started operation
	require.NoError(t, json.NewDecoder(startRecorder.Body).Decode(&started))
	assert.Equal(t, operationRunning, started.Status)
	assert.Equal(t, "Operation started.", started.Message)
	assert.Equal(t, "/api/operations/"+started.ID, started.StatusURL)

	runningRecorder := httptest.NewRecorder()
	h.handleGetOperation(runningRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/operations/"+started.ID, nil))
	require.Equal(t, http.StatusOK, runningRecorder.Code)

	var running operation
	require.NoError(t, json.NewDecoder(runningRecorder.Body).Decode(&running))
	assert.Equal(t, operationRunning, running.Status)

	close(release)
	require.Eventually(t, func() bool {
		completed, ok := h.operations.get(started.ID)
		return ok && completed.Status == operationSucceeded
	}, time.Second, time.Millisecond)

	completedRecorder := httptest.NewRecorder()
	h.handleGetOperation(completedRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/operations/"+started.ID, nil))
	var completed operation
	require.NoError(t, json.NewDecoder(completedRecorder.Body).Decode(&completed))
	assert.Equal(t, operationSucceeded, completed.Status)
	assert.Equal(t, "finished", completed.Message)
	assert.NotNil(t, completed.FinishedAt)
}

func TestOperation_FailureExposesStructuredError(t *testing.T) {
	h := &apiHandler{
		pathPrefix: "/admin",
		operations: newOperationStore(),
	}
	recorder := httptest.NewRecorder()
	h.startOperation(recorder, "test", func() (string, error) {
		return "", errBackupFailed.Format("storage unavailable")
	})

	var started operation
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&started))
	require.Eventually(t, func() bool {
		completed, ok := h.operations.get(started.ID)
		return ok && completed.Status == operationFailed
	}, time.Second, time.Millisecond)

	completed, ok := h.operations.get(started.ID)
	require.True(t, ok)
	assert.Equal(t, errBackupFailed.code, completed.ErrorCode)
	assert.Equal(t, "storage unavailable", completed.Error)
}

func TestOperation_UnknownIDAndMethod(t *testing.T) {
	h := &apiHandler{
		pathPrefix: "/admin",
		operations: newOperationStore(),
	}

	notFound := httptest.NewRecorder()
	h.handleGetOperation(notFound, httptest.NewRequest(http.MethodGet, "/admin/api/operations/unknown", nil))
	assert.Equal(t, http.StatusNotFound, notFound.Code)

	wrongMethod := httptest.NewRecorder()
	h.handleGetOperation(wrongMethod, httptest.NewRequest(http.MethodPost, "/admin/api/operations/unknown", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, wrongMethod.Code)
}

func TestOperation_PanicBecomesFailure(t *testing.T) {
	h := &apiHandler{
		pathPrefix: "/admin",
		operations: newOperationStore(),
	}
	recorder := httptest.NewRecorder()
	h.startOperation(recorder, "test", func() (string, error) {
		panic("sensitive internal detail")
	})

	var started operation
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&started))
	require.Eventually(t, func() bool {
		completed, ok := h.operations.get(started.ID)
		return ok && completed.Status == operationFailed
	}, time.Second, time.Millisecond)

	completed, ok := h.operations.get(started.ID)
	require.True(t, ok)
	assert.Equal(t, "operation failed unexpectedly", completed.Error)
	assert.NotContains(t, completed.Error, "sensitive")
}
