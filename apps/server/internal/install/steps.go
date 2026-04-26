package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// This file contains only universal install steps — those that run
// regardless of which runtime driver is selected. Driver-specific steps
// (install Docker, pull images, etc) live in the driver's own package
// and are contributed via install.Register.
//
// Step ordering runs in four phases:
//   preflight  → driver.Steps() → infra → activation

// preflightSteps run before the driver steps: verify environment and lay
// down OS packages that the driver install might depend on (curl, gpg,
// ca-certs, sqlite3).
func preflightSteps() []Step {
	return []Step{
		{Name: "check_root", Description: "Verifying root access", Run: checkRoot},
		{Name: "install_packages", Description: "Installing system packages", Run: installPackages},
	}
}

// infraSteps run after the driver is ready: install Caddy, lay out data
// dirs, write config and Caddyfile, init DB, install binary and admin
// script, write systemd unit. All runtime-agnostic.
func infraSteps() []Step {
	return []Step{
		{Name: "install_caddy", Description: "Installing Caddy web server", Run: installCaddy},
		{Name: "create_directories", Description: "Creating data directories", Run: createDirectories},
		{Name: "write_config", Description: "Writing OpenBerth configuration", Run: writeConfig},
		{Name: "init_database", Description: "Initializing database", Run: initDatabase},
		{Name: "write_caddyfile", Description: "Writing Caddy configuration", Run: writeCaddyfile},
		{Name: "verify_binary", Description: "Installing server binary", Run: verifyBinary},
		{Name: "write_admin_script", Description: "Writing admin CLI script", Run: writeAdminScript},
		{Name: "write_systemd_service", Description: "Writing systemd service", Run: writeSystemdService},
	}
}

// activationSteps enable services, tighten firewall, and run the final
// health checks.
func activationSteps() []Step {
	return []Step{
		{Name: "enable_services", Description: "Enabling and starting services", Run: enableServices},
		{Name: "configure_firewall", Description: "Configuring firewall", Run: configureFirewall},
		{Name: "verify_dns", Description: "Verifying DNS records", Run: verifyDNS},
		{Name: "health_check", Description: "Running health check", Run: healthCheck},
		{Name: "print_summary", Description: "Setup complete", Run: printSummary},
	}
}

// ── Preflight ───────────────────────────────────────────────────────

func checkRoot(ctx *Ctx) error {
	out, err := ctx.Cmd("id -u")
	if err != nil {
		return fmt.Errorf("failed to check user: %w", err)
	}
	if strings.TrimSpace(out) != "0" {
		return fmt.Errorf("must run as root (got uid=%s)", out)
	}
	ctx.Done("Root access verified")
	return nil
}

func installPackages(ctx *Ctx) error {
	_, err := ctx.Cmd("DEBIAN_FRONTEND=noninteractive apt-get update -qq && apt-get install -y -qq ca-certificates curl gnupg jq sqlite3 dnsutils >/dev/null 2>&1")
	if err != nil {
		return fmt.Errorf("apt-get install: %w", err)
	}
	ctx.Done("System packages installed")
	return nil
}

// ── Infra ───────────────────────────────────────────────────────────

func installCaddy(ctx *Ctx) error {
	if out, _ := ctx.Cmd("command -v caddy"); out != "" {
		ctx.Done("Caddy already installed")
		return nil
	}

	cmds := []string{
		"DEBIAN_FRONTEND=noninteractive apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https >/dev/null 2>&1",
		"curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' 2>/dev/null | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg 2>/dev/null",
		"curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' 2>/dev/null | tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null",
		"apt-get update -qq",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y -qq caddy >/dev/null 2>&1",
	}

	for _, cmd := range cmds {
		if _, err := ctx.Cmd(cmd); err != nil {
			return fmt.Errorf("caddy install: %w", err)
		}
	}
	ctx.Done("Caddy installed")
	return nil
}

func createDirectories(ctx *Ctx) error {
	if _, err := ctx.Cmd(fmt.Sprintf("mkdir -p %s/{deploys,uploads,persist} /etc/caddy/sites", dataDir)); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	ctx.Done("Data directories created")
	return nil
}

func writeConfig(ctx *Ctx) error {
	cfg := ctx.Config()
	tmpl := configJSONTemplate
	if cfg.Insecure {
		tmpl = configJSONInsecureTemplate
	} else if cfg.CloudflareProxy {
		tmpl = configJSONCloudflareTemplate
	}
	content := fmt.Sprintf(tmpl, cfg.Domain, cfg.DefaultTTL, cfg.MaxDeploys, cfg.WebDisabled, cfg.FlatURLs)
	if err := ctx.Write(dataDir+"/config.json", content, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	ctx.Done("Configuration written")
	return nil
}

func initDatabase(ctx *Ctx) error {
	cfg := ctx.Config()
	adminID := "usr_" + randomHex(8)
	passwordHash := hashPassword(cfg.AdminPassword)
	sql := fmt.Sprintf(dbInitSQLTemplate, cfg.MaxDeploys, cfg.DefaultTTL, adminID, cfg.AdminKey, passwordHash, cfg.DefaultTTL)

	escaped := strings.ReplaceAll(sql, "'", "'\\''")
	cmd := fmt.Sprintf("sqlite3 %s/openberth.db '%s'", dataDir, escaped)
	if _, err := ctx.Cmd(cmd); err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	ctx.Done("Database initialized")
	return nil
}

func writeCaddyfile(ctx *Ctx) error {
	cfg := ctx.Config()
	var content string
	if cfg.Insecure {
		content = fmt.Sprintf(caddyfileInsecureTemplate, cfg.Domain)
	} else if cfg.CloudflareProxy {
		content = fmt.Sprintf(caddyfileCloudflareTemplate, cfg.Domain)
	} else {
		content = fmt.Sprintf(caddyfileTemplate, cfg.Domain, cfg.Domain)
	}
	if err := ctx.Write("/etc/caddy/Caddyfile", content, 0644); err != nil {
		return fmt.Errorf("write Caddyfile: %w", err)
	}
	ctx.Done("Caddy configuration written")
	return nil
}

const installPath = "/usr/local/bin/berth-server"

func verifyBinary(ctx *Ctx) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("cannot resolve binary path: %w", err)
	}

	if exe == installPath {
		ctx.Done("Server binary already at " + installPath)
		return nil
	}

	src, err := os.Open(exe)
	if err != nil {
		return fmt.Errorf("cannot open current binary %s: %w", exe, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(installPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", installPath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy binary to %s: %w", installPath, err)
	}

	ctx.Done(fmt.Sprintf("Server binary installed to %s (copied from %s)", installPath, exe))
	return nil
}

func writeAdminScript(ctx *Ctx) error {
	if err := ctx.Write("/usr/local/bin/berth-admin", adminScriptTemplate, 0755); err != nil {
		return fmt.Errorf("write admin script: %w", err)
	}
	ctx.Done("Admin script installed")
	return nil
}

func writeSystemdService(ctx *Ctx) error {
	content := fmt.Sprintf(systemdServiceTemplate, dataDir)
	if err := ctx.Write("/etc/systemd/system/openberth.service", content, 0644); err != nil {
		return fmt.Errorf("write systemd service: %w", err)
	}
	ctx.Done("Systemd service written")
	return nil
}

// ── Activation ──────────────────────────────────────────────────────

func enableServices(ctx *Ctx) error {
	cmds := []string{
		"systemctl daemon-reload",
		"systemctl enable --now caddy",
		"systemctl reload caddy 2>/dev/null || true",
		"systemctl enable openberth",
		"systemctl restart openberth 2>/dev/null || true",
	}
	for _, cmd := range cmds {
		ctx.Cmd(cmd) // best-effort
	}
	ctx.Done("Services enabled and started")
	return nil
}

func configureFirewall(ctx *Ctx) error {
	out, _ := ctx.Cmd("command -v ufw")
	if out == "" {
		ctx.Done("No firewall detected (UFW)")
		return nil
	}

	cmds := []string{
		"ufw allow 80/tcp >/dev/null 2>&1",
		"ufw allow 22/tcp >/dev/null 2>&1",
	}
	if !ctx.Config().Insecure {
		cmds = append(cmds, "ufw allow 443/tcp >/dev/null 2>&1")
	}
	for _, cmd := range cmds {
		ctx.Cmd(cmd) // best-effort
	}
	if ctx.Config().Insecure {
		ctx.Done("Firewall rules added (22, 80)")
	} else {
		ctx.Done("Firewall rules added (22, 80, 443)")
	}
	return nil
}

func verifyDNS(ctx *Ctx) error {
	cfg := ctx.Config()
	serverIP, _ := ctx.Cmd("curl -s -4 ifconfig.me 2>/dev/null")
	resolvedIP, _ := ctx.Cmd(fmt.Sprintf("dig +short %s 2>/dev/null | head -1", cfg.Domain))

	serverIP = strings.TrimSpace(serverIP)
	resolvedIP = strings.TrimSpace(resolvedIP)

	if resolvedIP == "" {
		ctx.Warn("DNS not resolving yet", fmt.Sprintf("set A record for %s → %s", cfg.Domain, serverIP))
		return nil
	}

	if resolvedIP != serverIP {
		detail := fmt.Sprintf("%s resolves to %s, but server is %s", cfg.Domain, resolvedIP, serverIP)
		if isCloudflareIP(resolvedIP) {
			if cfg.CloudflareProxy {
				ctx.Done("DNS OK via Cloudflare proxy")
				return nil
			}
			detail += " — looks like Cloudflare, switch to DNS-only (gray cloud)"
		}
		ctx.Warn("DNS mismatch", detail)
		return nil
	}

	ctx.Done(fmt.Sprintf("DNS OK: %s → %s", cfg.Domain, serverIP))
	return nil
}

func healthCheck(ctx *Ctx) error {
	ctx.Cmd("sleep 2")
	out, err := ctx.Cmd("curl -s http://127.0.0.1:3456/health 2>/dev/null")
	if err != nil || !strings.Contains(out, "ok") {
		ctx.Warn("Health check failed", "check: journalctl -u openberth -n 20")
		return nil
	}
	ctx.Done("OpenBerth daemon is healthy")
	return nil
}

func printSummary(ctx *Ctx) error {
	ctx.Done("Setup complete")
	return nil
}

// isCloudflareIP checks whether an IP falls in one of Cloudflare's public
// edge ranges. Used by verifyDNS to disambiguate "pointing at Cloudflare"
// from "pointing at the wrong server".
func isCloudflareIP(ip string) bool {
	prefixes := []string{"104.", "172.64.", "172.65.", "172.66.", "172.67.", "103.21.", "103.22.", "103.31.", "141.101.", "108.162.", "190.93.", "188.114.", "197.234.", "198.41.", "162.158."}
	for _, p := range prefixes {
		if strings.HasPrefix(ip, p) {
			return true
		}
	}
	return false
}
