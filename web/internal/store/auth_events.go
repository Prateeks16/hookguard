package store

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
