package store

import (
	"testing"
	"time"
)

// SummaryWindow sums accepted/rejected from event_rollups and computes an
// accept rate, over a known small dataset where the numbers are hand-computed.
func TestSummaryWindowCountsAndAcceptRate(t *testing.T) {
	st := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	nowHour := now.Unix() / 3600

	if err := st.UpsertRollups([]RollupDelta{
		{Hour: nowHour, Provider: "stripe", Verdict: "accepted", N: 7},
		{Hour: nowHour, Provider: "stripe", Verdict: "rejected", N: 3},
		{Hour: nowHour - 1, Provider: "github", Verdict: "accepted", N: 5},
	}); err != nil {
		t.Fatalf("upsert rollups: %v", err)
	}

	sum, err := st.SummaryWindow(now, 24)
	if err != nil {
		t.Fatalf("summary window: %v", err)
	}
	if sum.Accepted != 12 {
		t.Errorf("Accepted = %d, want 12", sum.Accepted)
	}
	if sum.Rejected != 3 {
		t.Errorf("Rejected = %d, want 3", sum.Rejected)
	}
	wantRate := 12.0 / 15.0
	if sum.AcceptRate != wantRate {
		t.Errorf("AcceptRate = %v, want %v", sum.AcceptRate, wantRate)
	}
}

// Rollup buckets outside the requested window must not be counted.
func TestSummaryWindowExcludesBucketsOutsideWindow(t *testing.T) {
	st := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	nowHour := now.Unix() / 3600

	if err := st.UpsertRollups([]RollupDelta{
		{Hour: nowHour, Provider: "stripe", Verdict: "accepted", N: 1},
		{Hour: nowHour - 30, Provider: "stripe", Verdict: "accepted", N: 100}, // outside a 24h window
	}); err != nil {
		t.Fatalf("upsert rollups: %v", err)
	}

	sum, err := st.SummaryWindow(now, 24)
	if err != nil {
		t.Fatalf("summary window: %v", err)
	}
	if sum.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1 (30h-old bucket must be excluded from a 24h window)", sum.Accepted)
	}
}

// No events at all -> accept rate is 0, not NaN/Inf/crash.
func TestSummaryWindowNoEventsAcceptRateZero(t *testing.T) {
	st := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	sum, err := st.SummaryWindow(now, 24)
	if err != nil {
		t.Fatalf("summary window: %v", err)
	}
	if sum.Accepted != 0 || sum.Rejected != 0 {
		t.Fatalf("expected zero counts, got %+v", sum)
	}
	if sum.AcceptRate != 0 {
		t.Errorf("AcceptRate = %v, want 0 for no events", sum.AcceptRate)
	}
	if sum.P50LatencyMS != 0 {
		t.Errorf("P50LatencyMS = %v, want 0 for no events", sum.P50LatencyMS)
	}
}

// p50 latency over a known small odd-length dataset.
func TestSummaryWindowP50LatencyOddCount(t *testing.T) {
	st := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	nowMS := now.UnixMilli()

	latencies := []int64{10, 30, 20, 50, 40} // median = 30
	for i, l := range latencies {
		ev := Event{
			ReceivedAt: nowMS - int64(i)*1000,
			Path:       "/hook/stripe",
			Provider:   "stripe",
			Verdict:    "accepted",
			LatencyMS:  l,
		}
		if err := st.InsertEvents([]Event{ev}); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	sum, err := st.SummaryWindow(now, 24)
	if err != nil {
		t.Fatalf("summary window: %v", err)
	}
	if sum.P50LatencyMS != 30 {
		t.Errorf("P50LatencyMS = %d, want 30", sum.P50LatencyMS)
	}
}

// p50 latency over a known small even-length dataset: lower-of-middle-two
// tie-break, documented in p50LatencySince.
func TestSummaryWindowP50LatencyEvenCount(t *testing.T) {
	st := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	nowMS := now.UnixMilli()

	latencies := []int64{10, 20, 30, 40} // sorted; lower-of-middle-two = 20
	for i, l := range latencies {
		ev := Event{
			ReceivedAt: nowMS - int64(i)*1000,
			Path:       "/hook/stripe",
			Provider:   "stripe",
			Verdict:    "accepted",
			LatencyMS:  l,
		}
		if err := st.InsertEvents([]Event{ev}); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	sum, err := st.SummaryWindow(now, 24)
	if err != nil {
		t.Fatalf("summary window: %v", err)
	}
	if sum.P50LatencyMS != 20 {
		t.Errorf("P50LatencyMS = %d, want 20", sum.P50LatencyMS)
	}
}

// Events outside the window (older than the requested hours) must not
// contribute to the p50 sample.
func TestSummaryWindowP50LatencyExcludesOldEvents(t *testing.T) {
	st := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	nowMS := now.UnixMilli()

	if err := st.InsertEvents([]Event{
		{ReceivedAt: nowMS, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted", LatencyMS: 5},
		{ReceivedAt: nowMS - 30*3600*1000, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted", LatencyMS: 9000}, // 30h old
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	sum, err := st.SummaryWindow(now, 24)
	if err != nil {
		t.Fatalf("summary window: %v", err)
	}
	if sum.P50LatencyMS != 5 {
		t.Errorf("P50LatencyMS = %d, want 5 (old event must be excluded)", sum.P50LatencyMS)
	}
}

// HourlyCountsWindow zero-fills hours with no rollup rows and returns a
// dense, ascending-hour series.
func TestHourlyCountsWindowDenseSeries(t *testing.T) {
	st := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	nowHour := now.Unix() / 3600

	if err := st.UpsertRollups([]RollupDelta{
		{Hour: nowHour, Provider: "stripe", Verdict: "accepted", N: 4},
		{Hour: nowHour, Provider: "stripe", Verdict: "rejected", N: 1},
		{Hour: nowHour - 2, Provider: "github", Verdict: "accepted", N: 2},
	}); err != nil {
		t.Fatalf("upsert rollups: %v", err)
	}

	got, err := st.HourlyCountsWindow(now, 5)
	if err != nil {
		t.Fatalf("hourly counts: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len(got) = %d, want 5", len(got))
	}
	// ascending order: index 0 = nowHour-4, index 4 = nowHour
	if got[4].Accepted != 4 || got[4].Rejected != 1 {
		t.Errorf("last bucket = %+v, want accepted=4 rejected=1", got[4])
	}
	if got[2].Accepted != 2 {
		t.Errorf("nowHour-2 bucket = %+v, want accepted=2", got[2])
	}
	if got[0].Accepted != 0 || got[0].Rejected != 0 {
		t.Errorf("zero-fill bucket = %+v, want zeros", got[0])
	}
	if got[0].Hour != nowHour-4 {
		t.Errorf("got[0].Hour = %d, want %d", got[0].Hour, nowHour-4)
	}
}

// RecentRejected returns the last N rejected rows, newest first, and never
// includes accepted rows.
func TestRecentRejectedOrderAndFilter(t *testing.T) {
	st := newTestStore(t)
	base := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC).UnixMilli()

	events := []Event{
		{ReceivedAt: base, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted", Reason: ""},
		{ReceivedAt: base + 1000, Path: "/hook/github", Provider: "github", Verdict: "rejected", Reason: "signature mismatch", RemoteIP: "1.1.1.1"},
		{ReceivedAt: base + 2000, Path: "/hook/shopify", Provider: "shopify", Verdict: "rejected", Reason: "stale timestamp", RemoteIP: "2.2.2.2"},
	}
	if err := st.InsertEvents(events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	got, err := st.RecentRejected(10)
	if err != nil {
		t.Fatalf("recent rejected: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Reason != "stale timestamp" {
		t.Errorf("got[0].Reason = %q, want %q (newest first)", got[0].Reason, "stale timestamp")
	}
	if got[1].Reason != "signature mismatch" {
		t.Errorf("got[1].Reason = %q, want %q", got[1].Reason, "signature mismatch")
	}
	for _, e := range got {
		if e.Verdict != "rejected" {
			t.Errorf("got verdict %q, want only rejected rows", e.Verdict)
		}
	}
}

// RecentRejected respects its limit argument.
func TestRecentRejectedRespectsLimit(t *testing.T) {
	st := newTestStore(t)
	base := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC).UnixMilli()

	var events []Event
	for i := 0; i < 15; i++ {
		events = append(events, Event{
			ReceivedAt: base + int64(i)*1000,
			Path:       "/hook/stripe",
			Provider:   "stripe",
			Verdict:    "rejected",
			Reason:     "signature mismatch",
		})
	}
	if err := st.InsertEvents(events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	got, err := st.RecentRejected(10)
	if err != nil {
		t.Fatalf("recent rejected: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("len(got) = %d, want 10", len(got))
	}
}

// HasAnyEvent is false on a fresh store and true after any insert.
func TestHasAnyEventEmptyThenTrue(t *testing.T) {
	st := newTestStore(t)

	has, err := st.HasAnyEvent()
	if err != nil {
		t.Fatalf("has any event: %v", err)
	}
	if has {
		t.Fatal("expected false on empty store")
	}

	if err := st.InsertEvents([]Event{{
		ReceivedAt: time.Now().UnixMilli(),
		Path:       "/hook/stripe",
		Provider:   "stripe",
		Verdict:    "accepted",
	}}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	has, err = st.HasAnyEvent()
	if err != nil {
		t.Fatalf("has any event: %v", err)
	}
	if !has {
		t.Fatal("expected true after insert")
	}
}
