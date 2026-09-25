// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const operationRetention = time.Hour

type operationStatus string

const (
	operationRunning   operationStatus = "running"
	operationSucceeded operationStatus = "succeeded"
	operationFailed    operationStatus = "failed"
)

type operation struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Status     operationStatus `json:"status"`
	Message    string          `json:"message,omitempty"`
	Error      string          `json:"error,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	StatusURL  string          `json:"status_url"`
}

type operationStore struct {
	mu         sync.RWMutex
	operations map[string]operation
}

func newOperationStore() *operationStore {
	return &operationStore{
		operations: make(map[string]operation),
	}
}

func (s *operationStore) start(operationType string, run func() (string, error)) (operation, error) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return operation{}, err
	}

	now := time.Now().UTC()
	id := hex.EncodeToString(idBytes)
	op := operation{
		ID:        id,
		Type:      operationType,
		Status:    operationRunning,
		Message:   "Operation started.",
		StartedAt: now,
		StatusURL: "/api/operations/" + id,
	}

	s.mu.Lock()
	s.removeExpiredLocked(now)
	s.operations[id] = op
	s.mu.Unlock()

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("WebUI: asynchronous operation %s (%s) panicked: %v\n%s", id, operationType, recovered, debug.Stack())
				s.finish(id, "", errors.New("operation failed unexpectedly"))
			}
		}()
		message, err := run()
		s.finish(id, message, err)
		if err != nil {
			log.Printf("WebUI: asynchronous operation %s (%s) failed: %v", id, operationType, err)
		}
	}()

	return op, nil
}

func (s *operationStore) finish(id, message string, err error) {
	finishedAt := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.operations[id]
	current.FinishedAt = &finishedAt
	if err == nil {
		current.Status = operationSucceeded
		current.Message = message
	} else {
		current.Status = operationFailed
		current.Error = err.Error()
		var webErr webError
		if errors.As(err, &webErr) {
			current.ErrorCode = webErr.code
		}
	}
	s.operations[id] = current
}

func (s *operationStore) get(id string) (operation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(time.Now().UTC())
	op, ok := s.operations[id]
	return op, ok
}

func (s *operationStore) removeExpiredLocked(now time.Time) {
	for id, op := range s.operations {
		if op.FinishedAt != nil && now.Sub(*op.FinishedAt) > operationRetention {
			delete(s.operations, id)
		}
	}
}

func (h *apiHandler) startOperation(w http.ResponseWriter, operationType string, run func() (string, error)) {
	op, err := h.operations.start(operationType, run)
	if err != nil {
		writeWebErrLogged(w, errInternal, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, op)
}

func (h *apiHandler) handleGetOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeWebErr(w, errMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, h.pathPrefix+"/api/operations/")
	if id == "" || strings.Contains(id, "/") {
		writeWebErr(w, errOperationNotFound)
		return
	}
	op, ok := h.operations.get(id)
	if !ok {
		writeWebErr(w, errOperationNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonResponse(w, op)
}
