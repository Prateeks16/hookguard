package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Landing page (DESIGN.md §4/§10 M5) — a content-regression guard: asserts
// GET / returns 200 and still contains the load-bearing copy (H1, every
// proof-bar claim, every provider name, the pricing headline). Also
// exercises New()'s template.ParseFS over landing.html end-to-end, since a
// broken {{define}}/{{end}} pairing fails at server construction, not at
// render time.
func TestLandingPageServesExpectedContent(t *testing.T) {
	_, ts := newTestServer(t, true)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(raw)

	want := []string{
		"Every webhook checked at the gate.",
		"0 external dependencies",
		"1 static binary",
		"4 providers, 4 signature shapes",
		"14/14 differential cases agree with official libraries",
		"100% self-hosted",
		"Stripe",
		"GitHub",
		"Shopify",
		"PayPal",
		"Free. It's yours.",
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("landing page missing expected content: %q", w)
		}
	}
}
