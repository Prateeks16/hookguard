package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"hookguard/web/internal/store"
)

// GET /dashboard with zero events ever recorded renders the empty state.
func TestHandleOverviewEmptyState(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "empty-overview@example.com")

	resp, err := client.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("get dashboard: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "No traffic at the gate yet.") {
		t.Fatalf("expected empty-state copy, got: %s", body)
	}
	if !strings.Contains(body, "curl") {
		t.Fatalf("expected a curl snippet in the empty state, got: %s", body)
	}
}

// GET /dashboard with seeded events renders the correct stat numbers and the
// recent-rejections table in the right order.
func TestHandleOverviewWithSeededEvents(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "seeded-overview@example.com")

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	srv.Now = func() time.Time { return now }
	nowHour := now.Unix() / 3600

	if err := srv.Store.UpsertRollups([]store.RollupDelta{
		{Hour: nowHour, Provider: "stripe", Verdict: "accepted", N: 8},
		{Hour: nowHour, Provider: "stripe", Verdict: "rejected", N: 2},
	}); err != nil {
		t.Fatalf("upsert rollups: %v", err)
	}

	events := []store.Event{
		{ReceivedAt: now.UnixMilli() - 2000, Path: "/hook/stripe", Provider: "stripe", Verdict: "rejected", Reason: "signature mismatch", RemoteIP: "203.0.113.1"},
		{ReceivedAt: now.UnixMilli() - 1000, Path: "/hook/github", Provider: "github", Verdict: "rejected", Reason: "stale timestamp", RemoteIP: "203.0.113.2"},
		{ReceivedAt: now.UnixMilli(), Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted", LatencyMS: 12},
	}
	if err := srv.Store.InsertEvents(events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	resp, err := client.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("get dashboard: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, body)
	}

	if strings.Contains(body, "No traffic at the gate yet.") {
		t.Fatalf("did not expect empty state with seeded events, got: %s", body)
	}

	// 8 accepted, 2 rejected -> accept rate 80%.
	if !strings.Contains(body, ">8<") {
		t.Errorf("expected accepted count 8 in body")
	}
	if !strings.Contains(body, ">2<") {
		t.Errorf("expected rejected count 2 in body")
	}
	if !strings.Contains(body, "80%") {
		t.Errorf("expected accept rate 80%% in body")
	}

	// Recent rejections newest-first: stale timestamp (github) before
	// signature mismatch (stripe).
	staleIdx := strings.Index(body, "stale timestamp")
	mismatchIdx := strings.Index(body, "signature mismatch")
	if staleIdx == -1 || mismatchIdx == -1 {
		t.Fatalf("expected both rejection reasons present, got: %s", body)
	}
	if staleIdx > mismatchIdx {
		t.Errorf("expected newest rejection (stale timestamp) to appear before older one (signature mismatch)")
	}
	if !strings.Contains(body, "203.0.113.1") || !strings.Contains(body, "203.0.113.2") {
		t.Errorf("expected remote IPs of rejected rows in body")
	}
}

// GET /api/v1/stats/summary is session-authed: unauthenticated -> redirect
// to login, consistent with the existing requireAuth pattern.
func TestHandleStatsSummaryUnauthenticatedRedirects(t *testing.T) {
	_, ts := newTestServer(t, true)
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(ts.URL + "/api/v1/stats/summary?window=24h")
	if err != nil {
		t.Fatalf("get stats summary: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect to login", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("Location = %q, want /login prefix", resp.Header.Get("Location"))
	}
}

// GET /api/v1/stats/summary authenticated with seeded data returns the
// correct JSON numbers.
func TestHandleStatsSummaryAuthenticatedReturnsJSON(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "stats-json@example.com")

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	srv.Now = func() time.Time { return now }
	nowHour := now.Unix() / 3600

	if err := srv.Store.UpsertRollups([]store.RollupDelta{
		{Hour: nowHour, Provider: "stripe", Verdict: "accepted", N: 3},
		{Hour: nowHour, Provider: "stripe", Verdict: "rejected", N: 1},
	}); err != nil {
		t.Fatalf("upsert rollups: %v", err)
	}
	if err := srv.Store.InsertEvents([]store.Event{
		{ReceivedAt: now.UnixMilli(), Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted", LatencyMS: 42},
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	resp, err := client.Get(ts.URL + "/api/v1/stats/summary?window=24h")
	if err != nil {
		t.Fatalf("get stats summary: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got statsSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if got.Accepted != 3 {
		t.Errorf("Accepted = %d, want 3", got.Accepted)
	}
	if got.Rejected != 1 {
		t.Errorf("Rejected = %d, want 1", got.Rejected)
	}
	if got.AcceptRate != 0.75 {
		t.Errorf("AcceptRate = %v, want 0.75", got.AcceptRate)
	}
	if got.P50LatencyMS != 42 {
		t.Errorf("P50LatencyMS = %d, want 42", got.P50LatencyMS)
	}
	if got.Window != "24h" {
		t.Errorf("Window = %q, want 24h", got.Window)
	}
}

// Status strip: last_ingest_at set to "now" -> connected.
func TestStatusStripConnectedWhenRecentIngest(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "status-connected@example.com")

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	srv.Now = func() time.Time { return now }
	if err := srv.Store.SetSetting("last_ingest_at", strconv.FormatInt(now.UnixMilli(), 10)); err != nil {
		t.Fatalf("set setting: %v", err)
	}

	resp, err := client.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("get dashboard: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "gateway: connected") {
		t.Fatalf("expected connected status strip, got: %s", body)
	}
	if strings.Contains(body, "no signal from gateway") {
		t.Fatalf("did not expect no-signal copy when recently connected, got: %s", body)
	}
}

// Status strip: last_ingest_at unset -> "no signal from gateway" in the warn
// state.
func TestStatusStripNoSignalWhenNeverIngested(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "status-never@example.com")

	resp, err := client.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("get dashboard: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "no signal from gateway") {
		t.Fatalf("expected no-signal status strip, got: %s", body)
	}
}

// Status strip: last_ingest_at more than 60s ago -> "no signal from gateway".
func TestStatusStripNoSignalWhenStaleIngest(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, _ := loginAsFreshUser(t, srv, ts.URL, "status-stale@example.com")

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	srv.Now = func() time.Time { return now }
	staleAt := now.Add(-90 * time.Second)
	if err := srv.Store.SetSetting("last_ingest_at", strconv.FormatInt(staleAt.UnixMilli(), 10)); err != nil {
		t.Fatalf("set setting: %v", err)
	}

	resp, err := client.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("get dashboard: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "no signal from gateway") {
		t.Fatalf("expected no-signal status strip for 90s-old ingest, got: %s", body)
	}
}

// gatewayConnected unit-level boundary check, independent of HTTP plumbing.
func TestGatewayConnectedBoundary(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		ago  time.Duration
		want bool
	}{
		{"just now", 0, true},
		{"59s ago", 59 * time.Second, true},
		{"60s ago", 60 * time.Second, true},
		{"61s ago", 61 * time.Second, false},
		{"5m ago", 5 * time.Minute, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gatewayConnected(now, now.Add(-c.ago).UnixMilli())
			if got != c.want {
				t.Errorf("gatewayConnected(%v ago) = %v, want %v", c.ago, got, c.want)
			}
		})
	}
	if gatewayConnected(now, 0) {
		t.Error("gatewayConnected with lastIngestAtMS=0 (never set) should be false")
	}
}
