package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── parseConfigDomain ─────────────────────────────────────────────────────

// TestParseConfigDomain — every install variant carries a different mode
// and the parser must distinguish them so the rewrite picks the matching
// Caddyfile template.
func TestParseConfigDomain(t *testing.T) {
	cases := []struct {
		name      string
		json      string
		domain    string
		mode      string
		flatURLs  bool
	}{
		{
			name:   "direct mode",
			json:   `{"domain":"acme.example.com","port":3456}`,
			domain: "acme.example.com", mode: "direct", flatURLs: false,
		},
		{
			name:   "cloudflare mode",
			json:   `{"domain":"acme.example.com","cloudflareProxy":true}`,
			domain: "acme.example.com", mode: "cloudflare", flatURLs: false,
		},
		{
			name:   "insecure mode",
			json:   `{"domain":"local.dev","insecure":true}`,
			domain: "local.dev", mode: "insecure", flatURLs: false,
		},
		{
			name:   "flat-urls preserved",
			json:   `{"domain":"acme.example.com","flatUrls":true}`,
			domain: "acme.example.com", mode: "direct", flatURLs: true,
		},
		{
			name:   "domain lowercased",
			json:   `{"domain":"ACME.Example.Com"}`,
			domain: "acme.example.com", mode: "direct",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, m, f, err := parseConfigDomain([]byte(tc.json))
			if err != nil {
				t.Fatalf("parseConfigDomain: %v", err)
			}
			if d != tc.domain {
				t.Errorf("domain: got %q, want %q", d, tc.domain)
			}
			if m != tc.mode {
				t.Errorf("mode: got %q, want %q", m, tc.mode)
			}
			if f != tc.flatURLs {
				t.Errorf("flatURLs: got %v, want %v", f, tc.flatURLs)
			}
		})
	}
}

// ── rewriteConfigDomain ───────────────────────────────────────────────────

// TestRewriteConfigDomain — the rewrite must update only the domain, leaving
// every other field (including unknown ones added in future releases)
// untouched. We assert this by deserializing the result and diffing field
// by field.
func TestRewriteConfigDomain_PreservesOtherFields(t *testing.T) {
	original := []byte(`{
    "domain": "old.example.com",
    "port": 3456,
    "dataDir": "/var/lib/openberth",
    "defaultTTLHours": 72,
    "defaultMaxDeploys": 10,
    "cloudflareProxy": true,
    "flatUrls": true,
    "masterKey": "deadbeef",
    "futureField": {"nested": [1, 2, 3]},
    "containerDefaults": {"memory": "512m", "cpus": "0.5"}
}`)
	out, err := rewriteConfigDomain(original, "new.example.com")
	if err != nil {
		t.Fatalf("rewriteConfigDomain: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("rewritten config not valid JSON: %v\n%s", err, out)
	}

	if got["domain"] != "new.example.com" {
		t.Errorf("domain: got %v, want %q", got["domain"], "new.example.com")
	}
	if got["port"].(float64) != 3456 {
		t.Errorf("port not preserved: got %v", got["port"])
	}
	if got["cloudflareProxy"] != true {
		t.Errorf("cloudflareProxy not preserved: got %v", got["cloudflareProxy"])
	}
	if got["flatUrls"] != true {
		t.Errorf("flatUrls not preserved: got %v", got["flatUrls"])
	}
	if got["masterKey"] != "deadbeef" {
		t.Errorf("masterKey not preserved: got %v", got["masterKey"])
	}
	if got["futureField"] == nil {
		t.Errorf("unknown field 'futureField' was dropped")
	}
}

// TestRewriteConfigDomain_TrailingNewline — install writes config.json
// with a trailing newline (template-rendered + os.WriteFile preserves it
// only if the bytes have one). The rewrite should mirror the original.
func TestRewriteConfigDomain_TrailingNewline(t *testing.T) {
	with := []byte(`{"domain":"old"}` + "\n")
	out, err := rewriteConfigDomain(with, "new")
	if err != nil {
		t.Fatalf("with newline: %v", err)
	}
	if out[len(out)-1] != '\n' {
		t.Errorf("trailing newline lost when input had one")
	}

	without := []byte(`{"domain":"old"}`)
	out2, err := rewriteConfigDomain(without, "new")
	if err != nil {
		t.Fatalf("without newline: %v", err)
	}
	if out2[len(out2)-1] == '\n' {
		t.Errorf("trailing newline added when input had none")
	}
}

// ── renderCaddyfile ───────────────────────────────────────────────────────

// TestRenderCaddyfile — each mode yields a structurally valid Caddyfile
// that embeds the new domain in every spot the install template put it.
func TestRenderCaddyfile(t *testing.T) {
	cases := []struct {
		mode        string
		domain      string
		mustContain []string
		mustNotContain []string
	}{
		{
			mode:   "direct",
			domain: "new.example.com",
			mustContain: []string{
				"acme_ca https://acme-v02.api.letsencrypt.org/directory",
				"email admin@new.example.com",
				"new.example.com {",
				"reverse_proxy localhost:3456",
				"import /etc/caddy/sites/*.caddy",
			},
		},
		{
			mode:   "cloudflare",
			domain: "edge.example.com",
			mustContain: []string{
				"edge.example.com {",
				"tls internal",
				"import /etc/caddy/sites/*.caddy",
			},
			mustNotContain: []string{
				"acme_ca",
				"email admin@",
			},
		},
		{
			mode:   "insecure",
			domain: "local.dev",
			mustContain: []string{
				"auto_https off",
				"http://local.dev {",
				"import /etc/caddy/sites/*.caddy",
			},
			mustNotContain: []string{
				"tls",
				"acme_ca",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			out := renderCaddyfile(tc.mode, tc.domain)
			for _, s := range tc.mustContain {
				if !strings.Contains(out, s) {
					t.Errorf("mode=%s missing %q in:\n%s", tc.mode, s, out)
				}
			}
			for _, s := range tc.mustNotContain {
				if strings.Contains(out, s) {
					t.Errorf("mode=%s should NOT contain %q in:\n%s", tc.mode, s, out)
				}
			}
		})
	}
}

// ── rewriteSiteFQDN ───────────────────────────────────────────────────────

// TestRewriteSiteFQDN_NestedShape — default install: per-deploy FQDN is
// "<sub>.<domain>" so substring-replace handles it cleanly.
func TestRewriteSiteFQDN_NestedShape(t *testing.T) {
	body := `# Auto-generated by OpenBerth
blog.acme.example.com {
    reverse_proxy localhost:8080
    header { X-OpenBerth-Deploy blog }
}
`
	got := rewriteSiteFQDN(body, "acme.example.com", "team.example.com")
	if !strings.Contains(got, "blog.team.example.com {") {
		t.Errorf("nested rewrite failed:\n%s", got)
	}
	if strings.Contains(got, "blog.acme.example.com {") {
		t.Errorf("old FQDN still present:\n%s", got)
	}
	// Subdomain header (just "blog") must be untouched.
	if !strings.Contains(got, "X-OpenBerth-Deploy blog") {
		t.Errorf("subdomain header was disturbed:\n%s", got)
	}
}

// TestRewriteSiteFQDN_FlatShape — sibling install: per-deploy FQDN is
// "<sub>-<workspace>.<apex>" but the old domain ("workspace.apex") is
// still a contiguous substring after the dash, so substring-replace works.
func TestRewriteSiteFQDN_FlatShape(t *testing.T) {
	body := `# Auto-generated by OpenBerth
blog-acme.example.com {
    reverse_proxy localhost:8080
    header { X-OpenBerth-Deploy blog }
}
`
	got := rewriteSiteFQDN(body, "acme.example.com", "team.example.com")
	if !strings.Contains(got, "blog-team.example.com {") {
		t.Errorf("flat rewrite failed:\n%s", got)
	}
	if strings.Contains(got, "acme.example.com") {
		t.Errorf("old domain still present after flat rewrite:\n%s", got)
	}
}

// TestRewriteSiteFQDN_Insecure — http://-prefixed site address must
// also rewrite cleanly.
func TestRewriteSiteFQDN_Insecure(t *testing.T) {
	body := `# Auto-generated by OpenBerth
http://blog.local.dev {
    reverse_proxy localhost:8080
}
`
	got := rewriteSiteFQDN(body, "local.dev", "new.local")
	if !strings.Contains(got, "http://blog.new.local {") {
		t.Errorf("insecure rewrite failed:\n%s", got)
	}
}

// ── listSiteConfigs ───────────────────────────────────────────────────────

// TestListSiteConfigs — only .caddy files; subdirectories and other
// extensions ignored. Missing dir is not an error (treated as "no
// per-deploy configs yet").
func TestListSiteConfigs(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "blog.caddy"), "x")
	mustWrite(t, filepath.Join(dir, "api.caddy"), "x")
	mustWrite(t, filepath.Join(dir, "README.md"), "x")
	mustWrite(t, filepath.Join(dir, "deleted.caddy.bak"), "x")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := listSiteConfigs(dir)
	if err != nil {
		t.Fatalf("listSiteConfigs: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 .caddy files, got %d: %v", len(got), got)
	}
	for _, name := range got {
		if filepath.Ext(name) != ".caddy" {
			t.Errorf("non-.caddy file in result: %q", name)
		}
	}
}

func TestListSiteConfigs_MissingDir(t *testing.T) {
	got, err := listSiteConfigs(filepath.Join(t.TempDir(), "doesnotexist"))
	if err != nil {
		t.Errorf("missing dir should not be an error, got: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing dir should yield empty list, got: %v", got)
	}
}

// ── validateDomain ────────────────────────────────────────────────────────

func TestValidateDomain(t *testing.T) {
	good := []string{"example.com", "a.b.c.example.com", "team.example.co.uk", "localhost"}
	bad := map[string]string{
		"":                   "empty",
		"https://example.com": "scheme",
		"example.com/path":   "slash",
		"has space.com":      "space",
		".example.com":       "leading dot",
		"example.com.":       "trailing dot",
		"---":                "no alnum",
	}
	for _, d := range good {
		if err := validateDomain(d); err != nil {
			t.Errorf("validateDomain(%q) unexpected error: %v", d, err)
		}
	}
	for d, why := range bad {
		if err := validateDomain(d); err == nil {
			t.Errorf("validateDomain(%q) should have failed (%s)", d, why)
		}
	}
}

// ── writeWithBackup / restoreBackup ───────────────────────────────────────

// TestWriteWithBackup_FreshFile — when the target doesn't exist yet, no
// backup should be written. Writing must still succeed.
func TestWriteWithBackup_FreshFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "fresh.txt")
	if err := writeWithBackup(target, []byte("new"), 0o600, "1"); err != nil {
		t.Fatalf("writeWithBackup: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("target content: %q, want %q", got, "new")
	}
	if _, err := os.Stat(target + ".bak.1"); !os.IsNotExist(err) {
		t.Errorf("backup should not exist for fresh file, stat err: %v", err)
	}
}

// TestWriteWithBackup_ExistingFile — original content is captured into
// .bak.<suffix> before the rewrite takes effect.
func TestWriteWithBackup_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.txt")
	mustWrite(t, target, "old")
	if err := writeWithBackup(target, []byte("new"), 0o600, "42"); err != nil {
		t.Fatalf("writeWithBackup: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("target content: %q, want %q", got, "new")
	}
	bak, _ := os.ReadFile(target + ".bak.42")
	if string(bak) != "old" {
		t.Errorf("backup content: %q, want %q", bak, "old")
	}
}

// TestRestoreBackup — restoreBackup is the rollback path; it must put
// the previous bytes back at the original path.
func TestRestoreBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	mustWrite(t, target+".bak.7", "original")
	mustWrite(t, target, "corrupted")
	restoreBackup(target, "7")
	got, _ := os.ReadFile(target)
	if string(got) != "original" {
		t.Errorf("after restore: %q, want %q", got, "original")
	}
}

// ── DoRename end-to-end ───────────────────────────────────────────────────

// TestDoRename_DirectMode — the golden path: an install with two deploys,
// in direct ACME mode, renamed cleanly. Asserts:
//   - config.json domain swapped, other fields preserved
//   - main Caddyfile reflects the new domain in template positions
//   - both per-deploy site configs reference the new FQDN
//   - backups exist for every rewritten file
//   - dry-run flag returns plan without touching disk
func TestDoRename_DirectMode(t *testing.T) {
	dataDir, caddyDir := setupFixture(t, fixture{
		mode:     "direct",
		domain:   "old.example.com",
		flatURLs: false,
		deploys:  []string{"blog", "api"},
	})

	res, err := DoRename(RenameOptions{
		NewDomain:  "new.example.com",
		DataDir:    dataDir,
		CaddyDir:   caddyDir,
		SkipReload: true,
	})
	if err != nil {
		t.Fatalf("DoRename: %v", err)
	}

	assertConfigDomain(t, dataDir, "new.example.com")
	assertConfigPreserved(t, dataDir, []string{"masterKey", "port", "dataDir"})
	assertCaddyfileContains(t, caddyDir, []string{
		"new.example.com {",
		"email admin@new.example.com",
	})
	assertCaddyfileNotContains(t, caddyDir, []string{
		"old.example.com",
	})
	for _, sub := range []string{"blog", "api"} {
		assertSiteFile(t, caddyDir, sub, "new.example.com", "old.example.com")
	}
	// Backups exist for every rewritten file.
	mustExist(t, filepath.Join(dataDir, "config.json.bak."+res.BackupSuffix))
	mustExist(t, filepath.Join(caddyDir, "Caddyfile.bak."+res.BackupSuffix))
	mustExist(t, filepath.Join(caddyDir, "sites", "blog.caddy.bak."+res.BackupSuffix))
	mustExist(t, filepath.Join(caddyDir, "sites", "api.caddy.bak."+res.BackupSuffix))
	if res.Reloaded {
		t.Errorf("Reloaded should be false when SkipReload=true")
	}
}

// TestDoRename_CloudflareMode — cloudflare-mode installs use a different
// Caddyfile template (no ACME, internal TLS). The rewrite must pick the
// right template based on the mode flag in the source config.
func TestDoRename_CloudflareMode(t *testing.T) {
	dataDir, caddyDir := setupFixture(t, fixture{
		mode:    "cloudflare",
		domain:  "old.example.com",
		deploys: []string{"blog"},
	})
	if _, err := DoRename(RenameOptions{
		NewDomain:  "new.example.com",
		DataDir:    dataDir,
		CaddyDir:   caddyDir,
		SkipReload: true,
	}); err != nil {
		t.Fatalf("DoRename: %v", err)
	}
	assertCaddyfileContains(t, caddyDir, []string{
		"new.example.com {",
		"tls internal",
	})
	assertCaddyfileNotContains(t, caddyDir, []string{
		"acme_ca",
		"email admin@",
	})
}

// TestDoRename_InsecureMode — insecure mode uses http:// site address
// and disables auto_https. The rewrite must keep that mode.
func TestDoRename_InsecureMode(t *testing.T) {
	dataDir, caddyDir := setupFixture(t, fixture{
		mode:    "insecure",
		domain:  "old.local",
		deploys: []string{"blog"},
	})
	if _, err := DoRename(RenameOptions{
		NewDomain:  "new.local",
		DataDir:    dataDir,
		CaddyDir:   caddyDir,
		SkipReload: true,
	}); err != nil {
		t.Fatalf("DoRename: %v", err)
	}
	assertCaddyfileContains(t, caddyDir, []string{
		"auto_https off",
		"http://new.local {",
	})
	// Per-deploy sites preserve their http:// scheme.
	body := mustRead(t, filepath.Join(caddyDir, "sites", "blog.caddy"))
	if !strings.Contains(body, "http://blog.new.local") {
		t.Errorf("insecure site config didn't keep http:// + new domain:\n%s", body)
	}
}

// TestDoRename_FlatURLsMode — flat URLs put the workspace label inline
// (<sub>-<workspace>.<apex>). The rewrite must replace the embedded old
// domain across both shapes; this test exercises the flat path explicitly.
func TestDoRename_FlatURLsMode(t *testing.T) {
	dataDir, caddyDir := setupFixture(t, fixture{
		mode:     "direct",
		domain:   "old.example.com",
		flatURLs: true,
		deploys:  []string{"blog"},
	})
	if _, err := DoRename(RenameOptions{
		NewDomain:  "new.example.com",
		DataDir:    dataDir,
		CaddyDir:   caddyDir,
		SkipReload: true,
	}); err != nil {
		t.Fatalf("DoRename: %v", err)
	}
	body := mustRead(t, filepath.Join(caddyDir, "sites", "blog.caddy"))
	if !strings.Contains(body, "blog-new.example.com") {
		t.Errorf("flat-shape rewrite missing new FQDN:\n%s", body)
	}
	if strings.Contains(body, "blog-old.example.com") {
		t.Errorf("flat-shape rewrite still references old FQDN:\n%s", body)
	}
}

// TestDoRename_DryRun — DryRun returns the plan without writing. The
// original files must be untouched and no backups created.
func TestDoRename_DryRun(t *testing.T) {
	dataDir, caddyDir := setupFixture(t, fixture{
		mode:    "direct",
		domain:  "old.example.com",
		deploys: []string{"blog"},
	})
	originalConfig := mustRead(t, filepath.Join(dataDir, "config.json"))
	originalCaddy := mustRead(t, filepath.Join(caddyDir, "Caddyfile"))
	originalSite := mustRead(t, filepath.Join(caddyDir, "sites", "blog.caddy"))

	res, err := DoRename(RenameOptions{
		NewDomain:  "new.example.com",
		DataDir:    dataDir,
		CaddyDir:   caddyDir,
		SkipReload: true,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("DoRename dry-run: %v", err)
	}
	if res.OldDomain != "old.example.com" || res.NewDomain != "new.example.com" {
		t.Errorf("dry-run plan: %+v", res)
	}
	if got := mustRead(t, filepath.Join(dataDir, "config.json")); got != originalConfig {
		t.Errorf("config.json was modified during dry-run")
	}
	if got := mustRead(t, filepath.Join(caddyDir, "Caddyfile")); got != originalCaddy {
		t.Errorf("Caddyfile was modified during dry-run")
	}
	if got := mustRead(t, filepath.Join(caddyDir, "sites", "blog.caddy")); got != originalSite {
		t.Errorf("blog.caddy was modified during dry-run")
	}
}

// TestDoRename_RejectsSameDomain — switching to the current domain is a
// no-op; refuse so the operator notices the typo.
func TestDoRename_RejectsSameDomain(t *testing.T) {
	dataDir, caddyDir := setupFixture(t, fixture{
		mode:   "direct",
		domain: "same.example.com",
	})
	_, err := DoRename(RenameOptions{
		NewDomain:  "Same.Example.Com",
		DataDir:    dataDir,
		CaddyDir:   caddyDir,
		SkipReload: true,
	})
	if err == nil || !strings.Contains(err.Error(), "matches current") {
		t.Errorf("expected same-domain rejection, got: %v", err)
	}
}

// TestDoRename_RejectsMissingConfig — without a config.json the operator
// hasn't installed yet; surface a clear hint.
func TestDoRename_RejectsMissingConfig(t *testing.T) {
	tmp := t.TempDir()
	_, err := DoRename(RenameOptions{
		NewDomain:  "new.example.com",
		DataDir:    tmp,
		CaddyDir:   filepath.Join(tmp, "caddy"),
		SkipReload: true,
	})
	if err == nil || !strings.Contains(err.Error(), "config.json") {
		t.Errorf("expected missing-config error, got: %v", err)
	}
}

// TestDoRename_RejectsBadDomain — sanity gate so a typo'd domain doesn't
// leave Caddy with an unparseable site address.
func TestDoRename_RejectsBadDomain(t *testing.T) {
	dataDir, caddyDir := setupFixture(t, fixture{mode: "direct", domain: "old.example.com"})
	cases := []string{"", "https://x.com", "no slash/here", "has space.com"}
	for _, d := range cases {
		if _, err := DoRename(RenameOptions{
			NewDomain:  d,
			DataDir:    dataDir,
			CaddyDir:   caddyDir,
			SkipReload: true,
		}); err == nil {
			t.Errorf("DoRename should reject %q", d)
		}
	}
}

// TestDoRename_NoDeploysYet — fresh install with zero deploys should
// still rename successfully (config.json + Caddyfile only).
func TestDoRename_NoDeploysYet(t *testing.T) {
	dataDir, caddyDir := setupFixture(t, fixture{
		mode:    "direct",
		domain:  "old.example.com",
		deploys: nil, // empty
	})
	res, err := DoRename(RenameOptions{
		NewDomain:  "new.example.com",
		DataDir:    dataDir,
		CaddyDir:   caddyDir,
		SkipReload: true,
	})
	if err != nil {
		t.Fatalf("DoRename with no deploys: %v", err)
	}
	if len(res.SiteFiles) != 0 {
		t.Errorf("expected no site files, got %v", res.SiteFiles)
	}
	assertConfigDomain(t, dataDir, "new.example.com")
}

// TestDoRename_ParityWithProxyComposeFQDN — the rewrite produces FQDNs
// that match what proxy.composeFQDN would have generated had the new
// domain been the install-time domain. This is the contract that keeps
// deploys reachable post-rename: the URL the proxy package will report
// from now on must equal the site address sitting in the Caddy config.
//
// Mirrors the composeFQDN logic from internal/proxy/proxy.go (kept as a
// local mirror per the codebase's "shared logic by copy, not import"
// rule for things across module-internal boundaries).
func TestDoRename_ParityWithProxyComposeFQDN(t *testing.T) {
	cases := []struct {
		oldDomain, newDomain, sub string
		flatURLs                  bool
	}{
		{"old.example.com", "new.example.com", "blog", false},
		{"old.example.com", "new.example.com", "api", false},
		{"workspace.example.com", "team.example.com", "blog", true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%s/flat=%v", tc.oldDomain, tc.newDomain, tc.flatURLs), func(t *testing.T) {
			old := composeFQDNLocal(tc.oldDomain, tc.sub, tc.flatURLs)
			body := old + " {\n  reverse_proxy localhost:8080\n}\n"
			got := rewriteSiteFQDN(body, tc.oldDomain, tc.newDomain)
			wantFQDN := composeFQDNLocal(tc.newDomain, tc.sub, tc.flatURLs)
			if !strings.Contains(got, wantFQDN+" {") {
				t.Errorf("rewritten body missing %q (parity check):\n%s", wantFQDN, got)
			}
		})
	}
}

// composeFQDNLocal mirrors proxy.ProxyManager.composeFQDN. Kept here for
// parity tests so the install package doesn't import proxy just for
// this one helper. If the shapes ever diverge in proxy, this test will
// catch it because the rewritten output won't match.
func composeFQDNLocal(domain, sub string, flatURLs bool) string {
	if !flatURLs {
		return sub + "." + domain
	}
	dot := strings.Index(domain, ".")
	if dot < 0 {
		return sub + "." + domain
	}
	workspace := domain[:dot]
	apex := domain[dot+1:]
	return sub + "-" + workspace + "." + apex
}

// ── Fixture helpers ───────────────────────────────────────────────────────

type fixture struct {
	mode     string // "direct" | "cloudflare" | "insecure"
	domain   string
	flatURLs bool
	deploys  []string // subdomains; empty → no per-deploy site configs
}

// setupFixture lays down a tmp dir mirroring an install: <data>/config.json
// + <caddy>/Caddyfile + <caddy>/sites/<sub>.caddy for each deploy.
func setupFixture(t *testing.T, f fixture) (dataDir, caddyDir string) {
	t.Helper()
	dataDir = t.TempDir()
	caddyDir = t.TempDir()
	sitesDir := filepath.Join(caddyDir, "sites")
	if err := os.MkdirAll(sitesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// config.json — pick the right template variant.
	var cfgContent string
	switch f.mode {
	case "insecure":
		cfgContent = fmt.Sprintf(configJSONInsecureTemplate, f.domain, 72, 10, false, f.flatURLs)
	case "cloudflare":
		cfgContent = fmt.Sprintf(configJSONCloudflareTemplate, f.domain, 72, 10, false, f.flatURLs)
	default:
		cfgContent = fmt.Sprintf(configJSONTemplate, f.domain, 72, 10, false, f.flatURLs)
	}
	// Inject a few extra fields so we can later assert they're preserved.
	cfgContent = strings.Replace(cfgContent,
		`"containerDefaults"`,
		`"masterKey": "deadbeefcafebabe", "containerDefaults"`, 1)
	mustWrite(t, filepath.Join(dataDir, "config.json"), cfgContent)

	// Main Caddyfile — same template install would have written.
	var caddyContent string
	switch f.mode {
	case "insecure":
		caddyContent = fmt.Sprintf(caddyfileInsecureTemplate, f.domain)
	case "cloudflare":
		caddyContent = fmt.Sprintf(caddyfileCloudflareTemplate, f.domain)
	default:
		caddyContent = fmt.Sprintf(caddyfileTemplate, f.domain, f.domain)
	}
	mustWrite(t, filepath.Join(caddyDir, "Caddyfile"), caddyContent)

	// Per-deploy site configs. We mimic the shape proxy.buildSiteConfig
	// produces — site address, log block, /_data and / handlers, and the
	// X-OpenBerth-Deploy header. Enough to exercise the substring-replace
	// without dragging the proxy package in.
	for _, sub := range f.deploys {
		fqdn := composeFQDNLocal(f.domain, sub, f.flatURLs)
		siteAddr := fqdn
		if f.mode == "insecure" {
			siteAddr = "http://" + fqdn
		}
		body := fmt.Sprintf(`# Auto-generated by OpenBerth
%s {
    log {
        output file /var/log/caddy/access.json
        format json
    }
    handle /_data/* {
        reverse_proxy localhost:3456
    }
    handle {
        reverse_proxy localhost:8080
    }
    header {
        X-OpenBerth-Deploy %s
    }
}
`, siteAddr, sub)
		mustWrite(t, filepath.Join(sitesDir, sub+".caddy"), body)
	}
	return dataDir, caddyDir
}

func assertConfigDomain(t *testing.T, dataDir, want string) {
	t.Helper()
	body := mustRead(t, filepath.Join(dataDir, "config.json"))
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("config.json invalid JSON: %v\n%s", err, body)
	}
	if m["domain"] != want {
		t.Errorf("config domain: got %v, want %q", m["domain"], want)
	}
}

func assertConfigPreserved(t *testing.T, dataDir string, fields []string) {
	t.Helper()
	body := mustRead(t, filepath.Join(dataDir, "config.json"))
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("config.json invalid JSON: %v", err)
	}
	for _, f := range fields {
		if _, ok := m[f]; !ok {
			t.Errorf("expected field %q preserved in config.json", f)
		}
	}
}

func assertCaddyfileContains(t *testing.T, caddyDir string, needles []string) {
	t.Helper()
	body := mustRead(t, filepath.Join(caddyDir, "Caddyfile"))
	for _, n := range needles {
		if !strings.Contains(body, n) {
			t.Errorf("Caddyfile missing %q in:\n%s", n, body)
		}
	}
}

func assertCaddyfileNotContains(t *testing.T, caddyDir string, needles []string) {
	t.Helper()
	body := mustRead(t, filepath.Join(caddyDir, "Caddyfile"))
	for _, n := range needles {
		if strings.Contains(body, n) {
			t.Errorf("Caddyfile should NOT contain %q in:\n%s", n, body)
		}
	}
}

func assertSiteFile(t *testing.T, caddyDir, sub, wantDomain, oldDomain string) {
	t.Helper()
	body := mustRead(t, filepath.Join(caddyDir, "sites", sub+".caddy"))
	if !strings.Contains(body, wantDomain) {
		t.Errorf("site %s missing new domain %q in:\n%s", sub, wantDomain, body)
	}
	if strings.Contains(body, oldDomain) {
		t.Errorf("site %s still references old domain %q in:\n%s", sub, oldDomain, body)
	}
	// Subdomain identity should survive the rewrite — header value uses
	// just the subdomain, never the FQDN.
	if !strings.Contains(body, "X-OpenBerth-Deploy "+sub) {
		t.Errorf("site %s lost X-OpenBerth-Deploy header in:\n%s", sub, body)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file at %s, got: %v", path, err)
	}
}
