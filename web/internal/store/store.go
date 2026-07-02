// Package store owns the Console's SQLite database: schema migration and
// CRUD for users, sessions and auth events (endpoints/events land in M3/M4,
// but their tables are created now — see DESIGN.md §8.2, fixed schema
// upfront so later milestones need no migrations).
package store

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed migrations/0001_init.sql
var initSQL string

// Store wraps the console's database handle. Single-writer by design
// (DESIGN.md §8.1): SetMaxOpenConns(1) keeps write semantics simple on
// SQLite instead of juggling SQLITE_BUSY across concurrent writers.
type Store struct {
	db *sql.DB
}

// Open opens (creating if absent) the SQLite file at path in WAL mode and
// applies the embedded schema on first run. DESIGN.md §8.2's schema is
// fixed upfront, so there is only ever this one migration; applied-ness is
// tracked via sqlite_master rather than editing the embedded SQL, which is
// kept verbatim as the doc specifies it.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	db.SetMaxOpenConns(1)

	var exists int
	err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&exists)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("check schema: %w", err)
	}
	if exists == 0 {
		if _, err := db.Exec(initSQL); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
