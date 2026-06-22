// Package web provides the HTTP server for the stackctl admin UI.
package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookie   = "stackctl_session"
	sessionLifetime = 12 * time.Hour

	maxLoginAttempts = 5
	lockoutDuration  = 60 * time.Second
)

// session holds a single active admin session.
type session struct {
	token     string
	csrf      string // per-session CSRF token, embedded in every form
	createdAt time.Time
}

// sessionStore manages a single admin session. There is at most one valid
// session at any time — stackctl has exactly one admin.
type sessionStore struct {
	mu      sync.Mutex
	current *session
}

func (s *sessionStore) create() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.current = &session{
		token:     randomToken(),
		csrf:      randomToken(),
		createdAt: time.Now(),
	}
	return s.current.token
}

// csrfToken returns the CSRF token for the current session, or "" if there is
// no session. There is exactly one admin session, so no per-request lookup is
// needed — the template layer reads it via Server.pageData.
func (s *sessionStore) csrfToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return ""
	}
	return s.current.csrf
}

// validCSRF reports whether got matches the current session's CSRF token,
// using a constant-time comparison. A missing session or empty token fails.
func (s *sessionStore) validCSRF(got string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil || s.current.csrf == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.current.csrf), []byte(got)) == 1
}

// randomToken returns 32 bytes of crypto-random data as hex.
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func (s *sessionStore) valid(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current == nil || token == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(s.current.token), []byte(token)) != 1 {
		return false
	}
	if time.Since(s.current.createdAt) > sessionLifetime {
		s.current = nil
		return false
	}
	return true
}

func (s *sessionStore) destroy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = nil
}

// setSessionCookie writes the session cookie on the response.
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionLifetime.Seconds()),
	})
}

// clearSessionCookie deletes the session cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// getSessionToken extracts the session token from the request cookie.
func getSessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// rateLimiter tracks failed login attempts per IP.
type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*attemptRecord
}

type attemptRecord struct {
	count    int
	lockedAt time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{attempts: make(map[string]*attemptRecord)}
}

// isLocked returns true if the IP has exceeded max attempts.
func (rl *rateLimiter) isLocked(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rec, ok := rl.attempts[ip]
	if !ok {
		return false
	}
	if rec.count >= maxLoginAttempts {
		if time.Since(rec.lockedAt) < lockoutDuration {
			return true
		}
		// Lockout expired — reset.
		delete(rl.attempts, ip)
		return false
	}
	return false
}

// recordFailure increments the failure count for an IP.
func (rl *rateLimiter) recordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rec, ok := rl.attempts[ip]
	if !ok {
		rec = &attemptRecord{}
		rl.attempts[ip] = rec
	}
	rec.count++
	if rec.count >= maxLoginAttempts {
		rec.lockedAt = time.Now()
	}
}

// recordSuccess clears the failure count for an IP.
func (rl *rateLimiter) recordSuccess(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
}
