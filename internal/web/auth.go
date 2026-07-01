// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"gennadium/internal/config"
	"gennadium/internal/database"
)

const (
	otpLength         = 6
	otpExpiry         = 5 * time.Minute
	sessionExpiry     = 24 * time.Hour
	maxFailedAttempts = 5
	lockoutDuration   = 15 * time.Minute

	// modLoginExpiry bounds how long a one-time moderator login link + OTP
	// stays valid after the moderator requests it via the bot keyboard.
	modLoginExpiry = 5 * time.Minute
)

// Session roles. The role is encoded as a prefix of the session token (which
// is part of the hashed credential stored in the DB) so it cannot be forged or
// elevated: changing the prefix changes the hash, which then matches no stored
// session. Legacy / super-admin tokens carry no prefix and default to RoleSuper.
const (
	RoleSuper     = "super"
	RoleModerator = "moderator"

	// moderatorTokenPrefix marks a session token as belonging to a moderator.
	// Super-admin tokens are left unprefixed (raw hex) for backward
	// compatibility with sessions issued before roles existed.
	moderatorTokenPrefix = "m_"
)

// sessionRole derives the role of a session from its token. Only moderator
// tokens are prefixed; anything else (raw hex, including pre-existing tokens)
// is treated as a super-admin session.
func sessionRole(token string) string {
	if strings.HasPrefix(token, moderatorTokenPrefix) {
		return RoleModerator
	}
	return RoleSuper
}

type otpEntry struct {
	code      string
	expiresAt time.Time
}

// modLoginEntry is a pending one-time moderator login: a link token (stored
// hashed) paired with an OTP, both delivered to the moderator over Telegram.
// A successful login requires presenting both; the entry is single-use and
// attempt-limited.
type modLoginEntry struct {
	tokenHash string // sha256 hash of the one-time link token
	otp       string
	userID    int64 // moderator's Telegram user ID (for audit logging)
	expiresAt time.Time
	attempts  int
}

type session struct {
	token     string
	expiresAt time.Time
}

type failedAttemptInfo struct {
	count    int
	lockedAt time.Time
}

// AuthManager handles password + OTP authentication and session management.
// Sessions are persisted as hashes in the database so multiple container
// instances sharing the same remote DB can validate the same token.
type AuthManager struct {
	mu             sync.Mutex
	db             *database.DB
	pendingOTPs    []*otpEntry
	sessions       map[string]*session
	failedAttempts map[string]*failedAttemptInfo
	// passwordVerified tracks IPs that passed the password step and are awaiting OTP.
	passwordVerified map[string]time.Time
	// pendingModLogins holds one-time moderator login challenges (link token +
	// OTP) awaiting completion.
	pendingModLogins []*modLoginEntry
}

// NewAuthManager creates a new AuthManager backed by the given database.
// If db is non-nil, session token hashes are persisted to the web_sessions table.
func NewAuthManager(db *database.DB) *AuthManager {
	a := &AuthManager{
		db:               db,
		sessions:         make(map[string]*session),
		failedAttempts:   make(map[string]*failedAttemptInfo),
		passwordVerified: make(map[string]time.Time),
	}
	if db != nil {
		// Best-effort cleanup of expired sessions at startup.
		if err := db.DeleteExpiredWebSessions(); err != nil {
			log.Printf("auth: failed to prune expired web sessions: %v", err)
		}
	}
	return a
}

// ValidatePassword checks the supplied password against the expected one.
// Returns an error if locked out or password is wrong.
func (a *AuthManager) ValidatePassword(password, expected, ip string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.checkLockout(ip); err != nil {
		return err
	}

	if !config.VerifyWebUIPassword(password, expected) {
		return a.recordFailure(ip)
	}

	delete(a.failedAttempts, ip)
	a.passwordVerified[ip] = time.Now().Add(otpExpiry)
	return nil
}

// CreatePasswordSession creates a session directly (password-only login, no OTP).
func (a *AuthManager) CreatePasswordSession() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.createSession()
}

// CreateModeratorLogin mints a one-time moderator login challenge bound to the
// given Telegram user ID. It returns the raw link token (to embed in the login
// URL fragment) and the OTP (delivered separately). Both are required to log
// in. The challenge is single-use, attempt-limited and expires after
// modLoginExpiry. Only the token hash is retained server-side.
func (a *AuthManager) CreateModeratorLogin(userID int64) (token, otp string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	// Prune expired challenges.
	var active []*modLoginEntry
	for _, e := range a.pendingModLogins {
		if e.expiresAt.After(now) {
			active = append(active, e)
		}
	}

	token = generateRandomToken(32)
	otp = generateRandomCode(otpLength)
	active = append(active, &modLoginEntry{
		tokenHash: hashSessionToken(token),
		otp:       otp,
		userID:    userID,
		expiresAt: now.Add(modLoginExpiry),
	})
	a.pendingModLogins = active
	return token, otp
}

// ValidateModeratorLogin verifies a one-time moderator login (link token + OTP)
// and, on success, consumes the challenge and returns a new moderator session
// token. A generic error is returned whether the token or the OTP is wrong so
// the two cases are indistinguishable to a caller. Repeated failures are
// rate-limited per IP and a challenge is discarded after maxFailedAttempts.
func (a *AuthManager) ValidateModeratorLogin(token, code, ip string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.checkLockout(ip); err != nil {
		return "", err
	}

	now := time.Now()
	tokenHash := hashSessionToken(token)
	for i, e := range a.pendingModLogins {
		if e.expiresAt.Before(now) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(e.tokenHash), []byte(tokenHash)) != 1 {
			continue
		}
		// Matching, unexpired link token. Now the OTP must also match.
		if subtle.ConstantTimeCompare([]byte(e.otp), []byte(code)) == 1 {
			a.pendingModLogins = append(a.pendingModLogins[:i], a.pendingModLogins[i+1:]...)
			delete(a.failedAttempts, ip)
			return a.createSessionWithRole(RoleModerator), nil
		}
		// Wrong OTP for a valid token: burn an attempt and discard the
		// challenge once the cap is reached so a leaked link can't be brute
		// forced indefinitely.
		e.attempts++
		if e.attempts >= maxFailedAttempts {
			a.pendingModLogins = append(a.pendingModLogins[:i], a.pendingModLogins[i+1:]...)
		}
		return "", a.recordFailure(ip)
	}

	// Unknown / expired link token.
	return "", a.recordFailure(ip)
}

// GenerateOTP creates a new one-time password and returns it.
func (a *AuthManager) GenerateOTP() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Clean expired OTPs
	now := time.Now()
	var active []*otpEntry
	for _, otp := range a.pendingOTPs {
		if otp.expiresAt.After(now) {
			active = append(active, otp)
		}
	}

	code := generateRandomCode(otpLength)
	active = append(active, &otpEntry{
		code:      code,
		expiresAt: now.Add(otpExpiry),
	})
	a.pendingOTPs = active

	return code
}

// ValidateOTP checks the code against pending OTPs and returns a session token on success.
// If requirePasswordStep is true, the caller's IP must have passed password validation first.
func (a *AuthManager) ValidateOTP(code, ip string, requirePasswordStep bool) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()

	if err := a.checkLockout(ip); err != nil {
		return "", err
	}

	// If password step is required, verify the IP passed it
	if requirePasswordStep {
		expiry, ok := a.passwordVerified[ip]
		if !ok || now.After(expiry) {
			delete(a.passwordVerified, ip)
			return "", errAuthPasswordFirst
		}
	}

	for i, otp := range a.pendingOTPs {
		if otp.expiresAt.After(now) && subtle.ConstantTimeCompare([]byte(otp.code), []byte(code)) == 1 {
			// Valid - remove the used OTP
			a.pendingOTPs = append(a.pendingOTPs[:i], a.pendingOTPs[i+1:]...)
			delete(a.failedAttempts, ip)
			delete(a.passwordVerified, ip)
			return a.createSession(), nil
		}
	}

	// Invalid code - record failed attempt
	return "", a.recordFailure(ip)
}

// ValidateSession checks whether a session token is still valid.
// Falls back to the database when the token is not in the in-memory cache,
// so tokens issued by another container instance remain valid.
func (a *AuthManager) ValidateSession(token string) bool {
	a.mu.Lock()
	if s, ok := a.sessions[token]; ok {
		if s.expiresAt.After(time.Now()) {
			a.mu.Unlock()
			return true
		}
		delete(a.sessions, token)
	}
	db := a.db
	a.mu.Unlock()

	if db == nil || token == "" {
		return false
	}

	tokenHash := hashSessionToken(token)
	expiresAt, err := db.GetWebSessionExpiry(tokenHash)
	if err != nil {
		log.Printf("auth: failed to look up web session: %v", err)
		return false
	}
	if expiresAt.IsZero() {
		legacyExpiresAt, legacyErr := db.GetWebSessionExpiry(token)
		if legacyErr != nil {
			log.Printf("auth: failed to look up legacy web session: %v", legacyErr)
			return false
		}
		if !legacyExpiresAt.IsZero() {
			expiresAt = legacyExpiresAt
			if legacyExpiresAt.After(time.Now()) {
				if saveErr := db.SaveWebSession(tokenHash, legacyExpiresAt); saveErr != nil {
					log.Printf("auth: failed to migrate legacy web session: %v", saveErr)
				}
				if deleteErr := db.DeleteWebSession(token); deleteErr != nil {
					log.Printf("auth: failed to remove legacy web session: %v", deleteErr)
				}
			}
		}
	}
	if expiresAt.IsZero() {
		return false
	}
	if expiresAt.Before(time.Now()) {
		if err := db.DeleteWebSession(tokenHash); err != nil {
			log.Printf("auth: failed to delete expired web session: %v", err)
		}
		if err := db.DeleteWebSession(token); err != nil {
			log.Printf("auth: failed to delete expired legacy web session: %v", err)
		}
		return false
	}

	// Cache the validated session in memory for faster subsequent lookups.
	a.mu.Lock()
	a.sessions[token] = &session{token: token, expiresAt: expiresAt}
	a.mu.Unlock()
	return true
}

// Logout invalidates the given session token.
func (a *AuthManager) Logout(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	db := a.db
	a.mu.Unlock()

	if db != nil && token != "" {
		if err := db.DeleteWebSession(hashSessionToken(token)); err != nil {
			log.Printf("auth: failed to delete web session on logout: %v", err)
		}
		if err := db.DeleteWebSession(token); err != nil {
			log.Printf("auth: failed to delete legacy web session on logout: %v", err)
		}
	}
}

// checkLockout checks if the IP is locked out. Must be called with a.mu held.
func (a *AuthManager) checkLockout(ip string) error {
	now := time.Now()
	if info, ok := a.failedAttempts[ip]; ok {
		if info.count >= maxFailedAttempts {
			elapsed := now.Sub(info.lockedAt)
			if elapsed < lockoutDuration {
				remaining := lockoutDuration - elapsed
				return errAuthLockedOut.Format("too many failed attempts, try again in %d min", int(remaining.Minutes())+1)
			}
			delete(a.failedAttempts, ip)
		}
	}
	return nil
}

// recordFailure records a failed attempt. Must be called with a.mu held.
func (a *AuthManager) recordFailure(ip string) error {
	if _, ok := a.failedAttempts[ip]; !ok {
		a.failedAttempts[ip] = &failedAttemptInfo{}
	}
	a.failedAttempts[ip].count++
	if a.failedAttempts[ip].count >= maxFailedAttempts {
		a.failedAttempts[ip].lockedAt = time.Now()
		a.pendingOTPs = nil
		return errAuthLockedOut.Format("too many failed attempts, try again in %d min", int(lockoutDuration.Minutes()))
	}
	remaining := maxFailedAttempts - a.failedAttempts[ip].count
	return errAuthInvalidCredentials.Format("invalid credentials (%d attempts remaining)", remaining)
}

// createSession creates a new session token. Must be called with a.mu held.
// Also persists the session to the database (when configured) so other
// container instances sharing the DB can validate the token.
func (a *AuthManager) createSession() string {
	token := generateRandomToken(32)
	expiresAt := time.Now().Add(sessionExpiry)
	a.sessions[token] = &session{
		token:     token,
		expiresAt: expiresAt,
	}
	if a.db != nil {
		if err := a.db.SaveWebSession(hashSessionToken(token), expiresAt); err != nil {
			log.Printf("auth: failed to persist web session: %v", err)
		}
	}
	return token
}

// createSessionWithRole creates a session whose role is encoded as a token
// prefix. Super-admin sessions use the unprefixed createSession for backward
// compatibility; only moderator sessions are prefixed. Must be called with
// a.mu held.
func (a *AuthManager) createSessionWithRole(role string) string {
	if role != RoleModerator {
		return a.createSession()
	}
	token := moderatorTokenPrefix + generateRandomToken(32)
	expiresAt := time.Now().Add(sessionExpiry)
	a.sessions[token] = &session{
		token:     token,
		expiresAt: expiresAt,
	}
	if a.db != nil {
		if err := a.db.SaveWebSession(hashSessionToken(token), expiresAt); err != nil {
			log.Printf("auth: failed to persist moderator web session: %v", err)
		}
	}
	return token
}

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
}

const otpAlphabet = "0123456789"

func generateRandomCode(length int) string {
	code := make([]byte, length)
	alphaLen := big.NewInt(int64(len(otpAlphabet)))
	for i := range code {
		n, _ := rand.Int(rand.Reader, alphaLen)
		code[i] = otpAlphabet[n.Int64()]
	}
	return string(code)
}

func generateRandomToken(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		// A crypto/rand failure is catastrophic: returning a predictable token
		// would undermine session security, so fail closed. net/http recovers
		// the panic per-request and the auth mutex is released via defer during
		// unwinding, so no session is ever issued from weak randomness.
		panic(fmt.Sprintf("auth: failed to read random bytes for session token: %v", err))
	}
	return fmt.Sprintf("%x", b)
}
