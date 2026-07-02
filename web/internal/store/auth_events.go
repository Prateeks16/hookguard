package store

import "database/sql"

// Auth event kinds — DESIGN.md §5.3. Stored as plain strings (not a CHECK
// constraint) since new kinds may be added without a migration.
const (
	AuthEventLoginOK        = "login_ok"
	AuthEventLoginFail      = "login_fail"
	AuthEventLogout         = "logout"
	AuthEventPasswordChange = "pw_change"
	AuthEventSessionRevoke  = "session_revoke"
	AuthEventUserCreate     = "user_create"
)

type AuthEvent struct {
	ID     int64
	At     int64
	UserID *int64
	Email  string
	Kind   string
	IP     string
}

func (s *Store) InsertAuthEvent(e AuthEvent) error {
	_, err := s.db.Exec(
		`INSERT INTO auth_events (at, user_id, email, kind, ip) VALUES (?, ?, ?, ?, ?)`,
		e.At, e.UserID, e.Email, e.Kind, e.IP,
	)
	return err
}

// ListAuthEvents returns the most recent auth events (newest first), capped
// at limit — backs the read-only Settings security log.
func (s *Store) ListAuthEvents(limit int) ([]AuthEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, at, user_id, email, kind, ip FROM auth_events ORDER BY at DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuthEvent
	for rows.Next() {
		var e AuthEvent
		var uid sql.NullInt64
		if err := rows.Scan(&e.ID, &e.At, &uid, &e.Email, &e.Kind, &e.IP); err != nil {
			return nil, err
		}
		if uid.Valid {
			v := uid.Int64
			e.UserID = &v
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
