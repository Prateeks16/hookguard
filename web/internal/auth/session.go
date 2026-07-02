package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"
)

// SessionCookieName is the cookie carrying the raw session token. Only
// SHA-256(token) is ever stored server-side — DESIGN.md §5.1.
const SessionCookieName = "hg_session"

const (
	SessionIdleTimeout     = 7 * 24 * time.Hour
	SessionAbsoluteTimeout = 30 * 24 * time.Hour
)

const sessionTokenLen = 32 // bytes, crypto/rand — DESIGN.md §5.1

// NewSessionToken returns a fresh random session token (base64url, for the
// cookie) and its SHA-256 hash (for the DB row). The raw token is never
// stored.
func NewSessionToken() (token string, hash []byte, err error) {
	b := make([]byte, sessionTokenLen)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

const csrfTokenLen = 32 // bytes — DESIGN.md §5.1

func NewCSRFToken() (string, error) {
	b := make([]byte, csrfTokenLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
