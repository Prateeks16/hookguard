package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"
)

func githubSign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestGitHubVerifier(t *testing.T) {
	const secret = "ghsecret"
	v := GitHubVerifier{Secret: []byte(secret)}
	// emoji in the commit message exercises raw UTF-8 bytes — the GitHub trap
	// where ASCII-coercing the stream silently breaks the HMAC.
	body := []byte(`{"ref":"refs/heads/main","head_commit":{"message":"ship it 🚀✨"}}`)

	hdr := func(sig string) http.Header {
		h := http.Header{}
		h.Set("X-Hub-Signature-256", sig)
		return h
	}

	valid := hdr(githubSign(secret, body))

	cases := []struct {
		name    string
		body    []byte
		h       http.Header
		wantErr bool
	}{
		{"valid utf8", body, valid, false},
		{"tampered body", []byte(`{"ref":"refs/heads/evil"}`), valid, true},
		{"missing header", body, http.Header{}, true},
		{"no sha256 prefix", body, hdr(githubSign(secret, body)[len("sha256="):]), true},
		{"wrong secret", body, hdr(githubSign("wrong", body)), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := v.Verify(c.body, c.h, time.Time{}); (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got %v", c.wantErr, err)
			}
		})
	}
}
