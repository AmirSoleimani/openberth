package store

import "database/sql"

type Session struct {
	Token     string
	UserID    string
	CreatedAt string
	ExpiresAt string
}

func (s *Store) CreateSession(token, userID, expiresAt string) error {
	_, err := s.db.Exec(
		"INSERT INTO sessions (token, token_hash, user_id, expires_at) VALUES (?, ?, ?, ?)",
		token, s.hashToken(token), userID, expiresAt,
	)
	return err
}

func (s *Store) GetUserBySession(token string) (*User, error) {
	u := &User{}
	hash := s.hashToken(token)
	err := s.db.QueryRow(`
		SELECT u.id, u.name, u.api_key, u.role, u.max_deployments, u.default_ttl_hours,
		       COALESCE(u.display_name,''), COALESCE(u.password_hash,''), u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > strftime('%Y-%m-%d %H:%M:%S', 'now')`,
		hash,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.DisplayName, &u.PasswordHash, &u.CreatedAt)
	if err == nil {
		return u, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Backward-compat fallback: pre-backfill session row.
	err = s.db.QueryRow(`
		SELECT u.id, u.name, u.api_key, u.role, u.max_deployments, u.default_ttl_hours,
		       COALESCE(u.display_name,''), COALESCE(u.password_hash,''), u.created_at, s.token
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > strftime('%Y-%m-%d %H:%M:%S', 'now') AND (s.token_hash IS NULL OR s.token_hash = '')`,
		token,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.DisplayName, &u.PasswordHash, &u.CreatedAt, new(string))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.db.Exec("UPDATE sessions SET token_hash = ? WHERE token = ? AND (token_hash IS NULL OR token_hash = '')", hash, token)
	return u, nil
}

func (s *Store) DeleteSession(token string) error {
	hash := s.hashToken(token)
	_, err := s.db.Exec("DELETE FROM sessions WHERE token_hash = ? OR token = ?", hash, token)
	return err
}

func (s *Store) DeleteUserSessions(userID string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

func (s *Store) DeleteExpiredSessions() {
	s.db.Exec("DELETE FROM sessions WHERE expires_at <= strftime('%Y-%m-%d %H:%M:%S', 'now')")
}
