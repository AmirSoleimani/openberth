package store

import (
	"database/sql"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

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

type Deployment struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Subdomain   string `json:"subdomain"`
	Framework   string `json:"framework"`
	ContainerID string `json:"containerId"`
	Port        int    `json:"port"`
	Status      string `json:"status"`
	TTLHours    int    `json:"ttlHours"`
	EnvJSON     string `json:"envJson"`
	CreatedAt   string `json:"createdAt"`
	ExpiresAt   string `json:"expiresAt"`
	AccessMode  string `json:"accessMode"`
	AccessUser  string `json:"accessUser"`
	AccessHash  string `json:"-"`
	AccessUsers string `json:"accessUsers,omitempty"`
	Mode         string `json:"mode"` // "deploy" or "sandbox"
	NetworkQuota string `json:"networkQuota,omitempty"`
	Memory       string `json:"memory,omitempty"`
	CPUs         string `json:"cpus,omitempty"`
	Locked       bool   `json:"locked"`
}

type Store struct {
	db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.ensureSchema(); err != nil {
		return nil, err
	}
	return s, nil
}

// ensureSchema creates all tables and runs idempotent migrations.
// Called on initial open and after restore to ensure the schema is up to date.
func (s *Store) ensureSchema() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			api_key TEXT UNIQUE NOT NULL,
			role TEXT DEFAULT 'user',
			max_deployments INTEGER DEFAULT 10,
			default_ttl_hours INTEGER DEFAULT 72,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS deployments (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			name TEXT NOT NULL,
			subdomain TEXT UNIQUE NOT NULL,
			framework TEXT,
			container_id TEXT,
			port INTEGER,
			status TEXT DEFAULT 'building',
			ttl_hours INTEGER,
			env_json TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME,
			FOREIGN KEY (user_id) REFERENCES users(id)
		);
		CREATE TABLE IF NOT EXISTS oauth_clients (
			client_id TEXT PRIMARY KEY,
			client_secret TEXT DEFAULT '',
			redirect_uris TEXT DEFAULT '[]',
			client_name TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS oauth_codes (
			code TEXT PRIMARY KEY,
			client_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			redirect_uri TEXT NOT NULL,
			code_challenge TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			used INTEGER DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS oauth_tokens (
			token TEXT PRIMARY KEY,
			token_type TEXT NOT NULL,
			client_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME NOT NULL,
			FOREIGN KEY (user_id) REFERENCES users(id)
		);
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS login_codes (
			code TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			callback_url TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			used INTEGER DEFAULT 0
		);
	`)
	if err != nil {
		return err
	}

	// Migrations (idempotent — fails silently if columns already exist)
	for _, stmt := range []string{
		"ALTER TABLE deployments ADD COLUMN access_mode TEXT DEFAULT 'public'",
		"ALTER TABLE deployments ADD COLUMN access_user TEXT DEFAULT ''",
		"ALTER TABLE deployments ADD COLUMN access_hash TEXT DEFAULT ''",
		"ALTER TABLE deployments ADD COLUMN title TEXT DEFAULT ''",
		"ALTER TABLE deployments ADD COLUMN description TEXT DEFAULT ''",
		"ALTER TABLE users ADD COLUMN display_name TEXT DEFAULT ''",
		"ALTER TABLE users ADD COLUMN password_hash TEXT DEFAULT ''",
		"ALTER TABLE deployments ADD COLUMN mode TEXT DEFAULT 'deploy'",
		"ALTER TABLE deployments ADD COLUMN network_quota TEXT DEFAULT ''",
		"ALTER TABLE deployments ADD COLUMN access_users TEXT DEFAULT ''",
		"ALTER TABLE deployments ADD COLUMN locked INTEGER DEFAULT 0",
		"ALTER TABLE deployments ADD COLUMN memory TEXT DEFAULT ''",
		"ALTER TABLE deployments ADD COLUMN cpus TEXT DEFAULT ''",
	} {
		s.db.Exec(stmt)
	}

	s.db.Exec(`CREATE TABLE IF NOT EXISTS bandwidth_usage (
		deployment_id TEXT NOT NULL,
		period_start TEXT NOT NULL,
		bytes_out INTEGER DEFAULT 0,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (deployment_id, period_start)
	)`)

	return nil
}

func (s *Store) Close() {
	s.db.Close()
}

// Checkpoint flushes the WAL into the main database file.
// Call this before reading the DB file for backup.
func (s *Store) Checkpoint() error {
	_, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// Reopen closes the current database connection and opens a new one.
// Used after restoring a backup to pick up the new database file.
// Removes stale WAL/SHM files to prevent corruption when the DB file
// has been replaced, and runs schema migrations for compatibility.
func (s *Store) Reopen(dbPath string) error {
	s.db.Close()

	// Remove stale WAL/SHM files left from the previous DB.
	// After a restore, these belong to the OLD database and would corrupt
	// the newly restored one if SQLite tried to replay them.
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return err
	}
	s.db = db
	return s.ensureSchema()
}

// ── Users ──────────────────────────────────────────────────────────────

func (s *Store) GetUserByKey(apiKey string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		"SELECT id, name, api_key, role, max_deployments, default_ttl_hours, COALESCE(display_name,''), created_at FROM users WHERE api_key = ?",
		apiKey,
	).Scan(&u.ID, &u.Name, &u.APIKey, &u.Role, &u.MaxDeployments, &u.DefaultTTLHours, &u.DisplayName, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
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
		"INSERT INTO users (id, name, api_key, role, max_deployments, default_ttl_hours, display_name, password_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		u.ID, u.Name, u.APIKey, u.Role, u.MaxDeployments, u.DefaultTTLHours, u.DisplayName, u.PasswordHash,
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

// ── Deployments ────────────────────────────────────────────────────────

func (s *Store) CreateDeployment(d *Deployment) error {
	mode := d.Mode
	if mode == "" {
		mode = "deploy"
	}
	_, err := s.db.Exec(`
		INSERT INTO deployments (id, user_id, name, subdomain, framework, container_id, port, status, ttl_hours, env_json, expires_at, title, description, mode, network_quota, memory, cpus)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.UserID, d.Name, d.Subdomain, d.Framework, d.ContainerID, d.Port, d.Status, d.TTLHours, d.EnvJSON, d.ExpiresAt, d.Title, d.Description, mode, d.NetworkQuota, d.Memory, d.CPUs,
	)
	return err
}

func (s *Store) UpdateDeploymentStatus(id, status string) error {
	_, err := s.db.Exec("UPDATE deployments SET status = ? WHERE id = ?", status, id)
	return err
}

func (s *Store) UpdateDeploymentRunning(id, containerID string, port int) error {
	_, err := s.db.Exec(
		"UPDATE deployments SET container_id = ?, port = ?, status = 'running' WHERE id = ?",
		containerID, port, id,
	)
	return err
}

func (s *Store) GetDeployment(id string) (*Deployment, error) {
	d := &Deployment{}
	var lockedInt int
	err := s.db.QueryRow(
		"SELECT id, user_id, name, subdomain, framework, COALESCE(container_id,''), COALESCE(port,0), status, ttl_hours, COALESCE(env_json,'{}'), created_at, COALESCE(expires_at,''), COALESCE(access_mode,'public'), COALESCE(access_user,''), COALESCE(access_hash,''), COALESCE(title,''), COALESCE(description,''), COALESCE(mode,'deploy'), COALESCE(network_quota,''), COALESCE(access_users,''), COALESCE(memory,''), COALESCE(cpus,''), COALESCE(locked,0) FROM deployments WHERE id = ?",
		id,
	).Scan(&d.ID, &d.UserID, &d.Name, &d.Subdomain, &d.Framework, &d.ContainerID, &d.Port, &d.Status, &d.TTLHours, &d.EnvJSON, &d.CreatedAt, &d.ExpiresAt, &d.AccessMode, &d.AccessUser, &d.AccessHash, &d.Title, &d.Description, &d.Mode, &d.NetworkQuota, &d.AccessUsers, &d.Memory, &d.CPUs, &lockedInt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	d.Locked = lockedInt != 0
	return d, err
}

func (s *Store) GetDeploymentBySubdomain(subdomain string) (*Deployment, error) {
	d := &Deployment{}
	var lockedInt int
	err := s.db.QueryRow(
		"SELECT id, user_id, name, subdomain, framework, COALESCE(container_id,''), COALESCE(port,0), status, ttl_hours, COALESCE(env_json,'{}'), created_at, COALESCE(expires_at,''), COALESCE(access_mode,'public'), COALESCE(access_user,''), COALESCE(access_hash,''), COALESCE(title,''), COALESCE(description,''), COALESCE(mode,'deploy'), COALESCE(network_quota,''), COALESCE(access_users,''), COALESCE(memory,''), COALESCE(cpus,''), COALESCE(locked,0) FROM deployments WHERE subdomain = ?",
		subdomain,
	).Scan(&d.ID, &d.UserID, &d.Name, &d.Subdomain, &d.Framework, &d.ContainerID, &d.Port, &d.Status, &d.TTLHours, &d.EnvJSON, &d.CreatedAt, &d.ExpiresAt, &d.AccessMode, &d.AccessUser, &d.AccessHash, &d.Title, &d.Description, &d.Mode, &d.NetworkQuota, &d.AccessUsers, &d.Memory, &d.CPUs, &lockedInt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	d.Locked = lockedInt != 0
	return d, err
}

func (s *Store) ListDeployments(userID string) ([]Deployment, error) {
	var rows *sql.Rows
	var err error

	query := "SELECT id, user_id, name, subdomain, framework, COALESCE(container_id,''), COALESCE(port,0), status, ttl_hours, COALESCE(env_json,'{}'), created_at, COALESCE(expires_at,''), COALESCE(access_mode,'public'), COALESCE(access_user,''), COALESCE(access_hash,''), COALESCE(title,''), COALESCE(description,''), COALESCE(mode,'deploy'), COALESCE(network_quota,''), COALESCE(access_users,''), COALESCE(memory,''), COALESCE(cpus,''), COALESCE(locked,0) FROM deployments WHERE status != 'destroyed' "
	if userID != "" {
		rows, err = s.db.Query(query+"AND user_id = ? ORDER BY created_at DESC", userID)
	} else {
		rows, err = s.db.Query(query + "ORDER BY created_at DESC")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deploys []Deployment
	for rows.Next() {
		var d Deployment
		var lockedInt int
		rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Subdomain, &d.Framework, &d.ContainerID, &d.Port, &d.Status, &d.TTLHours, &d.EnvJSON, &d.CreatedAt, &d.ExpiresAt, &d.AccessMode, &d.AccessUser, &d.AccessHash, &d.Title, &d.Description, &d.Mode, &d.NetworkQuota, &d.AccessUsers, &d.Memory, &d.CPUs, &lockedInt)
		d.Locked = lockedInt != 0
		deploys = append(deploys, d)
	}
	return deploys, nil
}

// PublicDeployment holds a deployment with its owner's display name for gallery display.
type PublicDeployment struct {
	Deployment
	OwnerName string `json:"ownerName"`
}

func (s *Store) ListPublicDeployments() ([]PublicDeployment, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.user_id, d.name, d.subdomain, d.framework, COALESCE(d.container_id,''), COALESCE(d.port,0),
		       d.status, d.ttl_hours, d.created_at, COALESCE(d.expires_at,''), COALESCE(d.access_mode,'public'),
		       COALESCE(d.access_user,''), COALESCE(d.title,''), COALESCE(d.description,''),
		       COALESCE(NULLIF(u.display_name, ''), u.name), COALESCE(d.mode,'deploy'), COALESCE(d.network_quota,''),
		       COALESCE(d.access_users,''), COALESCE(d.locked,0)
		FROM deployments d
		LEFT JOIN users u ON d.user_id = u.id
		WHERE d.status != 'destroyed'
		ORDER BY d.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deploys []PublicDeployment
	for rows.Next() {
		var p PublicDeployment
		var lockedInt int
		rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Subdomain, &p.Framework, &p.ContainerID, &p.Port,
			&p.Status, &p.TTLHours, &p.CreatedAt, &p.ExpiresAt, &p.AccessMode,
			&p.AccessUser, &p.Title, &p.Description, &p.OwnerName, &p.Mode, &p.NetworkQuota,
			&p.AccessUsers, &lockedInt)
		p.Locked = lockedInt != 0
		deploys = append(deploys, p)
	}
	return deploys, nil
}

func (s *Store) CountActiveDeployments(userID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM deployments WHERE user_id = ? AND status != 'destroyed'",
		userID,
	).Scan(&count)
	return count, err
}

func (s *Store) DeleteDeployment(id string) error {
	_, err := s.db.Exec("DELETE FROM deployments WHERE id = ?", id)
	return err
}

func (s *Store) UpdateDeploymentAccess(id, accessMode, accessUser, accessHash, accessUsers string) error {
	_, err := s.db.Exec(
		"UPDATE deployments SET access_mode = ?, access_user = ?, access_hash = ?, access_users = ? WHERE id = ?",
		accessMode, accessUser, accessHash, accessUsers, id,
	)
	return err
}

func (s *Store) UpdateDeploymentMeta(id, title, description string) error {
	_, err := s.db.Exec(
		"UPDATE deployments SET title = ?, description = ? WHERE id = ?",
		title, description, id,
	)
	return err
}

func (s *Store) UpdateDeploymentMode(id, mode string) error {
	_, err := s.db.Exec("UPDATE deployments SET mode = ? WHERE id = ?", mode, id)
	return err
}

func (s *Store) UpdateDeploymentSubdomain(id, subdomain string) error {
	_, err := s.db.Exec("UPDATE deployments SET subdomain = ? WHERE id = ?", subdomain, id)
	return err
}

func (s *Store) UpdateDeploymentLocked(id string, locked bool) error {
	v := 0
	if locked {
		v = 1
	}
	_, err := s.db.Exec("UPDATE deployments SET locked = ? WHERE id = ?", v, id)
	return err
}

func (s *Store) UpdateDeploymentNetworkQuota(id, networkQuota string) error {
	_, err := s.db.Exec("UPDATE deployments SET network_quota = ? WHERE id = ?", networkQuota, id)
	return err
}

func (s *Store) UpdateDeploymentEnvJSON(id, envJSON string) error {
	_, err := s.db.Exec("UPDATE deployments SET env_json = ? WHERE id = ?", envJSON, id)
	return err
}

func (s *Store) UpdateDeploymentTTL(id string, ttlHours int, expiresAt string) error {
	_, err := s.db.Exec(
		"UPDATE deployments SET ttl_hours = ?, expires_at = ? WHERE id = ?",
		ttlHours, expiresAt, id,
	)
	return err
}

func (s *Store) ListDeploymentsByStatus(statuses ...string) ([]Deployment, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]interface{}, len(statuses))
	for i, st := range statuses {
		placeholders[i] = "?"
		args[i] = st
	}
	query := "SELECT id, user_id, name, subdomain, framework, COALESCE(container_id,''), COALESCE(port,0), status, COALESCE(access_mode,'public'), COALESCE(access_user,''), COALESCE(access_hash,''), COALESCE(mode,'deploy'), COALESCE(network_quota,''), COALESCE(access_users,''), COALESCE(memory,''), COALESCE(cpus,'') FROM deployments WHERE status IN (" + strings.Join(placeholders, ",") + ")"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deploys []Deployment
	for rows.Next() {
		var d Deployment
		rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Subdomain, &d.Framework, &d.ContainerID, &d.Port, &d.Status, &d.AccessMode, &d.AccessUser, &d.AccessHash, &d.Mode, &d.NetworkQuota, &d.AccessUsers, &d.Memory, &d.CPUs)
		deploys = append(deploys, d)
	}
	return deploys, nil
}

func (s *Store) GetExpiredDeployments() ([]Deployment, error) {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	rows, err := s.db.Query(
		"SELECT id, user_id, name, subdomain, framework, COALESCE(container_id,''), COALESCE(port,0), status, COALESCE(mode,'deploy'), COALESCE(network_quota,'') FROM deployments WHERE expires_at IS NOT NULL AND expires_at != '' AND expires_at <= ? AND status != 'destroyed'",
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deploys []Deployment
	for rows.Next() {
		var d Deployment
		rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Subdomain, &d.Framework, &d.ContainerID, &d.Port, &d.Status, &d.Mode, &d.NetworkQuota)
		deploys = append(deploys, d)
	}
	return deploys, nil
}
