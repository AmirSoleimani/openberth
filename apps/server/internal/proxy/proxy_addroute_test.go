package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AmirSoleimani/openberth/apps/server/internal/config"
)

// proxyForTest spins up a ProxyManager that writes Caddy site files to a
// tmpdir but skips the actual `caddy reload` (no Caddy running in tests).
// The reload command is invoked via os/exec; if Caddy isn't present it
// returns a non-zero exit, which AddRoute swallows. Each test reads the
// generated file directly to assert content.
func proxyForTest(t *testing.T, flatURLs bool) (*ProxyManager, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Domain:              "acme.example.com",
		Port:                3456,
		Insecure:            true, // drop https requirement so we don't need Caddy/TLS to run
		FlatURLs:            flatURLs,
		CaddyAccessLog:      filepath.Join(dir, "access.log"),
		CaddySitesDir:       dir,
		ProxySiteConfigMode: 0o600,
	}
	return NewProxyManager(cfg), dir
}

// TestAddRoute_NestedShape — what every existing self-hosted user gets:
//   workspace = "acme.example.com", deploy "blog"
//   → URL  = "http://blog.acme.example.com"
//   → site config file at dir/blog.caddy with site address "http://blog.acme.example.com"
func TestAddRoute_NestedShape(t *testing.T) {
	p, dir := proxyForTest(t, false)
	url := p.AddRoute("blog", 8080, nil)
	if url != "http://blog.acme.example.com" {
		t.Fatalf("URL: got %q, want %q", url, "http://blog.acme.example.com")
	}
	body, err := os.ReadFile(filepath.Join(dir, "blog.caddy"))
	if err != nil {
		t.Fatalf("read site file: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "http://blog.acme.example.com") {
		t.Errorf("site file missing nested address — got:\n%s", got)
	}
	if !strings.Contains(got, "X-OpenBerth-Deploy blog") {
		t.Errorf("site file missing deploy header — got:\n%s", got)
	}
}

// TestAddRoute_FlatShape — the shared-apex shape:
//   workspace = "acme.example.com", deploy "blog"
//   → URL  = "http://blog-acme.example.com"
//   → site config addresses the sibling-shaped FQDN
func TestAddRoute_FlatShape(t *testing.T) {
	p, dir := proxyForTest(t, true)
	url := p.AddRoute("blog", 8080, nil)
	if url != "http://blog-acme.example.com" {
		t.Fatalf("URL: got %q, want %q", url, "http://blog-acme.example.com")
	}
	body, err := os.ReadFile(filepath.Join(dir, "blog.caddy"))
	if err != nil {
		t.Fatalf("read site file: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "http://blog-acme.example.com") {
		t.Errorf("site file missing flat address — got:\n%s", got)
	}
	// Belt-and-suspenders: ensure we don't accidentally also write the nested form.
	if strings.Contains(got, "blog.acme.example.com {") {
		t.Errorf("site file should NOT contain nested form when FlatURLs=true — got:\n%s", got)
	}
}

// TestAddRoute_FlatMultipleDeploys — each deploy under the same workspace
// produces a sibling URL. Two deploys "blog" and "api" both share the
// suffix "-acme.example.com".
func TestAddRoute_FlatMultipleDeploys(t *testing.T) {
	p, _ := proxyForTest(t, true)
	urls := []string{
		p.AddRoute("blog", 8080, nil),
		p.AddRoute("api", 8081, nil),
		p.AddRoute("admin-panel", 8082, nil),
	}
	want := []string{
		"http://blog-acme.example.com",
		"http://api-acme.example.com",
		"http://admin-panel-acme.example.com",
	}
	for i, u := range urls {
		if u != want[i] {
			t.Errorf("deploy %d: got %q, want %q", i, u, want[i])
		}
	}
}

// TestRemoveRoute_DropsFile — same in either shape, the file lookup is
// keyed by deploy slug regardless of URL composition.
func TestRemoveRoute_DropsFile(t *testing.T) {
	p, dir := proxyForTest(t, true)
	_ = p.AddRoute("blog", 8080, nil)
	if _, err := os.Stat(filepath.Join(dir, "blog.caddy")); err != nil {
		t.Fatalf("site file missing after AddRoute: %v", err)
	}
	p.RemoveRoute("blog")
	if _, err := os.Stat(filepath.Join(dir, "blog.caddy")); !os.IsNotExist(err) {
		t.Errorf("site file should be gone after RemoveRoute, got err: %v", err)
	}
}
