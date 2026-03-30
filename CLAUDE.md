# OpenBerth — Claude Code Instructions

## What This Project Is

Self-hosted deployment platform: give it code, get a live URL. Node.js, Python, Go, Static HTML. Sandboxed in gVisor, auto-TLS via Caddy.

Three Go binaries, zero runtime deps: `apps/server` (daemon), `apps/cli` (client), `apps/mcp` (MCP stdio bridge).

## Build & Verify

```bash
make build                    # All three binaries
make server GOOS=linux        # Cross-compile server

# After changes — verify all three modules:
cd apps/server && go build ./... && go vet ./...
cd apps/cli && go build ./... && go vet ./...
cd apps/mcp && go build ./... && go vet ./...

make lint                     # golangci-lint v2
```

No automated tests yet. Verify manually against a running server.

## Architecture Rules

### Hard constraints
- **CGO_ENABLED=0** — All binaries fully static. Never introduce CGO.
- **Pure Go SQLite** — `modernc.org/sqlite`, not `mattn/go-sqlite3`.
- **No web frameworks** — `net/http` stdlib only. No Gin/Echo/Chi.
- **No ORMs** — Direct SQL with `database/sql`.
- **No third-party deps in CLI or MCP** unless absolutely necessary.

### Go workspace (`go.work`)
Three independent modules: `apps/server`, `apps/cli`, `apps/mcp`. Each has its own `go.mod`. **No cross-module imports** — CLI and MCP communicate with server via HTTP only.

**Shared logic by copy, not import.** Duplicate utility code (e.g., tarball creation) in both CLI and MCP rather than extracting a shared library.

### Server request flow
HTTP handler (`internal/httphandler/`) → Service method (`internal/service/`) → internal packages (store, container, proxy, framework)

- `service/types.go` — All param/result types
- `service/provision.go` — Shared provisioning helpers
- `service/errors.go` — Typed errors (ErrBadRequest, ErrNotFound, etc.) → handlers translate to HTTP status codes

### Two MCP implementations must stay in sync
1. **Server-side** (`apps/server/internal/httphandler/mcp/`) — Streamable HTTP, calls Service directly
2. **Standalone** (`apps/mcp/main.go`) — Stdio, bridges to HTTP API

When adding a tool: add definition + handler to **both**. Tool descriptions are prescriptive (tell AI *when* to use).

### Framework detection
`internal/framework/` — `LanguageProvider` interface, detection order: Go → Python → Node → Static. All callsites use `DetectWithOverrides()` which reads `.berth.json` overrides. New frameworks: implement the interface, register in `provider.go`.

## Development Recipes

### New API endpoint
1. Types in `service/types.go`
2. Business logic in `service/*.go`
3. HTTP handler in `httphandler/` — use `decodeJSON[T]()`, `requireAuth()`, `writeErr()`, `jsonResp()`
4. Route in `main.go`: `mux.HandleFunc("POST /api/foo/{id}", CORS(h.Foo))`
5. If MCP-accessible: add tool to both MCP implementations

### New MCP tool
1. Server-side `mcp/tools.go`: tool definition → `mcp/handler.go`: `case "berth_<name>"`
2. Standalone `apps/mcp/main.go`: same tool definition → `case "berth_<name>"` via HTTP

### New CLI command
1. Function in `apps/cli/commands.go`
2. Register in `main.go` switch
3. Help text in usage string

### New language/framework
1. `internal/framework/lang_<name>.go` implementing `LanguageProvider`
2. Register in `provider.go` `init()` — order matters (Static last)
3. Honor `fw.InstallCmd` in `BuildScript()` and `SandboxEntrypoint()`

## Key Patterns

### CLI deploy behavior
`berth deploy` auto-waits for build (polls 2s, up to 6 min), shows QR code, opens browser. `--no-wait` skips, `--no-qr` suppresses QR. QR auto-suppressed when piped. Shared `waitForBuild()` used by deploy, promote, and auto-update paths.

### Container model
Two-phase: build (unconstrained memory, gVisor) → runtime (512MB/0.5 CPU, gVisor). Blue-green updates with rollback on failure. Sandbox mode: single container with dev server + HMR.

### `.berth.json` dual purpose
CLI reads and writes client-side fields (`name`, `ttl`, `memory`, `port`, `protect`, `networkQuota`, `secrets`, `deploymentId`, `url`). Server reads override fields (`language`, `build`, `start`, `install`, `dev`). Both coexist in same file. Deploy parameters passed via flags are auto-persisted to `.berth.json` for subsequent deploys.

### Secrets store
Encrypted secrets (AES-256-GCM envelope encryption) stored server-side. Master key auto-generated on first server start, saved in `config.json`. User-scoped or global (admin-only). Referenced by name at deploy time (`--secret NAME`), resolved JIT at container creation. Secret values never stored in `env_json`. Rotating a secret auto-restarts affected deployments via `RecreateRuntime` (runtime-only restart, ~5s, no rebuild). MCP tools: `berth_secret_set`, `berth_secret_list` (returns names + descriptions for AI discoverability), `berth_secret_delete`.

### Auth chain
API key (`sc_` prefix) → OAuth token → Session cookie (`openberth_session`) → unauthenticated (nil)

## Gotchas

- **Gallery embedded at compile time** — Run `make gallery` before `make server` if gallery source changed (Makefile does this automatically for `make server`/`make build`).
- **Vite allowedHosts** — Vite 6+ needs both `server.allowedHosts` and `preview.allowedHosts` set to `true`. Patched in `lang_node.go` via `viteAllowHostsScript()`.
- **Module isolation** — CLI/MCP must never import from server module.
- **API key prefix `sc_`** kept for backwards compat (originally "sandcastle").
