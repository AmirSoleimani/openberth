package install

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// ANSI color codes used by the CLI output helpers and printResult.
const (
	cReset  = "\033[0m"
	cGreen  = "\033[32m"
	cRed    = "\033[31m"
	cYellow = "\033[33m"
	cCyan   = "\033[36m"
	cBold   = "\033[1m"
)

// ── CLI output helpers ──────────────────────────────────────────────────

func cliOK(msg string)   { fmt.Printf("  %s✓%s %s\n", cGreen, cReset, msg) }
func cliFail(msg string) { fmt.Fprintf(os.Stderr, "  %s✗%s %s\n", cRed, cReset, msg) }
func cliWarn(msg string) { fmt.Printf("  %s!%s %s\n", cYellow, cReset, msg) }
func cliSpin(msg string) { fmt.Printf("  %s⟳%s %s...", cYellow, cReset, msg) }
func cliDone()           { fmt.Println(" done") }

// printResult renders the post-install summary banner with domain, admin
// credentials, and DNS / next-step guidance.
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

// Run parses flags and executes the install sequence. Called from main.go
// when the binary is invoked as `berth-server install`.
func Run(args []string) {
	fs := flag.NewFlagSet("berth-server install", flag.ExitOnError)
	cfg := &Config{}

	fs.StringVar(&cfg.Domain, "domain", "", "OpenBerth domain (required)")
	fs.StringVar(&cfg.AdminKey, "admin-key", "", "Admin API key (auto-generated if omitted)")
	fs.StringVar(&cfg.Driver, "runtime", "", "Runtime driver (default: docker). Must match a registered driver.")
	fs.IntVar(&cfg.MaxDeploys, "max-deploys", 0, "Max deployments per user (default: 10)")
	fs.IntVar(&cfg.DefaultTTL, "default-ttl", 0, "Default TTL hours (default: 72)")
	fs.BoolVar(&cfg.CloudflareProxy, "cloudflare", false, "Use Cloudflare proxy mode (no ACME, internal TLS)")
	fs.BoolVar(&cfg.Insecure, "insecure", false, "Run without SSL/TLS (HTTP only)")
	fs.BoolVar(&cfg.WebDisabled, "no-web", false, "Disable web gallery, login, and setup pages (API/CLI/OIDC-only mode)")
	fs.BoolVar(&cfg.FlatURLs, "flat-urls", false, "Publish deploys at <name>-<workspace>.<apex> (siblings) instead of <name>.<workspace>.<apex> (nested), so a single apex wildcard cert covers every deploy.")

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
    --runtime <name>        Runtime driver (default: docker)
    --max-deploys <n>       Max deployments per user (default: 10)
    --default-ttl <hours>   Default TTL hours (default: 72)
    --cloudflare            Use Cloudflare proxy mode (no ACME, internal TLS)
    --insecure              Run without SSL/TLS (HTTP only)
    --no-web                Disable web gallery, login, and setup pages
    --flat-urls             Sibling deploy URLs (<name>-<workspace>.<apex>)
                            instead of nested (<name>.<workspace>.<apex>)
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
