package ingest

import (
	"path/filepath"
	"testing"
	"time"

	"hookguard/internal/gatewaysig"
	"hookguard/web/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestBatcherFlushPersistsEnqueuedEvent(t *testing.T) {
	st := newTestStore(t)
	b := NewBatcher(st)
	t.Cleanup(b.Close)

	b.Enqueue(store.Event{
		ReceivedAt: time.Now().UnixMilli(),
		Path:       "/hook/stripe",
		Provider:   "stripe",
		Verdict:    "accepted",
	})
	if err := b.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	n, err := st.CountEvents()
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}

func TestBatcherFlushUpsertsRollup(t *testing.T) {
	st := newTestStore(t)
	b := NewBatcher(st)
	t.Cleanup(b.Close)

	now := time.Now()
	for i := 0; i < 3; i++ {
		b.Enqueue(store.Event{
			ReceivedAt: now.UnixMilli(),
			Path:       "/hook/stripe",
			Provider:   "stripe",
			Verdict:    "accepted",
		})
	}
	if err := b.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	hour := now.UnixMilli() / 1000 / 3600
	n, err := st.RollupCount(hour, "stripe", "accepted")
	if err != nil {
		t.Fatalf("rollup count: %v", err)
	}
	if n != 3 {
		t.Fatalf("rollup n = %d, want 3", n)
	}
}

func TestBatcherFlushWithNothingEnqueuedIsNoop(t *testing.T) {
	st := newTestStore(t)
	b := NewBatcher(st)
	t.Cleanup(b.Close)

	if err := b.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	n, err := st.CountEvents()
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

func TestBatcherFlushSetsLastIngestAt(t *testing.T) {
	st := newTestStore(t)
	b := NewBatcher(st)
	t.Cleanup(b.Close)

	receivedAt := time.Now().UnixMilli()
	b.Enqueue(store.Event{ReceivedAt: receivedAt, Path: "/hook/github", Provider: "github", Verdict: "accepted"})
	if err := b.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	v, err := st.GetSetting("last_ingest_at")
	if err != nil {
		t.Fatalf("get setting: %v", err)
	}
	if v == "" {
		t.Fatal("last_ingest_at not set after flush")
	}
}

// The ticker itself (not just explicit Flush) eventually persists an
// enqueued event — confirms the 100ms tick actually drains the channel.
func TestBatcherTickerFlushesWithoutExplicitFlush(t *testing.T) {
	st := newTestStore(t)
	b := NewBatcher(st)
	t.Cleanup(b.Close)

	b.Enqueue(store.Event{ReceivedAt: time.Now().UnixMilli(), Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, err := st.CountEvents()
		if err != nil {
			t.Fatalf("count events: %v", err)
		}
		if n == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("event was not persisted by ticker within 2s")
}

// 5. End-to-end: sign an event exactly as the gateway's eventEmitter does
// (gatewaysig.Sign(secret, "console-ingest", eventJSON)) and confirm Verify
// accepts it and Decode round-trips every field.
func TestVerifyAndDecodeAcceptRealGatewaySignedEvent(t *testing.T) {
	secret := []byte("shared-internal-secret")
	sendTime := time.Date(2026, 7, 2, 12, 34, 56, 789000000, time.UTC)

	body := []byte(`{
		"ts": "` + sendTime.Format(time.RFC3339Nano) + `",
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

	sig := gatewaysig.Sign(secret, ExpectedProvider, body)

	if err := Verify(secret, body, sig); err != nil {
		t.Fatalf("verify real gateway-signed event: %v", err)
	}
	if err := CheckProviderHeader(ExpectedProvider); err != nil {
		t.Fatalf("check provider header: %v", err)
	}

	ev, err := Decode(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !ev.Timestamp.Equal(sendTime) {
		t.Errorf("Timestamp = %v, want %v", ev.Timestamp, sendTime)
	}
	if ev.Path != "/hook/stripe" || ev.Provider != "stripe" || ev.Verdict != "rejected" {
		t.Errorf("unexpected decoded event: %+v", ev)
	}
	if ev.Reason != "signature mismatch" || ev.UpstreamStatus != 0 || ev.LatencyMS != 2 {
		t.Errorf("unexpected decoded event: %+v", ev)
	}
	if ev.BodyBytes != 214 || ev.BodySHA256 == "" || ev.RemoteIP != "203.0.113.7" {
		t.Errorf("unexpected decoded event: %+v", ev)
	}

	// A signature computed with the wrong secret must not verify.
	if err := Verify([]byte("wrong-secret"), body, sig); err == nil {
		t.Fatal("expected verify failure with wrong secret")
	}
}

func TestCheckProviderHeaderRejectsWrongValue(t *testing.T) {
	if err := CheckProviderHeader("something-else"); err == nil {
		t.Fatal("expected rejection for wrong provider header")
	}
}
