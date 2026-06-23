package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Verifier authenticates a raw webhook body against one Provider's signature
// shape. Verify returns nil iff the signature is valid and — where the shape
// carries a timestamp — fresh within the replay window. rawBody must be the
// exact bytes received; never parse or re-serialize it before verifying.
type Verifier interface {
	Verify(rawBody []byte, h http.Header, now time.Time) error
}

// buildVerifier constructs the Verifier for a provider from an already-resolved
// secret and replay window. It is pure — no environment access — so all of its
// branches (empty secret, bad replay window, unknown provider, per-provider
// dispatch) are unit-testable. The caller (main) resolves the secret from the
// environment and passes it in.
func buildVerifier(provider, secret, replayWindow string) (Verifier, error) {
	if secret == "" {
		return nil, errors.New("empty secret")
	}
	window, err := parseWindow(replayWindow)
	if err != nil {
		return nil, fmt.Errorf("replay_window: %w", err)
	}

	switch provider {
	case "stripe":
		return StripeVerifier{Secret: []byte(secret), ReplayWindow: window}, nil
	case "github":
		return GitHubVerifier{Secret: []byte(secret)}, nil
	case "shopify":
		return ShopifyVerifier{Secret: []byte(secret)}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
}

func parseWindow(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
