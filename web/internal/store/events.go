package store

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// Event mirrors one row of the events table (DESIGN.md §8.2) — one gateway
// verdict per row, as decoded from the ingest JSON contract (§7.3).
type Event struct {
	ID             int64 // events.id, the SSE tail cursor (EventsSince) — 0 on rows not yet read back from the DB
	ReceivedAt     int64 // unix ms, gateway ts
	Path           string
	Provider       string
	Verdict        string
	Reason         string
	UpstreamStatus int
	LatencyMS      int64
	BodyBytes      int
	BodySHA256     string
	RemoteIP       string
}

// InsertEvents batch-inserts a flushed tick's worth of events in one
// transaction — the ingest batcher's whole reason for existing is to turn
// N per-request writes into one write per 100ms tick against the
// single-writer SQLite handle.
func (s *Store) InsertEvents(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO events (received_at, path, provider, verdict, reason, upstream_status, latency_ms, body_bytes, body_sha256, remote_ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range events {
		if _, err := stmt.Exec(e.ReceivedAt, e.Path, e.Provider, e.Verdict, e.Reason, e.UpstreamStatus, e.LatencyMS, e.BodyBytes, e.BodySHA256, e.RemoteIP); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RollupDelta is one (hour, provider, verdict) bucket's increment for a
// flushed batch — callers sum same-bucket events before calling
// UpsertRollups so a batch of 3 same-bucket events becomes one upsert of
// n=3, not three upserts racing each other.
type RollupDelta struct {
	Hour     int64
	Provider string
	Verdict  string
	N        int
}

// UpsertRollups adds each delta's N onto event_rollups (DESIGN.md §8.2:
// composite PK (hour, provider, verdict)), in one transaction per flush.
func (s *Store) UpsertRollups(deltas []RollupDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO event_rollups (hour, provider, verdict, n) VALUES (?, ?, ?, ?)
		 ON CONFLICT (hour, provider, verdict) DO UPDATE SET n = n + excluded.n`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, d := range deltas {
		if _, err := stmt.Exec(d.Hour, d.Provider, d.Verdict, d.N); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RollupCount is a test/debug helper: the n for one (hour, provider,
// verdict) bucket, 0 if the bucket doesn't exist yet.
func (s *Store) RollupCount(hour int64, provider, verdict string) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT n FROM event_rollups WHERE hour = ? AND provider = ? AND verdict = ?`,
		hour, provider, verdict,
	).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// CountEvents is a test/debug helper for asserting nothing was inserted.
func (s *Store) CountEvents() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n)
	return n, err
}

// LatestEvent returns the most recently inserted row — a test/debug helper
// for round-tripping every field of a single ingested event.
func (s *Store) LatestEvent() (*Event, error) {
	e := &Event{}
	err := s.db.QueryRow(
		`SELECT id, received_at, path, provider, verdict, reason, upstream_status, latency_ms, body_bytes, body_sha256, remote_ip
		 FROM events ORDER BY id DESC LIMIT 1`,
	).Scan(&e.ID, &e.ReceivedAt, &e.Path, &e.Provider, &e.Verdict, &e.Reason, &e.UpstreamStatus, &e.LatencyMS, &e.BodyBytes, &e.BodySHA256, &e.RemoteIP)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// Summary is the Overview stat-card data for one window (DESIGN.md §6.2,
// §7.4) — accepted/rejected counts from event_rollups (O(hours), not
// O(events)) plus a p50 latency computed in Go over a bounded sample of
// recent events, since rollups don't carry latency.
type Summary struct {
	Accepted     int
	Rejected     int
	AcceptRate   float64 // 0 when Accepted+Rejected == 0 — see AcceptRate doc
	P50LatencyMS int64
}

// latencySampleLimit bounds the p50 query so it stays O(1)-ish regardless of
// traffic volume; p50 over the most recent 1000 events is an approximation
// of the true 24h p50, not an exact figure, and callers/templates should
// treat it as such.
const latencySampleLimit = 1000

// SummaryWindow sums event_rollups over the last `hours` hourly buckets
// (inclusive of the current partial hour) ending at now, and computes p50
// latency from up to latencySampleLimit of the most recent events within
// that same window. now is passed in (not time.Now()) so tests are
// deterministic.
func (s *Store) SummaryWindow(now time.Time, hours int) (Summary, error) {
	nowHour := now.Unix() / 3600
	startHour := nowHour - int64(hours) + 1

	rows, err := s.db.Query(
		`SELECT verdict, SUM(n) FROM event_rollups WHERE hour >= ? AND hour <= ? GROUP BY verdict`,
		startHour, nowHour,
	)
	if err != nil {
		return Summary{}, err
	}
	defer rows.Close()

	var sum Summary
	for rows.Next() {
		var verdict string
		var n int
		if err := rows.Scan(&verdict, &n); err != nil {
			return Summary{}, err
		}
		switch verdict {
		case "accepted":
			sum.Accepted = n
		case "rejected":
			sum.Rejected = n
		}
	}
	if err := rows.Err(); err != nil {
		return Summary{}, err
	}

	total := sum.Accepted + sum.Rejected
	if total > 0 {
		sum.AcceptRate = float64(sum.Accepted) / float64(total)
	}

	startMS := startHour * 3600 * 1000
	p50, err := s.p50LatencySince(startMS)
	if err != nil {
		return Summary{}, err
	}
	sum.P50LatencyMS = p50

	return sum, nil
}

// p50LatencySince computes the median latency_ms of the most recent
// latencySampleLimit events at or after sinceMS (unix ms). Even-count
// samples take the lower of the two middle values — a documented,
// deterministic tie-break, not the average, since latency_ms is an integer
// and both choices are defensible approximations.
func (s *Store) p50LatencySince(sinceMS int64) (int64, error) {
	rows, err := s.db.Query(
		`SELECT latency_ms FROM events WHERE received_at >= ? ORDER BY received_at DESC LIMIT ?`,
		sinceMS, latencySampleLimit,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var latencies []int64
	for rows.Next() {
		var l int64
		if err := rows.Scan(&l); err != nil {
			return 0, err
		}
		latencies = append(latencies, l)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(latencies) == 0 {
		return 0, nil
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	mid := len(latencies) / 2
	if len(latencies)%2 == 1 {
		return latencies[mid], nil
	}
	return latencies[mid-1], nil
}

// HourlyCounts is one hour bucket's accepted/rejected split for the Overview
// chart (DESIGN.md §6.2).
type HourlyCounts struct {
	Hour     int64 // unix hour bucket, matches event_rollups.hour
	Accepted int
	Rejected int
}

// HourlyCountsWindow returns one HourlyCounts per hour from (now - hours + 1)
// through now inclusive, in ascending hour order, with zero-filled gaps for
// hours that have no rollup rows at all — the chart renderer needs a dense
// series, not a sparse one.
func (s *Store) HourlyCountsWindow(now time.Time, hours int) ([]HourlyCounts, error) {
	nowHour := now.Unix() / 3600
	startHour := nowHour - int64(hours) + 1

	rows, err := s.db.Query(
		`SELECT hour, verdict, SUM(n) FROM event_rollups WHERE hour >= ? AND hour <= ? GROUP BY hour, verdict`,
		startHour, nowHour,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byHour := make(map[int64]*HourlyCounts, hours)
	for rows.Next() {
		var hour int64
		var verdict string
		var n int
		if err := rows.Scan(&hour, &verdict, &n); err != nil {
			return nil, err
		}
		hc, ok := byHour[hour]
		if !ok {
			hc = &HourlyCounts{Hour: hour}
			byHour[hour] = hc
		}
		switch verdict {
		case "accepted":
			hc.Accepted = n
		case "rejected":
			hc.Rejected = n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]HourlyCounts, hours)
	for i := 0; i < hours; i++ {
		hour := startHour + int64(i)
		if hc, ok := byHour[hour]; ok {
			out[i] = *hc
		} else {
			out[i] = HourlyCounts{Hour: hour}
		}
	}
	return out, nil
}

// RecentRejected returns the most recent n rejected events (full row, newest
// first) for the Overview "Recent rejections" table (DESIGN.md §6.2) —
// reasons are first-class, so every field callers need to render the table
// is here.
func (s *Store) RecentRejected(n int) ([]Event, error) {
	rows, err := s.db.Query(
		`SELECT id, received_at, path, provider, verdict, reason, upstream_status, latency_ms, body_bytes, body_sha256, remote_ip
		 FROM events WHERE verdict = 'rejected' ORDER BY received_at DESC LIMIT ?`,
		n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.ReceivedAt, &e.Path, &e.Provider, &e.Verdict, &e.Reason, &e.UpstreamStatus, &e.LatencyMS, &e.BodyBytes, &e.BodySHA256, &e.RemoteIP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HasAnyEvent reports whether the events table has ever had a row inserted —
// drives the Overview empty state (DESIGN.md §6.2), independent of any time
// window (a 24h-old install with only stale events should not see the empty
// state).
func (s *Store) HasAnyEvent() (bool, error) {
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM events LIMIT 1)`).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

// EventFilter narrows ListEvents (DESIGN.md §6.2 "Live Logs" filters,
// querystring-backed). Zero-value fields mean "no filter on this dimension".
// Reason and Path are substring matches (SQL LIKE '%…%') rather than exact —
// reasons are free-ish text ("signature mismatch") and callers filtering the
// live log want "contains" behavior, not a dropdown of exact taxonomy
// strings the UI would have to keep in lockstep with the emitter's.
type EventFilter struct {
	Provider string
	Verdict  string
	Reason   string
	Path     string
	FromMS   int64 // unix ms, 0 = no lower bound
	ToMS     int64 // unix ms, 0 = no upper bound
}

// ListEvents returns up to limit events matching filter, newest first — the
// Live Logs page's list query (DESIGN.md §6.2).
func (s *Store) ListEvents(filter EventFilter, limit int) ([]Event, error) {
	query := `SELECT id, received_at, path, provider, verdict, reason, upstream_status, latency_ms, body_bytes, body_sha256, remote_ip FROM events WHERE 1=1`
	var args []any

	if filter.Provider != "" {
		query += ` AND provider = ?`
		args = append(args, filter.Provider)
	}
	if filter.Verdict != "" {
		query += ` AND verdict = ?`
		args = append(args, filter.Verdict)
	}
	if filter.Reason != "" {
		query += ` AND reason LIKE ? ESCAPE '\'`
		args = append(args, "%"+likeEscape(filter.Reason)+"%")
	}
	if filter.Path != "" {
		query += ` AND path LIKE ? ESCAPE '\'`
		args = append(args, "%"+likeEscape(filter.Path)+"%")
	}
	if filter.FromMS > 0 {
		query += ` AND received_at >= ?`
		args = append(args, filter.FromMS)
	}
	if filter.ToMS > 0 {
		query += ` AND received_at <= ?`
		args = append(args, filter.ToMS)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.ReceivedAt, &e.Path, &e.Provider, &e.Verdict, &e.Reason, &e.UpstreamStatus, &e.LatencyMS, &e.BodyBytes, &e.BodySHA256, &e.RemoteIP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// likeEscape escapes SQL LIKE metacharacters in user-supplied substrings so
// filter values containing '%' or '_' are matched literally, not as wildcards.
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// EventsSince returns up to limit events with id > sinceID, oldest first —
// the SSE tail cursor query (DESIGN.md §6.2 stream). Using the auto-increment
// id (rather than received_at) avoids clock-skew/duplicate-timestamp
// ambiguity between successive polls.
func (s *Store) EventsSince(sinceID int64, limit int) ([]Event, error) {
	rows, err := s.db.Query(
		`SELECT id, received_at, path, provider, verdict, reason, upstream_status, latency_ms, body_bytes, body_sha256, remote_ip
		 FROM events WHERE id > ? ORDER BY id ASC LIMIT ?`,
		sinceID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.ReceivedAt, &e.Path, &e.Provider, &e.Verdict, &e.Reason, &e.UpstreamStatus, &e.LatencyMS, &e.BodyBytes, &e.BodySHA256, &e.RemoteIP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestEventID returns the current max events.id, 0 if the table is empty —
// used by the SSE handler to establish the tail cursor for a fresh connection
// (only stream events from "now" forward, not the whole history).
func (s *Store) LatestEventID() (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(id) FROM events`).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id.Int64, nil
}

// DeleteEventsOlderThan deletes rows from events with received_at < cutoffMS,
// returning the count deleted (DESIGN.md §8.2: the nightly retention job).
// event_rollups is untouched — rollups are the O(hours) aggregate Overview
// and charts read from and are kept for 13 months regardless of retention_days.
func (s *Store) DeleteEventsOlderThan(cutoffMS int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM events WHERE received_at < ?`, cutoffMS)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
