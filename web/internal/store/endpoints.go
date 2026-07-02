package store

import (
	"database/sql"
	"errors"
)

type Endpoint struct {
	ID           int64
	Path         string
	Provider     string
	UpstreamURL  string
	ReplayWindow string
	SecretEnv    string
	WebhookID    string
	Active       bool
	CreatedAt    int64
	UpdatedAt    int64
}

func (s *Store) CreateEndpoint(e Endpoint) (int64, error) {
	active := 0
	if e.Active {
		active = 1
	}
	res, err := s.db.Exec(
		`INSERT INTO endpoints (path, provider, upstream_url, replay_window, secret_env, webhook_id, active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Path, e.Provider, e.UpstreamURL, e.ReplayWindow, e.SecretEnv, e.WebhookID, active, e.CreatedAt, e.UpdatedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetEndpointByID(id int64) (*Endpoint, error) {
	return s.scanEndpointRow(s.db.QueryRow(
		`SELECT id, path, provider, upstream_url, replay_window, secret_env, webhook_id, active, created_at, updated_at
		 FROM endpoints WHERE id = ?`, id,
	))
}

func (s *Store) GetEndpointByPath(path string) (*Endpoint, error) {
	return s.scanEndpointRow(s.db.QueryRow(
		`SELECT id, path, provider, upstream_url, replay_window, secret_env, webhook_id, active, created_at, updated_at
		 FROM endpoints WHERE path = ?`, path,
	))
}

func (s *Store) scanEndpointRow(row *sql.Row) (*Endpoint, error) {
	e := &Endpoint{}
	var active int
	err := row.Scan(&e.ID, &e.Path, &e.Provider, &e.UpstreamURL, &e.ReplayWindow, &e.SecretEnv, &e.WebhookID, &active, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.Active = active != 0
	return e, nil
}

// ListEndpoints returns every endpoint ordered by path — the same order the
// config export uses (DESIGN.md §8.2 export note).
func (s *Store) ListEndpoints() ([]Endpoint, error) {
	rows, err := s.db.Query(
		`SELECT id, path, provider, upstream_url, replay_window, secret_env, webhook_id, active, created_at, updated_at
		 FROM endpoints ORDER BY path`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Endpoint
	for rows.Next() {
		var e Endpoint
		var active int
		if err := rows.Scan(&e.ID, &e.Path, &e.Provider, &e.UpstreamURL, &e.ReplayWindow, &e.SecretEnv, &e.WebhookID, &active, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Active = active != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListActiveEndpoints backs the config export (DESIGN.md §8.2: "SELECT …
// FROM endpoints WHERE active=1 ORDER BY path").
func (s *Store) ListActiveEndpoints() ([]Endpoint, error) {
	rows, err := s.db.Query(
		`SELECT id, path, provider, upstream_url, replay_window, secret_env, webhook_id, active, created_at, updated_at
		 FROM endpoints WHERE active = 1 ORDER BY path`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Endpoint
	for rows.Next() {
		var e Endpoint
		var active int
		if err := rows.Scan(&e.ID, &e.Path, &e.Provider, &e.UpstreamURL, &e.ReplayWindow, &e.SecretEnv, &e.WebhookID, &active, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Active = active != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) UpdateEndpoint(e Endpoint) error {
	_, err := s.db.Exec(
		`UPDATE endpoints SET path = ?, provider = ?, upstream_url = ?, replay_window = ?, secret_env = ?, webhook_id = ?, updated_at = ?
		 WHERE id = ?`,
		e.Path, e.Provider, e.UpstreamURL, e.ReplayWindow, e.SecretEnv, e.WebhookID, e.UpdatedAt, e.ID,
	)
	return err
}

func (s *Store) SetEndpointActive(id int64, active bool, updatedAt int64) error {
	v := 0
	if active {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE endpoints SET active = ?, updated_at = ? WHERE id = ?`, v, updatedAt, id)
	return err
}

func (s *Store) DeleteEndpoint(id int64) error {
	_, err := s.db.Exec(`DELETE FROM endpoints WHERE id = ?`, id)
	return err
}
