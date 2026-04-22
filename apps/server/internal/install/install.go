package install

import "fmt"

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
	WebDisabled     bool
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

