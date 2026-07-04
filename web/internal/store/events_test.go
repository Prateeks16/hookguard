package store

import "testing"

// 4. Multiple events for the same (hour, provider, verdict) bucket sum
// correctly through UpsertRollups.
func TestUpsertRollupsSumsSameBucket(t *testing.T) {
	st := newTestStore(t)

	const hour = 474580 // an arbitrary unix-hour bucket
	deltas := []RollupDelta{
		{Hour: hour, Provider: "stripe", Verdict: "accepted", N: 1},
		{Hour: hour, Provider: "stripe", Verdict: "accepted", N: 1},
		{Hour: hour, Provider: "stripe", Verdict: "accepted", N: 1},
	}
	for _, d := range deltas {
		if err := st.UpsertRollups([]RollupDelta{d}); err != nil {
			t.Fatalf("upsert rollup: %v", err)
		}
	}

	n, err := st.RollupCount(hour, "stripe", "accepted")
	if err != nil {
		t.Fatalf("rollup count: %v", err)
	}
	if n != 3 {
		t.Fatalf("rollup n = %d, want 3", n)
	}
}

// A single call with a batched delta (the shape the ingest batcher actually
// uses — one summed delta per flush) also produces the correct total.
func TestUpsertRollupsBatchedDeltaSumsInOneCall(t *testing.T) {
	st := newTestStore(t)

	const hour = 474580
	if err := st.UpsertRollups([]RollupDelta{{Hour: hour, Provider: "stripe", Verdict: "accepted", N: 3}}); err != nil {
		t.Fatalf("upsert rollup: %v", err)
	}
	if err := st.UpsertRollups([]RollupDelta{{Hour: hour, Provider: "stripe", Verdict: "accepted", N: 2}}); err != nil {
		t.Fatalf("upsert rollup: %v", err)
	}

	n, err := st.RollupCount(hour, "stripe", "accepted")
	if err != nil {
		t.Fatalf("rollup count: %v", err)
	}
	if n != 5 {
		t.Fatalf("rollup n = %d, want 5", n)
	}
}

// Different verdict/provider buckets stay independent.
func TestUpsertRollupsDistinctBucketsIndependent(t *testing.T) {
	st := newTestStore(t)

	const hour = 474580
	err := st.UpsertRollups([]RollupDelta{
		{Hour: hour, Provider: "stripe", Verdict: "accepted", N: 2},
		{Hour: hour, Provider: "stripe", Verdict: "rejected", N: 1},
		{Hour: hour, Provider: "github", Verdict: "accepted", N: 5},
	})
	if err != nil {
		t.Fatalf("upsert rollups: %v", err)
	}

	cases := []struct {
		provider, verdict string
		want              int
	}{
		{"stripe", "accepted", 2},
		{"stripe", "rejected", 1},
		{"github", "accepted", 5},
	}
	for _, c := range cases {
		n, err := st.RollupCount(hour, c.provider, c.verdict)
		if err != nil {
			t.Fatalf("rollup count %s/%s: %v", c.provider, c.verdict, err)
		}
		if n != c.want {
			t.Errorf("rollup %s/%s = %d, want %d", c.provider, c.verdict, n, c.want)
		}
	}
}

func TestInsertEventsEmptySliceNoop(t *testing.T) {
	st := newTestStore(t)
	if err := st.InsertEvents(nil); err != nil {
		t.Fatalf("insert nil events: %v", err)
	}
	n, err := st.CountEvents()
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

func TestInsertEventsThenLatestEventRoundTrips(t *testing.T) {
	st := newTestStore(t)
	ev := Event{
		ReceivedAt:     1751459696789,
		Path:           "/hook/github",
		Provider:       "github",
		Verdict:        "accepted",
		Reason:         "",
		UpstreamStatus: 200,
		LatencyMS:      5,
		BodyBytes:      512,
		BodySHA256:     "deadbeef",
		RemoteIP:       "198.51.100.1",
	}
	if err := st.InsertEvents([]Event{ev}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	got, err := st.LatestEvent()
	if err != nil {
		t.Fatalf("latest event: %v", err)
	}
	if got.ID == 0 {
		t.Fatalf("expected LatestEvent to populate a nonzero ID")
	}
	got.ID = 0
	if *got != ev {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", *got, ev)
	}
}

// DeleteEventsOlderThan removes only rows with received_at < cutoffMS,
// leaving recent events and event_rollups untouched (DESIGN.md §8.2: the
// nightly job prunes events but rollups persist 13 months).
func TestDeleteEventsOlderThanPrunesOnlyOldRows(t *testing.T) {
	st := newTestStore(t)

	const cutoff = 1_700_000_000_000
	old1 := Event{ReceivedAt: cutoff - 2, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted"}
	old2 := Event{ReceivedAt: cutoff - 1, Path: "/hook/stripe", Provider: "stripe", Verdict: "rejected"}
	recent1 := Event{ReceivedAt: cutoff, Path: "/hook/github", Provider: "github", Verdict: "accepted"}
	recent2 := Event{ReceivedAt: cutoff + 100, Path: "/hook/github", Provider: "github", Verdict: "accepted"}
	if err := st.InsertEvents([]Event{old1, old2, recent1, recent2}); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	if err := st.UpsertRollups([]RollupDelta{{Hour: cutoff / 1000 / 3600, Provider: "stripe", Verdict: "accepted", N: 1}}); err != nil {
		t.Fatalf("upsert rollup: %v", err)
	}

	deleted, err := st.DeleteEventsOlderThan(cutoff)
	if err != nil {
		t.Fatalf("delete events older than: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}

	n, err := st.CountEvents()
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 2 {
		t.Fatalf("remaining count = %d, want 2", n)
	}

	remaining, err := st.ListEvents(EventFilter{}, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for _, e := range remaining {
		if e.ReceivedAt < cutoff {
			t.Fatalf("found stale event that should have been pruned: %+v", e)
		}
	}

	rollupN, err := st.RollupCount(cutoff/1000/3600, "stripe", "accepted")
	if err != nil {
		t.Fatalf("rollup count: %v", err)
	}
	if rollupN != 1 {
		t.Fatalf("event_rollups was touched by DeleteEventsOlderThan: rollup n = %d, want 1", rollupN)
	}
}

func TestDeleteEventsOlderThanNoMatchesReturnsZero(t *testing.T) {
	st := newTestStore(t)
	if err := st.InsertEvents([]Event{{ReceivedAt: 1_700_000_000_000, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted"}}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	deleted, err := st.DeleteEventsOlderThan(0)
	if err != nil {
		t.Fatalf("delete events older than: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
}
