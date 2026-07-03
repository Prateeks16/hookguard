package server

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"

	"hookguard/web/internal/store"
)

func TestHandleLogsListUnauthenticatedRedirects(t *testing.T) {
	_, ts := newTestServer(t, true)
	client := newClient(t)

	resp, err := client.Get(ts.URL + "/dashboard/logs")
	if err != nil {
		t.Fatalf("get logs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect to login", resp.StatusCode)
	}
}

func TestHandleLogsListEmptyState(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "empty-logs@example.com")

	resp, err := client.Get(ts.URL + "/dashboard/logs")
	if err != nil {
		t.Fatalf("get logs: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "No traffic at the gate yet.") {
		t.Fatalf("expected empty-state copy, got: %s", body)
	}
}

func TestHandleLogsListSeededRows(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "seeded-logs@example.com")

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	srv.Now = func() time.Time { return now }

	if err := srv.Store.InsertEvents([]store.Event{
		{ReceivedAt: now.UnixMilli() - 1000, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted", LatencyMS: 5, RemoteIP: "203.0.113.1"},
		{ReceivedAt: now.UnixMilli(), Path: "/hook/github", Provider: "github", Verdict: "rejected", Reason: "signature mismatch", RemoteIP: "203.0.113.2"},
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	resp, err := client.Get(ts.URL + "/dashboard/logs")
	if err != nil {
		t.Fatalf("get logs: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "No traffic at the gate yet.") {
		t.Fatalf("did not expect empty state with seeded events")
	}
	if !strings.Contains(body, "203.0.113.1") || !strings.Contains(body, "203.0.113.2") {
		t.Fatalf("expected both seeded rows in body, got: %s", body)
	}
	if !strings.Contains(body, "signature mismatch") {
		t.Fatalf("expected rejection reason in body")
	}
}

func TestHandleLogsListFilterByVerdict(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "filter-logs@example.com")

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	srv.Now = func() time.Time { return now }

	if err := srv.Store.InsertEvents([]store.Event{
		{ReceivedAt: now.UnixMilli(), Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted", RemoteIP: "203.0.113.9"},
		{ReceivedAt: now.UnixMilli(), Path: "/hook/github", Provider: "github", Verdict: "rejected", Reason: "stale timestamp", RemoteIP: "203.0.113.8"},
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	resp, err := client.Get(ts.URL + "/dashboard/logs?verdict=rejected")
	if err != nil {
		t.Fatalf("get logs: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "203.0.113.8") {
		t.Fatalf("expected the rejected row present, got: %s", body)
	}
	if strings.Contains(body, "203.0.113.9") {
		t.Fatalf("did not expect the accepted row with verdict=rejected filter, got: %s", body)
	}
}

func TestHandleLogsStreamUnauthenticatedRedirects(t *testing.T) {
	_, ts := newTestServer(t, true)
	client := newClient(t)

	resp, err := client.Get(ts.URL + "/dashboard/logs/stream")
	if err != nil {
		t.Fatalf("get logs stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect to login", resp.StatusCode)
	}
}

// TestHandleLogsStreamDeliversNewEvent is a full SSE-level test: it opens a
// real streaming connection, inserts an event directly via the store after
// the connection is live (simulating the ingest batcher's flush landing
// between poll ticks), and asserts the client actually receives it within a
// few ticks of logsStreamPollInterval.
func TestHandleLogsStreamDeliversNewEvent(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "stream-logs@example.com")

	resp, err := client.Get(ts.URL + "/dashboard/logs/stream")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	now := time.Now()
	if err := srv.Store.InsertEvents([]store.Event{
		{ReceivedAt: now.UnixMilli(), Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted", RemoteIP: "198.51.100.7", LatencyMS: 9},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case line := <-lines:
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, "198.51.100.7") {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the streamed event")
		}
	}
}
