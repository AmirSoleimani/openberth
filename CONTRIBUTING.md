# Contributing to OpenBerth

Thanks for your interest in contributing! Here's how to get started.

## Project Structure

OpenBerth is a Go workspace with three independent modules:

```
apps/
  server/      Server daemon (deployment platform + built-in installer)
  cli/         CLI client
  mcp/         MCP stdio server (for Claude Desktop / Cursor)
docs/          Documentation (installation, CLI reference, architecture, etc.)
examples/      Example projects (Next.js, Go, Python, JSX, static)
```

## Development Setup

1. **Go 1.24+** required
2. Clone and build:

```bash
git clone https://github.com/openberth/openberth.git
cd openberth
make build
```

This builds the server, CLI, and MCP into `bin/`.

To build individual modules:

```bash
cd apps/server && go build .
cd apps/cli && go build .
cd apps/mcp && go build .
```

## Making Changes

1. Fork the repo and create a feature branch
2. Make your changes
3. Ensure the build passes:

```bash
cd apps/server && go build ./... && go vet ./...
cd apps/cli && go build ./... && go vet ./...
cd apps/mcp && go build ./... && go vet ./...
```

4. Submit a pull request

## Linting

We use [golangci-lint](https://golangci-lint.run/) v2 for static analysis. Install it, then run:

```bash
make lint
```

CI runs the same linter on every PR.

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep function bodies focused — no unnecessary abstractions
- The server uses `internal/` packages for clean separation:
  - `internal/config` — configuration loading
  - `internal/store` — SQLite persistence
  - `internal/container` — Docker/gVisor container lifecycle
  - `internal/proxy` — Caddy reverse proxy management
  - `internal/framework` — language/framework detection
  - `internal/datastore` — per-deployment document store
  - `internal/install` — self-installer (local provisioning)

## Architecture

See [docs/architecture.md](docs/architecture.md) for detailed technical documentation covering the two-phase build system, blue-green deploys, security model, and more.

## Reporting Issues

Open an issue with:
- What you expected to happen
- What actually happened
- Steps to reproduce
- Relevant logs (`openberth logs <id>`, `journalctl -u openberth`)
