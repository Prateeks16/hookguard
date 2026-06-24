package main

import "testing"

// TestBuildVerifier exercises the pure factory directly — no environment
// mutation, no network access needed. Covers: empty secret, bad replay
// window, unknown provider, missing webhook_id, and correct per-provider
// dispatch (including PayPal, which validates webhook_id instead of secret).
func TestBuildVerifier(t *testing.T) {
	cases := []struct {
		name    string
		route   Route
		secret  string
		wantErr bool
	}{
		{"stripe ok", Route{Provider: "stripe", ReplayWindow: "5m"}, "s", false},
		{"github ok", Route{Provider: "github"}, "s", false},
		{"shopify ok", Route{Provider: "shopify"}, "s", false},
		{"paypal ok", Route{Provider: "paypal", WebhookID: "WH-123"}, "", false},
		{"empty secret", Route{Provider: "stripe", ReplayWindow: "5m"}, "", true},
		{"bad replay window", Route{Provider: "stripe", ReplayWindow: "5parsecs"}, "s", true},
		{"paypal missing webhook_id", Route{Provider: "paypal"}, "", true},
		{"unknown provider", Route{Provider: "twilio"}, "s", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := buildVerifier(c.route, c.secret, verifierDeps{})
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got %v", c.wantErr, err)
			}
			if !c.wantErr && v == nil {
				t.Fatal("expected a verifier, got nil")
			}
		})
	}

	// Dispatch lands on the right concrete type.
	v, err := buildVerifier(Route{Provider: "stripe", ReplayWindow: "5m"}, "s", verifierDeps{})
	if err != nil {
		t.Fatalf("stripe build: %v", err)
	}
	if _, ok := v.(StripeVerifier); !ok {
		t.Fatalf("want StripeVerifier, got %T", v)
	}
}
