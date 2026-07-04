package store

import (
	"database/sql"
	"errors"
	"strconv"
)

// DefaultRetentionDays is the fallback when the settings table has no
// retention_days row yet (a fresh install, DESIGN.md §8.2: "default 30").
const DefaultRetentionDays = 30

const settingRetentionDays = "retention_days"

// GetRetentionDays returns the configured retention window in days, falling
// back to DefaultRetentionDays if unset or unparsable — a corrupt/blank value
// should not disable retention, it should fall back to the documented default.
func (s *Store) GetRetentionDays() (int, error) {
	v, err := s.GetSetting(settingRetentionDays)
	if errors.Is(err, ErrNotFound) {
		return DefaultRetentionDays, nil
	}
	if err != nil {
		return 0, err
	}
	days, err := strconv.Atoi(v)
	if err != nil {
		return DefaultRetentionDays, nil
	}
	return days, nil
}

// SetRetentionDays stores the retention window, same key/value pattern as
// every other settings row.
func (s *Store) SetRetentionDays(days int) error {
	return s.SetSetting(settingRetentionDays, strconv.Itoa(days))
}

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
