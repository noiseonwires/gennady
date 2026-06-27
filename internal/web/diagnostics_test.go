// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticsTracker_RecordCall_Success(t *testing.T) {
	tr := NewDiagnosticsTracker()
	tr.RecordCall(ServiceWeather, 200, "", 150*time.Millisecond, "https://api.example/weather")

	results := tr.GetResults()
	r, ok := results[ServiceWeather]
	require.True(t, ok)
	assert.Equal(t, ServiceWeather, r.ServiceName)
	assert.Equal(t, 200, r.StatusCode)
	assert.True(t, r.Success)
	assert.Equal(t, int64(150), r.ResponseTimeMs)
	assert.Equal(t, "https://api.example/weather", r.RequestURL)
}

func TestDiagnosticsTracker_RecordCall_Failure(t *testing.T) {
	tr := NewDiagnosticsTracker()
	tr.RecordCall(ServiceOpenAIFull, 500, "boom", time.Second, "u")
	r := tr.GetResults()[ServiceOpenAIFull]
	require.NotNil(t, r)
	assert.False(t, r.Success)

	// 2xx but with an error message is still a failure.
	tr.RecordCall(ServiceOpenAIFull, 200, "partial", time.Second, "u")
	assert.False(t, tr.GetResults()[ServiceOpenAIFull].Success)
}

func TestDiagnosticsTracker_GetResultsIsCopy(t *testing.T) {
	tr := NewDiagnosticsTracker()
	tr.RecordCall(ServiceHolidays, 200, "", time.Millisecond, "u")
	results := tr.GetResults()
	results[ServiceHolidays].StatusCode = 999

	// Mutating the returned copy must not affect the tracker's state.
	assert.Equal(t, 200, tr.GetResults()[ServiceHolidays].StatusCode)
}

func TestDiagnosticsTracker_TelegramStatus(t *testing.T) {
	tr := NewDiagnosticsTracker()
	tr.SetTelegramConnected("polling", "mybot")
	st := tr.GetTelegramStatus()
	assert.True(t, st.Connected)
	assert.Equal(t, "polling", st.Mode)
	assert.Equal(t, "mybot", st.BotUsername)
	assert.False(t, st.ConnectedSince.IsZero())

	tr.RecordWebhookReceived()
	assert.False(t, tr.GetTelegramStatus().LastWebhookAt.IsZero())

	tr.SetTelegramError("disconnected")
	st = tr.GetTelegramStatus()
	assert.False(t, st.Connected)
	assert.Equal(t, "disconnected", st.LastError)
}
