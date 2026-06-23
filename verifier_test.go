package main

import "testing"

// TestBuildVerifier exercises the pure factory directly — no environment
// mutation needed. Covers all four branches: empty secret, bad replay window,
// unknown provider, and correct per-provider dispatch.
func TestBuildVerifier(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		secret   string
		window   string
		wantErr  bool
	}{
		{"stripe ok", "stripe", "s", "5m", false},
		{"github ok", "github", "s", "", false},
		{"shopify ok", "shopify", "s", "", false},
		{"empty secret", "stripe", "", "5m", true},
		{"bad replay window", "stripe", "s", "5parsecs", true},
		{"unknown provider", "paypal", "s", "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := buildVerifier(c.provider, c.secret, c.window)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got %v", c.wantErr, err)
			}
			if !c.wantErr && v == nil {
				t.Fatal("expected a verifier, got nil")
			}
		})
	}

	// Dispatch lands on the right concrete type.
	v, err := buildVerifier("stripe", "s", "5m")
	if err != nil {
		t.Fatalf("stripe build: %v", err)
	}
	if _, ok := v.(StripeVerifier); !ok {
		t.Fatalf("want StripeVerifier, got %T", v)
	}
}
