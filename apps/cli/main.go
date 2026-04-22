package main

import (
	"fmt"
	"os"
)

var version = "dev"

// ── Flag helpers ────────────────────────────────────────────────────

var args []string

func getFlag(name, def string) string {
	flag := "--" + name
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return def
}

func getFlags(name string) []string {
	flag := "--" + name
	var vals []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			vals = append(vals, args[i+1])
		}
	}
	return vals
}

func hasFlag(name string) bool {
	flag := "--" + name
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// ── Help ────────────────────────────────────────────────────────────

func printHelp() {
	fmt.Printf(`
  %s⚓ OpenBerth%s — Deploy AI-generated projects instantly

  %sUSAGE%s
    berth <command> [options]

  %sCOMMANDS%s
    init                Configure this project for OpenBerth
    deploy [file|dir]   Deploy (or update existing deployment)
    dev [dir]           Start sandbox with live file sync (hot reload)
    update [id]         Push code update (reads .berth.json if no ID)
    promote [id]        Promote a sandbox to a production deployment
    list                List your deployments (--all shows everyone's with owner)
    status [id]         Get deployment details
    logs [id]           View container logs
    protect [id]        Set access control (basic_auth, api_key, user, public)
    lock [id]           Lock deployment (reject updates until unlocked)
    unlock [id]         Unlock deployment (allow updates again)
    secret              Manage secrets (set, list, delete)
    quota [id]          Set or remove network quota
    pull [id]           Download deployment source code
    destroy [id]        Remove a deployment
    destroy --all       Remove all your deployments
    backup              Download full server backup (admin)
    restore <file>      Restore server from backup file (admin)
    login               Login via browser (sets up API key automatically)
    rotate-key          Rotate your API key (invalidates the current one)
    config              Manage CLI configuration
    upgrade             Upgrade the CLI to the latest version
    version             Show CLI and server version

  %sINIT OPTIONS%s
    --dir <path>        Project directory (default: current)
    --yes               Accept all defaults (non-interactive)

  %sDEPLOY OPTIONS%s
    --name <name>       Custom subdomain name (default: directory name)
    --ttl <duration>    Time to live: 24h, 7d, 0 for never (default: 72h)
    --env <KEY=VAL>     Environment variable (repeatable)
    --secret <NAME>     Bind a server-side secret (repeatable)
    --memory <limit>    Memory limit (default: 512m)
    --cpus <limit>      CPU limit (default: 0.5)
    --dir <path>        Project directory (default: current)
    --port <port>       Port your app listens on (overrides auto-detect)
    --env-file <path>   Load env vars from file (also auto-loads .env)
    --protect <mode>    Access control: basic_auth, api_key, user
    --username <user>   Username for basic_auth protect mode
    --password <pass>   Password for basic_auth protect mode
    --api-key <key>     Custom API key for api_key protect mode
    --users <user,...>  Restrict access to specific users (for user mode)
    --network-quota <q> Network transfer quota (e.g. 1g, 5g, 10g)
    --new               Force new deployment (ignore existing ID in config)
    --no-wait           Skip waiting for build (print URL and exit)
    --no-qr             Suppress QR code display

  %sDEV OPTIONS%s
    --attach <id>       Reattach file watcher to an existing sandbox
    --name <name>       Custom subdomain name (default: directory name)
    --ttl <duration>    Time to live (default: 4h)
    --env <KEY=VAL>     Environment variable (repeatable)
    --secret <NAME>     Bind a server-side secret (repeatable)
    --memory <limit>    Memory limit (default: 1g)
    --port <port>       Port override
    --protect <mode>    Access control: basic_auth, api_key, user
    --username <user>   Username for basic_auth protect mode
    --password <pass>   Password for basic_auth protect mode
    --api-key <key>     Custom API key for api_key protect mode
    --users <user,...>  Restrict access to specific users (for user mode)
    --network-quota <q> Network transfer quota (e.g. 500m, 1g)

  %sUPDATE OPTIONS%s
    --network-quota <q> Network transfer quota (e.g. 1g, 5g, 10g)

  %sLOGS OPTIONS%s
    --tail <n>          Number of lines to show (default: 200)
    --follow, -f        Stream logs in real time (SSE)

  %sSECRET COMMANDS%s
    berth secret set NAME VALUE [--description "desc"] [--global]
    berth secret list           List all secrets
    berth secret delete NAME    Delete a secret [--global]

  %sSINGLE-FILE MODE%s
    Deploy or dev .jsx, .tsx, .vue, .svelte, .html, .md, or .ipynb files directly.
    Auto-scaffolds with Vite + Tailwind CSS (or static HTML for .md/.ipynb).

  %sEXAMPLES%s
    berth init                          Configure project for OpenBerth
    berth init --yes                    Non-interactive with defaults
    berth deploy                        Deploy current directory
    berth deploy App.jsx                Single-file React deploy
    berth deploy --protect api_key      Deploy with API key protection
    berth deploy --protect basic_auth --username admin --password secret
    berth dev                           Dev sandbox with file watcher
    berth dev App.jsx                   Single-file dev with hot reload
    berth dev --attach abc123           Reattach to existing sandbox
    berth dev --protect api_key         Dev sandbox with API key protection
    berth deploy App.jsx --name cool    Custom subdomain
    berth protect abc123 --mode basic_auth --username admin --password secret
    berth protect abc123 --mode api_key
    berth protect abc123 --mode user
    berth protect abc123 --mode user --users alice,bob
    berth protect abc123 --mode public
    berth quota abc123 --set 5g         Set network quota
    berth quota abc123 --remove         Remove network quota
    berth update abc123def
    berth logs abc123def --tail 50
    berth logs --follow              Stream logs in real time
    berth logs abc123def -f          Stream logs (short flag)

  %sSETUP%s
    berth config set server https://openberth.example.com
    berth config set key sc_your_api_key_here
`, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset, cBold, cReset)
}

// ── Main ────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	args = os.Args[1:]
	command := os.Args[1]

	switch command {
	case "init":
		cmdInit()
	case "deploy":
		cmdDeploy()
	case "dev":
		cmdDev()
	case "update":
		cmdUpdate()
	case "promote":
		cmdPromote()
	case "list", "ls":
		cmdList()
	case "status":
		cmdStatus()
	case "logs":
		cmdLogs()
	case "pull":
		cmdPull()
	case "destroy", "rm":
		cmdDestroy()
	case "protect":
		cmdProtect()
	case "lock":
		cmdLock(true)
	case "unlock":
		cmdLock(false)
	case "quota":
		cmdQuota()
	case "secret":
		cmdSecret()
	case "backup":
		cmdBackup()
	case "restore":
		cmdRestore()
	case "login":
		cmdLogin()
	case "rotate-key":
		cmdRotateKey()
	case "config":
		cmdConfig()
	case "upgrade":
		cmdUpdateCLI()
	case "version", "--version", "-v":
		cmdVersion()
	case "help", "--help", "-h":
		printHelp()
	default:
		printHelp()
	}
}
