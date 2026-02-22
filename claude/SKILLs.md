Help me with OpenBerth deployment. Here is my question or context: $ARGUMENTS

Use the reference below to answer. If I asked about a specific issue, focus on the relevant section. If no arguments, give a brief overview of the two deployment workflows.

---

# OpenBerth Deploy Reference

## Deployment Workflows

**Choose your approach:**

| Approach | When to use | Flow |
|----------|-------------|------|
| **One-shot deploy** | Final code, no iteration needed | `berth_deploy` → `berth_status` → done |
| **Iterative sandbox** | Building step-by-step, expect changes | `berth_sandbox_create` → `berth_sandbox_push` (repeat) → `berth_sandbox_promote` |

### One-Shot Deploy (MCP)
1. `berth_deploy` — upload code, starts build (15-60s)
2. `berth_status` — poll until status is `running` (or `failed`)
3. If failed: `berth_logs` to diagnose

### Iterative Sandbox (MCP)
1. `berth_sandbox_create` — creates dev container with hot reload
2. `berth_sandbox_push` — push file changes (instant, no rebuild)
3. `berth_sandbox_install` — add/remove packages
4. `berth_sandbox_exec` — run commands in the container
5. `berth_sandbox_promote` — promote to production deployment

### CLI Commands
```
berth deploy              # one-shot deploy from current directory
berth dev                 # create sandbox with file watcher
berth update [id]         # update existing deployment
berth promote [id]        # promote sandbox to production
berth logs [id]           # view build/runtime logs
berth status [id]         # check deployment status
berth list                # list all deployments
berth destroy [id]        # tear down deployment
```

## `.berth.json` Reference

All fields are optional. CLI reads its fields client-side; server reads override fields server-side.

### CLI Fields (read by `berth` CLI)
| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `name` | string | Exact subdomain name | `"myapp"` |
| `ttl` | string | Auto-expire duration | `"24h"`, `"7d"` |
| `memory` | string | Container memory limit | `"512m"`, `"1g"` |
| `port` | string | App listen port | `"3000"` |
| `protect` | string | Access control mode | `"password"`, `"api-key"` |
| `networkQuota` | string | Bandwidth cap | `"1g"`, `"500m"` |

### Server-Side Override Fields (read during build)
| Field | Type | Overrides | Required for fallback? |
|-------|------|-----------|----------------------|
| `language` | string | Forces provider (`node`, `python`, `go`, `static`) | Yes |
| `build` | string | Build command | No |
| `start` | string | Start/run command | Yes (if detection fails) |
| `install` | string | Package install command | No |
| `dev` | string | Dev server command (sandbox mode) | No |

### Auto-Populated Fields (written by CLI after deploy)
| Field | Description |
|-------|-------------|
| `deploymentId` | ID of the last deployment |
| `url` | Live URL |
| `sandboxId` | ID of active sandbox |

### Examples

**Node.js with pnpm:**
```json
{
  "name": "myapp",
  "install": "pnpm install --frozen-lockfile",
  "build": "pnpm run build",
  "start": "node dist/server.js"
}
```

**Python with Poetry:**
```json
{
  "language": "python",
  "install": "pip install poetry && poetry install --no-root",
  "start": "poetry run uvicorn main:app --host 0.0.0.0 --port $PORT"
}
```

**Go with custom build:**
```json
{
  "language": "go",
  "build": "go build -o server ./cmd/api",
  "start": "./server"
}
```

**Static site with build step:**
```json
{
  "language": "node",
  "build": "npm run build",
  "start": "npx serve dist -l $PORT"
}
```

## Framework Detection

Detection order: **Go** → **Python** → **Node** → **Static** (first match wins).

| Language | Trigger files | Default port |
|----------|--------------|--------------|
| Go | `go.mod` | 8080 |
| Python | `requirements.txt`, `pyproject.toml`, `setup.py`, `*.py` | 8000 |
| Node | `package.json` | 3000 |
| Static | `index.html` (no other language detected) | 8080 |

**When to override with `.berth.json`:**
- Detection picks the wrong language (e.g., both `go.mod` and `package.json` exist)
- Using a non-standard package manager (pnpm, yarn, poetry)
- Custom build/start commands needed
- Detection fails entirely — provide `language` + `start` as fallback

## Troubleshooting

### Build Failed
1. Check logs: `berth logs <id>` or `berth_logs` MCP tool
2. Common causes:
   - Missing dependencies — check `install` override in `.berth.json`
   - Wrong framework detected — set `language` in `.berth.json`
   - Build command error — set `build` in `.berth.json`

### Wrong Framework Detected
Add `.berth.json` with the correct `language` field. If detection succeeds but commands are wrong, override just `build`/`start`/`install` without changing `language`.

### Container Crashes / App Won't Start
- App **must** listen on the `PORT` environment variable (`0.0.0.0:$PORT`)
- Check `start` command in `.berth.json` — must be a foreground process
- Check logs for startup errors: `berth logs <id>`

### Port Binding Issues
- Never hardcode a port — always use `$PORT` env var
- Bind to `0.0.0.0`, not `127.0.0.1` or `localhost`
- Example: `node server.js --port $PORT --host 0.0.0.0`

### Sandbox Push Not Reflecting
- `berth_sandbox_push` is instant but relies on the dev server's hot reload
- If changes don't appear, the dev server may need a restart: destroy and recreate
- For dependency changes, use `berth_sandbox_install` instead of push

## Environment & Data

### Environment Variables
| Variable | Description |
|----------|-------------|
| `PORT` | Port your app must listen on (always set) |
| `DATA_DIR` | Path to persistent `/data` directory |
| Custom vars | Set via `env` field in deploy params or CLI `--env` flag |

### Persistent Data
- Each deployment gets a `/data` directory that survives rebuilds
- `DATA_DIR` env var points to it
- Use for SQLite databases, uploaded files, local state

### Document Store API
Built-in REST API at `/_data/{collection}` on each deployment:
```
GET    /_data/{collection}         # list documents
POST   /_data/{collection}         # create document
GET    /_data/{collection}/{id}    # get document
PUT    /_data/{collection}/{id}    # update document
DELETE /_data/{collection}/{id}    # delete document
```
