package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ContainerDefaults struct {
	Memory       string `json:"memory"`
	CPUs         string `json:"cpus"`
	PIDLimit     int    `json:"pidsLimit"`
	DiskSize     string `json:"diskSize"`     // container root fs limit, e.g. "2g" (requires overlay2+xfs+pquota)
	NetworkQuota string `json:"networkQuota"` // max network transfer per container, e.g. "5g" (enforced via Caddy access log)
}

// RuntimeConfig picks the deploy backend and holds driver-specific settings.
// Driver is the registry key; empty defaults to "docker". Per-driver config
// blocks (e.g. Kubernetes{}) can be added here as new drivers land.
type RuntimeConfig struct {
	Driver string `json:"driver,omitempty"`
}

type Config struct {
	Domain          string            `json:"domain"`
	Port            int               `json:"port"`
	DataDir         string            `json:"dataDir"`
	DefaultTTLHours int               `json:"defaultTTLHours"`
	DefaultMaxDeploy int              `json:"defaultMaxDeploys"`
	Container       ContainerDefaults `json:"containerDefaults"`
	Runtime         RuntimeConfig     `json:"runtime,omitempty"`
	CloudflareProxy bool              `json:"cloudflareProxy"`
	Insecure        bool              `json:"insecure"`
	WebDisabled     bool              `json:"webDisabled"`
	MasterKey       string            `json:"masterKey"`

	// ProxySiteConfigMode sets the file mode for Caddy site-config files
	// written under CaddySitesDir. Defaults to 0600 — those files embed
	// basic-auth hashes and api-key secrets and must not be world-readable.
	// Operators whose Caddy runs as a non-root user that cannot read 0600
	// can override (e.g. 0640 with a shared group).
	ProxySiteConfigMode int `json:"proxySiteConfigMode,omitempty"`

	// MaxTarballBytes / MaxTarballEntries cap deploy-upload extraction to
	// protect against disk-fill and inode-exhaustion attacks. 0 → use
	// service-level defaults (see service.DefaultMaxTarBytes etc.).
	MaxTarballBytes   int64 `json:"maxTarballBytes,omitempty"`
	MaxTarballEntries int   `json:"maxTarballEntries,omitempty"`

	// MaxBackupBytes / MaxBackupEntries cap admin-backup restore similarly.
	// Default is intentionally larger than deploy tarballs.
	MaxBackupBytes   int64 `json:"maxBackupBytes,omitempty"`
	MaxBackupEntries int   `json:"maxBackupEntries,omitempty"`

	// LegacyBuildSecrets restores the pre-hardening behavior where user
	// secrets were injected into the build container as env vars. Leaks
	// those values to any postinstall script in any dependency. Deprecated:
	// this flag will be removed in a future release — tenants should move
	// any build-time secret usage to runtime, or petition for a dedicated
	// build-secrets opt-in mechanism.
	LegacyBuildSecrets bool `json:"legacyBuildSecrets,omitempty"`

	// NetworkIsolation controls whether each deployment's build and runtime
	// containers run on a dedicated Docker bridge network, preventing a
	// tenant container from talking to neighbours or probing Docker's
	// internal DNS for other deployments.
	//   "per-deploy"    — default; one network per deployment.
	//   "shared-legacy" — single default bridge, old behavior. Deprecated.
	NetworkIsolation string `json:"networkIsolation,omitempty"`

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

	// Hostnames are case-insensitive; normalize here so every subdomain
	// comparison elsewhere in the codebase can assume a lowercase Domain.
	cfg.Domain = strings.ToLower(cfg.Domain)

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

	if cfg.ProxySiteConfigMode == 0 {
		cfg.ProxySiteConfigMode = 0o600
	}
	if cfg.NetworkIsolation == "" {
		cfg.NetworkIsolation = "per-deploy"
	}

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
