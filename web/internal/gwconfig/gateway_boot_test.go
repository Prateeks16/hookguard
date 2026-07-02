package gwconfig

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"hookguard/web/internal/store"
)

// repoRoot resolves the root gateway module directory from this test file's
// location (web/internal/gwconfig -> repo root is three levels up), same
// relative depth as repoConfigPath above.
func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../../")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		t.Fatalf("repo root guess %s doesn't contain go.mod: %v", abs, err)
	}
	return abs
}

// TestGatewayBootsAgainstExportedConfig is the most realistic version of the
// verify criterion "the gateway boots successfully against an exported file
// (go run . with exported config in a temp dir — scripted)": it builds the
// actual root gateway binary, writes an Export()-produced config.json plus
// the secret env vars it demands into a temp dir, runs the real binary
// there, and confirms it logs its routes and starts listening rather than
// exiting with a config/verifier error. This exercises real LoadConfig and
// buildVerifier code, not a reimplementation of their rules.
func TestGatewayBootsAgainstExportedConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs the root binary; skipped in -short")
	}

	root := repoRoot(t)
	binName := "hookguard_boottest"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(t.TempDir(), binName)

	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build root gateway: %v\n%s", err, out)
	}

	endpoints := []store.Endpoint{
		{Path: "/hook/stripe", Provider: "stripe", UpstreamURL: "http://localhost:8080/stripe", SecretEnv: "BOOTTEST_STRIPE_SECRET", ReplayWindow: "5m", Active: true},
		{Path: "/hook/paypal", Provider: "paypal", UpstreamURL: "http://localhost:8080/paypal", WebhookID: "WH-BOOTTEST", Active: true},
	}
	cfgJSON, err := Marshal(Export(endpoints))
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}

	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "config.json"), cfgJSON, 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	cmd := exec.Command(binPath)
	cmd.Dir = runDir
	cmd.Env = append(os.Environ(),
		"INTERNAL_SECRET=boottest-internal-secret",
		"BOOTTEST_STRIPE_SECRET=boottest-stripe-secret",
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	type line struct {
		text string
		err  error
	}
	lines := make(chan line, 16)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			lines <- line{text: sc.Text()}
		}
		lines <- line{err: sc.Err()}
	}()

	deadline := time.After(10 * time.Second)
	var seenStripe, seenPaypal, listening bool
	for !listening {
		select {
		case l := <-lines:
			if l.err != nil {
				t.Fatalf("reading gateway stderr: %v", l.err)
			}
			if strings.Contains(l.text, "route /hook/stripe") {
				seenStripe = true
			}
			if strings.Contains(l.text, "route /hook/paypal") {
				seenPaypal = true
			}
			if strings.Contains(l.text, "listening on :9000") {
				listening = true
			}
			if strings.Contains(l.text, "config:") || strings.Contains(l.text, "route ") && strings.Contains(l.text, "error") {
				t.Fatalf("gateway logged an error line: %s", l.text)
			}
		case <-deadline:
			t.Fatal("timed out waiting for gateway to report it's listening")
		}
	}

	if !seenStripe {
		t.Error("gateway never logged the exported stripe route")
	}
	if !seenPaypal {
		t.Error("gateway never logged the exported paypal route")
	}
}
