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

// verifierDeps holds shared, non-secret dependencies a provider's factory
// branch may need beyond the route's own config. Currently just the HTTP
// client PayPal uses to fetch its public certificate; widen this struct
// rather than adding more buildVerifier scalars as providers grow.
type verifierDeps struct {
	Client *http.Client
}

// buildVerifier constructs the Verifier for a Route from an already-resolved
// secret. It is pure — no environment access, no I/O — so all of its branches
// are unit-testable. The caller (main) resolves the secret from the
// environment and passes it in. Each provider validates its own required
// config: the HMAC providers need a non-empty secret; PayPal has no shared
// secret and instead needs Route.WebhookID.
func buildVerifier(r Route, secret string, deps verifierDeps) (Verifier, error) {
	switch r.Provider {
	case "stripe":
		if secret == "" {
			return nil, errors.New("empty secret")
		}
		window, err := parseWindow(r.ReplayWindow)
		if err != nil {
			return nil, fmt.Errorf("replay_window: %w", err)
		}
		return StripeVerifier{Secret: []byte(secret), ReplayWindow: window}, nil
	case "github":
		if secret == "" {
			return nil, errors.New("empty secret")
		}
		return GitHubVerifier{Secret: []byte(secret)}, nil
	case "shopify":
		if secret == "" {
			return nil, errors.New("empty secret")
		}
		return ShopifyVerifier{Secret: []byte(secret)}, nil
	case "paypal":
		if r.WebhookID == "" {
			return nil, errors.New("missing webhook_id")
		}
		return NewPayPalVerifier(r.WebhookID, deps.Client), nil
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
