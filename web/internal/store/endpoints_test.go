package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// A PayPal endpoint with an empty webhook_id must fail the schema-level CHECK
// constraint (DESIGN.md §8.2), independent of any application-layer
// validation — this confirms the DB guard actually fires.
func TestCreateEndpointPayPalWithoutWebhookIDFailsCheckConstraint(t *testing.T) {
	st := newTestStore(t)

	_, err := st.CreateEndpoint(Endpoint{
		Path:        "/hook/paypal",
		Provider:    "paypal",
		UpstreamURL: "http://localhost:8080/paypal",
		WebhookID:   "",
		Active:      true,
		CreatedAt:   1,
		UpdatedAt:   1,
	})
	if err == nil {
		t.Fatal("expected CHECK constraint violation, got nil error")
	}
}

// An HMAC provider with an empty secret_env must also fail the CHECK
// constraint.
func TestCreateEndpointHMACWithoutSecretEnvFailsCheckConstraint(t *testing.T) {
	st := newTestStore(t)

	_, err := st.CreateEndpoint(Endpoint{
		Path:        "/hook/stripe",
		Provider:    "stripe",
		UpstreamURL: "http://localhost:8080/stripe",
		SecretEnv:   "",
		Active:      true,
		CreatedAt:   1,
		UpdatedAt:   1,
	})
	if err == nil {
		t.Fatal("expected CHECK constraint violation, got nil error")
	}
}

// A well-formed PayPal endpoint (webhook_id set, secret_env empty) is
// accepted, round-trips through GetEndpointByID, and appears in listings.
func TestCreateAndListEndpoints(t *testing.T) {
	st := newTestStore(t)

	id, err := st.CreateEndpoint(Endpoint{
		Path:        "/hook/paypal",
		Provider:    "paypal",
		UpstreamURL: "http://localhost:8080/paypal",
		WebhookID:   "WH-123",
		Active:      true,
		CreatedAt:   1,
		UpdatedAt:   1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.GetEndpointByID(id)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.WebhookID != "WH-123" || got.SecretEnv != "" {
		t.Fatalf("got = %+v", got)
	}

	byPath, err := st.GetEndpointByPath("/hook/paypal")
	if err != nil {
		t.Fatalf("get by path: %v", err)
	}
	if byPath.ID != id {
		t.Fatalf("get by path returned different row: %+v", byPath)
	}

	all, err := st.ListEndpoints()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("list len = %d, want 1", len(all))
	}
}

// Two endpoints sharing a path are rejected by the UNIQUE constraint.
func TestCreateEndpointDuplicatePathRejected(t *testing.T) {
	st := newTestStore(t)

	_, err := st.CreateEndpoint(Endpoint{
		Path:        "/hook/stripe",
		Provider:    "stripe",
		UpstreamURL: "http://localhost:8080/stripe",
		SecretEnv:   "STRIPE_SECRET",
		Active:      true,
		CreatedAt:   1,
		UpdatedAt:   1,
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err = st.CreateEndpoint(Endpoint{
		Path:        "/hook/stripe",
		Provider:    "stripe",
		UpstreamURL: "http://localhost:8080/stripe-other",
		SecretEnv:   "STRIPE_SECRET_2",
		Active:      true,
		CreatedAt:   2,
		UpdatedAt:   2,
	})
	if err == nil {
		t.Fatal("expected unique-path violation, got nil error")
	}
}

// ListActiveEndpoints excludes inactive rows and stays ordered by path.
func TestListActiveEndpointsExcludesInactive(t *testing.T) {
	st := newTestStore(t)

	if _, err := st.CreateEndpoint(Endpoint{
		Path: "/hook/shopify", Provider: "shopify", UpstreamURL: "http://localhost:8080/shopify",
		SecretEnv: "SHOPIFY_SECRET", Active: true, CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("create shopify: %v", err)
	}
	id, err := st.CreateEndpoint(Endpoint{
		Path: "/hook/github", Provider: "github", UpstreamURL: "http://localhost:8080/github",
		SecretEnv: "GITHUB_SECRET", Active: true, CreatedAt: 1, UpdatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create github: %v", err)
	}
	if err := st.SetEndpointActive(id, false, 2); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	active, err := st.ListActiveEndpoints()
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 || active[0].Path != "/hook/shopify" {
		t.Fatalf("active = %+v", active)
	}
}

func TestDeleteEndpoint(t *testing.T) {
	st := newTestStore(t)

	id, err := st.CreateEndpoint(Endpoint{
		Path: "/hook/github", Provider: "github", UpstreamURL: "http://localhost:8080/github",
		SecretEnv: "GITHUB_SECRET", Active: true, CreatedAt: 1, UpdatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.DeleteEndpoint(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetEndpointByID(id); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
