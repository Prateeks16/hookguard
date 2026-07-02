package store

import (
	"database/sql"
	"errors"
)

var ErrNotFound = errors.New("not found")

type User struct {
	ID           int64
	Email        string
	PasswordHash string
	Role         string
	Active       bool
	CreatedAt    int64
}

// CountUsers is used to decide whether a new signup becomes the first
// (admin) user — DESIGN.md §5.2.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(email, passwordHash, role string, createdAt int64) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO users (email, password_hash, role, active, created_at) VALUES (?, ?, ?, 1, ?)`,
		email, passwordHash, role, createdAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetUserByEmail(email string) (*User, error) {
	u := &User{}
	var active int
	err := s.db.QueryRow(
		`SELECT id, email, password_hash, role, active, created_at FROM users WHERE email = ? COLLATE NOCASE`,
		email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &active, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Active = active != 0
	return u, nil
}

func (s *Store) GetUserByID(id int64) (*User, error) {
	u := &User{}
	var active int
	err := s.db.QueryRow(
		`SELECT id, email, password_hash, role, active, created_at FROM users WHERE id = ?`,
		id,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &active, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Active = active != 0
	return u, nil
}

func (s *Store) UpdatePasswordHash(userID int64, passwordHash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	return err
}

// ListUsers backs the admin-only Settings user list — DESIGN.md §6.2.
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, email, password_hash, role, active, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		var active int
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &active, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Active = active != 0
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) SetUserActive(userID int64, active bool) error {
	v := 0
	if active {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE users SET active = ? WHERE id = ?`, v, userID)
	return err
}
