// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gennadium/internal/config"
)

func TestNewAuthManager_NilDB(t *testing.T) {
	a := NewAuthManager(nil)
	require.NotNil(t, a)
	assert.NotNil(t, a.sessions)
	assert.NotNil(t, a.failedAttempts)
	assert.NotNil(t, a.passwordVerified)
}

func TestNewAuthManager_WithDB(t *testing.T) {
	db := newTestDB(t)
	a := NewAuthManager(db)
	require.NotNil(t, a)
	assert.Same(t, db, a.db)
}

func TestValidatePassword_PlaintextSuccess(t *testing.T) {
	a := NewAuthManager(nil)
	err := a.ValidatePassword("secret", "secret", "1.2.3.4")
	require.NoError(t, err)
	// IP should now be marked password-verified.
	a.mu.Lock()
	_, ok := a.passwordVerified["1.2.3.4"]
	a.mu.Unlock()
	assert.True(t, ok)
}

func TestValidatePassword_HashedSuccess(t *testing.T) {
	a := NewAuthManager(nil)
	hashed, err := config.HashWebUIPasswordForStorage("hunter2")
	require.NoError(t, err)
	require.NoError(t, a.ValidatePassword("hunter2", hashed, "ip"))
}

func TestValidatePassword_WrongPassword(t *testing.T) {
	a := NewAuthManager(nil)
	err := a.ValidatePassword("wrong", "secret", "ip")
	require.Error(t, err)
	var we webError
	require.True(t, errors.As(err, &we))
	assert.Equal(t, errAuthInvalidCredentials.code, we.code)
}

func TestValidatePassword_LockoutAfterMaxAttempts(t *testing.T) {
	a := NewAuthManager(nil)
	ip := "10.0.0.1"
	// First maxFailedAttempts-1 attempts: invalid credentials.
	for i := 0; i < maxFailedAttempts-1; i++ {
		err := a.ValidatePassword("wrong", "secret", ip)
		var we webError
		require.True(t, errors.As(err, &we))
		assert.Equal(t, errAuthInvalidCredentials.code, we.code)
	}
	// The maxFailedAttempts-th attempt triggers lockout.
	err := a.ValidatePassword("wrong", "secret", ip)
	var we webError
	require.True(t, errors.As(err, &we))
	assert.Equal(t, errAuthLockedOut.code, we.code)

	// Even a correct password is rejected while locked out.
	err = a.ValidatePassword("secret", "secret", ip)
	require.Error(t, err)
	require.True(t, errors.As(err, &we))
	assert.Equal(t, errAuthLockedOut.code, we.code)
}

func TestValidatePassword_ResetsFailuresOnSuccess(t *testing.T) {
	a := NewAuthManager(nil)
	ip := "10.0.0.2"
	require.Error(t, a.ValidatePassword("wrong", "secret", ip))
	require.NoError(t, a.ValidatePassword("secret", "secret", ip))
	a.mu.Lock()
	_, ok := a.failedAttempts[ip]
	a.mu.Unlock()
	assert.False(t, ok, "failed attempts should be cleared after a success")
}

func TestCreatePasswordSession_ValidSession(t *testing.T) {
	a := NewAuthManager(nil)
	token := a.CreatePasswordSession()
	require.NotEmpty(t, token)
	assert.True(t, a.ValidateSession(token))
}

func TestGenerateOTP_AndValidate(t *testing.T) {
	a := NewAuthManager(nil)
	code := a.GenerateOTP()
	require.Len(t, code, otpLength)

	token, err := a.ValidateOTP(code, "ip", false)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	assert.True(t, a.ValidateSession(token))
}

func TestGenerateOTP_PrunesExpired(t *testing.T) {
	a := NewAuthManager(nil)
	// Inject an already-expired OTP.
	a.mu.Lock()
	a.pendingOTPs = append(a.pendingOTPs, &otpEntry{code: "000000", expiresAt: time.Now().Add(-time.Minute)})
	a.mu.Unlock()

	a.GenerateOTP()

	a.mu.Lock()
	count := len(a.pendingOTPs)
	a.mu.Unlock()
	// The expired one should have been pruned, leaving only the new code.
	assert.Equal(t, 1, count)
}

func TestValidateOTP_WrongCode(t *testing.T) {
	a := NewAuthManager(nil)
	a.GenerateOTP()
	_, err := a.ValidateOTP("999999", "ip", false)
	require.Error(t, err)
}

func TestValidateOTP_RequirePasswordStep(t *testing.T) {
	a := NewAuthManager(nil)
	code := a.GenerateOTP()

	// Without prior password verification, OTP must be rejected.
	_, err := a.ValidateOTP(code, "ip", true)
	require.Error(t, err)
	var we webError
	require.True(t, errors.As(err, &we))
	assert.Equal(t, errAuthPasswordFirst.code, we.code)

	// After password verification, the same code succeeds.
	require.NoError(t, a.ValidatePassword("secret", "secret", "ip"))
	token, err := a.ValidateOTP(code, "ip", true)
	require.NoError(t, err)
	require.NotEmpty(t, token)
}

func TestValidateOTP_ExpiredPasswordStep(t *testing.T) {
	a := NewAuthManager(nil)
	code := a.GenerateOTP()
	require.NoError(t, a.ValidatePassword("secret", "secret", "ip"))
	// Force the password-verified window to be in the past.
	a.mu.Lock()
	a.passwordVerified["ip"] = time.Now().Add(-time.Minute)
	a.mu.Unlock()

	_, err := a.ValidateOTP(code, "ip", true)
	require.Error(t, err)
	var we webError
	require.True(t, errors.As(err, &we))
	assert.Equal(t, errAuthPasswordFirst.code, we.code)
}

func TestValidateOTP_LockoutAfterFailures(t *testing.T) {
	a := NewAuthManager(nil)
	ip := "10.0.0.3"
	a.GenerateOTP()
	for i := 0; i < maxFailedAttempts-1; i++ {
		_, err := a.ValidateOTP("111111", ip, false)
		require.Error(t, err)
	}
	_, err := a.ValidateOTP("111111", ip, false)
	require.Error(t, err)
	var we webError
	require.True(t, errors.As(err, &we))
	assert.Equal(t, errAuthLockedOut.code, we.code)
}

func TestValidateSession_Unknown(t *testing.T) {
	a := NewAuthManager(nil)
	assert.False(t, a.ValidateSession("nope"))
	assert.False(t, a.ValidateSession(""))
}

func TestValidateSession_ExpiredInMemory(t *testing.T) {
	a := NewAuthManager(nil)
	a.mu.Lock()
	a.sessions["tok"] = &session{token: "tok", expiresAt: time.Now().Add(-time.Hour)}
	a.mu.Unlock()
	assert.False(t, a.ValidateSession("tok"))
}

func TestValidateSession_FromDB(t *testing.T) {
	db := newTestDB(t)
	a := NewAuthManager(db)
	token := a.CreatePasswordSession()

	// Drop the in-memory cache so validation must hit the DB.
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()

	assert.True(t, a.ValidateSession(token))
}

func TestLogout_InvalidatesSession(t *testing.T) {
	db := newTestDB(t)
	a := NewAuthManager(db)
	token := a.CreatePasswordSession()
	require.True(t, a.ValidateSession(token))

	a.Logout(token)
	assert.False(t, a.ValidateSession(token))
}

func TestLogout_EmptyTokenNoPanic(t *testing.T) {
	a := NewAuthManager(newTestDB(t))
	assert.NotPanics(t, func() { a.Logout("") })
}

func TestHashSessionToken_StableAndPrefixed(t *testing.T) {
	h1 := hashSessionToken("abc")
	h2 := hashSessionToken("abc")
	assert.Equal(t, h1, h2)
	assert.Contains(t, h1, "sha256:")
	assert.NotEqual(t, hashSessionToken("abc"), hashSessionToken("abd"))
}

func TestGenerateRandomCode_Charset(t *testing.T) {
	code := generateRandomCode(20)
	require.Len(t, code, 20)
	for _, c := range code {
		assert.Contains(t, otpAlphabet, string(c))
	}
}

func TestGenerateRandomToken_Length(t *testing.T) {
	tok := generateRandomToken(16)
	// hex encoding doubles the byte length.
	assert.Len(t, tok, 32)
	assert.NotEqual(t, generateRandomToken(16), generateRandomToken(16))
}
