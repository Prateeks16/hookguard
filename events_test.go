package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"hookguard/internal/gatewaysig"
)

// TestEventsURLUnsetEmitsNothing is the headline safety property: without
// EVENTS_URL, newEventEmitter starts no goroutine and record is a no-op, so
// the gateway's behavior is unaffected by this file existing.
func TestEventsURLUnsetEmitsNothing(t *testing.T) {
	e := newEventEmitter("", []byte("secret"))
	if e.enabled() {
		t.Fatal("emitter with empty url should be disabled")
	}
	req := httptest.NewRequest(http.MethodPost, "/hook/stripe", nil)
	e.record(Route{Path: "/hook/stripe", Provider: "stripe"}, []byte("body"), req, "accepted", "", 200, time.Millisecond)
	// record on a disabled emitter must not panic and must not touch ch.
	if len(e.ch) != 0 {
		t.Fatalf("disabled emitter queued an event: len=%d", len(e.ch))
	}
}

// TestNilEmitterRecordIsSafe covers the real call site: makeHandler is passed
// a nil *eventEmitter whenever the caller never constructed one (see the
// updated main_test.go call sites).
func TestNilEmitterRecordIsSafe(t *testing.T) {
	var e *eventEmitter
	req := httptest.NewRequest(http.MethodPost, "/hook/stripe", nil)
	e.record(Route{Path: "/hook/stripe"}, []byte("body"), req, "accepted", "", 200, time.Millisecond)
}

// TestEventEmitterPostsSignedEvent proves the delivered event: correct JSON
// shape, correct headers, and a Gateway signature over "console-ingest" that
// verifies with gatewaysig.Verify — the same primitive the gateway signs
// forwarded webhooks with.
func TestEventEmitterPostsSignedEvent(t *testing.T) {
	secret := []byte("ingest-secret")

	var mu sync.Mutex
	var gotBody []byte
	var gotHeader http.Header
	received := make(chan struct{}, 1)

	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotBody, _ = io.ReadAll(r.Body)
		gotHeader = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		select {
		case received <- struct{}{}:
		default:
		}
	}))
	defer collector.Close()

	e := newEventEmitter(collector.URL, secret)
	req := httptest.NewRequest(http.MethodPost, "/hook/stripe", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	e.record(Route{Path: "/hook/stripe", Provider: "stripe"}, []byte("payload"), req, "rejected", "signature mismatch", 0, 3*time.Millisecond)

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("collector never received the event")
	}

	mu.Lock()
	defer mu.Unlock()

	if gotHeader.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", gotHeader.Get("Content-Type"))
	}
	if gotHeader.Get(gatewaysig.ProviderHeader) != "console-ingest" {
		t.Errorf("provider header = %q, want console-ingest", gotHeader.Get(gatewaysig.ProviderHeader))
	}
	if err := gatewaysig.Verify(secret, "console-ingest", gotBody, gotHeader.Get(gatewaysig.Header)); err != nil {
		t.Errorf("gateway signature did not verify: %v", err)
	}

	var ev verifyEvent
	if err := json.Unmarshal(gotBody, &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if ev.Path != "/hook/stripe" || ev.Provider != "stripe" || ev.Verdict != "rejected" ||
		ev.Reason != "signature mismatch" || ev.RemoteIP != "203.0.113.7" || ev.BodyBytes != len("payload") {
		t.Errorf("event fields wrong: %+v", ev)
	}
	if ev.BodySHA256 == "" {
		t.Error("body_sha256 empty")
	}
}

// TestDropOldestOnOverflow fills the channel past capacity without a
// consumer draining it and asserts emit never blocks and the channel never
// exceeds its configured size — the backpressure guarantee the hot path
// depends on.
func TestDropOldestOnOverflow(t *testing.T) {
	e := &eventEmitter{url: "http://unused.invalid", ch: make(chan verifyEvent, 256)}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			e.emit(verifyEvent{Path: "/hook/x"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit blocked under overflow — hot path would stall")
	}

	if len(e.ch) > 256 {
		t.Fatalf("channel exceeded capacity: len=%d", len(e.ch))
	}
}

// TestClassifyReasonCoversAllVerifierErrors enumerates every literal error
// string stripe.go, github.go, shopify.go and paypal.go can return from
// Verify today. If a verifier's error text changes, this test catches the
// drift instead of the event taxonomy silently misclassifying it.
func TestClassifyReasonCoversAllVerifierErrors(t *testing.T) {
	cases := []struct {
		err  string
		want string
	}{
		// stripe.go
		{"missing Stripe-Signature header", "missing header"},
		{"malformed Stripe-Signature header", "bad encoding"},
		{"invalid timestamp", "bad encoding"},
		{"timestamp outside replay window", "stale timestamp"},
		{"no matching signature", "signature mismatch"},
		// github.go
		{"missing X-Hub-Signature-256 header", "missing header"},
		{"malformed X-Hub-Signature-256 header", "bad encoding"},
		{"invalid signature encoding", "bad encoding"},
		{"signature mismatch", "signature mismatch"},
		// shopify.go
		{"missing X-Shopify-Hmac-SHA256 header", "missing header"},
		// paypal.go
		{"missing PayPal signature headers", "missing header"},
		{`unsupported paypal-auth-algo "MD5withRSA"`, "unsupported algorithm"},
		{"paypal cert: invalid paypal-cert-url: parse \"::bad\": missing protocol scheme", "cert host rejected"},
		{"paypal cert: paypal-cert-url must be https", "cert host rejected"},
		{`paypal cert: paypal-cert-url host "evil.com" is not a trusted PayPal host`, "cert host rejected"},
		{"paypal cert: cert fetch: status 500", "other"},
		{"paypal cert: parse certificate: x509: malformed certificate", "bad encoding"},
		{"paypal cert: no certificate found in response", "bad encoding"},
		{"paypal cert: certificate chain: x509: certificate signed by unknown authority", "cert chain invalid"},
		{"paypal cert: not an RSA key", "other"},
		{"invalid paypal-transmission-sig encoding", "bad encoding"},
	}

	for _, c := range cases {
		t.Run(c.err, func(t *testing.T) {
			got := classifyReason(errString(c.err))
			if got != c.want {
				t.Errorf("classifyReason(%q) = %q, want %q", c.err, got, c.want)
			}
		})
	}

	if got := classifyReason(nil); got != "" {
		t.Errorf("classifyReason(nil) = %q, want empty", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
