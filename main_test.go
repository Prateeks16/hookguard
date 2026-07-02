package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"hookguard/internal/gatewaysig"
)

// passVerifier accepts everything — lets the passthrough test isolate the
// forward path from signature logic.
type passVerifier struct{}

func (passVerifier) Verify([]byte, http.Header, time.Time) error { return nil }

// TestRawBodyPassthrough is the Day-1 invariant guard: the bytes the Upstream
// receives must equal the bytes the client sent, exactly. The payload is built
// to break naive JSON re-serialization — odd spacing, non-sorted keys, a
// trailing-zero float, and a multibyte UTF-8 character. If anyone later adds a
// JSON parse/re-encode in the forward path, this fails.
func TestRawBodyPassthrough(t *testing.T) {
	payload := []byte("{ \"b\":1,\"a\":  100.00, \"msg\":\"héllo 🚀\" }")

	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := Route{Path: "/hook/test", Upstream: upstream.URL}
	gw := httptest.NewServer(makeHandler(route, passVerifier{}, []byte("itest"), &http.Client{Timeout: 5 * time.Second}, nil))
	defer gw.Close()

	resp, err := http.Post(gw.URL, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()

	if !bytes.Equal(got, payload) {
		t.Fatalf("byte mismatch:\n sent: %q\n recv: %q", payload, got)
	}
}

// TestGatewaySignatureEndToEnd proves the Day-4 trust boundary: a request that
// passes provider verification reaches the upstream with a valid Gateway
// signature and is accepted; an attacker hitting the upstream directly with a
// forged Gateway signature is rejected.
func TestGatewaySignatureEndToEnd(t *testing.T) {
	const (
		provider     = "stripe"
		stripeSecret = "whsec_e2e"
	)
	internal := []byte("internal_e2e")
	body := []byte(`{"id":"evt_e2e","amount":4200}`)

	// Upstream verifies only the Gateway signature (like cmd/upstream).
	var accepted bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		p := r.Header.Get(gatewaysig.ProviderHeader)
		if err := gatewaysig.Verify(internal, p, b, r.Header.Get(gatewaysig.Header)); err != nil {
			http.Error(w, "bad gateway sig", http.StatusUnauthorized)
			return
		}
		accepted = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	v := StripeVerifier{Secret: []byte(stripeSecret), ReplayWindow: 5 * time.Minute}
	route := Route{Provider: provider, Upstream: upstream.URL}
	gw := httptest.NewServer(makeHandler(route, v, internal, &http.Client{Timeout: 5 * time.Second}, nil))
	defer gw.Close()

	// Valid chain: real Stripe signature -> gateway -> upstream accepts.
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, _ := http.NewRequest(http.MethodPost, gw.URL, bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", "t="+ts+",v1="+stripeSign(stripeSecret, ts, body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("valid post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !accepted {
		t.Fatalf("valid chain should be accepted: status=%d accepted=%v", resp.StatusCode, accepted)
	}

	// Attacker bypasses the gateway, hits the upstream directly with a forged
	// gateway signature.
	bad, _ := http.NewRequest(http.MethodPost, upstream.URL, bytes.NewReader(body))
	bad.Header.Set(gatewaysig.ProviderHeader, provider)
	bad.Header.Set(gatewaysig.Header, "deadbeef")
	br, err := http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatalf("direct post: %v", err)
	}
	br.Body.Close()
	if br.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged gateway sig should be rejected, got %d", br.StatusCode)
	}
}
