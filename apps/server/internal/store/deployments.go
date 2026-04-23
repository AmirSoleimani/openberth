package store

import (
	"database/sql"
	"strings"
	"time"
)

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
	SecretsJSON  string `json:"secretsJson"`
	OwnerName    string `json:"ownerName,omitempty"` // populated by list queries via JOIN; empty elsewhere
}

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
	err := s.db.QueryRow(`
		SELECT d.id, d.user_id, d.name, d.subdomain, d.framework,
			COALESCE(d.container_id,''), COALESCE(d.port,0), d.status, d.ttl_hours,
			COALESCE(d.env_json,'{}'), d.created_at, COALESCE(d.expires_at,''),
			COALESCE(d.access_mode,'public'), COALESCE(d.access_user,''),
			COALESCE(d.access_hash,''), COALESCE(d.title,''), COALESCE(d.description,''),
			COALESCE(d.mode,'deploy'), COALESCE(d.network_quota,''),
			COALESCE(d.access_users,''), COALESCE(d.memory,''), COALESCE(d.cpus,''),
			COALESCE(d.locked,0),
			COALESCE(NULLIF(u.display_name, ''), u.name, '') AS owner_name
		FROM deployments d
		LEFT JOIN users u ON d.user_id = u.id
		WHERE d.id = ?`,
		id,
	).Scan(&d.ID, &d.UserID, &d.Name, &d.Subdomain, &d.Framework, &d.ContainerID, &d.Port, &d.Status, &d.TTLHours, &d.EnvJSON, &d.CreatedAt, &d.ExpiresAt, &d.AccessMode, &d.AccessUser, &d.AccessHash, &d.Title, &d.Description, &d.Mode, &d.NetworkQuota, &d.AccessUsers, &d.Memory, &d.CPUs, &lockedInt, &d.OwnerName)
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

// ListDeployments returns non-destroyed deployments, always joined with users
// to populate OwnerName. If userID is empty all deployments are returned;
// otherwise results are filtered to that owner.
func (s *Store) ListDeployments(userID string) ([]Deployment, error) {
	base := `SELECT d.id, d.user_id, d.name, d.subdomain, d.framework,
		COALESCE(d.container_id,''), COALESCE(d.port,0), d.status, d.ttl_hours,
		COALESCE(d.env_json,'{}'), d.created_at, COALESCE(d.expires_at,''),
		COALESCE(d.access_mode,'public'), COALESCE(d.access_user,''),
		COALESCE(d.access_hash,''), COALESCE(d.title,''), COALESCE(d.description,''),
		COALESCE(d.mode,'deploy'), COALESCE(d.network_quota,''),
		COALESCE(d.access_users,''), COALESCE(d.memory,''), COALESCE(d.cpus,''),
		COALESCE(d.locked,0),
		COALESCE(NULLIF(u.display_name, ''), u.name, '') AS owner_name
	FROM deployments d
	LEFT JOIN users u ON d.user_id = u.id
	WHERE d.status != 'destroyed' `

	var rows *sql.Rows
	var err error
	if userID != "" {
		rows, err = s.db.Query(base+"AND d.user_id = ? ORDER BY d.created_at DESC", userID)
	} else {
		rows, err = s.db.Query(base + "ORDER BY d.created_at DESC")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deploys []Deployment
	for rows.Next() {
		var d Deployment
		var lockedInt int
		rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Subdomain, &d.Framework, &d.ContainerID, &d.Port,
			&d.Status, &d.TTLHours, &d.EnvJSON, &d.CreatedAt, &d.ExpiresAt, &d.AccessMode,
			&d.AccessUser, &d.AccessHash, &d.Title, &d.Description, &d.Mode, &d.NetworkQuota,
			&d.AccessUsers, &d.Memory, &d.CPUs, &lockedInt, &d.OwnerName)
		d.Locked = lockedInt != 0
		deploys = append(deploys, d)
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
