package store

import (
	"database/sql"
	"errors"
)

// GetSetting/SetSetting/DeleteSetting back the instance key/value table
// (DESIGN.md §8.2). Password-reset tokens are stored here too, one row per
// pending reset keyed by "pwreset:<user_id>" — the fixed schema has no
// dedicated table for a single-use, short-lived value like this one.
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}
