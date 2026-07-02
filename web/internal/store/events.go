package store

import (
	"database/sql"
	"errors"
)

// Event mirrors one row of the events table (DESIGN.md §8.2) — one gateway
// verdict per row, as decoded from the ingest JSON contract (§7.3).
type Event struct {
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
		`SELECT received_at, path, provider, verdict, reason, upstream_status, latency_ms, body_bytes, body_sha256, remote_ip
		 FROM events ORDER BY id DESC LIMIT 1`,
	).Scan(&e.ReceivedAt, &e.Path, &e.Provider, &e.Verdict, &e.Reason, &e.UpstreamStatus, &e.LatencyMS, &e.BodyBytes, &e.BodySHA256, &e.RemoteIP)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}
