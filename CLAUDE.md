# OpenBerth — Claude Code Instructions

## What This Project Is

OpenBerth is a self-hosted deployment platform: give it code, get a live URL. It supports Node.js, Python, Go, and static HTML. Every deployment runs sandboxed in gVisor containers with automatic TLS via Caddy (or HTTP-only in `--insecure` mode).

Three binaries, all pure Go, zero runtime dependencies:
- **`apps/server`** — Main daemon (API, container management, SQLite, Caddy config, MCP handler, self-installer)
- **`apps/cli`** — Client CLI (deploy, update, dev mode, file watcher)
- **`apps/mcp`** — MCP stdio server for Claude Desktop / Cursor (bridges to server API)

The server binary includes a built-in `install` subcommand (`berth-server install --domain example.com`) that provisions the host machine — no separate installer binary needed. The installer is **Linux-only** (Ubuntu/Debian) since it depends on apt, Docker, gVisor, Caddy, and systemd.

## Project Layout

```
apps/
  server/              Main daemon
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
        handler.go     Shared handler setup, middleware, helpers
        auth.go        Authentication middleware
        deploy.go      Deploy/update/destroy/list/status/logs handlers
        sandbox.go     Sandbox HTTP handlers
        admin.go       Admin API handlers
        login.go       Browser login flow
        oidc.go        OAuth/OIDC provider
        oauth.go       OAuth client/token management
        data.go        /_data/* document store handler
        cleanup.go     TTL-based expiry handler
      service/         Business logic layer
        service.go     Service struct, constructor, dependencies
        types.go       All param/result types (consolidated)
        provision.go   Shared deploy/sandbox provisioning helpers
        deploy.go      Code deploy/update operations
        tarball.go     Tarball deploy/update, backup restore, RebuildAll
        sandbox.go     Sandbox create/push/exec/install/promote
        query.go       List/get deployment queries
        manage.go      Destroy, protect, lock, metadata operations
        lifecycle.go   Startup reconciliation, cleanup, quota reset
        helpers.go     Utility functions (sanitize, shell quote, detect lang)
        access.go      Access control computation
        errors.go      Typed service errors (BadRequest, NotFound, etc.)
    gallery/           React/TypeScript gallery UI (embedded at build time)
    main.go            HTTP routing, setup, entry point
    gallery_embed.go   Embedded gallery static files
  cli/                 CLI client (pure stdlib, zero deps except fsnotify)
  mcp/                 Standalone MCP server (stdio transport, HTTP bridge to server)
examples/              Example projects (nextapp, goapp, pythonapp, jsxapp, rsvpapp)
bin/                   Build output
.github/               CI workflows, issue templates, PR template, dependabot
.golangci.yml          Lint config (golangci-lint v2)
```

## Build Commands

```bash
make build          # Build server + CLI + MCP (installs CLI and MCP)
make server         # Build server only (builds gallery first)
make cli            # Build CLI only
make mcp            # Build MCP only
make gallery        # Build React gallery UI
make vet            # Run go vet on all modules
make lint           # Run golangci-lint on all modules (requires golangci-lint v2)
make clean          # Clean bin/
```

All `make` targets inject version via `-ldflags "-X main.version=..."` using `git describe`. Override with `make build VERSION=v1.0.0`.

**Cross-compilation:** The `server` target accepts `GOOS`/`GOARCH`. Native builds go to `bin/berth-server`; cross-compiled builds include the target in the filename:

```bash
make server                          # bin/berth-server (native)
make server GOOS=linux               # bin/berth-server-linux-arm64
make server GOOS=linux GOARCH=amd64  # bin/berth-server-linux-amd64
```

**Upload to remote host:**
```bash
make deploy GOOS=linux DEPLOY_HOST=mybox DEPLOY_PATH=/opt/openberth
```

`deploy` only does scp — build first with `make server GOOS=linux`.

**Individual module builds:**
```bash
cd apps/server && go build ./...
cd apps/cli && go build ./...
cd apps/mcp && go build ./...
```

**Verification after changes:**
```bash
cd apps/server && go build ./... && go vet ./...
cd apps/mcp && go build ./... && go vet ./...
cd apps/cli && go build ./... && go vet ./...
```

## Architecture Rules

### Go Conventions
- **CGO_ENABLED=0** — All binaries are fully static. Never introduce CGO dependencies.
- **Pure Go SQLite** — Uses `modernc.org/sqlite`, not `mattn/go-sqlite3`. This is intentional for static builds.
- **No web frameworks** — Server uses `net/http` stdlib only. No Gin, Echo, Chi, etc.
- **No ORMs** — Direct SQL with `database/sql`. Keep it simple.
- **Standard error handling** — Typed service errors in `internal/service/errors.go` (ErrBadRequest, ErrNotFound, ErrForbidden, etc.). HTTP handlers translate these to status codes. MCP handler uses `errorResult()` / `textResult()`.

### Go Workspace
This is a **Go workspace** (`go.work`) with three independent modules (server, cli, mcp). Each app has its own `go.mod`. Don't add cross-module dependencies — the CLI and MCP are intentionally dependency-free from the server.

### Server Architecture

**Request flow:** HTTP handler (`internal/httphandler/`) → Service method (`internal/service/`) → internal packages (store, container, proxy, framework)

- `internal/httphandler/` — HTTP layer (parse request, call service, write response). Go 1.22+ enhanced patterns.
- `internal/httphandler/mcp/` — Server-side MCP handler (Streamable HTTP transport, calls same service methods)
- `internal/service/` — Business logic layer. All deploy/sandbox/query/manage operations live here.
  - `types.go` — All param and result types in one place (CodeDeployParams, DeployResult, etc.)
  - `provision.go` — Shared provisioning helpers (validation, identity generation, framework detection, async build, access control)
  - `deploy.go` / `tarball.go` / `sandbox.go` — Operation implementations using helpers from provision.go
- `internal/` — Domain packages (config, store, container, proxy, framework, datastore, bandwidth)

**Key pattern:** The MCP handler and HTTP handlers share business logic through Service methods. Never duplicate business logic between them.

### MCP Architecture

**Two MCP implementations exist:**

1. **Server-side MCP** (`apps/server/internal/httphandler/mcp/handler.go`) — Streamable HTTP transport, runs inside the server process, calls Service methods directly. Used by Claude.ai web interface.

2. **Standalone MCP** (`apps/mcp/main.go`) — Stdio transport, separate binary, bridges to server via HTTP API. Used by Claude Desktop, Cursor, and other local MCP clients.

**Both must stay in sync.** When adding a new tool:
1. Add the tool definition (name, description, schema) to both files
2. Add the handler in the server-side MCP (calls `do*` directly)
3. Add the handler in the standalone MCP (calls HTTP API)
4. Tool descriptions must be identical between both MCPs (except standalone has extra `_dir` tools)

**Tool descriptions are prescriptive, not just descriptive.** They tell the AI *when* to use the tool, *when not* to, and *what to expect*. Response messages include concrete next-step guidance with IDs.

### Container Model

**Two-phase deployment:**
1. **Build phase** — Unconstrained memory, gVisor sandboxed, produces artifacts in a Docker volume
2. **Runtime phase** — Tight limits (512MB mem, 0.5 CPU default), gVisor sandboxed, serves pre-built output

**Blue-green updates:** New volume created, artifacts cached from old, build runs while old container serves, swap on success, rollback on failure.

**Sandbox (dev) mode:** Single container with dev server, hot reload via HMR/file sync, promotes to production deployment.

### Subdomain Naming

When a user provides an explicit `name`, it becomes the exact subdomain (e.g. `name: "myapp"` → `myapp.domain.com`). Collisions return an error. When no name is provided, an auto-generated name with a random suffix is used (e.g. `app-d6b2f1-d6b2.domain.com`). See `generateDeployIdentity()` in `service/provision.go`.

### Framework Detection

`internal/framework/` implements the `LanguageProvider` interface. Detection order: Go → Python → Node → Static (first match wins). Each provider handles detection, version selection, build/run commands, and cache volumes.

When adding a new framework, implement the provider interface and register it — don't add special cases in the handlers.

### Security Model
- **Assume code is hostile** — Every container runs in gVisor with all capabilities dropped
- **Localhost-only ports** — Containers bind `127.0.0.1:port`, Caddy handles external traffic
- **No secrets in code** — Environment variables are stored separately, never in the tarball
- **Hard delete** — Destroying a deployment removes everything (DB record, code, volumes, proxy config)
- **Network quota** (optional) — Caddy access log based byte cap per deployment, configurable via admin settings or per-deploy override
- **Disk size limit** (optional) — Container root filesystem size limit via `--storage-opt`

### TLS Modes

Three TLS modes controlled at install time:

| Mode | Install flag | Caddy behavior | URLs |
|------|-------------|----------------|------|
| **Direct** (default) | (none) | ACME Let's Encrypt certificates | `https://` |
| **Cloudflare** | `--cloudflare` | `tls internal` (self-signed), Cloudflare handles public TLS | `https://` |
| **Insecure** | `--insecure` | `auto_https off`, HTTP only | `http://` |

The mode is stored in `config.json` (`cloudflareProxy: true` or `insecure: true`) and propagated to:
- `config.go` → `BaseURL` (`http://` or `https://`)
- `proxy.go` → per-site Caddy configs (site address prefix, TLS directive)
- `service/provision.go` → deployment URLs returned by API

## Common Development Recipes

### Adding a New API Endpoint

1. Define param/result types in `service/types.go`
2. Implement business logic method on `*Service` in the appropriate `service/*.go` file
3. Add HTTP handler in `httphandler/` — use `decodeJSON[T]()` for request parsing, `requireAuth()`/`requireAdmin()` for auth, `writeErr()` for error responses, `jsonResp()` for success
4. Register the route in `main.go` using Go 1.22+ patterns (e.g. `mux.HandleFunc("POST /api/foo/{id}", CORS(h.Foo))`)
5. If the endpoint should be accessible via MCP, also add it as an MCP tool (see below)

### Adding a New MCP Tool

Both MCP implementations must stay in sync:

1. **Server-side** (`httphandler/mcp/tools.go`): Add tool definition (name, description, inputSchema) to the tools list
2. **Server-side** (`httphandler/mcp/handler.go`): Add `case "berth_<name>"` in `callTool` switch — call Service method directly
3. **Standalone** (`apps/mcp/main.go`): Add identical tool definition to the tools list
4. **Standalone** (`apps/mcp/main.go`): Add `case "berth_<name>"` — call HTTP API via `doPost`/`doGet`
5. Tool name format: `berth_<action>` (e.g. `berth_deploy`, `berth_sandbox_create`)
6. Tool descriptions are prescriptive — tell AI *when* to use, *when not* to, and *what to expect*
7. Response messages should include next-step guidance with deployment IDs

### Adding a New Language/Framework

1. Create `internal/framework/lang_<name>.go` implementing the `LanguageProvider` interface
2. Register it in `internal/framework/provider.go` `init()` — order matters (most specific first, Static last)
3. The interface methods to implement:
   - `Language()` — identifier string
   - `Detect(projectDir)` — return `*FrameworkInfo` or nil
   - `BuildScript(fw)` — shell script for build container
   - `RunScript(fw)` — shell script for runtime container
   - `CacheVolumes(userID)` — Docker `-v` flags for dependency caching
   - `RebuildCopyScript()` — copy cached deps between volumes
   - `SandboxEntrypoint(fw, port)` — dev server startup script
   - `SandboxEnv()` — sandbox-specific env overrides
   - `StaticOnly()` — true if no build/run needed

### Adding a New CLI Command

1. Add the command function in `apps/cli/commands.go`
2. Register it in the `switch` in `apps/cli/main.go`
3. Add help text to the usage string
4. Use `apiRequest()` for HTTP calls — it handles auth headers and base URL

## Internal Naming Conventions

These identifiers are used across container, proxy, and service layers. Keep them consistent.

| Category | Pattern | Example |
|----------|---------|---------|
| Build container | `openberth-build-{id}` | `openberth-build-abc123` |
| Runtime container | `openberth-run-{id}` | `openberth-run-abc123` |
| Sandbox container | `openberth-sandbox-{id}` | `openberth-sandbox-abc123` |
| Docker labels | `openberth=true`, `openberth.phase=build\|run\|sandbox`, `openberth.id={id}` | |
| Build script | `.openberth-build.sh` | Written into deploy volume |
| Run script | `.openberth-run.sh` | Written into deploy volume |
| Sandbox script | `.openberth-sandbox.sh` | Written into deploy volume |
| CLI entry script | `.openberth-entry.sh` | Auto-scaffold entrypoint |
| CLI config file | `.berth.json` | Per-project config |
| Ignore file | `.berthignore` | Like .gitignore for deploys |
| Cookie name | `openberth_session` | Browser auth |
| API key prefix | `sc_` | Kept for backwards compat |
| Session prefix | `ses_` | Session tokens |
| Cache volumes | `openberth-go-mod`, `openberth-pip-cache`, `openberth-npm-cache-{userId}` | |

## HTTP Handler Patterns

Shared helpers in `httphandler/handler.go` — use these instead of writing raw responses:

```go
jsonResp(w, 200, data)           // JSON success response
jsonErr(w, 400, "message")       // JSON error response
writeErr(w, err)                 // Translate *service.AppError → JSON error
body, ok := decodeJSON[T](w, r)  // Decode JSON body (writes 400 on failure)
user := h.requireAuth(w, r)      // Returns user or writes 401 (check for nil)
user := h.requireAdmin(w, r)     // Returns admin user or writes 401/403
```

**Error propagation pattern:**
- Service methods return `*service.AppError` (from `ErrBadRequest()`, `ErrNotFound()`, etc.)
- HTTP handlers call `writeErr(w, err)` which extracts the status code
- MCP handlers call `errorResult(err.Error())` which returns an MCP error response

## Authentication Flow

1. **API key**: `Authorization: Bearer sc_...` or `X-API-Key: sc_...` header → looked up in `users` table
2. **OAuth token**: Non-`sc_` bearer token → looked up in `oauth_tokens` table
3. **Session cookie**: `openberth_session=ses_...` → looked up in `sessions` table
4. If none match → unauthenticated (nil user)

## Known Gotchas

- **Gallery is embedded at compile time**: `apps/server/gallery/dist/` is included via `go:embed` in `gallery_embed.go`. If you change gallery source, run `make gallery` before `make server` (the Makefile handles this automatically for `make server` and `make build`).
- **Vite allowedHosts**: Vite 6+ checks Host headers against `allowedHosts`. Since OpenBerth runs behind Caddy, both `server.allowedHosts` and `preview.allowedHosts` must be set to `true`. This is patched automatically in `lang_node.go` via `viteAllowHostsScript()`.
- **Hidden file prefix**: Files starting with `.openberth` are excluded from source downloads and directory listings. Don't use this prefix for user-visible files.
- **go.work.sum**: Auto-generated, not committed. Listed in `.gitignore`.
- **Module isolation**: The CLI and MCP modules must not import from the server module. They communicate via HTTP API only.
- **Container scripts as hidden files**: Build/run/sandbox shell scripts are written into the deploy volume as `.openberth-*.sh` files, then executed inside the container. They're not part of the user's source code.

## CI / GitHub

GitHub Actions run on every PR and push to main:
- **CI** (`.github/workflows/ci.yml`) — Build + vet matrix over all 3 Go modules + gallery. The `ci-pass` job is the single required check for branch protection.
- **Lint** (`.github/workflows/lint.yml`) — `golangci-lint` v2 matrix over all 3 modules. Config at `.golangci.yml` (repo root).
- **Security** (`.github/workflows/security.yml`) — `govulncheck` weekly + on push to main.
- **Release** (`.github/workflows/release.yml`) — Tag-triggered, cross-compiles all binaries + checksums.
- **Dependabot** (`.github/dependabot.yml`) — Weekly updates for Go modules, npm (gallery), and GitHub Actions.

Community files: `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CONTRIBUTING.md`, issue templates, PR template.

## Code Style

- Follow `gofmt` and `go vet` — run both before committing
- Run `make lint` before submitting PRs (requires [golangci-lint](https://golangci-lint.run/) v2)
- Keep functions focused — no unnecessary abstractions
- Flat is better than nested — avoid deep package hierarchies
- Error messages should be actionable — include what failed and what to do next
- No third-party dependencies in CLI or MCP unless absolutely necessary
- Server dependencies must be pure Go (no CGO)

## API Endpoints Reference

### Deployments
- `POST /api/deploy` — Upload tarball
- `POST /api/deploy/code` — Inline files (JSON)
- `POST /api/deploy/{id}/update` — Update tarball
- `POST /api/deploy/{id}/update/code` — Update inline files
- `GET /api/deployments` — List
- `GET /api/deployments/{id}` — Status
- `GET /api/deployments/{id}/logs` — Logs
- `GET /api/deployments/{id}/source` — Download source code (tarball or JSON)
- `DELETE /api/deployments/{id}` — Destroy
- `PATCH /api/deployments/{id}` — Update metadata (network quota, etc.)
- `POST /api/deployments/{id}/protect` — Access control

### Sandbox
- `POST /api/sandbox` — Create (JSON body with files)
- `POST /api/sandbox/{id}/push` — Push file changes
- `POST /api/sandbox/{id}/install` — Install packages
- `POST /api/sandbox/{id}/exec` — Run command
- `GET /api/sandbox/{id}/logs` — Get logs
- `DELETE /api/sandbox/{id}` — Destroy
- `POST /api/sandbox/{id}/promote` — Promote to production

### Data
- `GET/POST/PUT/DELETE /_data/{collection}[/{id}]` — Per-deployment document store

## Database

SQLite at `{dataDir}/openberth.db` with WAL mode. Tables: `users`, `deployments`, `oauth_clients`, `oauth_codes`, `oauth_tokens`, `sessions`, `settings`, `login_codes`.

Per-deployment document store: SQLite at `{persistDir}/{deploymentID}/store.db`.

## Testing Changes

There are no automated tests yet. Verify changes by:
1. `go build ./...` and `go vet ./...` on all three modules
2. For server changes: deploy to a test instance and verify with CLI
3. For MCP changes: test with Claude Desktop or MCP inspector
4. For CLI changes: test deploy/update/dev commands against a running server

## File Paths on Server

- Config: `/var/lib/openberth/config.json`
- Database: `/var/lib/openberth/openberth.db`
- Deploy source: `/var/lib/openberth/deploys/{id}/`
- Persistent data: `/var/lib/openberth/persist/{id}/`
- Caddy sites: `/etc/caddy/sites/{subdomain}.caddy`
- Systemd service: `/etc/systemd/system/openberth.service`
