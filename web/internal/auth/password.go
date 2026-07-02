// Package auth implements the Console's authentication primitives:
// Argon2id password hashing, session tokens, CSRF, and login rate limiting.
// DESIGN.md §5.1 fixes every parameter here — this is a security product's
// own login, so nothing here is novel or tunable-by-accident.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters — DESIGN.md §5.1. Encoded into the PHC string so a
// future param bump can still verify old hashes.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB = 64MiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

const (
	minPasswordLen = 12
	maxPasswordLen = 128
)

// ValidatePassword enforces DESIGN.md §5.1's length-only policy (NIST-style:
// no composition rules).
func ValidatePassword(pw string) error {
	if len(pw) < minPasswordLen {
		return fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(pw) > maxPasswordLen {
		return fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}
	return nil
}

// HashPassword returns the PHC-formatted Argon2id hash of pw, with a fresh
// random salt.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return encodeHash(pw, salt), nil
}

func encodeHash(pw string, salt []byte) string {
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		b64(salt), b64(key))
}

// dummyHash is verified against on unknown-email login so the response
// timing doesn't distinguish "no such user" from "wrong password" —
// DESIGN.md §5.1 enumeration/timing defense. Any valid PHC string works;
// this one's plaintext is not a real credential.
var dummyHash = mustHash("hookguard-dummy-hash-for-timing-defense")

func mustHash(pw string) string {
	h, err := HashPassword(pw)
	if err != nil {
		panic(err)
	}
	return h
}

// VerifyPassword reports whether pw matches the PHC-encoded hash. Callers
// authenticating a login should call this even for a nonexistent user (pass
// DummyHash()) so failure timing is uniform.
func VerifyPassword(pw, phc string) bool {
	salt, key, params, err := decodeHash(phc)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, params.time, params.memory, uint8(params.threads), uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1
}

// DummyHash returns a fixed, valid Argon2id hash to run VerifyPassword
// against when no such user exists.
func DummyHash() string { return dummyHash }

type argonParams struct {
	time, threads uint32
	memory        uint32
}

func decodeHash(phc string) (salt, key []byte, params argonParams, err error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, params, errors.New("not an argon2id PHC string")
	}
	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, params, err
	}
	var mem, t, p uint32
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return nil, nil, params, err
	}
	if salt, err = unb64(parts[4]); err != nil {
		return nil, nil, params, err
	}
	if key, err = unb64(parts[5]); err != nil {
		return nil, nil, params, err
	}
	return salt, key, argonParams{time: t, memory: mem, threads: p}, nil
}
