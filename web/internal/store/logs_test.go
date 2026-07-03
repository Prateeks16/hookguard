package store

import "testing"

func seedLogEvents(t *testing.T, st *Store) []Event {
	t.Helper()
	events := []Event{
		{ReceivedAt: 1000, Path: "/hook/stripe", Provider: "stripe", Verdict: "accepted", Reason: "", RemoteIP: "10.0.0.1"},
		{ReceivedAt: 2000, Path: "/hook/stripe", Provider: "stripe", Verdict: "rejected", Reason: "signature mismatch", RemoteIP: "10.0.0.2"},
		{ReceivedAt: 3000, Path: "/hook/github", Provider: "github", Verdict: "rejected", Reason: "stale timestamp", RemoteIP: "10.0.0.3"},
		{ReceivedAt: 4000, Path: "/hook/shopify", Provider: "shopify", Verdict: "accepted", Reason: "", RemoteIP: "10.0.0.4"},
		{ReceivedAt: 5000, Path: "/hook/paypal", Provider: "paypal", Verdict: "rejected", Reason: "cert host rejected", RemoteIP: "10.0.0.5"},
	}
	if err := st.InsertEvents(events); err != nil {
		t.Fatalf("seed insert events: %v", err)
	}
	return events
}

// 1. ListEvents with no filter returns everything, newest first.
func TestListEventsNoFilterNewestFirst(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	got, err := st.ListEvents(EventFilter{}, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	if got[0].Provider != "paypal" || got[len(got)-1].Provider != "stripe" {
		t.Fatalf("expected newest-first order, got providers: %v", providersOf(got))
	}
}

// 2. Filter by provider excludes non-matching rows.
func TestListEventsFilterByProvider(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	got, err := st.ListEvents(EventFilter{Provider: "stripe"}, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, e := range got {
		if e.Provider != "stripe" {
			t.Errorf("unexpected provider %q in stripe-filtered results", e.Provider)
		}
	}
}

// 3. Filter by verdict excludes non-matching rows.
func TestListEventsFilterByVerdict(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	got, err := st.ListEvents(EventFilter{Verdict: "rejected"}, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for _, e := range got {
		if e.Verdict != "rejected" {
			t.Errorf("unexpected verdict %q in rejected-filtered results", e.Verdict)
		}
	}
}

// 4. Filter by reason substring matches partial text, case-sensitive per
// SQLite's default LIKE behavior for non-ASCII-agnostic columns (documented:
// substring match, not exact).
func TestListEventsFilterByReasonSubstring(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	got, err := st.ListEvents(EventFilter{Reason: "signature"}, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Reason != "signature mismatch" {
		t.Errorf("reason = %q, want signature mismatch", got[0].Reason)
	}
}

// 5. Filter by path substring.
func TestListEventsFilterByPathSubstring(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	got, err := st.ListEvents(EventFilter{Path: "github"}, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Path != "/hook/github" {
		t.Errorf("path = %q, want /hook/github", got[0].Path)
	}
}

// 6. Filter by time range (from/to unix ms) is inclusive on both ends and
// excludes events outside the window.
func TestListEventsFilterByTimeRange(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	got, err := st.ListEvents(EventFilter{FromMS: 2000, ToMS: 4000}, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (received_at 2000,3000,4000)", len(got))
	}
	for _, e := range got {
		if e.ReceivedAt < 2000 || e.ReceivedAt > 4000 {
			t.Errorf("event received_at %d outside requested [2000,4000] range", e.ReceivedAt)
		}
	}
}

// 7. Combined filters (provider + verdict) narrow further, and a filter
// combination matching nothing returns an empty (not nil-erroring) slice.
func TestListEventsCombinedFiltersAndNoMatch(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	got, err := st.ListEvents(EventFilter{Provider: "stripe", Verdict: "rejected"}, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(got) != 1 || got[0].Reason != "signature mismatch" {
		t.Fatalf("expected exactly the stripe/rejected row, got %+v", got)
	}

	none, err := st.ListEvents(EventFilter{Provider: "stripe", Verdict: "rejected", Reason: "stale timestamp"}, 100)
	if err != nil {
		t.Fatalf("list events (no match): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no rows, got %d", len(none))
	}
}

// 8. Limit caps the result count even with more matching rows available.
func TestListEventsRespectsLimit(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	got, err := st.ListEvents(EventFilter{}, 2)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

// 9. EventsSince returns only rows with id greater than the cursor, in
// ascending id order (the SSE tail's requirement).
func TestEventsSinceReturnsOnlyNewerIDsInOrder(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	all, err := st.ListEvents(EventFilter{}, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("setup: len(all) = %d, want 5", len(all))
	}
	// all is newest-first; the 3rd-oldest row's id is our cursor.
	thirdOldestID := all[len(all)-3].ID

	got, err := st.EventsSince(thirdOldestID, 100)
	if err != nil {
		t.Fatalf("events since: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (rows after the cursor)", len(got))
	}
	for i, e := range got {
		if e.ID <= thirdOldestID {
			t.Errorf("row %d has id %d, want > cursor %d", i, e.ID, thirdOldestID)
		}
	}
	if got[0].ID >= got[1].ID {
		t.Errorf("expected ascending id order, got %d then %d", got[0].ID, got[1].ID)
	}
}

// 10. EventsSince with sinceID 0 returns everything (a fresh connection with
// no prior cursor), and with a cursor at or beyond the max id returns none.
func TestEventsSinceZeroAndBeyondMax(t *testing.T) {
	st := newTestStore(t)
	seedLogEvents(t, st)

	fromZero, err := st.EventsSince(0, 100)
	if err != nil {
		t.Fatalf("events since 0: %v", err)
	}
	if len(fromZero) != 5 {
		t.Fatalf("len = %d, want 5", len(fromZero))
	}

	maxID, err := st.LatestEventID()
	if err != nil {
		t.Fatalf("latest event id: %v", err)
	}
	if maxID != fromZero[len(fromZero)-1].ID {
		t.Fatalf("LatestEventID() = %d, want %d", maxID, fromZero[len(fromZero)-1].ID)
	}

	none, err := st.EventsSince(maxID, 100)
	if err != nil {
		t.Fatalf("events since max: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no rows beyond max id, got %d", len(none))
	}
}

func providersOf(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Provider
	}
	return out
}
