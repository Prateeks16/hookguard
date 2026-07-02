package auth

import (
	"crypto/rand"
	"encoding/base64"
)

const resetTokenLen = 32

// NewResetToken returns a fresh random password-reset token and its SHA-256
// hash, mirroring session tokens: only the hash is stored, so a settings DB
// leak doesn't yield a usable reset link.
func NewResetToken() (token string, hash []byte, err error) {
	b := make([]byte, resetTokenLen)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}
