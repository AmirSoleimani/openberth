package store

import (
	"database/sql"
	"encoding/json"
	"log"
	"time"
)

type OAuthClient struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name"`
	CreatedAt    string   `json:"created_at,omitempty"`
}

type OAuthCode struct {
	Code          string
	ClientID      string
	UserID        string
	RedirectURI   string
	CodeChallenge string
	Used          bool
	Expired       bool // computed via SQL: expires_at <= datetime('now')
}

type OAuthToken struct {
	Token     string
	TokenType string // "access" or "refresh"
	ClientID  string
	UserID    string
	ExpiresAt time.Time // used when creating tokens
	Expired   bool      // computed via SQL when reading
}

// ── OAuth Clients ─────────────────────────────────────────────────

func (s *Store) CreateOAuthClient(c *OAuthClient) error {
	uris, _ := json.Marshal(c.RedirectURIs)
	_, err := s.db.Exec(
		"INSERT INTO oauth_clients (client_id, client_secret, redirect_uris, client_name) VALUES (?, ?, ?, ?)",
		c.ClientID, c.ClientSecret, string(uris), c.ClientName,
	)
	return err
}

func (s *Store) GetOAuthClient(clientID string) (*OAuthClient, error) {
	c := &OAuthClient{}
	var urisJSON string
	err := s.db.QueryRow(
		"SELECT client_id, client_secret, redirect_uris, client_name FROM oauth_clients WHERE client_id = ?",
		clientID,
	).Scan(&c.ClientID, &c.ClientSecret, &urisJSON, &c.ClientName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(urisJSON), &c.RedirectURIs)
	return c, nil
}

// ── OAuth Codes ───────────────────────────────────────────────────

func (s *Store) CreateOAuthCode(c *OAuthCode) error {
	expiresAt := time.Now().Add(1 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	log.Printf("[oauth] Creating auth code: expires_at=%s datetime_now=%s",
		expiresAt, time.Now().UTC().Format("2006-01-02 15:04:05"))
	_, err := s.db.Exec(
		"INSERT INTO oauth_codes (code, client_id, user_id, redirect_uri, code_challenge, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		c.Code, c.ClientID, c.UserID, c.RedirectURI, c.CodeChallenge, expiresAt,
	)
	return err
}

// GetOAuthCode retrieves a code and checks expiry using SQL datetime('now').
// No Go time parsing needed — the Expired field is computed by SQLite.
func (s *Store) GetOAuthCode(code string) (*OAuthCode, error) {
	c := &OAuthCode{}
	var used, expired int
	err := s.db.QueryRow(`
		SELECT code, client_id, user_id, redirect_uri, code_challenge, used,
			CASE WHEN expires_at <= strftime('%Y-%m-%d %H:%M:%S', 'now') THEN 1 ELSE 0 END
		FROM oauth_codes WHERE code = ?`,
		code,
	).Scan(&c.Code, &c.ClientID, &c.UserID, &c.RedirectURI, &c.CodeChallenge, &used, &expired)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Used = used != 0
	c.Expired = expired != 0

	// Debug logging
	var storedExpires string
	s.db.QueryRow("SELECT expires_at FROM oauth_codes WHERE code = ?", code).Scan(&storedExpires)
	var sqliteNow string
	s.db.QueryRow("SELECT strftime('%Y-%m-%d %H:%M:%S', 'now')").Scan(&sqliteNow)
	log.Printf("[oauth] Code lookup: stored_expires=%q sqlite_now=%q used=%d expired=%d",
		storedExpires, sqliteNow, used, expired)

	return c, nil
}

func (s *Store) MarkOAuthCodeUsed(code string) error {
	_, err := s.db.Exec("UPDATE oauth_codes SET used = 1 WHERE code = ?", code)
	return err
}

// ── OAuth Tokens ──────────────────────────────────────────────────

func (s *Store) CreateOAuthToken(t *OAuthToken) error {
	expiresAt := t.ExpiresAt.UTC().Format("2006-01-02 15:04:05")
	_, err := s.db.Exec(
		"INSERT INTO oauth_tokens (token, token_hash, token_type, client_id, user_id, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		t.Token, s.hashToken(t.Token), t.TokenType, t.ClientID, t.UserID, expiresAt,
	)
	return err
}

func (s *Store) GetOAuthToken(token string) (*OAuthToken, error) {
	t := &OAuthToken{}
	var expired int
	hash := s.hashToken(token)
	err := s.db.QueryRow(`
		SELECT token, token_type, client_id, user_id,
			CASE WHEN expires_at <= strftime('%Y-%m-%d %H:%M:%S', 'now') THEN 1 ELSE 0 END
		FROM oauth_tokens WHERE token_hash = ?`,
		hash,
	).Scan(&t.Token, &t.TokenType, &t.ClientID, &t.UserID, &expired)
	if err == nil {
		t.Expired = expired != 0
		return t, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Backward-compat fallback: pre-backfill row.
	err = s.db.QueryRow(`
		SELECT token, token_type, client_id, user_id,
			CASE WHEN expires_at <= strftime('%Y-%m-%d %H:%M:%S', 'now') THEN 1 ELSE 0 END
		FROM oauth_tokens WHERE token = ? AND (token_hash IS NULL OR token_hash = '')`,
		token,
	).Scan(&t.Token, &t.TokenType, &t.ClientID, &t.UserID, &expired)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Expired = expired != 0
	s.db.Exec("UPDATE oauth_tokens SET token_hash = ? WHERE token = ? AND (token_hash IS NULL OR token_hash = '')", hash, token)
	return t, nil
}

// GetUserByOAuthToken joins oauth_tokens + users, checks expiry in SQL.
func (s *Store) GetUserByOAuthToken(token string) (*User, error) {
	u := &User{}
	var expired int
	hash := s.hashToken(token)
	err := s.db.QueryRow(`
		SELECT u.id, u.name, u.api_key, u.role, u.max_deployments, u.default_ttl_hours, u.created_at,
			CASE WHEN t.expires_at <= strftime('%Y-%m-%d %H:%M:%S', 'now') THEN 1 ELSE 0 END
		FROM oauth_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ? AND t.token_type = 'access'`,
		hash,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.CreatedAt, &expired)
	if err == nil {
		if expired != 0 {
			return nil, nil
		}
		return u, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Backward-compat fallback.
	err = s.db.QueryRow(`
		SELECT u.id, u.name, u.api_key, u.role, u.max_deployments, u.default_ttl_hours, u.created_at,
			CASE WHEN t.expires_at <= strftime('%Y-%m-%d %H:%M:%S', 'now') THEN 1 ELSE 0 END
		FROM oauth_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token = ? AND t.token_type = 'access' AND (t.token_hash IS NULL OR t.token_hash = '')`,
		token,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.CreatedAt, &expired)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if expired != 0 {
		return nil, nil
	}
	s.db.Exec("UPDATE oauth_tokens SET token_hash = ? WHERE token = ? AND (token_hash IS NULL OR token_hash = '')", hash, token)
	return u, nil
}

func (s *Store) DeleteExpiredOAuthData() {
	s.db.Exec("DELETE FROM oauth_codes WHERE expires_at <= strftime('%Y-%m-%d %H:%M:%S', 'now') OR used = 1")
	s.db.Exec("DELETE FROM oauth_tokens WHERE expires_at <= strftime('%Y-%m-%d %H:%M:%S', 'now')")
}
