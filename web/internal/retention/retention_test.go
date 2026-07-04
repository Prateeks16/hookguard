package retention

import (
	"path/filepath"
	"testing"
	"time"

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

// Sweep computes the cutoff from retention_days and now, and deletes
// exactly the events older than it.
func TestSweepDeletesEventsOlderThanRetentionWindow(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetRetentionDays(30); err != nil {
		t.Fatalf("set retention days: %v", err)
	}

	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	old := now.Add(-31 * 24 * time.Hour).UnixMilli()
	recent := now.Add(-29 * 24 * time.Hour).UnixMilli()

	if err := st.InsertEvents([]store.Event{
		{ReceivedAt: old, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted"},
		{ReceivedAt: recent, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted"},
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	deleted, err := Sweep(st, now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	n, err := st.CountEvents()
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Fatalf("remaining count = %d, want 1", n)
	}
}

// A retention_days change takes effect on the next Sweep call without
// reconstructing anything — Sweep reads the setting fresh every time.
func TestSweepReadsRetentionDaysFreshEachCall(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetRetentionDays(30); err != nil {
		t.Fatalf("set retention days: %v", err)
	}

	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	tenDaysAgo := now.Add(-10 * 24 * time.Hour).UnixMilli()
	if err := st.InsertEvents([]store.Event{
		{ReceivedAt: tenDaysAgo, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted"},
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	if deleted, err := Sweep(st, now); err != nil || deleted != 0 {
		t.Fatalf("sweep before setting change: deleted=%d err=%v, want 0/nil", deleted, err)
	}

	if err := st.SetRetentionDays(5); err != nil {
		t.Fatalf("set retention days: %v", err)
	}
	deleted, err := Sweep(st, now)
	if err != nil {
		t.Fatalf("sweep after setting change: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted after tightening retention = %d, want 1", deleted)
	}
}

func TestNewJobRunsSweepImmediatelyAtStartup(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetRetentionDays(1); err != nil {
		t.Fatalf("set retention days: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	if err := st.InsertEvents([]store.Event{
		{ReceivedAt: old, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted"},
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	j := NewJob(st)
	t.Cleanup(j.Close)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, err := st.CountEvents()
		if err != nil {
			t.Fatalf("count events: %v", err)
		}
		if n == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stale event was not pruned by the immediate startup sweep")
}

func TestJobCloseStopsGoroutineCleanly(t *testing.T) {
	st := newTestStore(t)
	j := NewJob(st)
	j.Close() // must return promptly, not hang
}
