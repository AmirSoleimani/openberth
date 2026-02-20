# CLI Reference

## Server Install

Provision a fresh Linux VM as a OpenBerth server. Run this on the server itself (not remotely):

```bash
# 1. Get a Linux VM, SSH in
# 2. Copy or download the server binary
scp berth-server-linux root@server:/tmp/berth-server

# 3. Install (copies itself to /usr/local/bin/berth-server)
chmod +x /tmp/berth-server
/tmp/berth-server install --domain openberth.example.com
```

Options:

```bash
berth-server install --domain <domain>       # Required: your OpenBerth domain
berth-server install --admin-key <key>       # Custom admin API key (auto-generated if omitted)
berth-server install --max-deploys <n>       # Max deployments per user (default: 10)
berth-server install --default-ttl <hours>   # Default TTL hours (default: 72)
berth-server install --cloudflare            # Cloudflare proxy mode (no ACME, internal TLS)
berth-server install --insecure              # HTTP only (no SSL/TLS, mutually exclusive with --cloudflare)
```

Example for HTTP-only setup:

```bash
berth-server install --domain local.dev --insecure
```

The installer runs 20 steps: installs Docker, gVisor, Caddy, creates the database, writes systemd service, and starts everything. Must be run as root.

## Deploy

```bash
openberth deploy                            # Deploy current directory
openberth deploy App.jsx                    # Deploy a single file
openberth deploy ./myproject                # Deploy specific directory
openberth deploy --name my-app              # Exact subdomain (my-app.domain.com)
openberth deploy --ttl 7d                   # Custom expiry (24h, 7d, 0=never)
openberth deploy --port 5000                # Override app listen port
openberth deploy --memory 1g               # Custom memory limit
openberth deploy --cpus 1.0                # Custom CPU allocation
openberth deploy --env API_KEY=xxx          # Set environment variable
openberth deploy --env-file .env.prod       # Load env vars from file
openberth deploy --protect api_key          # Deploy with API key protection
openberth deploy --protect basic_auth --username admin --password secret
openberth deploy --protect user --users alice,bob  # Restrict to specific users
openberth deploy --network-quota 5g         # Set network transfer quota
```

### Single-File Deploy

Deploy any `.jsx`, `.tsx`, `.vue`, `.svelte`, or `.html` file directly — the CLI auto-scaffolds a Vite project:

```bash
openberth deploy App.jsx           # React
openberth deploy dashboard.tsx     # React + TypeScript
openberth deploy Widget.vue        # Vue
openberth deploy Counter.svelte    # Svelte
openberth deploy page.html         # Static HTML
```

The scaffolder parses all `import` statements to detect dependencies, detects Tailwind CSS usage from class names, and produces a complete build-ready project. By the time the tarball reaches the server, it looks like any other Vite project.

## Dev (Sandbox)

Start a sandbox with live file sync and hot reload:

```bash
openberth dev                               # Dev sandbox for current directory
openberth dev App.jsx                       # Single-file dev with hot reload
openberth dev --name my-sandbox             # Custom subdomain
openberth dev --ttl 8h                      # Custom TTL (default: 4h)
openberth dev --attach abc123               # Reattach to existing sandbox
openberth dev --protect api_key             # Protected sandbox
openberth dev --protect user --users alice  # Restrict sandbox to specific users
openberth dev --network-quota 500m          # Network quota
```

File changes are watched locally and pushed to the running sandbox automatically. When ready, promote to a production deployment:

```bash
openberth promote <id>
openberth promote <id> --network-quota 5g   # Set quota on promoted deployment
```

## Update

All resource and config flags work on updates too:

```bash
openberth update <id>                       # Push code changes
openberth update <id> --memory 2g           # Change memory limit
openberth update <id> --port 3000           # Change port
openberth update <id> --env-file .env.prod  # Update env vars
openberth update <id> --cpus 2.0            # Change CPU allocation
openberth update <id> --network-quota 5g    # Set network quota
```

## Access Control

### At deploy time

Set protection when deploying so it's active the moment the URL goes live:

```bash
openberth deploy --protect basic_auth --username admin --password secret
openberth deploy --protect api_key                    # auto-generated key
openberth deploy --protect api_key --api-key my-key   # custom key
openberth dev --protect api_key                       # sandbox with protection
```

### Post-deploy

Protect any running deployment with browser-native basic auth, API key authentication, or user-based access control:

```bash
# Password-protect (browser login prompt)
openberth protect <id> --mode basic_auth --username admin --password secret

# API key protect (header or query param)
openberth protect <id> --mode api_key                    # auto-generated key
openberth protect <id> --mode api_key --api-key my-key   # custom key

# User-based protection (any authenticated OpenBerth user)
openberth protect <id> --mode user

# User-based protection (restricted to specific users)
openberth protect <id> --mode user --users alice,bob,charlie

# Remove protection
openberth protect <id> --mode public
```

With `basic_auth`, visitors see a browser login prompt. With `api_key`, requests must include the `X-Api-Key: <key>` header — otherwise they get a 401. With `user`, visitors must be authenticated OpenBerth users — optionally restricted to a specific list via `--users`. The deployment owner and admins always have access. Protection survives code updates (`openberth update`).

## Network Quota

Manage network transfer quota on existing deployments:

```bash
openberth quota <id> --set 5g               # Set quota (e.g. 500m, 1g, 5g, 10g)
openberth quota <id> --remove               # Remove quota
```

Quota can also be set at deploy time with `--network-quota`.

## Lock / Unlock

Prevent all changes to a running deployment:

```bash
openberth lock <id>         # Lock — rejects updates, destroy, protect, metadata changes
openberth unlock <id>       # Unlock — allow changes again
```

A locked deployment keeps running and serving traffic. Locking prevents `openberth update`, sandbox file pushes, destroy, protect, and metadata edits. Only the deployment owner or an admin can lock/unlock.

## Pull (Download Source)

Download a deployment's source code to a local directory:

```bash
openberth pull <id>                        # Download to current directory
openberth pull <id> --output ./backup      # Download to specific directory
openberth pull                             # Uses deployment ID from .berth.json
```

## Other Commands

```bash
openberth list                  # List all deployments (locked = protected)
openberth status <id>           # Deployment details + access mode
openberth logs <id>             # Container logs
openberth logs <id> --tail 50   # Last 50 lines
openberth destroy <id>          # Remove a deployment
openberth destroy --all         # Remove all deployments
openberth version               # Show CLI + server version
openberth login                 # Login via browser (sets up API key)
openberth config set <k> <v>    # Configure CLI
openberth config show           # Show config
```

## Environment Variables

Three layers, last wins:

```bash
# 1. Auto-loaded: .env in project dir (loaded automatically if present)
# 2. --env-file: explicit file
# 3. --env: individual overrides (highest priority)

openberth deploy --env-file .env.prod --env SECRET_KEY=override
```

The `.env` parser handles comments (`#`), empty lines, `export` prefix, and quoted values. The `PORT` env var is always set automatically to match the app's listen port.

## Stdin Pipe

For AI agents and scripts, pipe JSON directly to deploy:

```bash
# Simple: just file paths to content
echo '{"index.html": "<h1>Hello World</h1>"}' | openberth deploy --stdin

# Full: with options
echo '{
  "files": {
    "app.py": "from flask import Flask\napp = Flask(__name__)\n@app.route(\"/\")\ndef index(): return \"Hello\"",
    "requirements.txt": "flask\ngunicorn"
  },
  "name": "my-api",
  "env": {"SECRET": "abc"}
}' | openberth deploy --stdin

# Machine-readable output for agents
echo '{"index.html": "..."}' | openberth deploy --stdin --json
```
