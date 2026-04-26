package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfig_PreFlatURLsCompat — an existing install's config.json
// (written before the --flat-urls option existed) has no `flatUrls`
// key. It must deserialize without error, FlatURLs must default to
// false, and the install must keep its pre-existing nested URL
// behavior.
//
// Verifies the test-plan claim:
//   "Existing config.json files (no flatUrls key) deserialize without
//    error and behave as nested."
func TestLoadConfig_PreFlatURLsCompat(t *testing.T) {
	dir := t.TempDir()
	preFlatURLsJSON := `{
  "domain": "openberth.example.com",
  "port": 3456,
  "dataDir": "` + dir + `",
  "defaultTTLHours": 72,
  "defaultMaxDeploys": 10,
  "containerDefaults": {
    "memory": "512m",
    "cpus": "0.5",
    "pidsLimit": 256
  }
}`
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(preFlatURLsJSON), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	t.Setenv("DATA_DIR", dir)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig on pre-flatUrls config: %v", err)
	}
	if cfg.Domain != "openberth.example.com" {
		t.Errorf("Domain: got %q, want %q", cfg.Domain, "openberth.example.com")
	}
	if cfg.FlatURLs {
		t.Errorf("FlatURLs: got true, want false (key absent must default to false)")
	}
}

// TestLoadConfig_ExplicitFlatURLs — when the install IS run with
// --flat-urls, the resulting config.json carries `"flatUrls": true`,
// and the loader honors it on boot.
func TestLoadConfig_ExplicitFlatURLs(t *testing.T) {
	dir := t.TempDir()
	flatJSON := `{
  "domain": "openberth.example.com",
  "port": 3456,
  "dataDir": "` + dir + `",
  "defaultTTLHours": 72,
  "defaultMaxDeploys": 10,
  "flatUrls": true,
  "containerDefaults": {"memory": "512m", "cpus": "0.5", "pidsLimit": 256}
}`
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(flatJSON), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	t.Setenv("DATA_DIR", dir)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.FlatURLs {
		t.Errorf("FlatURLs: got false, want true")
	}
}

// TestConfigJSON_OmitsFlatURLsWhenFalse — sanity check on the
// `omitempty` JSON tag. A config struct with the default FlatURLs=false
// marshals WITHOUT a `flatUrls` key. This means an upgrade that re-emits
// a config (e.g., via `berth-server` config migration) doesn't suddenly
// surface a new key in the operator's diff when nothing changed.
func TestConfigJSON_OmitsFlatURLsWhenFalse(t *testing.T) {
	cfg := Config{Domain: "openberth.example.com", FlatURLs: false}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := generic["flatUrls"]; present {
		t.Errorf("flatUrls key should be omitted when false (omitempty), got JSON: %s", b)
	}
}
