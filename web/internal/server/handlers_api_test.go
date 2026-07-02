package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"hookguard/internal/gatewaysig"
	"hookguard/web/internal/ingest"
)

const testInternalSecret = "ingest-test-internal-secret"

func signedIngestRequest(t *testing.T, ts string, body []byte, provider, secret string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts+"/api/v1/ingest", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set(ingest.ProviderHeader, provider)
	req.Header.Set(ingest.SignatureHeader, gatewaysig.Sign([]byte(secret), provider, body))
	return req
}

func sampleEventJSON(ts time.Time) []byte {
	return []byte(`{
		"ts": "` + ts.Format(time.RFC3339Nano) + `",
		"path": "/hook/stripe",
		"provider": "stripe",
		"verdict": "rejected",
		"reason": "signature mismatch",
		"upstream_status": 0,
		"latency_ms": 2,
		"body_bytes": 214,
		"body_sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		"remote_ip": "203.0.113.7"
	}`)
}

// 1. Bad/missing gatewaysig signature -> 401, no row inserted.
func TestIngestBadSignatureRejectedNoInsert(t *testing.T) {
	srv, ts := newTestServer(t, true)
	body := sampleEventJSON(time.Now().UTC())

	req := signedIngestRequest(t, ts.URL, body, ingest.ExpectedProvider, "wrong-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	n, err := srv.Store.CountEvents()
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 0 {
		t.Fatalf("events count = %d, want 0 after bad signature", n)
	}
}

// missing signature header entirely -> also 401, no row.
func TestIngestMissingSignatureRejectedNoInsert(t *testing.T) {
	srv, ts := newTestServer(t, true)
	body := sampleEventJSON(time.Now().UTC())

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/ingest", strings.NewReader(string(body)))
	req.Header.Set(ingest.ProviderHeader, ingest.ExpectedProvider)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	n, err := srv.Store.CountEvents()
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 0 {
		t.Fatalf("events count = %d, want 0 after missing signature", n)
	}
}

// 2. Valid signed event -> 200/202, and after Flush() the row exists with
// every field round-tripped correctly.
func TestIngestValidEventPersistsAllFields(t *testing.T) {
	srv, ts := newTestServer(t, true)
	sendTime := time.Date(2026, 7, 2, 12, 34, 56, 789000000, time.UTC)
	body := sampleEventJSON(sendTime)

	req := signedIngestRequest(t, ts.URL, body, ingest.ExpectedProvider, testInternalSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	if err := srv.Ingest.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	rows, err := srv.Store.CountEvents()
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if rows != 1 {
		t.Fatalf("events count = %d, want 1", rows)
	}

	got, err := srv.Store.LatestEvent()
	if err != nil {
		t.Fatalf("query latest event: %v", err)
	}
	if got.ReceivedAt != sendTime.UnixMilli() {
		t.Errorf("ReceivedAt = %d, want %d", got.ReceivedAt, sendTime.UnixMilli())
	}
	if got.Path != "/hook/stripe" {
		t.Errorf("Path = %q, want /hook/stripe", got.Path)
	}
	if got.Provider != "stripe" {
		t.Errorf("Provider = %q, want stripe", got.Provider)
	}
	if got.Verdict != "rejected" {
		t.Errorf("Verdict = %q, want rejected", got.Verdict)
	}
	if got.Reason != "signature mismatch" {
		t.Errorf("Reason = %q, want %q", got.Reason, "signature mismatch")
	}
	if got.UpstreamStatus != 0 {
		t.Errorf("UpstreamStatus = %d, want 0", got.UpstreamStatus)
	}
	if got.LatencyMS != 2 {
		t.Errorf("LatencyMS = %d, want 2", got.LatencyMS)
	}
	if got.BodyBytes != 214 {
		t.Errorf("BodyBytes = %d, want 214", got.BodyBytes)
	}
	if got.BodySHA256 != "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" {
		t.Errorf("BodySHA256 = %q, unexpected", got.BodySHA256)
	}
	if got.RemoteIP != "203.0.113.7" {
		t.Errorf("RemoteIP = %q, want 203.0.113.7", got.RemoteIP)
	}
}

// 3. Malformed JSON body, but a valid gatewaysig signature over those exact
// (malformed) bytes -> 400, no row inserted.
func TestIngestMalformedJSONRejectedNoInsert(t *testing.T) {
	srv, ts := newTestServer(t, true)
	body := []byte(`{"ts": "not valid json`)

	req := signedIngestRequest(t, ts.URL, body, ingest.ExpectedProvider, testInternalSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	if err := srv.Ingest.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	n, err := srv.Store.CountEvents()
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 0 {
		t.Fatalf("events count = %d, want 0 after malformed body", n)
	}
}
