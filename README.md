# OpenBerth

A self-hosted deployment platform. Give it code, get a live URL.

```
berth deploy ./my-project --name my-project
# => https://my-project.openberth.example.com
```

Supports Node.js (Next.js, Vite, Nuxt, SvelteKit), Python (Django, FastAPI, Flask), Go, and static HTML. Every deployment runs sandboxed in gVisor containers with automatic TLS. Written entirely in Go — three static binaries, zero runtime dependencies.

## Quick Start

### 1. Set up DNS

Point your domain and a wildcard at your server:

```
A     openberth.example.com       → your-server-ip
A     *.openberth.example.com     → your-server-ip
```

### 2. Install the server

Download the server binary from [Releases](https://github.com/openberth/openberth/releases), then copy it to a fresh Ubuntu 22.04/24.04 VM:

```bash
scp berth-server-linux-amd64 root@your-server:/tmp/berth-server
ssh root@your-server

chmod +x /tmp/berth-server
/tmp/berth-server install --domain openberth.example.com
```

Done in ~2 minutes. The installer sets up Docker, gVisor, Caddy, SQLite, and systemd. It prints your admin API key at the end.

### 3. Install the CLI

Download the CLI for your platform from [Releases](https://github.com/openberth/openberth/releases):

| Platform | Binary |
|----------|--------|
| macOS (Apple Silicon) | `berth-darwin-arm64` |
| macOS (Intel) | `berth-darwin-amd64` |
| Linux | `berth-linux-amd64` |
| Windows | `berth-windows-amd64.exe` |

```bash
# macOS example:
chmod +x berth-darwin-arm64
sudo mv berth-darwin-arm64 /usr/local/bin/berth

berth config set server https://openberth.example.com
berth config set key sc_your_admin_key
```

### 4. Deploy something

```bash
berth deploy ./my-project           # deploy a directory
berth deploy App.jsx                # deploy a single file
berth deploy --name my-app          # exact subdomain (my-app.domain.com)
```

---

## CLI Reference

```bash
# Deploy
berth deploy                              # current directory
berth deploy ./myproject                  # specific directory
berth deploy App.jsx                      # single file (auto-scaffolds Vite)
berth deploy --name my-app                # custom subdomain
berth deploy --ttl 7d                     # custom expiry (24h, 7d, 0=never)
berth deploy --memory 1g --cpus 1.0       # resource limits
berth deploy --env API_KEY=xxx            # environment variables
berth deploy --env-file .env.prod         # env vars from file
berth deploy --protect api_key            # deploy with access protection
berth deploy --network-quota 5g           # network transfer quota

# Dev mode (live sync + hot reload)
berth dev                                 # start a sandbox
berth dev App.jsx                         # single file with hot reload
berth promote <id>                        # promote sandbox to production

# Update
berth update <id>                         # push code changes
berth update <id> --memory 2g             # change resource limits
berth update <id> --env-file .env.prod    # update env vars

# Manage
berth list                                # all deployments
berth status <id>                         # deployment details
berth logs <id>                           # container logs
berth destroy <id>                        # remove deployment
berth pull <id> --output ./backup         # download source

# Access control
berth protect <id> --mode basic_auth --username admin --password secret
berth protect <id> --mode api_key
berth protect <id> --mode user --users alice,bob
berth protect <id> --mode public          # remove protection

# Network quota
berth quota <id> --set 5g
berth quota <id> --remove

# Lock / unlock
berth lock <id>                           # prevent all changes
berth unlock <id>
```

## AI Integration

Three integration surfaces so any AI can deploy code directly.

### HTTP API

```bash
# Deploy inline files
curl -X POST https://openberth.example.com/api/deploy/code \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"files": {"index.html": "<h1>Hello</h1>"}, "name": "my-app"}'

# Deploy a tarball
curl -X POST https://openberth.example.com/api/deploy \
  -H "Authorization: Bearer $KEY" \
  -F "tarball=@project.tar.gz" -F "name=my-app"

# Update
curl -X POST https://openberth.example.com/api/deploy/abc123/update/code \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"files": {"index.html": "<h1>Updated</h1>"}}'
```

### MCP Server (Claude Desktop / Cursor)

Add to your MCP config:

```json
{
  "mcpServers": {
    "openberth": {
      "command": "berth-mcp",
      "env": {
        "BERTH_SERVER": "https://openberth.example.com",
        "BERTH_KEY": "sc_your_key"
      }
    }
  }
}
```

Tools: `berth_deploy`, `berth_update`, `berth_status`, `berth_logs`, `berth_list`, `berth_protect`, `berth_destroy`, plus sandbox tools.

The server also exposes a built-in MCP endpoint at `/mcp` (Streamable HTTP transport) for web-based AI like Claude.ai.

### Stdin Pipe (Terminal Agents)

```bash
echo '{"index.html": "<h1>Hello</h1>"}' | berth deploy --stdin --json
```

## Supported Languages

| Language | Detection | Frameworks | Version Source |
|----------|-----------|------------|----------------|
| **Node.js** | `package.json` | Next.js, Nuxt, SvelteKit, Vite, CRA, Vue CLI, Angular | `.nvmrc`, `.node-version`, `engines.node` |
| **Python** | `requirements.txt`, `pyproject.toml`, `Pipfile` | Django, FastAPI, Flask | `.python-version`, `requires-python`, `runtime.txt` |
| **Go** | `go.mod` | Any (Gin, Echo, Fiber, stdlib) | `go.mod` `go 1.23` directive |
| **Static** | `index.html` | Plain HTML/CSS/JS | -- |

Versions are detected automatically. If your `go.mod` says `go 1.23`, the build runs in `golang:1.23`. If `.python-version` says `3.11`, it uses `python:3.11-slim`.

Framework is auto-detected. If detection is wrong or your project isn't recognized, add a `.berth.json` with override fields:

```json
{
  "language": "node",
  "start": "node dist/server.js",
  "build": "npm run build",
  "install": "pnpm install --frozen-lockfile",
  "dev": "npm run dev -- --host 0.0.0.0 --port $PORT"
}
```

Only `language` and `start` are required for unrecognized projects. All fields are optional if detection succeeds — set only the ones you want to override.

### Single-File Deploy

Deploy `.jsx`, `.tsx`, `.vue`, `.svelte`, or `.html` files directly — the CLI auto-scaffolds a Vite project:

```bash
berth deploy App.jsx           # React
berth deploy dashboard.tsx     # React + TypeScript
berth deploy Widget.vue        # Vue
berth deploy Counter.svelte    # Svelte
```

The scaffolder parses imports to detect dependencies, detects Tailwind from class names, and produces a build-ready project.

## Persistent Data

Deployments get two ways to persist data across rebuilds:

**`/data` directory** — bind-mounted host directory, survives rebuilds. Access via `DATA_DIR` env var:

```js
const db = new Database(process.env.DATA_DIR + '/app.db');
```

**`/_data/*` REST API** — built-in document store, works from any deployment including static HTML:

```js
// No backend needed — just fetch from the deployment's own domain
await fetch('/_data/votes', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({option: 'pizza'})
});

const {documents} = await fetch('/_data/votes').then(r => r.json());
```

---

## How It Works

### Two-Phase Builds

Every deploy creates two containers:

1. **Build** — installs dependencies and runs the production build (gVisor sandboxed, no memory limit)
2. **Runtime** — starts the production server (gVisor sandboxed, tight limits: 512MB RAM, 0.5 CPU default)

```
CLI                                   SERVER
 |                                     |
 +- detect language                    |
 +- tar project                        |
 +- upload ─────────────────────────>  extract → detect framework
                                       |
                                      Phase 1: Build container (gVisor)
                                       |  npm ci / go build / pip install
                                       |
                                      Phase 2: Runtime container (gVisor)
                                       |  next start / ./server / gunicorn
                                       |
 +- receive URL <────────────────────  Caddy route → HTTPS subdomain
```

No Dockerfiles. No image registry. The server uses pre-pulled base images matched to your language version.

### Blue-Green Updates

Updates create a new volume, copy cached dependencies from the old one, build while the old container keeps serving, then swap. On failure, it rolls back automatically.

Dependency caching is lockfile-aware: if `package-lock.json` / `go.sum` / `requirements.txt` hasn't changed, the install step is skipped entirely.

### Security Model

Every container runs inside gVisor (`runsc`), which intercepts all syscalls in userspace. Container escapes hit gVisor's synthetic kernel, not your host.

| Layer | Protection |
|-------|------------|
| **gVisor** | User-space kernel for syscall isolation |
| **Capabilities** | All dropped (`--cap-drop=ALL`) |
| **Privileges** | `no-new-privileges` flag |
| **Resources** | Per-container memory, CPU, PID limits |
| **Network** | Localhost-only binding; Caddy handles external traffic |
| **Auth** | Per-user API keys; optional per-deployment access control |
| **Expiry** | Auto-destruct after TTL (default 72h) |
| **Quota** | Optional per-deployment network transfer limit |

### TLS Modes

| Mode | Install flag | Behavior |
|------|-------------|----------|
| **Direct** (default) | — | Caddy provisions Let's Encrypt certificates |
| **Cloudflare** | `--cloudflare` | Internal TLS; Cloudflare handles public TLS at the edge |
| **Insecure** | `--insecure` | HTTP only, no TLS (for local dev or external termination) |

### Architecture

```
Browser ──── HTTPS ────> Caddy ──── reverse proxy ──> Runtime Container (gVisor)
                           ^
CLI / AI ── tarball ──> Server ──── docker ──> Build Container (gVisor)
                           |
                         SQLite
```

Three binaries, all pure Go (`CGO_ENABLED=0`):

| Binary | Description |
|--------|-------------|
| `berth-server` | Main daemon — API, containers, SQLite, Caddy config, MCP, gallery UI, self-installer |
| `berth` | Client CLI |
| `berth-mcp` | Standalone MCP server for Claude Desktop / Cursor |

### Project Structure

```
apps/
  server/              Main daemon
    internal/
      httphandler/     HTTP handlers (Go 1.22+ routing)
        mcp/           Server-side MCP (Streamable HTTP transport)
      service/         Business logic layer
      config/          Configuration
      store/           SQLite persistence
      container/       Docker/gVisor container lifecycle
      proxy/           Caddy reverse proxy management
      framework/       Language/framework detection
      datastore/       Per-deployment document store
      bandwidth/       Network quota tracking
      install/         Self-installer
    gallery/           React/TypeScript gallery UI (embedded at build time)
  cli/                 Client CLI
  mcp/                 Standalone MCP server (stdio transport)
```

## Building From Source

```bash
make build          # server + CLI + MCP
make server         # server only (builds gallery first)
make cli            # CLI only
make mcp            # MCP only
make vet            # run go vet on all modules
make lint           # run golangci-lint on all modules
make cli-all        # CLI for all platforms (linux/mac/windows, amd64/arm64)

# Cross-compile server for Linux
make server GOOS=linux GOARCH=amd64
```

## Admin

```bash
# Server-side admin CLI (on the server)
berth-admin user add --name alice --max-deployments 10
berth-admin user list
berth-admin status
berth-admin config
berth-admin cleanup
```

See [Managing Users](docs/managing-users.md) for the admin HTTP API and settings (OIDC, network quota, session TTL).

## Troubleshooting

**Container stuck in "building":**
```bash
berth logs <id>              # check build output
journalctl -u openberth -f       # daemon logs
```

**Caddy TLS issues:**
```bash
dig openberth.example.com        # verify DNS points to server
journalctl -u caddy -f            # Caddy logs
# DNS must NOT be Cloudflare-proxied in default mode (use gray cloud)
```

**Wrong language version:**
Add a version file (`.nvmrc`, `.python-version`) or check `berth logs <id>` to see which image was used.

## Documentation

- [Installation](docs/installation.md) — server provisioning, DNS, TLS modes, manual setup
- [CLI Reference](docs/cli-reference.md) — all commands, flags, env vars, stdin pipe
- [AI Integration](docs/ai-integration.md) — HTTP API, MCP server, stdin pipe
- [Managing Users](docs/managing-users.md) — admin CLI, admin HTTP API, OIDC, settings
- [Persistent Data](docs/persistent-data.md) — `/data` directory and `/_data/*` REST API
- [Architecture](docs/architecture.md) — two-phase builds, blue-green deploys, security, internals

## License

MIT
