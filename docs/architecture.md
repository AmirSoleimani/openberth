# Architecture

A self-hosted deployment platform that takes a tarball (or a single `.jsx` file) and returns a live URL — sandboxed, ephemeral, production-built, in any language. HTTPS by default, with optional `--insecure` mode for HTTP-only setups.

## Design Philosophy

OpenBerth closes the gap between "I have code" and "here's a live URL." The core constraint is that untrusted code runs on shared infrastructure, so every design decision follows from one principle: **assume the code is hostile.**

The platform is written entirely in Go, producing static binaries with zero runtime dependencies. No language runtimes on the server, no package manager supply chains, no garbage collector pauses under load. Language runtimes only exist inside sandboxed containers.

## System Overview

```
+--------------+     HTTPS      +---------+     reverse     +----------------------+
|   Browser    | -------------> |  Caddy   | ---- proxy ---> |  Runtime Container   |
|              |                |  (TLS)   |                 |  (gVisor sandbox)    |
+--------------+                +---------+                 +----------------------+
                                      ^
+--------------+    tarball     +-----+-------+    docker     +----------------------+
|   CLI        | ------------->|  OpenBerth  | -----------> |  Build Container     |
|  (Go binary) |  + env/port    |  Server      |   run/stop   |  (gVisor sandbox)    |
+--------------+                |  (Go binary) |              +----------------------+
                                +--------------+
                                      |
                                      v
                                +--------------+
                                |   SQLite     |
                                |   (modernc)  |
                                +--------------+
```

## Project Structure

```
apps/
  server/              Main daemon (API, containers, SQLite, Caddy, MCP, self-installer)
    internal/
      config/          Configuration loading (Insecure, CloudflareProxy, BaseURL)
      store/           SQLite persistence (users, deployments, OAuth)
      container/       Docker/gVisor container lifecycle
      proxy/           Caddy reverse proxy management (protocol-aware)
      framework/       Language/framework detection (Go, Python, Node, Static)
      datastore/       Per-deployment document store
      install/         Self-installer (local provisioning, --insecure/--cloudflare modes)
      bandwidth/       Caddy access log bandwidth tracker
      httphandler/     HTTP handlers (Go 1.22+ routing)
        mcp/           Server-side MCP handler (Streamable HTTP transport)
      service/         Business logic layer
        types.go       All param/result types (consolidated)
        provision.go   Shared provisioning helpers
        deploy.go      Code deploy/update operations
        tarball.go     Tarball deploy/update, backup restore
        sandbox.go     Sandbox create/push/exec/install/promote
        query.go       List/get deployment queries
        manage.go      Destroy, protect, lock, metadata
        lifecycle.go   Startup reconciliation, cleanup
        helpers.go     Utility functions
        access.go      Access control computation
        errors.go      Typed service errors
    gallery/           React/TypeScript gallery UI (embedded at build time)
    main.go            HTTP routing, setup, entry point
    gallery_embed.go   Embedded gallery static files
  cli/                 CLI client (pure stdlib, zero deps except fsnotify)
  mcp/                 Standalone MCP server (stdio transport, HTTP bridge)
examples/              Example projects (nextapp, goapp, pythonapp, jsxapp, rsvpapp)
```

## Binary Architecture

Three binaries, all pure Go (`CGO_ENABLED=0`), fully static:

| Binary | Size | Description |
|--------|------|-------------|
| `berth-server` | ~15 MB | Main daemon (includes embedded SQLite engine, gallery UI, and self-installer) |
| `openberth` (CLI) | ~8 MB | Client CLI, zero dependencies |
| `berth-mcp` | ~8 MB | Standalone MCP server for Claude Desktop / Cursor |

The server binary includes a built-in `install` subcommand (`berth-server install --domain example.com`) that provisions the host machine — no separate installer binary needed.

Cross-compilation produces binaries for Linux (amd64/arm64), macOS (amd64/arm64), and Windows (amd64) from a single `make cli-all` command.

## Server Architecture

**Request flow:** HTTP handler (`internal/httphandler/`) → Service method (`internal/service/`) → internal packages (store, container, proxy, framework)

- `internal/httphandler/` — HTTP layer (parse request, call service, write response). Uses Go 1.22+ enhanced routing patterns.
- `internal/httphandler/mcp/` — Server-side MCP handler (Streamable HTTP transport, calls same service methods)
- `internal/service/` — Business logic layer. All deploy/sandbox/query/manage operations live here.
  - `types.go` — All param and result types consolidated in one place
  - `provision.go` — Shared provisioning helpers (validation, identity generation, framework detection, async build, access control)
  - `deploy.go` / `tarball.go` / `sandbox.go` — Operation implementations using shared helpers
- `internal/` — Domain packages (config, store, container, proxy, framework, datastore, bandwidth)

The MCP handler and HTTP handlers share business logic through Service methods. Business logic is never duplicated between them.

## MCP Architecture

Two MCP implementations exist:

1. **Server-side MCP** (`apps/server/internal/httphandler/mcp/handler.go`) — Streamable HTTP transport, runs inside the server process, calls Service methods directly. Used by Claude.ai web interface.

2. **Standalone MCP** (`apps/mcp/main.go`) — Stdio transport, separate binary, bridges to server via HTTP API. Used by Claude Desktop, Cursor, and other local MCP clients.

Both must stay in sync. When adding a new tool:
1. Add the tool definition (name, description, schema) to both files
2. Add the handler in the server-side MCP (calls `do*` directly)
3. Add the handler in the standalone MCP (calls HTTP API)

Tool descriptions are prescriptive, not just descriptive — they tell the AI *when* to use the tool, *when not* to, and *what to expect*.

## Gallery UI

The server includes a React/TypeScript gallery UI that displays all active deployments. It's built separately (`make gallery`) and embedded into the server binary at compile time via `gallery_embed.go`. The gallery is served at the root domain.

The gallery supports client-side routing:
- `/gallery/` — main gallery with all deployments
- `/gallery/user/{userId}` — user profile showing only that user's deployments
- `/gallery/settings` — admin settings (OIDC, users, network quota)

Clicking an owner name in any deployment card navigates to that user's profile page.

## Multi-Language Support

The two-phase build system (build container -> runtime container) works the same way for every language. Only three things vary: the Docker image, the build command, and the start command.

### Language Detection Order

Detection runs top-down, first match wins:

1. `go.mod` -> Go
2. `requirements.txt` / `pyproject.toml` / `Pipfile` / `manage.py` / `app.py` -> Python
3. `package.json` -> Node.js (further classified by deps: Next.js, Nuxt, Vite, etc.)
4. `index.html` -> Static HTML

### Version Detection

Each language declares its version in standard project files. The detector reads these and selects the corresponding Docker image tag.

| Language | Priority chain | Example match | Resulting image |
|---|---|---|---|
| **Go** | `go.mod` -> `go X.Y` directive | `go 1.23` | `golang:1.23` |
| **Python** | `.python-version` -> `pyproject.toml` `requires-python` -> `runtime.txt` | `3.11.4` | `python:3.11-slim` |
| **Node** | `.nvmrc` -> `.node-version` -> `package.json` `engines.node` | `v22.1.0` | `node:22-slim` |

Defaults when no version file exists: Go 1.22, Python 3.12, Node 20.

### Build & Runtime Per Language

**Go** — compiled to static binary, runs in minimal image:

| Phase | Image | Command |
|---|---|---|
| Build | `golang:1.23` | `CGO_ENABLED=0 go build -o /app/bin/server .` |
| Run | `debian:bookworm-slim` | `/app/bin/server` |

**Python** — venv persisted in volume:

| Phase | Image | Command |
|---|---|---|
| Build | `python:3.12-slim` | `python -m venv + pip install -r requirements.txt` |
| Run | `python:3.12-slim` | `gunicorn app:app` / `uvicorn app:app` |

Python framework detection: `manage.py` -> Django (gunicorn + wsgi), `fastapi` in deps -> uvicorn, `flask` in deps -> gunicorn.

**Node.js** — build and run use same image:

| Phase | Image | Command (Next.js example) |
|---|---|---|
| Build | `node:20-slim` | `npm ci + next build` |
| Run | `node:20-slim` | `next start -H 0.0.0.0` |

For Go, the runtime image is a different, minimal image. The build image contains the full compiler toolchain; the runtime image contains only the binary and libc.

## Two-Phase Build System

Every deploy splits into two discrete phases.

### Phase 1: Build

A short-lived container with no memory ceiling installs dependencies and runs the production build. It operates inside gVisor for syscall isolation but is otherwise unconstrained. A 4GB swap file on the host acts as a safety net.

The build writes output to a Docker volume. Once it exits successfully, the volume contains the full application: source, dependencies, and compiled output.

### Phase 2: Run

A second container, tightly constrained, mounts that volume and starts the production server. This container runs under gVisor with all capabilities dropped, configurable memory/CPU limits, `no-new-privileges`, and a PID ceiling. It serves pre-built output — no file watching, no hot reload, no compilation.

### Why Production Builds

Dev servers (`next dev`, `vite`) maintain in-memory state and filesystem watchers that break when underlying files change. Production builds eliminate this category entirely. `next build` produces static output. `next start` serves it statelessly.

## Sandbox (Dev) Mode

In addition to production deployments, OpenBerth supports sandbox mode for interactive development:

- A single container runs a dev server with hot reload
- File changes are pushed via the `push` endpoint and synced into the running container
- Packages can be installed into the running sandbox
- Commands can be executed inside the sandbox
- When ready, a sandbox can be promoted to a full production deployment

Sandbox mode uses the same gVisor isolation as production deployments.

## Blue-Green Volume Deployment

Updates use blue-green volumes to avoid corruption:

```
Time -------------------------------------------------->

Volume A  [===== serving =====]           deleted
Runtime   [===== serving =====]
                                 ~2s swap
Volume B       [build into B]  [===== serving =====>
```

1. New volume created
2. Cached build artifacts copied from old volume (`node_modules`, `venv/`, `go.sum`)
3. Build container runs against new volume while old runtime keeps serving
4. On success: swap containers (~2 second downtime), delete old volume
5. On failure: roll back to old container + old volume automatically

### Incremental Install Optimization

The build script hashes the dependency lockfile before and after copying new source. If the hash matches (no dependency changes), it skips the install step entirely.

| Scenario | Node | Go | Python |
|---|---|---|---|
| Code-only change | Skip `npm install` (~2s) | Skip `go mod download` (~1s) | Skip `pip install` (~1s) |
| 1 new dep | `npm install` diff (~5s) | `go mod download` (~3s) | `pip install` diff (~5s) |
| Fresh build | `npm ci` (~20s) | `go mod download` + build (~15s) | `pip install` (~15s) |

## Shared Cache Volumes

Each language gets persistent Docker volumes shared across all deploys:

```
openberth-npm-cache       -> /root/.npm              (Node)
openberth-go-mod          -> /go/pkg/mod             (Go)
openberth-go-build        -> /root/.cache/go-build   (Go)
openberth-pip-cache       -> /root/.cache/pip         (Python)
```

These are mounted into build containers only, never runtime containers.

## Security Model

Every container — build and runtime — runs inside gVisor's `runsc` runtime. gVisor intercepts all syscalls in userspace, presenting a synthetic kernel to the containerized process. Even if an attacker achieves code execution, they interact with gVisor's kernel reimplementation rather than the host kernel.

On top of gVisor:

- **All Linux capabilities dropped.** Process runs as unprivileged user.
- **`no-new-privileges`** prevents escalation via setuid binaries.
- **Resource ceilings**: configurable memory, CPU, PID limits, and network transfer quota per deployment.
- **Network quota** (optional): Caddy access log based byte cap per deployment. The bandwidth tracker tails Caddy's structured log, aggregates response bytes per subdomain, and rewrites the site config to serve 503 when quota is exceeded. Configurable via admin settings or per-deployment API override.
- **Disk size limit** (optional): `--storage-opt` constraint on container root filesystem (requires overlay2+xfs+pquota).
- **Localhost-only ports.** Containers bind `127.0.0.1:<port>`. Caddy handles external traffic.
- **Per-user API key authentication.** Every API request requires a bearer token tied to a user.
- **Automatic expiry.** Configurable TTL (default 72 hours) after which deployments are destroyed.
- **Hard delete on destroy.** DB record removed entirely, subdomain freed for reuse.

## Infrastructure Stack

**Caddy** handles TLS and reverse proxying. Three modes:

| Mode | Config flag | Caddy behavior | Deployment URLs |
|------|------------|----------------|-----------------|
| **Direct** (default) | (none) | ACME Let's Encrypt certificates | `https://` |
| **Cloudflare** | `cloudflareProxy: true` | `tls internal` (self-signed), Cloudflare handles public TLS (SSL mode "Full") | `https://` |
| **Insecure** | `insecure: true` | `auto_https off`, HTTP only | `http://` |

Each deployment gets a `.caddy` file in `/etc/caddy/sites/` mapping a subdomain to a localhost port. In insecure mode, site addresses are prefixed with `http://` to prevent Caddy from attempting TLS. Caddy reloads via its admin API when routes change.

**SQLite** (via `modernc.org/sqlite`, pure Go) stores users and deployments. The pure-Go implementation eliminates CGO — the server binary is fully static.

**Docker** with gVisor provides container isolation. Base images are pre-pulled for each supported language.

## Persistent Data

See [persistent-data.md](persistent-data.md) for full details. In summary:

- **`/data` bind mount** — every dynamic container gets a persistent directory surviving rebuilds
- **`/_data/*` REST API** — built-in document store for any deployment (including static HTML)

Both share the same host directory per deployment (`/var/lib/openberth/persist/{id}/`).

## Environment Variables & Port Configuration

The CLI loads env vars from three sources (later overrides earlier): `.env` auto-load, `--env-file`, `--env` flags. The server always injects `PORT` matching the container's listen port.

The detector assigns default ports per framework (Next.js -> 3000, Go -> 8080, Python -> 8000). The `--port` flag overrides this. Apps should read `PORT` from environment.

Memory and CPU are configurable per deployment on both `deploy` and `update`.

## Single-File Deploy

The CLI scaffolds complete Vite projects from single `.jsx`, `.tsx`, `.vue`, `.svelte`, or `.html` files. The scaffolder parses imports to detect dependencies, detects Tailwind from class names, and produces a build-ready project. By the time the tarball reaches the server, it looks like any other Vite project.

## File Layout on Server

```
/usr/local/bin/
  berth-server           Single Go binary (daemon)
  berth-admin            Admin helper script
/var/lib/openberth/
  openberth.db               SQLite (users + deployments)
  config.json                 Server config
  deploys/<id>/               Extracted project code per deployment
  persist/<id>/               Persistent data directory per deployment
  persist/<id>/store.db       SQLite document store (/_data API)
/etc/caddy/
  Caddyfile                   Main config
  sites/<subdomain>.caddy     Per-deploy reverse proxy
```

## Deployment Lifecycle

```
CLI: openberth deploy [file|dir] [--port --memory --cpus --env --env-file --protect --network-quota]
  |
  +- Scaffold temp Vite project (if single file)
  +- Auto-load .env from project dir
  +- Load --env-file (if specified)
  +- Create tarball (respects .gitignore)
  +- Upload to POST /api/deploy with env + port + memory + cpus + protect + quota
  |
Server:
  +- Extract tarball
  +- Detect language + framework + version
  +- Process access control (if --protect specified, active before route goes live)
  +- Select build image (golang:1.23 / python:3.12-slim / node:20-slim)
  +- Phase 1: Build container (no mem limit, gVisor)
  |   +- Language-specific install (npm ci / go mod download / pip install)
  |   +- Production build (next build / go build)
  +- Phase 2: Runtime container (configurable limits, gVisor)
  |   +- May use different image (debian:bookworm-slim for Go)
  |   +- Start server (next start / ./server / gunicorn / etc.)
  +- Configure Caddy route (with access control if set) + reload
  +- Return live URL (+ access mode and API key if protected)
```

## Error Handling

The CLI reads the full response body before JSON decode. If the server returns HTML (Caddy running but daemon down), the error identifies this and suggests `systemctl status openberth`. The `--verbose` / `--debug` flag prints the full HTTP exchange.

The server logs every deploy/update/destroy with timing, language, framework, port, and gVisor status. Build failures include full build output.
