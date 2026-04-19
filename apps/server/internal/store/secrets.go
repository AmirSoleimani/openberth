package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type Secret struct {
	ID           int64
	UserID       *string
	Scope        string
	Name         string
	Description  string
	EncryptedDEK []byte
	DEKNonce     []byte
	Ciphertext   []byte
	ValueNonce   []byte
	CreatedBy    *string // user ID of the original creator; NULL for pre-migration rows (legacy globals — admin-only)
	CreatedAt    string
	UpdatedAt    string
}

type SecretMeta struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// SetSecret upserts a secret. userID is nil for global scope. createdBy is the
// user ID performing the creation; it is only written on INSERT and never
// overwritten on UPDATE (so the original creator is preserved across rotations).
func (s *Store) SetSecret(userID *string, scope, name, description, createdBy string, encDEK, dekNonce, ciphertext, valNonce []byte) error {
	_, err := s.db.Exec(`
		INSERT INTO secrets (user_id, scope, name, description, created_by, encrypted_dek, dek_nonce, ciphertext, value_nonce, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, name) DO UPDATE SET
			description = excluded.description,
			encrypted_dek = excluded.encrypted_dek,
			dek_nonce = excluded.dek_nonce,
			ciphertext = excluded.ciphertext,
			value_nonce = excluded.value_nonce,
			updated_at = CURRENT_TIMESTAMP`,
		userID, scope, name, description, createdBy, encDEK, dekNonce, ciphertext, valNonce,
	)
	return err
}

// GetSecret returns a secret by name for a user, preferring user-scoped over global.
func (s *Store) GetSecret(userID string, name string) (*Secret, error) {
	sec := &Secret{}
	// Try user-scoped first
	err := s.db.QueryRow(`
		SELECT id, user_id, scope, name, description, encrypted_dek, dek_nonce, ciphertext, value_nonce, created_by, created_at, updated_at
		FROM secrets WHERE user_id = ? AND name = ?`,
		userID, name,
	).Scan(&sec.ID, &sec.UserID, &sec.Scope, &sec.Name, &sec.Description,
		&sec.EncryptedDEK, &sec.DEKNonce, &sec.Ciphertext, &sec.ValueNonce,
		&sec.CreatedBy, &sec.CreatedAt, &sec.UpdatedAt)
	if err == nil {
		return sec, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	// Fall back to global
	err = s.db.QueryRow(`
		SELECT id, user_id, scope, name, description, encrypted_dek, dek_nonce, ciphertext, value_nonce, created_by, created_at, updated_at
		FROM secrets WHERE user_id IS NULL AND name = ?`,
		name,
	).Scan(&sec.ID, &sec.UserID, &sec.Scope, &sec.Name, &sec.Description,
		&sec.EncryptedDEK, &sec.DEKNonce, &sec.Ciphertext, &sec.ValueNonce,
		&sec.CreatedBy, &sec.CreatedAt, &sec.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sec, err
}

// CountUserSecrets returns the number of user-scoped secrets owned by a user.
func (s *Store) CountUserSecrets(userID string) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM secrets WHERE user_id = ?", userID).Scan(&count)
	return count, err
}

// CountGlobalsCreatedBy returns the number of global secrets originally created by a user.
func (s *Store) CountGlobalsCreatedBy(userID string) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM secrets WHERE user_id IS NULL AND created_by = ?", userID).Scan(&count)
	return count, err
}

// GetGlobalSecret returns the global secret with the given name, or nil if none.
// Distinct from GetSecret, which prefers user-scoped over global when both exist.
func (s *Store) GetGlobalSecret(name string) (*Secret, error) {
	sec := &Secret{}
	err := s.db.QueryRow(`
		SELECT id, user_id, scope, name, description, encrypted_dek, dek_nonce, ciphertext, value_nonce, created_by, created_at, updated_at
		FROM secrets WHERE user_id IS NULL AND name = ?`,
		name,
	).Scan(&sec.ID, &sec.UserID, &sec.Scope, &sec.Name, &sec.Description,
		&sec.EncryptedDEK, &sec.DEKNonce, &sec.Ciphertext, &sec.ValueNonce,
		&sec.CreatedBy, &sec.CreatedAt, &sec.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sec, err
}

// DeleteSecret removes a secret. userID is nil for global scope.
func (s *Store) DeleteSecret(userID *string, name string) error {
	var err error
	if userID == nil {
		_, err = s.db.Exec("DELETE FROM secrets WHERE user_id IS NULL AND name = ?", name)
	} else {
		_, err = s.db.Exec("DELETE FROM secrets WHERE user_id = ? AND name = ?", *userID, name)
	}
	return err
}

// ListSecrets returns metadata for a user's secrets plus all global secrets.
func (s *Store) ListSecrets(userID string) ([]SecretMeta, error) {
	rows, err := s.db.Query(`
		SELECT name, scope, COALESCE(description,''), created_at, COALESCE(updated_at, created_at)
		FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY name
				ORDER BY CASE WHEN user_id IS NOT NULL THEN 0 ELSE 1 END
			) AS rn
			FROM secrets
			WHERE user_id = ? OR user_id IS NULL
		)
		WHERE rn = 1
		ORDER BY scope, name`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metas []SecretMeta
	for rows.Next() {
		var m SecretMeta
		if err := rows.Scan(&m.Name, &m.Scope, &m.Description, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	return metas, nil
}

// GetSecretsByNames fetches multiple secrets by name, preferring user-scoped over global.
func (s *Store) GetSecretsByNames(userID string, names []string) ([]Secret, error) {
	if len(names) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(names))
	args := make([]interface{}, 0, len(names)+1)
	args = append(args, userID)
	for i, n := range names {
		placeholders[i] = "?"
		args = append(args, n)
	}
	inClause := strings.Join(placeholders, ",")

	// Use a window function to rank user-scoped over global for duplicate names.
	// ROW_NUMBER partitioned by name, ordered so user-scoped (non-null user_id) comes first.
	query := fmt.Sprintf(`
		SELECT id, user_id, scope, name, description, encrypted_dek, dek_nonce, ciphertext, value_nonce, created_by, created_at, updated_at
		FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY name
				ORDER BY CASE WHEN user_id IS NOT NULL THEN 0 ELSE 1 END
			) AS rn
			FROM secrets
			WHERE (user_id = ? OR user_id IS NULL) AND name IN (%s)
		)
		WHERE rn = 1`, inClause)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var secrets []Secret
	for rows.Next() {
		var sec Secret
		if err := rows.Scan(&sec.ID, &sec.UserID, &sec.Scope, &sec.Name, &sec.Description,
			&sec.EncryptedDEK, &sec.DEKNonce, &sec.Ciphertext, &sec.ValueNonce,
			&sec.CreatedBy, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
			return nil, err
		}
		secrets = append(secrets, sec)
	}
	return secrets, nil
}

// GetDeploymentsUsingSecret finds all deployments whose secrets_json contains the given secret name.
func (s *Store) GetDeploymentsUsingSecret(secretName string) ([]Deployment, error) {
	pattern := fmt.Sprintf(`%%"%s"%%`, secretName)
	rows, err := s.db.Query(`
		SELECT id, user_id, name, subdomain, framework, COALESCE(container_id,''), COALESCE(port,0),
		       status, ttl_hours, COALESCE(env_json,'{}'), created_at, COALESCE(expires_at,''),
		       COALESCE(access_mode,'public'), COALESCE(access_user,''), COALESCE(access_hash,''),
		       COALESCE(title,''), COALESCE(description,''), COALESCE(mode,'deploy'),
		       COALESCE(network_quota,''), COALESCE(access_users,''), COALESCE(memory,''),
		       COALESCE(cpus,''), COALESCE(locked,0), COALESCE(secrets_json,'[]')
		FROM deployments
		WHERE status != 'destroyed' AND secrets_json LIKE ?`,
		pattern,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deploys []Deployment
	for rows.Next() {
		var d Deployment
		var lockedInt int
		if err := rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Subdomain, &d.Framework, &d.ContainerID, &d.Port,
			&d.Status, &d.TTLHours, &d.EnvJSON, &d.CreatedAt, &d.ExpiresAt,
			&d.AccessMode, &d.AccessUser, &d.AccessHash, &d.Title, &d.Description,
			&d.Mode, &d.NetworkQuota, &d.AccessUsers, &d.Memory, &d.CPUs, &lockedInt, &d.SecretsJSON); err != nil {
			return nil, err
		}
		d.Locked = lockedInt != 0
		deploys = append(deploys, d)
	}
	return deploys, nil
}

// UpdateDeploymentSecrets stores the secret names JSON array on a deployment.
func (s *Store) UpdateDeploymentSecrets(deployID string, secretNames []string) error {
	data, err := json.Marshal(secretNames)
	if err != nil {
		return fmt.Errorf("marshal secret names: %w", err)
	}
	_, err = s.db.Exec("UPDATE deployments SET secrets_json = ? WHERE id = ?", string(data), deployID)
	return err
}
