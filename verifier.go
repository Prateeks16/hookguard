package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// Verifier authenticates a raw webhook body against one Provider's signature
// shape. Verify returns nil iff the signature is valid and — where the shape
// carries a timestamp — fresh within the replay window. rawBody must be the
// exact bytes received; never parse or re-serialize it before verifying.
type Verifier interface {
	Verify(rawBody []byte, h http.Header, now time.Time) error
}

// buildVerifier constructs the Verifier for a Route, reading its secret from the
// environment. Fails fast on missing secret, bad replay window, or a provider
// with no implementation yet.
func buildVerifier(r Route) (Verifier, error) {
	secret := os.Getenv(r.SecretEnv)
	if secret == "" {
		return nil, fmt.Errorf("missing secret env %s", r.SecretEnv)
	}
	window, err := parseWindow(r.ReplayWindow)
	if err != nil {
		return nil, fmt.Errorf("replay_window: %w", err)
	}

	switch r.Provider {
	case "stripe":
		return StripeVerifier{Secret: []byte(secret), ReplayWindow: window}, nil
	case "github":
		return GitHubVerifier{Secret: []byte(secret)}, nil
	case "shopify":
		return ShopifyVerifier{Secret: []byte(secret)}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", r.Provider)
	}
}

func parseWindow(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
