# Installation

## Prerequisites

- **Ubuntu 22.04 or 24.04** (x86_64). Hetzner CX22 or equivalent recommended.
- **A domain** you control (e.g., `openberth.example.com`)

## DNS Setup

Point both the root domain and a wildcard at your server:

```
A     openberth.example.com       → your-server-ip
A     *.openberth.example.com     → your-server-ip
```

### Default mode (Let's Encrypt)

DNS must point directly to the server — **not** through Cloudflare's proxy (use "DNS-only" / gray cloud). Caddy needs to receive ACME HTTP-01 challenges directly to provision TLS certificates.

### Cloudflare proxy mode

If you prefer Cloudflare's proxy (orange cloud) for DDoS protection, caching, and hiding your origin IP, install with the `--cloudflare` flag. In this mode Caddy uses self-signed internal certificates instead of Let's Encrypt, and Cloudflare handles public TLS at the edge. Set Cloudflare's SSL/TLS mode to **Full** (not Full Strict).

```
A     openberth.example.com       → your-server-ip   (orange cloud / proxied)
A     *.openberth.example.com     → your-server-ip   (orange cloud / proxied)
```

### Insecure mode (HTTP only)

For local development, internal networks, or environments where TLS is handled externally, install with `--insecure`. Caddy will serve HTTP only — no certificates, no ACME challenges.

```bash
berth-server install --domain local.dev --insecure
```

All deployment URLs will use `http://` instead of `https://`. This mode is mutually exclusive with `--cloudflare`.

## Install the Server

### Automated Install (Recommended)

The server binary includes a built-in `install` subcommand that provisions the host machine. Download it from [Releases](https://github.com/openberth/openberth/releases), copy it to a fresh Ubuntu server, and run it.

**1. Download the server binary:**

Download `berth-server-linux-amd64` (or `arm64`) from the [latest release](https://github.com/openberth/openberth/releases/latest).

Or build from source: `make server GOOS=linux GOARCH=amd64`

**2. Copy the binary to your server and run the installer:**

```bash
scp berth-server-linux-amd64 root@1.2.3.4:/tmp/berth-server
ssh root@1.2.3.4

# On the server:
chmod +x /tmp/berth-server
/tmp/berth-server install --domain openberth.example.com
```

The installer automatically copies itself to `/usr/local/bin/berth-server` — you can run it from any path.

**Options:**

| Flag | Default | Description |
|------|---------|-------------|
| `--domain` | (required) | Your OpenBerth domain |
| `--admin-key` | auto-generated | Admin API key (auto-generates `sc_...` if omitted) |
| `--max-deploys` | `10` | Max deployments per user |
| `--default-ttl` | `72` | Default deployment TTL in hours |
| `--cloudflare` | off | Use Cloudflare proxy mode (internal TLS, no ACME) |
| `--insecure` | off | Run without SSL/TLS (HTTP only, mutually exclusive with `--cloudflare`) |

**What the installer does (20 steps):**

1. Verifies root access
2. Installs system packages (ca-certificates, curl, jq, sqlite3, dnsutils)
3. Installs Docker CE
4. Installs gVisor (runsc) and configures Docker to use it
5. Tests gVisor runtime
6. Installs Caddy
7. Pulls base Docker images (node:20-slim, caddy:2-alpine)
8. Creates data directories (`/var/lib/openberth/`)
9. Creates shared Docker volumes (npm cache)
10. Writes server config (`/var/lib/openberth/config.json`)
11. Initializes SQLite database with admin user
12. Writes Caddyfile (Let's Encrypt ACME, internal TLS for Cloudflare, or HTTP-only for insecure mode)
13. Verifies the server binary is in place
14. Writes `berth-admin` helper script
15. Writes systemd service unit
16. Enables and starts Caddy + OpenBerth services
17. Configures UFW firewall (ports 22, 80, 443 — insecure mode skips 443)
18. Verifies DNS resolution
19. Runs health check
20. Prints summary with admin API key and generated password

The installer also generates a random admin password for browser login and prints it in the summary.

### Manual Install

If you prefer to provision manually:

1. Install Docker CE and configure gVisor (`runsc`) as a runtime
2. Install Caddy and configure it for your domain with wildcard TLS
3. Create directories: `/var/lib/openberth/{deploys,uploads,persist}`, `/etc/caddy/sites/`
4. Write `/var/lib/openberth/config.json` (see `apps/server/internal/install/templates.go` for schema)
5. Build the server: `cd apps/server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o berth-server .`
6. Upload the binary to `/usr/local/bin/berth-server`
7. Create a systemd service (see `apps/server/internal/install/templates.go`)
8. Initialize the SQLite database (the server creates tables on first start)
9. Create an admin user via the `berth-admin` script or direct SQLite

## Install the CLI

Download the CLI binary for your platform from [Releases](https://github.com/openberth/openberth/releases/latest):

| Platform | Binary |
|----------|--------|
| macOS (Apple Silicon) | `openberth-darwin-arm64` |
| macOS (Intel) | `openberth-darwin-amd64` |
| Linux (x86_64) | `openberth-linux-amd64` |
| Linux (ARM64) | `openberth-linux-arm64` |
| Windows | `openberth-windows-amd64.exe` |

```bash
chmod +x openberth-darwin-arm64
sudo mv openberth-darwin-arm64 /usr/local/bin/openberth
```

Or build from source: `make cli` (requires Go 1.24+).

Configure it to talk to your server:

```bash
openberth config set server https://openberth.example.com   # or http:// for --insecure mode
openberth config set key sc_your_admin_key_here
```

## Verify

```bash
openberth version
# Should show CLI version, server version, and domain

openberth deploy ./examples/jsxapp
# Should return a live HTTPS URL
```
