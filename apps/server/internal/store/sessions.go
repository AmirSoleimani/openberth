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
		"INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)",
		token, userID, expiresAt,
	)
	return err
}

func (s *Store) GetUserBySession(token string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(`
		SELECT u.id, u.name, u.api_key, u.role, u.max_deployments, u.default_ttl_hours,
		       COALESCE(u.display_name,''), COALESCE(u.password_hash,''), u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > strftime('%Y-%m-%d %H:%M:%S', 'now')`,
		token,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.DisplayName, &u.PasswordHash, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE token = ?", token)
	return err
}

func (s *Store) DeleteUserSessions(userID string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

func (s *Store) DeleteExpiredSessions() {
	s.db.Exec("DELETE FROM sessions WHERE expires_at <= strftime('%Y-%m-%d %H:%M:%S', 'now')")
}
