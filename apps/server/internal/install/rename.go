package install

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RenameOptions configures a rename-domain run. Caller fills paths from
// the config (defaults match the locations write-time install lays down)
// so unit tests can point everything at a tmpdir.
type RenameOptions struct {
	// NewDomain is the replacement domain (lowercased before use).
	NewDomain string
	// DataDir holds config.json (default: /var/lib/openberth).
	DataDir string
	// CaddyDir holds Caddyfile and the sites/ subdirectory.
	CaddyDir string
	// SkipReload suppresses the systemctl reload caddy step. Used by tests
	// and by operators who want to stage the swap and reload manually.
	SkipReload bool
	// SkipRestart suppresses the systemctl restart openberth step. Without
	// the restart the daemon's in-memory cfg still holds the old domain
	// even though Caddy is now serving the new one — API responses, OAuth
	// metadata, and deploy URLs would emit the stale value until the
	// operator restarts manually. Tests set this true (no daemon to
	// restart in a tmpdir).
	SkipRestart bool
	// DryRun prints planned changes and returns without writing.
	DryRun bool
	// Stdout/Stderr — overridable for tests; default to os.Stdout/Stderr.
	Stdout, Stderr *os.File
}

// RenameResult summarises what changed (or would change in DryRun mode).
type RenameResult struct {
	OldDomain     string
	NewDomain     string
	ConfigPath    string
	CaddyfilePath string
	SitesDir      string
	SiteFiles     []string // basenames (e.g. "blog.caddy")
	BackupSuffix  string
	CaddyMode     string // "direct" | "cloudflare" | "insecure" — informational, derived from config flags
	FlatURLs      bool
	Reloaded      bool
	Restarted     bool
	DaemonHint    string
}

// RunRename parses CLI flags and executes the rename. Called from main.go
// when the binary is invoked as `berth-server rename-domain`.
func RunRename(args []string) {
	fs := flag.NewFlagSet("berth-server rename-domain", flag.ExitOnError)
	var (
		to        string
		dataDir   string
		caddyDir  string
		yes       bool
		dryRun    bool
		noReload  bool
		noRestart bool
	)
	fs.StringVar(&to, "to", "", "New domain (required)")
	fs.StringVar(&dataDir, "data-dir", "/var/lib/openberth", "Data directory holding config.json")
	fs.StringVar(&caddyDir, "caddy-dir", "/etc/caddy", "Directory holding Caddyfile and sites/")
	fs.BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	fs.BoolVar(&dryRun, "dry-run", false, "Print planned changes without writing")
	fs.BoolVar(&noReload, "no-reload", false, "Skip systemctl reload caddy")
	fs.BoolVar(&noRestart, "no-restart", false, "Skip systemctl restart openberth (leaves daemon emitting old domain)")

	fs.Usage = func() {
		fmt.Printf(`
  %s⚓ OpenBerth Server Rename-Domain%s — Swap the install's primary domain

  %sUSAGE%s
    berth-server rename-domain --to <new-domain> [options]

  %sEXAMPLE%s
    berth-server rename-domain --to new.example.com
    berth-server rename-domain --to staging.example.com --dry-run

  %sOPTIONS%s
    --to <domain>        New domain (required)
    --data-dir <path>    Data directory (default: /var/lib/openberth)
    --caddy-dir <path>   Caddy config directory (default: /etc/caddy)
    --yes                Skip confirmation prompt
    --dry-run            Print planned changes without applying them
    --no-reload          Don't run systemctl reload caddy
    --no-restart         Don't run systemctl restart openberth (leaves
                         the daemon emitting the old domain in API
                         responses, OAuth metadata, and deploy URLs
                         until you restart by hand)

  %sNOTES%s
    Updates config.json, /etc/caddy/Caddyfile and every per-deploy site
    config under /etc/caddy/sites/, then reloads Caddy and restarts the
    openberth daemon. Backups are kept next to each modified file with
    a .bak.<timestamp> suffix.

    DNS for *.<new-domain> must point at this server before reload, or
    new ACME cert issuance will fail and deploys will be unreachable.
`, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset)
	}

	fs.Parse(args)

	if to == "" {
		cliFail("--to is required")
		fs.Usage()
		os.Exit(1)
	}

	opts := RenameOptions{
		NewDomain:   to,
		DataDir:     dataDir,
		CaddyDir:    caddyDir,
		SkipReload:  noReload,
		SkipRestart: noRestart,
		DryRun:      dryRun,
	}

	if !yes && !dryRun {
		if !confirmRename(os.Stdin, opts) {
			cliWarn("Aborted")
			os.Exit(1)
		}
	}

	res, err := DoRename(opts)
	if err != nil {
		cliFail(err.Error())
		os.Exit(1)
	}

	printRenameResult(res, dryRun)
}

// confirmRename reads y/N from stdin (any answer not starting with 'y'
// returns false). Returns true if the operator typed yes.
func confirmRename(in *os.File, opts RenameOptions) bool {
	fmt.Printf("\n  %s⚓ OpenBerth Rename-Domain%s\n", cBold, cReset)
	fmt.Printf("  %s›%s New domain: %s\n", cCyan, cReset, opts.NewDomain)
	fmt.Printf("  %s›%s Data dir:   %s\n", cCyan, cReset, opts.DataDir)
	fmt.Printf("  %s›%s Caddy dir:  %s\n", cCyan, cReset, opts.CaddyDir)
	fmt.Println()
	fmt.Print("  Proceed? [y/N] ")
	var answer string
	fmt.Fscanln(in, &answer)
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y")
}

func printRenameResult(res *RenameResult, dryRun bool) {
	fmt.Println()
	if dryRun {
		fmt.Printf("  %s── DRY RUN — no files written ──%s\n", cYellow, cReset)
	}
	fmt.Printf("  %s✓%s Domain: %s → %s\n", cGreen, cReset, res.OldDomain, res.NewDomain)
	fmt.Printf("  %s✓%s Config:    %s\n", cGreen, cReset, res.ConfigPath)
	fmt.Printf("  %s✓%s Caddyfile: %s (%s mode)\n", cGreen, cReset, res.CaddyfilePath, res.CaddyMode)
	fmt.Printf("  %s✓%s Site configs: %d under %s\n", cGreen, cReset, len(res.SiteFiles), res.SitesDir)
	if !dryRun {
		fmt.Printf("  %s✓%s Backups: *.bak.%s next to each rewritten file\n", cGreen, cReset, res.BackupSuffix)
		if res.Reloaded {
			fmt.Printf("  %s✓%s Caddy reloaded\n", cGreen, cReset)
		} else {
			fmt.Printf("  %s!%s Caddy NOT reloaded — run: systemctl reload caddy\n", cYellow, cReset)
		}
		if res.Restarted {
			fmt.Printf("  %s✓%s openberth daemon restarted (now emitting %s)\n", cGreen, cReset, res.NewDomain)
		} else {
			fmt.Printf("  %s!%s openberth NOT restarted — run: systemctl restart openberth\n", cYellow, cReset)
		}
	}
	fmt.Println()
	fmt.Printf("  Next steps:\n")
	fmt.Printf("    1. Verify DNS: *.%s → this server\n", res.NewDomain)
	if !res.Restarted && !dryRun {
		fmt.Printf("    2. %s\n", res.DaemonHint)
	}
	fmt.Println()
}

// DoRename is the pure-logic entry point. Tests call this directly with
// tmpdir paths; the CLI wraps it with prompts and stdout formatting.
//
// Steps (in order):
//  1. Read config.json, capture old domain + mode + flatURLs.
//  2. Validate the new domain is sane and different from the old.
//  3. List per-deploy site configs under <CaddyDir>/sites/.
//  4. Plan rewrites for: config.json, Caddyfile, every site config.
//  5. If DryRun, return the plan without writing.
//  6. Write each replacement to a temp file; backup the original; rename
//     the temp into place. On any failure, rollback by restoring backups
//     for files already swapped, and return an error.
//  7. Reload Caddy unless SkipReload.
func DoRename(opts RenameOptions) (*RenameResult, error) {
	if opts.NewDomain == "" {
		return nil, fmt.Errorf("new domain is required")
	}
	newDomain := strings.ToLower(strings.TrimSpace(opts.NewDomain))
	if err := validateDomain(newDomain); err != nil {
		return nil, err
	}

	configPath := filepath.Join(opts.DataDir, "config.json")
	caddyfilePath := filepath.Join(opts.CaddyDir, "Caddyfile")
	sitesDir := filepath.Join(opts.CaddyDir, "sites")

	rawCfg, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w (run `berth-server install` first?)", configPath, err)
	}

	oldDomain, mode, flatURLs, err := parseConfigDomain(rawCfg)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if oldDomain == newDomain {
		return nil, fmt.Errorf("new domain matches current domain (%s)", oldDomain)
	}

	// Plan: rewrite config.json
	newCfgBytes, err := rewriteConfigDomain(rawCfg, newDomain)
	if err != nil {
		return nil, fmt.Errorf("rewrite config: %w", err)
	}

	// Plan: rewrite main Caddyfile by substring-replacing the old domain.
	// We deliberately don't re-render from the install template — operators
	// frequently hand-edit the Caddyfile (e.g. switching to `tls internal`
	// for Cloudflare-Full mode, adding custom directives, tightening TLS
	// settings) and a template re-render would silently clobber those
	// customizations. Substring replace updates every occurrence of the
	// old domain (site address, ACME email, redirect targets) and leaves
	// every other line untouched.
	rawCaddyfile, err := os.ReadFile(caddyfilePath)
	if err != nil {
		return nil, fmt.Errorf("read Caddyfile %s: %w (run `berth-server install` first?)", caddyfilePath, err)
	}
	newCaddyfile := rewriteCaddyfileDomain(string(rawCaddyfile), oldDomain, newDomain)

	// Plan: rewrite per-deploy site configs
	siteFiles, err := listSiteConfigs(sitesDir)
	if err != nil {
		return nil, fmt.Errorf("read sites dir %s: %w", sitesDir, err)
	}

	res := &RenameResult{
		OldDomain:     oldDomain,
		NewDomain:     newDomain,
		ConfigPath:    configPath,
		CaddyfilePath: caddyfilePath,
		SitesDir:      sitesDir,
		SiteFiles:     siteFiles,
		CaddyMode:     mode,
		FlatURLs:      flatURLs,
		DaemonHint:    "Restart the daemon: systemctl restart openberth",
	}

	if opts.DryRun {
		return res, nil
	}

	// Apply: write everything, with rollback on failure.
	suffix := timestampSuffix()
	res.BackupSuffix = suffix
	rolledback := []string{}
	rollback := func() {
		for _, p := range rolledback {
			restoreBackup(p, suffix)
		}
	}

	if err := writeWithBackup(configPath, newCfgBytes, 0o600, suffix); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}
	rolledback = append(rolledback, configPath)

	if err := writeWithBackup(caddyfilePath, []byte(newCaddyfile), 0o644, suffix); err != nil {
		rollback()
		return nil, fmt.Errorf("write Caddyfile: %w", err)
	}
	rolledback = append(rolledback, caddyfilePath)

	for _, name := range siteFiles {
		path := filepath.Join(sitesDir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			rollback()
			return nil, fmt.Errorf("read site config %s: %w", name, err)
		}
		newBody := rewriteSiteFQDN(string(body), oldDomain, newDomain)
		// Preserve site-config file mode rather than forcing 0600 — operators
		// running Caddy as a non-root user may have widened modes via
		// proxySiteConfigMode.
		info, err := os.Stat(path)
		if err != nil {
			rollback()
			return nil, fmt.Errorf("stat site config %s: %w", name, err)
		}
		if err := writeWithBackup(path, []byte(newBody), info.Mode().Perm(), suffix); err != nil {
			rollback()
			return nil, fmt.Errorf("write site config %s: %w", name, err)
		}
		rolledback = append(rolledback, path)
	}

	if !opts.SkipReload {
		if err := reloadCaddy(); err != nil {
			// Don't rollback — files are correct, the operator just needs to
			// reload by hand. Surface the error so they know.
			res.Reloaded = false
			return res, fmt.Errorf("files written, but caddy reload failed: %w (run: systemctl reload caddy)", err)
		}
		res.Reloaded = true
	}

	if !opts.SkipRestart {
		// Pull the daemon's listen port from the just-written config —
		// install can in principle override the default 3456, and the
		// post-restart health probe needs to hit the right port.
		port := parseConfigPort(newCfgBytes)
		if err := restartOpenberth(port, newDomain); err != nil {
			// Same rationale as the reload error path: files are correct,
			// the daemon just hasn't picked them up yet. Don't rollback.
			res.Restarted = false
			return res, fmt.Errorf("files written and caddy reloaded, but openberth restart did not become healthy: %w (run: systemctl restart openberth)", err)
		}
		res.Restarted = true
	}

	return res, nil
}

// parseConfigPort pulls the daemon's listen port from raw config.json
// bytes. Returns the install default (3456) if the field is absent — the
// install template always writes it, but defending against a hand-edited
// config that dropped the field costs nothing.
func parseConfigPort(raw []byte) int {
	var probe struct {
		Port int `json:"port"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.Port == 0 {
		return 3456
	}
	return probe.Port
}

// restartOpenberth runs `systemctl restart openberth` and then polls the
// daemon's /health endpoint until it reports the new domain or a deadline
// passes. Reporting only "service started" isn't enough — the daemon
// could come up healthy on the OLD config (e.g. if our config.json write
// was rolled back, or systemd raced) so we explicitly check the domain
// in the response body matches what we just wrote.
func restartOpenberth(port int, expectedDomain string) error {
	if err := exec.Command("systemctl", "restart", "openberth").Run(); err != nil {
		return fmt.Errorf("systemctl restart: %w", err)
	}
	return waitForHealthDomain(port, expectedDomain, 30*time.Second)
}

// waitForHealthDomain polls 127.0.0.1:<port>/health until the response
// includes "domain":"<expected>" or the deadline is reached. The poll
// interval is short (250ms) because typical openberth startup is under
// 3s — we want the rename to feel instant, not a polite 30s wait.
func waitForHealthDomain(port int, expected string, timeout time.Duration) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(250 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var h struct {
			Status string `json:"status"`
			Domain string `json:"domain"`
		}
		if err := json.Unmarshal(body, &h); err == nil &&
			h.Status == "ok" && h.Domain == expected {
			return nil
		}
		lastErr = fmt.Errorf("got %s: %s", resp.Status, strings.TrimSpace(string(body)))
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout")
	}
	return fmt.Errorf("daemon never reported domain=%s within %s: %w", expected, timeout, lastErr)
}

// validateDomain enforces the same shape as install: non-empty, no scheme,
// no trailing slash, no spaces, must contain at least one alphanumeric.
func validateDomain(d string) error {
	if d == "" {
		return fmt.Errorf("domain is empty")
	}
	if strings.Contains(d, "/") {
		return fmt.Errorf("domain must not contain '/' (got %q)", d)
	}
	if strings.Contains(d, " ") {
		return fmt.Errorf("domain must not contain spaces (got %q)", d)
	}
	if strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return fmt.Errorf("domain must not start or end with '.' (got %q)", d)
	}
	if strings.HasPrefix(d, "http://") || strings.HasPrefix(d, "https://") {
		return fmt.Errorf("domain must not include scheme (got %q)", d)
	}
	hasAlnum := false
	for _, r := range d {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			hasAlnum = true
			break
		}
	}
	if !hasAlnum {
		return fmt.Errorf("domain has no alphanumeric characters (got %q)", d)
	}
	return nil
}

// parseConfigDomain extracts the current domain plus the install mode
// inferred from the cloudflareProxy / insecure flags. We don't unmarshal
// into config.Config because we want to preserve unknown fields exactly
// when round-tripping.
func parseConfigDomain(raw []byte) (domain, mode string, flatURLs bool, err error) {
	var probe struct {
		Domain          string `json:"domain"`
		CloudflareProxy bool   `json:"cloudflareProxy"`
		Insecure        bool   `json:"insecure"`
		FlatURLs        bool   `json:"flatUrls"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", "", false, err
	}
	domain = strings.ToLower(strings.TrimSpace(probe.Domain))
	switch {
	case probe.Insecure:
		mode = "insecure"
	case probe.CloudflareProxy:
		mode = "cloudflare"
	default:
		mode = "direct"
	}
	return domain, mode, probe.FlatURLs, nil
}

// rewriteConfigDomain returns the original config bytes with only the
// "domain" field replaced. Other fields (including unknown ones added in
// future releases) are preserved. We round-trip via a generic map so the
// indentation stays consistent with what install wrote.
func rewriteConfigDomain(raw []byte, newDomain string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	m["domain"] = newDomain
	out, err := json.MarshalIndent(m, "", "    ")
	if err != nil {
		return nil, err
	}
	// Preserve trailing newline if the original had one.
	if len(raw) > 0 && raw[len(raw)-1] == '\n' {
		out = append(out, '\n')
	}
	return out, nil
}

// rewriteCaddyfileDomain replaces every occurrence of oldDomain in the
// main Caddyfile with newDomain. Same substring-replace logic as for
// per-deploy site configs (see rewriteSiteFQDN), and for the same reason:
// operators hand-edit their Caddyfile in real installs (custom TLS
// directives, security headers, additional vhosts) and a template
// re-render would lose those edits silently.
//
// Substring replace touches:
//   - the site address line (e.g. `fog.aliib.nl {` → `new.aliib.nl {`)
//   - any `email admin@<old>` directive
//   - any redirect target referencing the old domain
//   - any custom directive that names the old domain
//
// It does NOT touch:
//   - the `import /etc/caddy/sites/*.caddy` line (unrelated)
//   - operator-added `tls internal`, custom headers, etc.
//   - any line that doesn't reference the old domain string
func rewriteCaddyfileDomain(content, oldDomain, newDomain string) string {
	return strings.ReplaceAll(content, oldDomain, newDomain)
}

// rewriteSiteFQDN replaces every occurrence of oldDomain inside a site
// config with newDomain. Both nested (<sub>.<old>) and flat
// (<sub>-<workspace-of-old>.<apex>) shapes embed the old domain as a
// contiguous suffix, so a plain string-replace handles both:
//
//   nested: "blog.acme.example.com" → "blog.team.example.com"
//   flat:   "blog-acme.example.com" → "blog-team.example.com"
//
// Edge cases:
//   - Custom edits referring to the old domain elsewhere in the file
//     (e.g. an upstream URL) get rewritten too. That's the right call —
//     if the operator is replacing the install domain wholesale, any
//     other reference to it in their per-deploy config almost certainly
//     should follow.
//   - Deploys whose old FQDN happened to be a substring of an unrelated
//     URL would be at risk in principle, but the install domain is the
//     install domain — collisions are vanishingly unlikely in practice.
func rewriteSiteFQDN(content, oldDomain, newDomain string) string {
	return strings.ReplaceAll(content, oldDomain, newDomain)
}

// listSiteConfigs returns the basenames (e.g. "blog.caddy") of every
// .caddy file in the sites dir. Order is deterministic (sorted) so dry-
// run output and tests are stable.
func listSiteConfigs(sitesDir string) ([]string, error) {
	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".caddy" {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

// writeWithBackup atomically replaces `path` with `content`, leaving the
// previous bytes at `path.bak.<suffix>`. Atomicity comes from writing to
// path.tmp.<suffix> first and renaming — same volume so rename is atomic.
func writeWithBackup(path string, content []byte, mode os.FileMode, suffix string) error {
	if existing, err := os.ReadFile(path); err == nil {
		bak := path + ".bak." + suffix
		if err := os.WriteFile(bak, existing, mode); err != nil {
			return fmt.Errorf("backup %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing %s: %w", path, err)
	}
	tmp := path + ".tmp." + suffix
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// restoreBackup undoes one writeWithBackup call. Best-effort: errors are
// silently ignored because we're already in a failure path.
func restoreBackup(path, suffix string) {
	bak := path + ".bak." + suffix
	if data, err := os.ReadFile(bak); err == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
}

func timestampSuffix() string {
	// Mirror `date +%s` — short enough to not pollute filenames, unique
	// enough across hand-rerun within the same operator session.
	return fmt.Sprintf("%d", nowUnix())
}

// nowUnix is a seam so tests can pin a deterministic suffix.
var nowUnix = func() int64 {
	return time.Now().Unix()
}

func reloadCaddy() error {
	if err := exec.Command("systemctl", "reload", "caddy").Run(); err == nil {
		return nil
	}
	return exec.Command("caddy", "reload", "--config", "/etc/caddy/Caddyfile").Run()
}
