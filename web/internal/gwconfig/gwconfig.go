// Package gwconfig converts between the Console's store.Endpoint rows and
// the gateway's config.json shape (root hookguard package's Route/Config,
// config.go) — DESIGN.md §8.2, §9. Field names and omitempty behavior mirror
// config.go exactly so an exported file is byte-for-byte the same schema the
// gateway's LoadConfig already reads.
package gwconfig

import (
	"encoding/json"
	"fmt"
	"time"

	"hookguard/web/internal/store"
)

// Route mirrors root config.go's Route struct field-for-field, including
// which fields carry omitempty, so marshaled output matches the gateway's
// own encoding.
type Route struct {
	Path         string `json:"path"`
	Provider     string `json:"provider"`
	Upstream     string `json:"upstream"`
	ReplayWindow string `json:"replay_window"`
	SecretEnv    string `json:"secret_env"`
	WebhookID    string `json:"webhook_id,omitempty"`
}

// Config mirrors root config.go's Config struct.
type Config struct {
	Routes []Route `json:"routes"`
}

// FromEndpoint converts one store.Endpoint to its config.json Route shape.
func FromEndpoint(e store.Endpoint) Route {
	return Route{
		Path:         e.Path,
		Provider:     e.Provider,
		Upstream:     e.UpstreamURL,
		ReplayWindow: e.ReplayWindow,
		SecretEnv:    e.SecretEnv,
		WebhookID:    e.WebhookID,
	}
}

// ToEndpoint converts a Route back to a store.Endpoint shell (id/timestamps
// unset — the caller fills those in at insert time). Used by Import and the
// seed command.
func ToEndpoint(r Route) store.Endpoint {
	return store.Endpoint{
		Path:         r.Path,
		Provider:     r.Provider,
		UpstreamURL:  r.Upstream,
		ReplayWindow: r.ReplayWindow,
		SecretEnv:    r.SecretEnv,
		WebhookID:    r.WebhookID,
		Active:       true,
	}
}

// Export serializes endpoints to the gateway's config.json shape. Callers
// pass ListActiveEndpoints() per DESIGN.md §8.2's export note ("SELECT …
// WHERE active=1 ORDER BY path" — ordering is the store's job).
func Export(endpoints []store.Endpoint) Config {
	routes := make([]Route, 0, len(endpoints))
	for _, e := range endpoints {
		routes = append(routes, FromEndpoint(e))
	}
	return Config{Routes: routes}
}

// Marshal renders a Config as indented JSON, matching the repo's own
// config.json formatting (two-space indent).
func Marshal(c Config) ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}

// Import parses a config.json-shaped file into store.Endpoint shells, ready
// for insertion via the seed command or a test fixture. It applies the same
// per-provider shape validation as the gateway's buildVerifier (Validate
// below) so a malformed import fails loudly instead of producing a row the
// DB's CHECK constraint would then reject with a less useful error.
func Import(data []byte) ([]store.Endpoint, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	out := make([]store.Endpoint, 0, len(c.Routes))
	for i, r := range c.Routes {
		if err := Validate(r); err != nil {
			return nil, fmt.Errorf("route %d (%s): %w", i, r.Path, err)
		}
		out = append(out, ToEndpoint(r))
	}
	return out, nil
}

// Validate mirrors root verifier.go's buildVerifier per-provider rules
// (paypal.go, stripe.go, github.go, shopify.go registerProvider closures):
// HMAC providers (stripe/github/shopify) require a non-empty SecretEnv;
// paypal requires a non-empty WebhookID and no SecretEnv; Stripe's
// ReplayWindow, if set, must parse via time.ParseDuration. Kept in exact
// sync with those files intentionally — there is no shared symbol to import
// since buildVerifier's registry closures are unexported.
func Validate(r Route) error {
	if r.Path == "" {
		return fmt.Errorf("path is required")
	}
	switch r.Provider {
	case "paypal":
		if r.WebhookID == "" {
			return fmt.Errorf("paypal requires webhook_id")
		}
		if r.SecretEnv != "" {
			return fmt.Errorf("paypal must not set secret_env")
		}
	case "stripe", "github", "shopify":
		if r.SecretEnv == "" {
			return fmt.Errorf("%s requires secret_env", r.Provider)
		}
		if r.WebhookID != "" {
			return fmt.Errorf("%s must not set webhook_id", r.Provider)
		}
		if r.Provider == "stripe" && r.ReplayWindow != "" {
			if _, err := time.ParseDuration(r.ReplayWindow); err != nil {
				return fmt.Errorf("replay_window: %w", err)
			}
		}
	default:
		return fmt.Errorf("unknown provider %q", r.Provider)
	}
	return nil
}
