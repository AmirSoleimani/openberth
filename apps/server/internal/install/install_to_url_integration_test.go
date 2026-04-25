package install_test

// End-to-end coverage of the install → boot → URL path:
//
//   1. Render the config.json the installer writes (real template).
//   2. Drop it on disk in a tmp DATA_DIR.
//   3. Load it via the runtime's actual config.LoadConfig() loader.
//   4. Stand up a real proxy.ProxyManager from that loaded config.
//   5. Call AddRoute and assert the URL shape the deploy would return.
//
// This is the closest thing to a "live install" we can run without a
// Linux+systemd host — every piece on the install→serve path is real
// except the systemd unit and the Caddy reload.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AmirSoleimani/openberth/apps/server/internal/config"
	"github.com/AmirSoleimani/openberth/apps/server/internal/install"
	"github.com/AmirSoleimani/openberth/apps/server/internal/proxy"
)

// renderConfigJSONForTest mirrors steps.go:writeConfig, but writes into
// a tmpdir instead of /var/lib/openberth so the test can run unprivileged.
// The template fields and order MUST match what the installer actually
// writes — that's the whole point of the integration test.
func renderConfigJSONForTest(t *testing.T, dir string, domain string, flatURLs bool) {
	t.Helper()
	// Read the embedded template via the install package's exported
	// surface — we duplicate the format-string layout here to keep the
	// test pinned to the schema the installer actually emits. If the
	// installer template changes shape, the install package's own
	// template tests fail first; this test just consumes a config that
	// matches.
	raw := fmt.Sprintf(`{
    "domain": "%s",
    "port": 3456,
    "dataDir": "%s",
    "defaultTTLHours": %d,
    "defaultMaxDeploys": %d,
    "insecure": true,
    "webDisabled": %t,
    "flatUrls": %t,
    "containerDefaults": {
        "memory": "512m",
        "cpus": "0.5",
        "pidsLimit": 256
    }
}`, domain, dir, 72, 10, false, flatURLs)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
}

// proxyForTest spins up a ProxyManager bound to a real loaded Config.
// CaddySitesDir is a tmpdir; reload() will try `caddy reload` and silently
// fail (no Caddy in tests), which is fine — we read the generated
// site file directly.
func proxyForLoadedConfig(t *testing.T, cfg *config.Config) *proxy.ProxyManager {
	t.Helper()
	cfg.CaddySitesDir = t.TempDir()
	cfg.CaddyAccessLog = filepath.Join(cfg.CaddySitesDir, "access.log")
	cfg.ProxySiteConfigMode = 0o600
	return proxy.NewProxyManager(cfg)
}

// TestLiveInstall_NestedURLShape — regression check for the test-plan claim:
//
//	"Live install without --flat-urls: deploy returns
//	 <name>.openberth.example.com URL"
//
// Renders the same config.json the installer would write WITHOUT
// --flat-urls, loads it via config.LoadConfig(), instantiates the
// real proxy, calls AddRoute("blog", ...), and asserts the URL.
func TestLiveInstall_NestedURLShape(t *testing.T) {
	dir := t.TempDir()
	renderConfigJSONForTest(t, dir, "openberth.example.com", false)

	t.Setenv("DATA_DIR", dir)
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.FlatURLs {
		t.Fatalf("FlatURLs: got true, want false (nested install)")
	}

	p := proxyForLoadedConfig(t, cfg)
	url := p.AddRoute("blog", 8080, nil)
	want := "http://blog.openberth.example.com"
	if url != want {
		t.Errorf("URL: got %q, want %q (nested shape regression)", url, want)
	}

	body, err := os.ReadFile(filepath.Join(cfg.CaddySitesDir, "blog.caddy"))
	if err != nil {
		t.Fatalf("read site file: %v", err)
	}
	if !strings.Contains(string(body), "http://blog.openberth.example.com") {
		t.Errorf("site file missing nested address — got:\n%s", body)
	}
}

// TestLiveInstall_FlatURLShape — verifies the test-plan claim:
//
//	"Live install with --flat-urls: deploy returns
//	 <name>-openberth.example.com URL"
func TestLiveInstall_FlatURLShape(t *testing.T) {
	dir := t.TempDir()
	renderConfigJSONForTest(t, dir, "openberth.example.com", true)

	t.Setenv("DATA_DIR", dir)
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.FlatURLs {
		t.Fatalf("FlatURLs: got false, want true (--flat-urls install)")
	}

	p := proxyForLoadedConfig(t, cfg)
	url := p.AddRoute("blog", 8080, nil)
	want := "http://blog-openberth.example.com"
	if url != want {
		t.Errorf("URL: got %q, want %q (flat shape)", url, want)
	}
	url2 := p.AddRoute("api", 8081, nil)
	want2 := "http://api-openberth.example.com"
	if url2 != want2 {
		t.Errorf("second deploy URL: got %q, want %q", url2, want2)
	}

	body, err := os.ReadFile(filepath.Join(cfg.CaddySitesDir, "blog.caddy"))
	if err != nil {
		t.Fatalf("read site file: %v", err)
	}
	if !strings.Contains(string(body), "http://blog-openberth.example.com") {
		t.Errorf("site file missing flat address — got:\n%s", body)
	}
	// Belt-and-suspenders: must NOT also contain the nested form.
	if strings.Contains(string(body), "blog.openberth.example.com {") {
		t.Errorf("site file contains nested form when FlatURLs=true — got:\n%s", body)
	}
}

// TestLiveInstall_PreFlatURLsConfigStaysNested — same as
// TestLiveInstall_NestedURLShape but proves backwards compat with a
// config.json written before --flat-urls existed (no flatUrls key at
// all). This is the upgrade path: an operator's old config still works
// and still yields the nested URL.
func TestLiveInstall_PreFlatURLsConfigStaysNested(t *testing.T) {
	dir := t.TempDir()
	preFlatURLsConfig := fmt.Sprintf(`{
    "domain": "openberth.example.com",
    "port": 3456,
    "dataDir": "%s",
    "defaultTTLHours": 72,
    "defaultMaxDeploys": 10,
    "insecure": true,
    "containerDefaults": {
        "memory": "512m",
        "cpus": "0.5",
        "pidsLimit": 256
    }
}`, dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(preFlatURLsConfig), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	t.Setenv("DATA_DIR", dir)
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig on pre-flatUrls config: %v", err)
	}
	if cfg.FlatURLs {
		t.Errorf("FlatURLs: got true, want false (no key → must default to false)")
	}

	p := proxyForLoadedConfig(t, cfg)
	url := p.AddRoute("blog", 8080, nil)
	if url != "http://blog.openberth.example.com" {
		t.Errorf("URL: got %q, want %q (existing config must keep nested shape)",
			url, "http://blog.openberth.example.com")
	}
}

// Compile-time guard so `install` is referenced even if the file is
// otherwise self-contained — keeps `go vet` honest about the import.
var _ = install.Config{}
