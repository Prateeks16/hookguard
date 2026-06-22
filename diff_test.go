package main

// Differential harness (the project's correctness proof). For each provider we
// run a matrix of payloads through our Verifier AND through an independent
// oracle, then assert the two verdicts agree — and that they match the expected
// verdict. The oracle is the official provider library where one exists
// (stripe-go, go-github); Shopify has no official Go library, so its oracle is
// an independent re-implementation of the documented algorithm (noted honestly
// in the report).
//
// These imports are TEST-ONLY: no non-test file imports stripe-go or go-github,
// so the shipped gateway binary stays zero-dependency. Verify with:
//   go list -deps . | grep -E 'stripe|go-github'   # (empty)

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"testing"
	"time"

	gh "github.com/google/go-github/v66/github"
	"github.com/stripe/stripe-go/v82/webhook"
)

// logDiff records one row and fails the test on a disagreement (our verdict !=
// oracle verdict) or a wrong verdict (verdict != expected).
func logDiff(t *testing.T, provider, name string, ours, oracle, want bool) {
	t.Helper()
	status := "OK"
	switch {
	case ours != oracle:
		status = "DISAGREE"
		t.Errorf("[%s] %s: ours=%v oracle=%v — verdicts must match", provider, name, ours, oracle)
	case ours != want:
		status = "WRONG"
		t.Errorf("[%s] %s: verdict=%v want=%v", provider, name, ours, want)
	}
	t.Logf("%-8s %-22s ours=%-5v oracle=%-5v %s", provider, name, ours, oracle, status)
}

func TestDifferentialStripe(t *testing.T) {
	const secret = "whsec_diff"
	v := StripeVerifier{Secret: []byte(secret), ReplayWindow: 5 * time.Minute}

	now := time.Now()
	nowTS := strconv.FormatInt(now.Unix(), 10)
	staleTS := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)

	// Valid JSON event shapes so the oracle's only failure source is the
	// signature/timestamp, never a JSON-unmarshal error.
	plain := []byte(`{"id":"evt_1","object":"event","type":"payment_intent.succeeded"}`)
	emoji := []byte(`{"id":"evt_2","object":"event","note":"thanks 🚀✨"}`)
	spaced := []byte("{ \"id\":\"evt_3\", \"object\":\"event\",  \"amount\":100.00 }")

	hdr := func(ts string, body []byte) string { return "t=" + ts + ",v1=" + stripeSign(secret, ts, body) }

	cases := []struct {
		name   string
		body   []byte
		header string
		want   bool
	}{
		{"valid plain", plain, hdr(nowTS, plain), true},
		{"valid emoji", emoji, hdr(nowTS, emoji), true},
		{"valid spaced+float", spaced, hdr(nowTS, spaced), true},
		{"tampered body", plain, hdr(nowTS, emoji), false}, // header signs emoji, body is plain
		{"wrong secret", plain, "t=" + nowTS + ",v1=" + stripeSign("other", nowTS, plain), false},
		{"stale timestamp", plain, hdr(staleTS, plain), false},
	}

	for _, c := range cases {
		h := http.Header{}
		h.Set("Stripe-Signature", c.header)
		ours := v.Verify(c.body, h, now) == nil
		// IgnoreAPIVersionMismatch isolates stripe-go's signature/timestamp
		// verdict from its event-deserialization concerns — we are diffing
		// signature verification, not SDK API-version compatibility.
		_, oerr := webhook.ConstructEventWithOptions(c.body, c.header, secret,
			webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
		logDiff(t, "stripe", c.name, ours, oerr == nil, c.want)
	}
}

func TestDifferentialGitHub(t *testing.T) {
	const secret = "ghdiff"
	v := GitHubVerifier{Secret: []byte(secret)}

	plain := []byte(`{"ref":"refs/heads/main"}`)
	emoji := []byte(`{"ref":"refs/heads/main","msg":"ship it 🚀"}`)

	cases := []struct {
		name   string
		body   []byte
		header string
		want   bool
	}{
		{"valid plain", plain, githubSign(secret, plain), true},
		{"valid emoji", emoji, githubSign(secret, emoji), true},
		{"tampered body", plain, githubSign(secret, emoji), false},
		{"wrong secret", plain, githubSign("other", plain), false},
	}

	for _, c := range cases {
		h := http.Header{}
		h.Set("X-Hub-Signature-256", c.header)
		ours := v.Verify(c.body, h, time.Time{}) == nil
		oracle := gh.ValidateSignature(c.header, c.body, []byte(secret)) == nil
		logDiff(t, "github", c.name, ours, oracle, c.want)
	}
}

// shopifyOracle re-implements Shopify's documented algorithm independently (no
// official Go library exists). Base64(HMAC-SHA256(body)).
func shopifyOracle(secret string, body []byte, header string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(header), []byte(want))
}

func TestDifferentialShopify(t *testing.T) {
	const secret = "shopdiff"
	v := ShopifyVerifier{Secret: []byte(secret)}

	plain := []byte(`{"id":1,"financial_status":"paid"}`)
	emoji := []byte(`{"id":2,"note":"thanks 🚀"}`)

	cases := []struct {
		name   string
		body   []byte
		header string
		want   bool
	}{
		{"valid plain", plain, shopifySign(secret, plain), true},
		{"valid emoji", emoji, shopifySign(secret, emoji), true},
		{"tampered body", plain, shopifySign(secret, emoji), false},
		{"wrong secret", plain, shopifySign("other", plain), false},
	}

	for _, c := range cases {
		h := http.Header{}
		h.Set("X-Shopify-Hmac-SHA256", c.header)
		ours := v.Verify(c.body, h, time.Time{}) == nil
		logDiff(t, "shopify", c.name, ours, shopifyOracle(secret, c.body, c.header), c.want)
	}
}
