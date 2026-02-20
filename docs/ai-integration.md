# AI Integration

OpenBerth has three integration surfaces so any AI — web chat, desktop app, or terminal agent — can deploy code directly.

## 1. HTTP API (Web-Based AI)

Two endpoints for different scales:

### Inline JSON (small projects, 1-20 files)

```bash
curl -X POST https://openberth.example.com/api/deploy/code \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "files": {
      "package.json": "{\"dependencies\":{\"next\":\"latest\"}}",
      "pages/index.js": "export default () => <h1>Hello</h1>"
    },
    "name": "my-app"
  }'
```

Optional fields: `memory` (e.g. `"1g"`), `cpus` (e.g. `"1"`), `network_quota` (e.g. `"5g"` — overrides admin default), `protect_mode` (`"basic_auth"`, `"api_key"`, `"user"`), `protect_username`, `protect_password`, `protect_api_key`, `protect_users` (comma-separated usernames for `"user"` mode, e.g. `"alice,bob"`).

### Tarball (large projects, binary assets)

```bash
tar czf project.tar.gz -C ./my-project .
curl -X POST https://openberth.example.com/api/deploy \
  -H "Authorization: Bearer $KEY" \
  -F "tarball=@project.tar.gz" \
  -F "name=my-app" \
  -F "protect_mode=api_key"
```

Both return:

```json
{"id": "abc123", "url": "https://my-app.openberth.example.com", "status": "building"}
```

When `protect_mode` is set, the response also includes `accessMode` and (for `api_key`) the generated `apiKey`.

### Update a deployment

```bash
# Inline JSON update
curl -X POST https://openberth.example.com/api/deploy/abc123/update/code \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"files": {"pages/index.js": "export default () => <h1>Updated</h1>"}}'

# Tarball update
curl -X POST https://openberth.example.com/api/deploy/abc123/update \
  -H "Authorization: Bearer $KEY" \
  -F "tarball=@project.tar.gz"
```

### Access control

```bash
# Basic auth
curl -X POST https://openberth.example.com/api/deployments/abc123/protect \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"mode": "basic_auth", "username": "admin", "password": "secret"}'

# API key (auto-generated)
curl -X POST https://openberth.example.com/api/deployments/abc123/protect \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"mode": "api_key"}'

# User-based (any authenticated user)
curl -X POST https://openberth.example.com/api/deployments/abc123/protect \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"mode": "user"}'

# User-based (restricted to specific users)
curl -X POST https://openberth.example.com/api/deployments/abc123/protect \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"mode": "user", "users": ["alice", "bob"]}'

# Remove protection
curl -X POST https://openberth.example.com/api/deployments/abc123/protect \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"mode": "public"}'
```

### Update metadata (network quota)

```bash
curl -X PATCH https://openberth.example.com/api/deployments/abc123 \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"network_quota": "5g"}'
```

### Other endpoints

```
GET  /api/deployments              List all deployments
GET  /api/deployments/{id}         Deployment status
GET  /api/deployments/{id}/logs    Container logs
PATCH /api/deployments/{id}        Update metadata (network quota)
DELETE /api/deployments/{id}       Destroy deployment
```

### Sandbox endpoints

```
POST /api/sandbox                  Create sandbox (JSON body with files)
POST /api/sandbox/{id}/push        Push file changes
POST /api/sandbox/{id}/install     Install packages
POST /api/sandbox/{id}/exec        Run command in sandbox
GET  /api/sandbox/{id}/logs        Get sandbox logs
POST /api/sandbox/{id}/promote     Promote to production deployment
DELETE /api/sandbox/{id}           Destroy sandbox
```

Sandbox create accepts the same `protect_mode`, `protect_username`, `protect_password`, `protect_api_key`, `protect_users`, and `network_quota` fields as deploy.

## 2. MCP Server (Claude Desktop / Cursor)

The standalone MCP server lets AI assistants deploy via native tool calls over stdio transport.

### Install

Download `berth-mcp-{os}-{arch}` from [Releases](https://github.com/openberth/openberth/releases/latest):

```bash
chmod +x berth-mcp-darwin-arm64
sudo mv berth-mcp-darwin-arm64 /usr/local/bin/berth-mcp
```

Or build from source: `make mcp` (requires Go 1.24+).

### Configure Claude Desktop

Add to `~/.config/claude/claude_desktop_config.json`:

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

Use `http://` for the server URL if OpenBerth was installed with `--insecure` mode.

### Available tools

Once configured, the AI can call these tools:

- `berth_deploy` — Deploy inline files (supports `protect_mode`, `protect_users`, `network_quota`)
- `berth_deploy_dir` — Deploy a local directory (supports `protect_mode`, `protect_users`, `network_quota`)
- `berth_update` — Update a deployment
- `berth_status` — Get deployment status
- `berth_logs` — Get container logs
- `berth_list` — List all deployments
- `berth_protect` — Set access control (supports `users` list for `user` mode)
- `berth_destroy` — Destroy a deployment
- `berth_sandbox_create` — Create a dev sandbox (supports `protect_mode`, `protect_users`, `network_quota`)
- `berth_sandbox_push` — Push file changes to sandbox
- `berth_sandbox_install` — Install packages in sandbox
- `berth_sandbox_exec` — Run command in sandbox
- `berth_sandbox_promote` — Promote sandbox to production

## 3. Server-Side MCP (Claude.ai Web)

The server also exposes an MCP endpoint using Streamable HTTP transport at `/mcp`. This runs inside the server process and calls business logic directly — no HTTP round-trip. It's used by Claude.ai's web interface.

The server-side MCP supports the same tools as the standalone MCP (minus the `_dir` variants which require local filesystem access).

## 4. Stdin Pipe (Terminal Agents)

Claude Code, Aider, or any terminal AI can pipe JSON directly to the CLI:

```bash
# Simple: just file paths to content
echo '{"index.html": "<h1>Hello World</h1>"}' | openberth deploy --stdin

# Full: with options
echo '{
  "files": {
    "app.py": "from flask import Flask\napp = Flask(__name__)",
    "requirements.txt": "flask\ngunicorn"
  },
  "name": "my-api",
  "env": {"SECRET": "abc"}
}' | openberth deploy --stdin

# Machine-readable output for agents
echo '{"index.html": "..."}' | openberth deploy --stdin --json
```
