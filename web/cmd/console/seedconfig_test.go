package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hookguard/web/internal/store"
)

// TestSeedConfigCommand builds the actual console binary and runs
// `console seed-config <path>` against the repo's real config.json, then
// confirms the rows landed in the resulting SQLite file via the store
// package — exercising the real CLI wiring, not just the underlying
// gwconfig/store calls each already have unit tests for.
func TestSeedConfigCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs the console binary; skipped in -short")
	}

	binPath := filepath.Join(t.TempDir(), "console_seedtest.exe")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build console: %v\n%s", err, out)
	}

	dataDir := t.TempDir()
	repoConfig, err := filepath.Abs("../../../config.json")
	if err != nil {
		t.Fatalf("resolve repo config.json: %v", err)
	}
	if _, err := os.Stat(repoConfig); err != nil {
		t.Fatalf("repo config.json not found at %s: %v", repoConfig, err)
	}

	cmd := exec.Command(binPath, "seed-config", repoConfig)
	cmd.Env = append(os.Environ(), "CONSOLE_DATA_DIR="+dataDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("seed-config: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "seeded 4 endpoint(s)") {
		t.Fatalf("expected 4 seeded endpoints, got: %s", out)
	}

	st, err := store.Open(filepath.Join(dataDir, "console.db"))
	if err != nil {
		t.Fatalf("open seeded store: %v", err)
	}
	defer st.Close()

	all, err := st.ListEndpoints()
	if err != nil {
		t.Fatalf("list endpoints: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("endpoint count = %d, want 4", len(all))
	}

	ep, err := st.GetEndpointByPath("/hook/paypal")
	if err != nil {
		t.Fatalf("expected /hook/paypal to be seeded: %v", err)
	}
	if ep.WebhookID != "WH-CHANGE-ME" || ep.SecretEnv != "" {
		t.Fatalf("seeded paypal endpoint = %+v", ep)
	}

	// Running seed-config again is idempotent: existing paths are skipped,
	// not duplicated.
	cmd = exec.Command(binPath, "seed-config", repoConfig)
	cmd.Env = append(os.Environ(), "CONSOLE_DATA_DIR="+dataDir)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("second seed-config: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "seeded 0 endpoint(s)") {
		t.Fatalf("expected second run to seed 0 new endpoints, got: %s", out)
	}
	all, err = st.ListEndpoints()
	if err != nil {
		t.Fatalf("list endpoints after second seed: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("endpoint count after re-seed = %d, want 4 (no duplicates)", len(all))
	}
}
