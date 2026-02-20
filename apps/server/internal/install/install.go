package install

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const totalSteps = 20
const dataDir = "/var/lib/openberth"

// StepStatus represents the current state of a provisioning step.
type StepStatus string

const (
	StepRunning   StepStatus = "running"
	StepCompleted StepStatus = "completed"
	StepWarning   StepStatus = "warning"
	StepFailed    StepStatus = "failed"
)

// Event represents a state change during provisioning.
type Event struct {
	Step     string
	Status   StepStatus
	Message  string
	Detail   string
	Progress int
	Total    int
}

// EventHandler is called for every state change during provisioning.
type EventHandler func(Event)

// Config holds all configuration for a local provisioning run.
type Config struct {
	Domain          string
	AdminKey        string
	AdminPassword   string
	CloudflareProxy bool
	Insecure        bool
	MaxDeploys      int
	DefaultTTL      int
}

func (c *Config) setDefaults() {
	if c.MaxDeploys == 0 {
		c.MaxDeploys = 10
	}
	if c.DefaultTTL == 0 {
		c.DefaultTTL = 72
	}
	if c.AdminKey == "" {
		c.AdminKey = generateKey()
	}
	if c.AdminPassword == "" {
		c.AdminPassword = generatePassword()
	}
}

func (c *Config) validate() error {
	if c.Domain == "" {
		return fmt.Errorf("--domain is required")
	}
	if c.Insecure && c.CloudflareProxy {
		return fmt.Errorf("--insecure and --cloudflare are mutually exclusive")
	}
	return nil
}

// provisioner runs the 20-step provisioning sequence locally.
type provisioner struct {
	cfg     *Config
	onEvent EventHandler
}

func (p *provisioner) emit(step string, status StepStatus, msg, detail string, progress int) {
	if p.onEvent != nil {
		p.onEvent(Event{
			Step:     step,
			Status:   status,
			Message:  msg,
			Detail:   detail,
			Progress: progress,
			Total:    totalSteps,
		})
	}
}

// runAll executes all provisioning steps in order.
func (p *provisioner) runAll() error {
	steps := []struct {
		name string
		fn   func() error
	}{
		{"check_root", p.checkRoot},
		{"install_packages", p.installPackages},
		{"install_docker", p.installDocker},
		{"install_gvisor", p.installGVisor},
		{"test_gvisor", p.testGVisor},
		{"install_caddy", p.installCaddy},
		{"pull_images", p.pullImages},
		{"create_directories", p.createDirectories},
		{"create_volumes", p.createVolumes},
		{"write_config", p.writeConfig},
		{"init_database", p.initDatabase},
		{"write_caddyfile", p.writeCaddyfile},
		{"verify_binary", p.verifyBinary},
		{"write_admin_script", p.writeAdminScript},
		{"write_systemd_service", p.writeSystemdService},
		{"enable_services", p.enableServices},
		{"configure_firewall", p.configureFirewall},
		{"verify_dns", p.verifyDNS},
		{"health_check", p.healthCheck},
		{"print_summary", p.printSummary},
	}

	for i, s := range steps {
		step := i + 1
		p.emit(s.name, StepRunning, stepMessage(s.name), "", step)
		if err := s.fn(); err != nil {
			p.emit(s.name, StepFailed, stepMessage(s.name), err.Error(), step)
			return fmt.Errorf("step %d/%d (%s): %w", step, totalSteps, s.name, err)
		}
	}

	return nil
}

func stepMessage(name string) string {
	messages := map[string]string{
		"check_root":            "Verifying root access",
		"install_packages":      "Installing system packages",
		"install_docker":        "Installing Docker",
		"install_gvisor":        "Installing gVisor sandbox runtime",
		"test_gvisor":           "Testing gVisor runtime",
		"install_caddy":         "Installing Caddy web server",
		"pull_images":           "Pulling base Docker images",
		"create_directories":    "Creating data directories",
		"create_volumes":        "Creating Docker volumes",
		"write_config":          "Writing OpenBerth configuration",
		"init_database":         "Initializing database",
		"write_caddyfile":       "Writing Caddy configuration",
		"verify_binary":         "Installing server binary",
		"write_admin_script":    "Writing admin CLI script",
		"write_systemd_service": "Writing systemd service",
		"enable_services":       "Enabling and starting services",
		"configure_firewall":    "Configuring firewall",
		"verify_dns":            "Verifying DNS records",
		"health_check":          "Running health check",
		"print_summary":         "Setup complete",
	}
	if msg, ok := messages[name]; ok {
		return msg
	}
	return name
}

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
	content := fmt.Sprintf(tmpl, p.cfg.Domain, p.cfg.DefaultTTL, p.cfg.MaxDeploys)
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

// ── CLI output helpers ──────────────────────────────────────────────────

const (
	cReset  = "\033[0m"
	cGreen  = "\033[32m"
	cRed    = "\033[31m"
	cYellow = "\033[33m"
	cCyan   = "\033[36m"
	cBold   = "\033[1m"
)

func cliOK(msg string)   { fmt.Printf("  %s✓%s %s\n", cGreen, cReset, msg) }
func cliFail(msg string) { fmt.Fprintf(os.Stderr, "  %s✗%s %s\n", cRed, cReset, msg) }
func cliWarn(msg string) { fmt.Printf("  %s!%s %s\n", cYellow, cReset, msg) }
func cliSpin(msg string) { fmt.Printf("  %s⟳%s %s...", cYellow, cReset, msg) }
func cliDone()           { fmt.Println(" done") }

func printResult(domain, adminKey, adminPassword string, cloudflare, insecure bool) {
	serverIP, _ := runCmd("curl -s -4 ifconfig.me 2>/dev/null")
	serverIP = strings.TrimSpace(serverIP)
	if serverIP == "" {
		serverIP = "<server-ip>"
	}

	scheme := "https"
	if insecure {
		scheme = "http"
	}

	mode := "Direct (Let's Encrypt)"
	if insecure {
		mode = "Insecure (HTTP only)"
	} else if cloudflare {
		mode = "Cloudflare Proxy"
	}

	fmt.Println()
	fmt.Printf("  %s╔══════════════════════════════════════════════════════════╗%s\n", cGreen, cReset)
	fmt.Printf("  %s║  ⚓ OpenBerth is ready!                                ║%s\n", cGreen, cReset)
	fmt.Printf("  %s╠══════════════════════════════════════════════════════════╣%s\n", cGreen, cReset)
	fmt.Printf("  %s║%s                                                          %s║%s\n", cGreen, cReset, cGreen, cReset)
	fmt.Printf("  %s║%s  Domain:    %s%-40s%s  %s║%s\n", cGreen, cReset, cCyan, domain, cReset, cGreen, cReset)
	fmt.Printf("  %s║%s  Admin key: %s%-40s%s  %s║%s\n", cGreen, cReset, cYellow, adminKey, cReset, cGreen, cReset)
	fmt.Printf("  %s║%s  Password:  %s%-40s%s  %s║%s\n", cGreen, cReset, cYellow, adminPassword, cReset, cGreen, cReset)
	fmt.Printf("  %s║%s  Server:    %-40s  %s║%s\n", cGreen, cReset, serverIP, cGreen, cReset)
	fmt.Printf("  %s║%s  Mode:      %-40s  %s║%s\n", cGreen, cReset, mode, cGreen, cReset)
	fmt.Printf("  %s║%s                                                          %s║%s\n", cGreen, cReset, cGreen, cReset)
	if cloudflare {
		fmt.Printf("  %s║%s  DNS (Cloudflare orange cloud OK):                       %s║%s\n", cGreen, cReset, cGreen, cReset)
	} else {
		fmt.Printf("  %s║%s  DNS Required:                                           %s║%s\n", cGreen, cReset, cGreen, cReset)
	}
	fmt.Printf("  %s║%s    A   %s → server IP               %s║%s\n", cGreen, cReset, domain, cGreen, cReset)
	fmt.Printf("  %s║%s    A   *.%s → server IP             %s║%s\n", cGreen, cReset, domain, cGreen, cReset)
	fmt.Printf("  %s║%s                                                          %s║%s\n", cGreen, cReset, cGreen, cReset)
	fmt.Printf("  %s║%s  Next steps:                                             %s║%s\n", cGreen, cReset, cGreen, cReset)
	fmt.Printf("  %s║%s    berth config set server %s://%s  %s║%s\n", cGreen, cReset, scheme, domain, cGreen, cReset)
	fmt.Printf("  %s║%s    berth config set key %s...            %s║%s\n", cGreen, cReset, adminKey[:min(20, len(adminKey))], cGreen, cReset)
	fmt.Printf("  %s║%s    Login at %s://%s/gallery/          %s║%s\n", cGreen, cReset, scheme, domain, cGreen, cReset)
	fmt.Printf("  %s║%s                                                          %s║%s\n", cGreen, cReset, cGreen, cReset)
	fmt.Printf("  %s╚══════════════════════════════════════════════════════════╝%s\n", cGreen, cReset)
	fmt.Println()
}

// ── Run is the entry point called from main.go ──────────────────────────

// Run parses flags and executes the install sequence.
func Run(args []string) {
	fs := flag.NewFlagSet("berth-server install", flag.ExitOnError)
	cfg := &Config{}

	fs.StringVar(&cfg.Domain, "domain", "", "OpenBerth domain (required)")
	fs.StringVar(&cfg.AdminKey, "admin-key", "", "Admin API key (auto-generated if omitted)")
	fs.IntVar(&cfg.MaxDeploys, "max-deploys", 0, "Max deployments per user (default: 10)")
	fs.IntVar(&cfg.DefaultTTL, "default-ttl", 0, "Default TTL hours (default: 72)")
	fs.BoolVar(&cfg.CloudflareProxy, "cloudflare", false, "Use Cloudflare proxy mode (no ACME, internal TLS)")
	fs.BoolVar(&cfg.Insecure, "insecure", false, "Run without SSL/TLS (HTTP only)")

	fs.Usage = func() {
		fmt.Printf(`
  %s⚓ OpenBerth Server Install%s — Provision this server

  %sUSAGE%s
    berth-server install [options]

  %sEXAMPLE%s
    berth-server install --domain openberth.example.com
    berth-server install --domain local.dev --insecure

  %sOPTIONS%s
    --domain <domain>       OpenBerth domain (required)
    --admin-key <key>       Admin API key (auto-generated if omitted)
    --max-deploys <n>       Max deployments per user (default: 10)
    --default-ttl <hours>   Default TTL hours (default: 72)
    --cloudflare            Use Cloudflare proxy mode (no ACME, internal TLS)
    --insecure              Run without SSL/TLS (HTTP only)
`, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset)
	}

	fs.Parse(args)

	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		cliFail(err.Error())
		fs.Usage()
		os.Exit(1)
	}

	fmt.Printf("\n  %s⚓ OpenBerth Server Install%s\n\n", cBold, cReset)
	fmt.Printf("  %s›%s Domain: %s\n", cCyan, cReset, cfg.Domain)
	fmt.Println()

	prov := &provisioner{
		cfg: cfg,
		onEvent: func(ev Event) {
			switch ev.Status {
			case StepRunning:
				cliSpin(ev.Message)
			case StepCompleted:
				cliDone()
				cliOK(ev.Message)
			case StepWarning:
				cliDone()
				if ev.Detail != "" {
					cliWarn(ev.Message + ": " + ev.Detail)
				} else {
					cliWarn(ev.Message)
				}
			case StepFailed:
				cliDone()
				if ev.Detail != "" {
					cliFail(ev.Message + ": " + ev.Detail)
				} else {
					cliFail(ev.Message)
				}
			}
		},
	}

	if err := prov.runAll(); err != nil {
		fmt.Println()
		cliFail(fmt.Sprintf("Installation failed: %v", err))
		os.Exit(1)
	}

	printResult(cfg.Domain, cfg.AdminKey, cfg.AdminPassword, cfg.CloudflareProxy, cfg.Insecure)
}

// ── Utility functions ────────────────────────────────────────────────────

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func generateKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return "sc_" + hex.EncodeToString(b)
}

func generatePassword() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 16)
	for i := range result {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		result[i] = chars[n.Int64()]
	}
	return string(result)
}

func hashPassword(password string) string {
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash)
}

func isCloudflareIP(ip string) bool {
	prefixes := []string{"104.", "172.64.", "172.65.", "172.66.", "172.67.", "103.21.", "103.22.", "103.31.", "141.101.", "108.162.", "190.93.", "188.114.", "197.234.", "198.41.", "162.158."}
	for _, p := range prefixes {
		if strings.HasPrefix(ip, p) {
			return true
		}
	}
	return false
}
