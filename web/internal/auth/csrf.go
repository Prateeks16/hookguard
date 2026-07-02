package auth

import "crypto/subtle"

// CSRFHeader is sent by htmx requests; forms carry the same value as a
// hidden input under this name too.
const CSRFHeader = "X-CSRF-Token"
const CSRFFormField = "csrf_token"

// CheckCSRF verifies got against the session's stored token in constant
// time. SameSite=Lax is the backstop, not the mechanism — DESIGN.md §5.1.
func CheckCSRF(want, got string) bool {
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}
