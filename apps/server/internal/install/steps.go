package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Each method below is one step executed in order by (*provisioner).runAll.
// Step numbering matches the `progress` argument to p.emit and the total in
// install.go's const totalSteps.

// Step 1: Verify running as root
func (p *provisioner) checkRoot() error {
	out, err := runCmd("id -u")
	if err != nil {
		return fmt.Errorf("failed to check user: %w", err)
	}
	if strings.TrimSpace(out) != "0" {
		return fmt.Errorf("must run as root (got uid=%s)", out)
	}
	p.emit("check_root", StepCompleted, "Root access verified", "", 1)
	return nil
}

// Step 2: Install system packages
func (p *provisioner) installPackages() error {
	_, err := runCmd("DEBIAN_FRONTEND=noninteractive apt-get update -qq && apt-get install -y -qq ca-certificates curl gnupg jq sqlite3 dnsutils >/dev/null 2>&1")
	if err != nil {
		return fmt.Errorf("apt-get install: %w", err)
	}
	p.emit("install_packages", StepCompleted, "System packages installed", "", 2)
	return nil
}

// Step 3: Install Docker
func (p *provisioner) installDocker() error {
	if out, _ := runCmd("command -v docker"); out != "" {
		p.emit("install_docker", StepCompleted, "Docker already installed", "", 3)
		return nil
	}

	cmds := []string{
		"install -m 0755 -d /etc/apt/keyrings",
		"curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg 2>/dev/null",
		"chmod a+r /etc/apt/keyrings/docker.gpg",
		`echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list`,
		"apt-get update -qq",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin >/dev/null 2>&1",
		"systemctl enable --now docker",
	}

	for _, cmd := range cmds {
		if _, err := runCmd(cmd); err != nil {
			return fmt.Errorf("docker install: %w", err)
		}
	}

	p.emit("install_docker", StepCompleted, "Docker installed", "", 3)
	return nil
}

// Step 4: Install gVisor
func (p *provisioner) installGVisor() error {
	if out, _ := runCmd("command -v runsc"); out != "" {
		p.emit("install_gvisor", StepCompleted, "gVisor already installed", "", 4)
		return nil
	}

	cmds := []string{
		`ARCH=$(uname -m) && URL="https://storage.googleapis.com/gvisor/releases/release/latest/${ARCH}" && curl -fsSL "${URL}/runsc" -o /usr/local/bin/runsc && curl -fsSL "${URL}/containerd-shim-runsc-v1" -o /usr/local/bin/containerd-shim-runsc-v1 && chmod +x /usr/local/bin/runsc /usr/local/bin/containerd-shim-runsc-v1`,
	}

	for _, cmd := range cmds {
		if _, err := runCmd(cmd); err != nil {
			return fmt.Errorf("gvisor install: %w", err)
		}
	}

	if err := writeFile("/etc/docker/daemon.json", daemonJSONTemplate, 0644); err != nil {
		return fmt.Errorf("write daemon.json: %w", err)
	}

	if _, err := runCmd("systemctl restart docker"); err != nil {
		return fmt.Errorf("restart docker: %w", err)
	}

	p.emit("install_gvisor", StepCompleted, "gVisor installed and registered", "", 4)
	return nil
}

// Step 5: Test gVisor runtime
func (p *provisioner) testGVisor() error {
	_, err := runCmd("docker run --rm --runtime=runsc hello-world >/dev/null 2>&1")
	if err != nil {
		p.emit("test_gvisor", StepWarning, "gVisor test failed", "will fall back to runc — check KVM/CPU support", 5)
		return nil // Non-fatal
	}
	p.emit("test_gvisor", StepCompleted, "gVisor runtime verified", "", 5)
	return nil
}

// Step 6: Install Caddy
func (p *provisioner) installCaddy() error {
	if out, _ := runCmd("command -v caddy"); out != "" {
		p.emit("install_caddy", StepCompleted, "Caddy already installed", "", 6)
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
		if _, err := runCmd(cmd); err != nil {
			return fmt.Errorf("caddy install: %w", err)
		}
	}

	p.emit("install_caddy", StepCompleted, "Caddy installed", "", 6)
	return nil
}

// Step 7: Pull base Docker images
func (p *provisioner) pullImages() error {
	if _, err := runCmd("docker pull node:20-slim -q && docker pull caddy:2-alpine -q"); err != nil {
		return fmt.Errorf("pull images: %w", err)
	}
	p.emit("pull_images", StepCompleted, "Base images pulled", "", 7)
	return nil
}

// Step 8: Create data directories
func (p *provisioner) createDirectories() error {
	if _, err := runCmd(fmt.Sprintf("mkdir -p %s/{deploys,uploads,persist} /etc/caddy/sites", dataDir)); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	p.emit("create_directories", StepCompleted, "Data directories created", "", 8)
	return nil
}

// Step 9: Create Docker volumes
func (p *provisioner) createVolumes() error {
	if _, err := runCmd("docker volume create openberth-npm-cache >/dev/null 2>&1 || true"); err != nil {
		return fmt.Errorf("create volume: %w", err)
	}
	p.emit("create_volumes", StepCompleted, "Docker volumes created", "", 9)
	return nil
}

// Step 10: Write config.json
func (p *provisioner) writeConfig() error {
	tmpl := configJSONTemplate
	if p.cfg.Insecure {
		tmpl = configJSONInsecureTemplate
	} else if p.cfg.CloudflareProxy {
		tmpl = configJSONCloudflareTemplate
	}
	content := fmt.Sprintf(tmpl, p.cfg.Domain, p.cfg.DefaultTTL, p.cfg.MaxDeploys, p.cfg.WebDisabled)
	if err := writeFile(dataDir+"/config.json", content, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	p.emit("write_config", StepCompleted, "Configuration written", "", 10)
	return nil
}

// Step 11: Initialize SQLite database
func (p *provisioner) initDatabase() error {
	adminID := "usr_" + randomHex(8)
	passwordHash := hashPassword(p.cfg.AdminPassword)
	sql := fmt.Sprintf(dbInitSQLTemplate, p.cfg.MaxDeploys, p.cfg.DefaultTTL, adminID, p.cfg.AdminKey, passwordHash, p.cfg.DefaultTTL)

	escaped := strings.ReplaceAll(sql, "'", "'\\''")
	cmd := fmt.Sprintf("sqlite3 %s/openberth.db '%s'", dataDir, escaped)
	if _, err := runCmd(cmd); err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	p.emit("init_database", StepCompleted, "Database initialized", "", 11)
	return nil
}

// Step 12: Write Caddyfile
func (p *provisioner) writeCaddyfile() error {
	var content string
	if p.cfg.Insecure {
		content = fmt.Sprintf(caddyfileInsecureTemplate, p.cfg.Domain)
	} else if p.cfg.CloudflareProxy {
		content = fmt.Sprintf(caddyfileCloudflareTemplate, p.cfg.Domain)
	} else {
		content = fmt.Sprintf(caddyfileTemplate, p.cfg.Domain, p.cfg.Domain)
	}
	if err := writeFile("/etc/caddy/Caddyfile", content, 0644); err != nil {
		return fmt.Errorf("write Caddyfile: %w", err)
	}
	p.emit("write_caddyfile", StepCompleted, "Caddy configuration written", "", 12)
	return nil
}

// Step 13: Install server binary to /usr/local/bin/berth-server
const installPath = "/usr/local/bin/berth-server"

func (p *provisioner) verifyBinary() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}
	// Resolve symlinks to get the real path
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("cannot resolve binary path: %w", err)
	}

	// If already at the install path, nothing to do
	if exe == installPath {
		p.emit("verify_binary", StepCompleted, "Server binary already at "+installPath, "", 13)
		return nil
	}

	// Copy the running binary to the install path
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

	p.emit("verify_binary", StepCompleted, fmt.Sprintf("Server binary installed to %s (copied from %s)", installPath, exe), "", 13)
	return nil
}

// Step 14: Write admin CLI helper script
func (p *provisioner) writeAdminScript() error {
	if err := writeFile("/usr/local/bin/berth-admin", adminScriptTemplate, 0755); err != nil {
		return fmt.Errorf("write admin script: %w", err)
	}
	p.emit("write_admin_script", StepCompleted, "Admin script installed", "", 14)
	return nil
}

// Step 15: Write systemd service file
func (p *provisioner) writeSystemdService() error {
	content := fmt.Sprintf(systemdServiceTemplate, dataDir)
	if err := writeFile("/etc/systemd/system/openberth.service", content, 0644); err != nil {
		return fmt.Errorf("write systemd service: %w", err)
	}
	p.emit("write_systemd_service", StepCompleted, "Systemd service written", "", 15)
	return nil
}

// Step 16: Enable and start services
func (p *provisioner) enableServices() error {
	cmds := []string{
		"systemctl daemon-reload",
		"systemctl enable --now caddy",
		"systemctl reload caddy 2>/dev/null || true",
		"systemctl enable openberth",
		"systemctl restart openberth 2>/dev/null || true",
	}
	for _, cmd := range cmds {
		runCmd(cmd) // Best-effort for reload/restart
	}
	p.emit("enable_services", StepCompleted, "Services enabled and started", "", 16)
	return nil
}

// Step 17: Configure firewall (if UFW present)
func (p *provisioner) configureFirewall() error {
	out, _ := runCmd("command -v ufw")
	if out == "" {
		p.emit("configure_firewall", StepCompleted, "No firewall detected", "skipping UFW configuration", 17)
		return nil
	}

	cmds := []string{
		"ufw allow 80/tcp >/dev/null 2>&1",
		"ufw allow 22/tcp >/dev/null 2>&1",
	}
	if !p.cfg.Insecure {
		cmds = append(cmds, "ufw allow 443/tcp >/dev/null 2>&1")
	}
	for _, cmd := range cmds {
		runCmd(cmd) // Best-effort
	}
	if p.cfg.Insecure {
		p.emit("configure_firewall", StepCompleted, "Firewall rules added (22, 80)", "", 17)
	} else {
		p.emit("configure_firewall", StepCompleted, "Firewall rules added (22, 80, 443)", "", 17)
	}
	return nil
}

// Step 18: Verify DNS resolution
func (p *provisioner) verifyDNS() error {
	serverIP, _ := runCmd("curl -s -4 ifconfig.me 2>/dev/null")
	resolvedIP, _ := runCmd(fmt.Sprintf("dig +short %s 2>/dev/null | head -1", p.cfg.Domain))

	serverIP = strings.TrimSpace(serverIP)
	resolvedIP = strings.TrimSpace(resolvedIP)

	if resolvedIP == "" {
		p.emit("verify_dns", StepWarning, "DNS not resolving yet", fmt.Sprintf("set A record for %s → %s", p.cfg.Domain, serverIP), 18)
		return nil
	}

	if resolvedIP != serverIP {
		detail := fmt.Sprintf("%s resolves to %s, but server is %s", p.cfg.Domain, resolvedIP, serverIP)
		if isCloudflareIP(resolvedIP) {
			if p.cfg.CloudflareProxy {
				p.emit("verify_dns", StepCompleted, "DNS OK via Cloudflare proxy", "", 18)
				return nil
			}
			detail += " — looks like Cloudflare, switch to DNS-only (gray cloud)"
		}
		p.emit("verify_dns", StepWarning, "DNS mismatch", detail, 18)
		return nil
	}

	p.emit("verify_dns", StepCompleted, fmt.Sprintf("DNS OK: %s → %s", p.cfg.Domain, serverIP), "", 18)
	return nil
}

// Step 19: Health check
func (p *provisioner) healthCheck() error {
	runCmd("sleep 2")
	out, err := runCmd("curl -s http://127.0.0.1:3456/health 2>/dev/null")
	if err != nil || !strings.Contains(out, "ok") {
		p.emit("health_check", StepWarning, "Health check failed", "check: journalctl -u openberth -n 20", 19)
		return nil // Non-fatal
	}
	p.emit("health_check", StepCompleted, "OpenBerth daemon is healthy", "", 19)
	return nil
}

// Step 20: Print summary
func (p *provisioner) printSummary() error {
	p.emit("print_summary", StepCompleted, "Setup complete", "", 20)
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
