package gwconfig

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"hookguard/web/internal/store"
)

// repoConfigPath is the real gateway config this milestone must stay
// parity-compatible with (DESIGN.md §10 M3 verify criteria) — the web module
// lives one directory below repo root.
const repoConfigPath = "../../../config.json"

// The single most important test in this milestone: load the repo's actual
// config.json, run it through Import -> Export, and assert the result is
// semantically equal to the original — same routes present, same field
// values, regardless of key order or JSON whitespace.
func TestGoldenParityAgainstRepoConfig(t *testing.T) {
	data, err := os.ReadFile(repoConfigPath)
	if err != nil {
		t.Fatalf("read repo config.json: %v", err)
	}

	endpoints, err := Import(data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(endpoints) == 0 {
		t.Fatal("import produced zero endpoints")
	}

	// Import doesn't assign timestamps/ids; give each a distinct
	// CreatedAt/UpdatedAt like the seed command would, then round-trip
	// through Export exactly as the store's ListActiveEndpoints ordering
	// would (already alphabetical by path from ListEndpoints/ListActive).
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].Path < endpoints[j].Path })

	exported := Export(endpoints)
	exportedJSON, err := Marshal(exported)
	if err != nil {
		t.Fatalf("marshal exported: %v", err)
	}

	var want Config
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatalf("unmarshal original: %v", err)
	}
	var got Config
	if err := json.Unmarshal(exportedJSON, &got); err != nil {
		t.Fatalf("unmarshal exported: %v", err)
	}

	assertSameRoutes(t, want.Routes, got.Routes)
}

func assertSameRoutes(t *testing.T, want, got []Route) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("route count = %d, want %d", len(got), len(want))
	}

	byPath := func(routes []Route) map[string]Route {
		m := make(map[string]Route, len(routes))
		for _, r := range routes {
			m[r.Path] = r
		}
		return m
	}
	wantByPath, gotByPath := byPath(want), byPath(got)

	for path, wantRoute := range wantByPath {
		gotRoute, ok := gotByPath[path]
		if !ok {
			t.Fatalf("exported config missing route %q", path)
		}
		if gotRoute != wantRoute {
			t.Fatalf("route %q mismatch:\n want %+v\n got  %+v", path, wantRoute, gotRoute)
		}
	}
}

// Import applies buildVerifier-mirroring validation: a paypal route with no
// webhook_id is rejected, not silently accepted into a shape the DB would
// then refuse.
func TestImportRejectsPayPalWithoutWebhookID(t *testing.T) {
	data := []byte(`{"routes":[{"path":"/hook/paypal","provider":"paypal","upstream":"http://localhost:8080/paypal"}]}`)
	if _, err := Import(data); err == nil {
		t.Fatal("expected import to reject paypal route without webhook_id")
	}
}

func TestImportRejectsHMACWithoutSecretEnv(t *testing.T) {
	data := []byte(`{"routes":[{"path":"/hook/stripe","provider":"stripe","upstream":"http://localhost:8080/stripe"}]}`)
	if _, err := Import(data); err == nil {
		t.Fatal("expected import to reject stripe route without secret_env")
	}
}

func TestImportRejectsBadReplayWindow(t *testing.T) {
	data := []byte(`{"routes":[{"path":"/hook/stripe","provider":"stripe","upstream":"http://localhost:8080/stripe","secret_env":"S","replay_window":"not-a-duration"}]}`)
	if _, err := Import(data); err == nil {
		t.Fatal("expected import to reject unparseable replay_window")
	}
}

// Export omits webhook_id for non-paypal routes and never emits secret_env
// for paypal routes with a value, matching config.go's own encoding.
func TestExportFieldShapePerProvider(t *testing.T) {
	endpoints := []store.Endpoint{
		{Path: "/hook/stripe", Provider: "stripe", UpstreamURL: "http://x/stripe", SecretEnv: "STRIPE_SECRET", ReplayWindow: "5m"},
		{Path: "/hook/paypal", Provider: "paypal", UpstreamURL: "http://x/paypal", WebhookID: "WH-1"},
	}
	out, err := Marshal(Export(endpoints))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"webhook_id": "WH-1"`) {
		t.Errorf("expected webhook_id in paypal route, got: %s", s)
	}

	var routes Config
	if err := json.Unmarshal(out, &routes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, r := range routes.Routes {
		if r.Provider == "stripe" && r.WebhookID != "" {
			t.Errorf("stripe route should not carry webhook_id, got %q", r.WebhookID)
		}
	}
}
