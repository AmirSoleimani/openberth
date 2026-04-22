package store

import "database/sql"

type User struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	APIKey          string `json:"apiKey,omitempty"`
	Role            string `json:"role"`
	MaxDeployments  int    `json:"maxDeployments"`
	DefaultTTLHours int    `json:"defaultTtlHours"`
	DisplayName     string `json:"displayName"`
	PasswordHash    string `json:"-"`
	CreatedAt       string `json:"createdAt"`
}

func (s *Store) GetUserByKey(apiKey string) (*User, error) {
	u := &User{}
	// Preferred path: index lookup by keyed hash. The plaintext column is
	// still populated this release so a downgrade-compatible fallback is
	// available if the hash column is missing or null for a given row.
	hash := s.hashToken(apiKey)
	err := s.db.QueryRow(
		"SELECT id, name, api_key, role, max_deployments, default_ttl_hours, COALESCE(display_name,''), created_at FROM users WHERE api_key_hash = ?",
		hash,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.DisplayName, &u.CreatedAt)
	if err == nil {
		return u, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Fallback: pre-backfill row where api_key_hash is NULL. Opportunistically
	// set the hash on hit so future lookups use the indexed path.
	err = s.db.QueryRow(
		"SELECT id, name, api_key, role, max_deployments, default_ttl_hours, COALESCE(display_name,''), created_at FROM users WHERE api_key = ? AND (api_key_hash IS NULL OR api_key_hash = '')",
		apiKey,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.DisplayName, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.db.Exec("UPDATE users SET api_key_hash = ? WHERE id = ? AND (api_key_hash IS NULL OR api_key_hash = '')", hash, u.ID)
	return u, nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query("SELECT id, name, role, max_deployments, default_ttl_hours, COALESCE(display_name,''), created_at FROM users ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		rows.Scan(&u.ID, &u.Name, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.DisplayName, &u.CreatedAt)
		users = append(users, u)
	}
	return users, nil
}

func (s *Store) CreateUser(u *User) error {
	_, err := s.db.Exec(
		"INSERT INTO users (id, name, api_key, api_key_hash, role, max_deployments, default_ttl_hours, display_name, password_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		u.ID, u.Name, u.APIKey, s.hashToken(u.APIKey), u.Role, u.MaxDeployments, u.DefaultTTLHours, u.DisplayName, u.PasswordHash,
	)
	return err
}

func (s *Store) GetUserByName(name string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		"SELECT id, name, api_key, role, max_deployments, default_ttl_hours, COALESCE(display_name,''), COALESCE(password_hash,''), created_at FROM users WHERE name = ?",
		name,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.DisplayName, &u.PasswordHash, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *Store) GetUserByID(id string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		"SELECT id, name, api_key, role, max_deployments, default_ttl_hours, COALESCE(display_name,''), COALESCE(password_hash,''), created_at FROM users WHERE id = ?",
		id,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.DisplayName, &u.PasswordHash, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *Store) UpdateUserPassword(userID, passwordHash string) error {
	_, err := s.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", passwordHash, userID)
	return err
}

func (s *Store) UpdateUserMaxDeployments(userID string, max int) error {
	_, err := s.db.Exec("UPDATE users SET max_deployments = ? WHERE id = ?", max, userID)
	return err
}

func (s *Store) UpdateUserDisplayName(userID, displayName string) error {
	_, err := s.db.Exec("UPDATE users SET display_name = ? WHERE id = ?", displayName, userID)
	return err
}

func (s *Store) UpdateUserAPIKey(userID, apiKey string) error {
	_, err := s.db.Exec("UPDATE users SET api_key = ?, api_key_hash = ? WHERE id = ?", apiKey, s.hashToken(apiKey), userID)
	return err
}

func (s *Store) CountUsers() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

func (s *Store) DeleteUser(name string) (bool, error) {
	res, err := s.db.Exec("DELETE FROM users WHERE name = ?", name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteUserAuthState removes sessions, OAuth codes, OAuth tokens, and login codes for a user.
// These don't count as "resources" an admin needs to clean up manually before deletion —
// they're ephemeral auth state that should be nuked alongside the user row.
// Sessions have a FK to users, so this must run before the user row is deleted.
// The other three tables have no FK, so they'd orphan otherwise.
func (s *Store) DeleteUserAuthState(userID string) error {
	stmts := []string{
		"DELETE FROM sessions WHERE user_id = ?",
		"DELETE FROM oauth_codes WHERE user_id = ?",
		"DELETE FROM oauth_tokens WHERE user_id = ?",
		"DELETE FROM login_codes WHERE user_id = ?",
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt, userID); err != nil {
			return err
		}
	}
	return nil
}
