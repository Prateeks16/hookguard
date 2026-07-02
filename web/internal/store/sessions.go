package store

import (
	"database/sql"
	"errors"
)

type Session struct {
	ID         int64
	TokenHash  []byte
	UserID     int64
	CSRFToken  string
	CreatedAt  int64
	LastSeenAt int64
	ExpiresAt  int64
	IP         string
	UserAgent  string
}

func (s *Store) CreateSession(sess Session) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO sessions (token_hash, user_id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.TokenHash, sess.UserID, sess.CSRFToken, sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt, sess.IP, sess.UserAgent,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetSessionByTokenHash(tokenHash []byte) (*Session, error) {
	sess := &Session{}
	var ip, ua sql.NullString
	err := s.db.QueryRow(
		`SELECT id, token_hash, user_id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent
		 FROM sessions WHERE token_hash = ?`,
		tokenHash,
	).Scan(&sess.ID, &sess.TokenHash, &sess.UserID, &sess.CSRFToken, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &ip, &ua)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.IP, sess.UserAgent = ip.String, ua.String
	return sess, nil
}

func (s *Store) TouchSession(id, lastSeenAt int64) error {
	_, err := s.db.Exec(`UPDATE sessions SET last_seen_at = ? WHERE id = ?`, lastSeenAt, id)
	return err
}

func (s *Store) DeleteSession(id int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteSessionByTokenHash(tokenHash []byte) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteSessionsForUserExcept revokes every session for userID other than
// keepID — backs the Settings "revoke all others" action.
func (s *Store) DeleteSessionsForUserExcept(userID, keepID int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND id != ?`, userID, keepID)
	return err
}

func (s *Store) ListSessionsForUser(userID int64) ([]Session, error) {
	rows, err := s.db.Query(
		`SELECT id, token_hash, user_id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent
		 FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var sess Session
		var ip, ua sql.NullString
		if err := rows.Scan(&sess.ID, &sess.TokenHash, &sess.UserID, &sess.CSRFToken, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &ip, &ua); err != nil {
			return nil, err
		}
		sess.IP, sess.UserAgent = ip.String, ua.String
		out = append(out, sess)
	}
	return out, rows.Err()
}
