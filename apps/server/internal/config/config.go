package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ContainerDefaults struct {
	Memory       string `json:"memory"`
	CPUs         string `json:"cpus"`
	PIDLimit     int    `json:"pidsLimit"`
	DiskSize     string `json:"diskSize"`     // container root fs limit, e.g. "2g" (requires overlay2+xfs+pquota)
	NetworkQuota string `json:"networkQuota"` // max network transfer per container, e.g. "5g" (enforced via Caddy access log)
}

type Config struct {
	Domain          string            `json:"domain"`
	Port            int               `json:"port"`
	DataDir         string            `json:"dataDir"`
	DefaultTTLHours int               `json:"defaultTTLHours"`
	DefaultMaxDeploy int              `json:"defaultMaxDeploys"`
	Container       ContainerDefaults `json:"containerDefaults"`
	CloudflareProxy bool              `json:"cloudflareProxy"`
	Insecure        bool              `json:"insecure"`
	MasterKey       string            `json:"masterKey"`
	Backend         string            `json:"backend"` // "docker" (default) or "kubernetes"

	// Derived paths
	DeploysDir     string `json:"-"`
	UploadsDir     string `json:"-"`
	DBPath         string `json:"-"`
	CaddySitesDir  string `json:"-"`
	BaseURL        string `json:"-"`
	PersistDir     string `json:"-"`
	CaddyAccessLog string `json:"-"`
}

func LoadConfig() (*Config, error) {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/lib/openberth"
	}

	cfg := &Config{
		Domain:          "localhost",
		Port:            3456,
		DataDir:         dataDir,
		DefaultTTLHours: 72,
		DefaultMaxDeploy: 10,
		Container: ContainerDefaults{
			Memory:   "512m",
			CPUs:     "0.5",
			PIDLimit: 256,
		},
	}

	cfgPath := filepath.Join(dataDir, "config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(data, cfg)
	}

	cfg.DataDir = dataDir
	cfg.DeploysDir = filepath.Join(dataDir, "deploys")
	cfg.UploadsDir = filepath.Join(dataDir, "uploads")
	cfg.DBPath = filepath.Join(dataDir, "openberth.db")
	cfg.CaddySitesDir = "/etc/caddy/sites"
	if cfg.Insecure {
		cfg.BaseURL = "http://" + cfg.Domain
	} else {
		cfg.BaseURL = "https://" + cfg.Domain
	}
	cfg.PersistDir = filepath.Join(dataDir, "persist")
	cfg.CaddyAccessLog = "/var/log/caddy/access.json"

	// Ensure directories exist
	for _, d := range []string{cfg.DeploysDir, cfg.UploadsDir, cfg.CaddySitesDir, cfg.PersistDir} {
		os.MkdirAll(d, 0755)
	}

	// Auto-generate master key for secrets encryption if not set
	if cfg.MasterKey == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate master key: %w", err)
		}
		cfg.MasterKey = hex.EncodeToString(key)
		// Persist the generated key back to config file
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err == nil {
			os.WriteFile(cfgPath, data, 0600)
		}
	}

	return cfg, nil
}

// GetMasterKeyBytes decodes the hex-encoded master key into a 32-byte array.
func (c *Config) GetMasterKeyBytes() ([32]byte, error) {
	var key [32]byte
	b, err := hex.DecodeString(c.MasterKey)
	if err != nil {
		return key, fmt.Errorf("decode master key: %w", err)
	}
	if len(b) != 32 {
		return key, fmt.Errorf("master key must be 32 bytes, got %d", len(b))
	}
	copy(key[:], b)
	return key, nil
}
