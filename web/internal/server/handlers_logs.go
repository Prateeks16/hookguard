package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hookguard/web/internal/store"
)

// logsListLimit bounds GET /dashboard/logs' page size — the Live Logs list
// isn't paginated in M4 (DESIGN.md §6.2 doesn't call for it), so a single
// bounded page of the most recent matches is the whole view.
const logsListLimit = 200

// logsStreamPollInterval is how often the SSE handler re-queries the store
// for rows newer than its cursor (DESIGN.md §6.2, "poll the store, don't
// build a pub/sub bus" per the M4d task brief).
const logsStreamPollInterval = time.Second

// logsStreamBatchLimit bounds one EventsSince call inside the SSE ticker —
// generous enough that a Console catching up after a brief stall doesn't
// need multiple ticks, small enough that one tick's JSON payload stays
// reasonable.
const logsStreamBatchLimit = 500

// logRow is a template-ready view of one event for the Live Logs table —
// same rationale as overview's rejectedRow: time formatting happens once in
// Go, not per-render in html/template (no funcmap exists in this codebase).
type logRow struct {
	ID             int64
	Time           string
	Provider       string
	Path           string
	Verdict        string // "accepted" | "rejected"
	Reason         string
	UpstreamStatus int
	LatencyMS      int64
	BodyBytes      int
	BodySHA256     string
	RemoteIP       string
}

func toLogRow(e store.Event) logRow {
	return logRow{
		ID:             e.ID,
		Time:           time.UnixMilli(e.ReceivedAt).UTC().Format("2006-01-02 15:04:05"),
		Provider:       e.Provider,
		Path:           e.Path,
		Verdict:        e.Verdict,
		Reason:         e.Reason,
		UpstreamStatus: e.UpstreamStatus,
		LatencyMS:      e.LatencyMS,
		BodyBytes:      e.BodyBytes,
		BodySHA256:     e.BodySHA256,
		RemoteIP:       e.RemoteIP,
	}
}

func toLogRows(events []store.Event) []logRow {
	rows := make([]logRow, len(events))
	for i, e := range events {
		rows[i] = toLogRow(e)
	}
	return rows
}

// logsFilterForm is the querystring-backed filter state (DESIGN.md §6.2):
// both the values used to query the store and the values re-echoed into the
// filter form's inputs so the current filter is visibly reflected.
type logsFilterForm struct {
	Provider string
	Verdict  string
	Reason   string
	Path     string
	From     string // yyyy-MM-ddTHH:mm, raw querystring value for <input type=datetime-local>
	To       string
}

func parseLogsFilter(r *http.Request) (store.EventFilter, logsFilterForm) {
	q := r.URL.Query()
	form := logsFilterForm{
		Provider: q.Get("provider"),
		Verdict:  q.Get("verdict"),
		Reason:   q.Get("reason"),
		Path:     q.Get("path"),
		From:     q.Get("from"),
		To:       q.Get("to"),
	}
	filter := store.EventFilter{
		Provider: form.Provider,
		Verdict:  form.Verdict,
		Reason:   form.Reason,
		Path:     form.Path,
		FromMS:   parseLocalDateTimeMS(form.From),
		ToMS:     parseLocalDateTimeMS(form.To),
	}
	return filter, form
}

// parseLocalDateTimeMS parses an HTML <input type="datetime-local"> value
// ("2026-07-03T12:00") as UTC and returns unix ms, or 0 if empty/unparsable —
// 0 means "no bound" in EventFilter, so a bad value degrades to "ignored"
// rather than a 400, matching the rest of this handler's tolerant querystring
// parsing (DESIGN.md's "shareable URLs" goal shouldn't 400 on a hand-edited
// or stale link).
func parseLocalDateTimeMS(v string) int64 {
	if v == "" {
		return 0
	}
	t, err := time.Parse("2006-01-02T15:04", v)
	if err != nil {
		return 0
	}
	return t.UTC().UnixMilli()
}

type logsData struct {
	pageData
	Filter    logsFilterForm
	Rows      []logRow
	HasRows   bool
	StreamURL string
}

// streamURLForFilter builds /dashboard/logs/stream?... carrying the current
// page's filters, so a client reconnecting to the SSE endpoint sees only
// matching future rows (DESIGN.md §6.2: "client reconnects to
// /dashboard/logs/stream?provider=... matching the page's active filters").
func streamURLForFilter(form logsFilterForm) string {
	q := url.Values{}
	add := func(key, val string) {
		if val != "" {
			q.Set(key, val)
		}
	}
	add("provider", form.Provider)
	add("verdict", form.Verdict)
	add("reason", form.Reason)
	add("path", form.Path)
	add("from", form.From)
	add("to", form.To)

	if len(q) == 0 {
		return "/dashboard/logs/stream"
	}
	return "/dashboard/logs/stream?" + q.Encode()
}

// handleLogsList is GET /dashboard/logs (DESIGN.md §6.2, §7.4): renders the
// current page of events matching the querystring filters. Filtering is a
// plain GET form (no JS) so the view stays bookmarkable/shareable; the live
// tail on top of this static page is handled client-side by logs.js against
// GET /dashboard/logs/stream.
func (s *Server) handleLogsList(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	sess := sessionFromContext(r)
	connected, lastIngestAt, lastEventAgo := s.dashboardStatus()

	filter, form := parseLogsFilter(r)
	events, err := s.Store.ListEvents(filter, logsListLimit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := logsData{
		pageData: pageData{
			User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "logs",
			Connected: connected, LastIngestAt: lastIngestAt, LastEventAgo: lastEventAgo,
		},
		Filter:    form,
		Rows:      toLogRows(events),
		HasRows:   len(events) > 0,
		StreamURL: streamURLForFilter(form),
	}
	s.render(w, "logs.html", data)
}

// sseEvent is the wire format for one row pushed over
// GET /dashboard/logs/stream — a vanilla JSON object per DESIGN.md §6.2's
// "plain EventSource is simpler and sufficient" guidance (no htmx SSE
// extension is vendored in web/ui/static/js).
type sseEvent struct {
	ID             int64  `json:"id"`
	Time           string `json:"time"`
	Provider       string `json:"provider"`
	Path           string `json:"path"`
	Verdict        string `json:"verdict"`
	Reason         string `json:"reason"`
	UpstreamStatus int    `json:"upstream_status"`
	LatencyMS      int64  `json:"latency_ms"`
	BodyBytes      int    `json:"body_bytes"`
	BodySHA256     string `json:"body_sha256"`
	RemoteIP       string `json:"remote_ip"`
}

func toSSEEvent(row logRow) sseEvent {
	return sseEvent{
		ID: row.ID, Time: row.Time, Provider: row.Provider, Path: row.Path,
		Verdict: row.Verdict, Reason: row.Reason, UpstreamStatus: row.UpstreamStatus,
		LatencyMS: row.LatencyMS, BodyBytes: row.BodyBytes, BodySHA256: row.BodySHA256,
		RemoteIP: row.RemoteIP,
	}
}

// writeSSEEvents encodes one SSE tick's worth of new rows as one
// "data: <json array>\n\n" frame — batching the whole tick into a single
// frame (rather than one frame per row) keeps ordering trivial for the
// client and avoids partial-write interleaving concerns.
func writeSSEEvents(w io.Writer, rows []logRow) error {
	if len(rows) == 0 {
		return nil
	}
	payload := make([]sseEvent, len(rows))
	for i, r := range rows {
		payload[i] = toSSEEvent(r)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

// handleLogsStream is GET /dashboard/logs/stream (DESIGN.md §6.2, §7.4): an
// SSE endpoint that polls the store on a fixed interval rather than building
// a pub/sub bus (the task's explicit simplification). Each connection starts
// its cursor at the current max event id — a fresh tab sees only events from
// "now" forward, matching the live-tail intent (the initial page load's table
// already covers history via ListEvents). The ticker goroutine lives entirely
// inside this handler and stops the moment the client disconnects
// (r.Context().Done()), so there is no per-connection goroutine leak.
func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	filter, _ := parseLogsFilter(r)

	cursor, err := s.Store.LatestEventID()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(logsStreamPollInterval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events, err := s.Store.EventsSince(cursor, logsStreamBatchLimit)
			if err != nil {
				return
			}
			if len(events) == 0 {
				continue
			}
			rows := toLogRows(filterEventsForStream(events, filter))
			if err := writeSSEEvents(w, rows); err != nil {
				return
			}
			flusher.Flush()
			cursor = events[len(events)-1].ID
		}
	}
}

// filterEventsForStream applies the same-dimension filters as ListEvents to
// a batch of newly-arrived events, in Go rather than a second SQL query —
// EventsSince already did the DB round trip for the cursor tail; filtering
// the (small, per-tick) batch in memory avoids a second query per tick.
func filterEventsForStream(events []store.Event, filter store.EventFilter) []store.Event {
	if filter == (store.EventFilter{}) {
		return events
	}
	out := events[:0:0]
	for _, e := range events {
		if filter.Provider != "" && e.Provider != filter.Provider {
			continue
		}
		if filter.Verdict != "" && e.Verdict != filter.Verdict {
			continue
		}
		if filter.Reason != "" && !containsFold(e.Reason, filter.Reason) {
			continue
		}
		if filter.Path != "" && !containsFold(e.Path, filter.Path) {
			continue
		}
		if filter.FromMS > 0 && e.ReceivedAt < filter.FromMS {
			continue
		}
		if filter.ToMS > 0 && e.ReceivedAt > filter.ToMS {
			continue
		}
		out = append(out, e)
	}
	return out
}

func containsFold(haystack, needle string) bool {
	return needle == "" || strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
