package main

// Static markdown content for the berth_guide MCP tool. Each topic is
// self-contained so an AI client can fetch only the section it needs.
//
// This file is **duplicated verbatim** in apps/mcp/guide.go (the
// standalone stdio bridge). Per CLAUDE.md "shared logic by copy, not
// import" — the three Go modules don't import each other. A unit test
// (mcp/guide_test.go and apps/mcp/guide_test.go) compares hashes to
// catch drift in CI when only one side is updated.

const aiGuideOverview = `# OpenBerth conventions — primer

Call this tool again with a ` + "`topic`" + ` argument for a deep-dive on any section below.

OpenBerth turns code into a live HTTPS URL. To use it well you need to know a handful of platform conventions that aren't obvious from the tool list:

| Topic | Covers |
|---|---|
| ` + "`workflow`" + ` | When to use sandbox vs deploy; the build-status poll loop. |
| ` + "`storage`" + ` | Persistent ` + "`/data`" + ` mount and per-deployment ` + "`/_data`" + ` document store. Where to put app state. |
| ` + "`secrets`" + ` | How to handle secrets — you never see values, the user stores them, you reference by name. |
| ` + "`limits`" + ` | Memory/CPU defaults, build vs runtime, document-store limits. |
| ` + "`ttl`" + ` | Format spec for deployment expiry (` + "`24h`" + `, ` + "`7d`" + `, ` + "`0`" + ` for never). |
| ` + "`access`" + ` | Four access-control modes: ` + "`public`" + ` / ` + "`basic_auth`" + ` / ` + "`api_key`" + ` / ` + "`user`" + `. |
| ` + "`frameworks`" + ` | Auto-detection order, version pinning sources, ` + "`.berth.json`" + ` overrides. |
| ` + "`single-file`" + ` | What gets auto-scaffolded around ` + "`.jsx`" + `, ` + "`.tsx`" + `, ` + "`.vue`" + `, ` + "`.svelte`" + `, ` + "`.html`" + `, ` + "`.md`" + `, ` + "`.ipynb`" + `. |

When in doubt, start with ` + "`storage`" + ` and ` + "`secrets`" + ` — they're the most commonly misused. ` + "`workflow`" + ` is also a good early read so you pick sandbox vs deploy correctly.
`

const aiGuideWorkflow = `## Workflow — sandbox vs deploy

Two paths; pick by intent:

**Iterative development** (you'll change code multiple times):
1. ` + "`berth_sandbox_create`" + ` — boots a hot-reload container with your code bind-mounted read-write
2. ` + "`berth_sandbox_push`" + ` — apply file changes instantly (~1s, no rebuild)
3. ` + "`berth_sandbox_promote`" + ` — convert the working sandbox into a production deployment

**One-shot deploy** (final code, no iteration expected):
1. ` + "`berth_deploy`" + ` (inline files via the server-side MCP) or ` + "`berth_deploy_dir`" + ` (local directory, available only on the standalone bridge)
2. ` + "`berth_status`" + ` to poll. Builds take 15-60s. Statuses: ` + "`building`" + `, ` + "`running`" + `, ` + "`failed`" + `.
3. If ` + "`failed`" + `, call ` + "`berth_logs`" + ` for the build error.

Pre-flight: call ` + "`berth_list`" + ` first when starting a new task — avoid creating a duplicate of an existing deployment with the same intent.

` + "`berth_update`" + ` triggers a **full rebuild** every time. While iterating, prefer ` + "`berth_sandbox_push`" + ` (instant) and only ` + "`berth_update`" + ` once you have a stable artifact to ship as a new immutable version.

Every successful deploy returns an HTTPS URL of the form ` + "`https://<subdomain>.<apex>/`" + ` (or ` + "`<subdomain>-<workspace>.<apex>`" + ` in flat-URLs mode). Tell the user which URL their app is at.
`

const aiGuideStorage = `## Storage — ` + "`/data`" + ` (persistent) and ` + "`/_data`" + ` (document store)

Every container gets two storage primitives. Use them; don't reinvent storage.

### ` + "`/data`" + ` — host-mounted, persistent, survives rebuilds

- Available via the ` + "`DATA_DIR`" + ` env var (set to ` + "`/data`" + ` automatically — read it from your code).
- Persists across rebuilds, redeploys, and updates. Survives blue-green container swaps.
- Deploy mode: backed by ` + "`<DataDir>/persist/<deployID>/`" + ` on the host.
- Sandbox mode: same host directory, just bind-mounted directly.
- **Always write app state under ` + "`$DATA_DIR`" + `.** Writing to ` + "`/tmp`" + ` (256 MB tmpfs) is scratch-only and lost on restart. ` + "`/app/code`" + ` is read-only on deploy. Anywhere else in the runtime image will not survive.

### ` + "`/_data`" + ` — per-deployment SQLite document store, exposed as REST

Hit it from inside the container at ` + "`http://localhost/_data/...`" + ` or from outside at ` + "`<deploy-url>/_data/...`" + `.

| Method | Path | Action |
|---|---|---|
| GET | ` + "`/_data`" + ` | List collections |
| GET | ` + "`/_data/{collection}`" + ` | List docs (` + "`?limit=&offset=`" + `) |
| POST | ` + "`/_data/{collection}`" + ` | Create doc, returns auto-generated ID |
| GET | ` + "`/_data/{collection}/{id}`" + ` | Fetch one |
| PUT | ` + "`/_data/{collection}/{id}`" + ` | Replace |
| DELETE | ` + "`/_data/{collection}/{id}`" + ` | Remove one |
| DELETE | ` + "`/_data/{collection}`" + ` | Drop the whole collection |

Limits (see ` + "`limits`" + ` topic): 100 KB/doc, 10,000 docs/collection, 100 collections, 50 MB DB. CORS open. No auth — gating happens at the deployment level.

**Rule of thumb:** use ` + "`/_data`" + ` for structured records (users, tasks, posts). Use ` + "`$DATA_DIR`" + ` for files/blobs (uploaded images, generated PDFs, sqlite-of-your-own).
`

const aiGuideSecrets = `## Secrets — referenced by name, never by value

**You never see secret values.** The user/operator stores secrets server-side via ` + "`berth_secret_set`" + `; you reference them by name when deploying.

### Workflow

1. The user calls ` + "`berth_secret_set`" + ` with a name + value (you can suggest the name — e.g. ` + "`DB_URL`" + `, ` + "`STRIPE_KEY`" + `, ` + "`OPENAI_API_KEY`" + `).
2. At deploy/sandbox-create time, pass ` + "`secrets: [\"DB_URL\", \"STRIPE_KEY\"]`" + `. The platform decrypts and injects them as environment variables at container startup.
3. ` + "`berth_secret_list`" + ` returns metadata only (name, scope, description, timestamps) — values are never in the response.
4. Rotation: calling ` + "`berth_secret_set`" + ` again on an existing name auto-restarts every deployment using that secret (~5s, runtime-only restart, no rebuild).
5. ` + "`berth_secret_delete`" + ` removes a secret. Deployments that referenced it will fail to start the env injection until removed from their secrets list.

### Don't

- Don't ask the user to paste a secret value into chat for inclusion in your generated env vars.
- Don't write secrets into source files (` + "`.env`" + `, hardcoded constants, JSON config).
- Don't try to read a secret value via any tool — there isn't one. Values are write-only.

### Do

- Whenever your generated code needs an env var that looks sensitive (DB URL, API key, OAuth secret, signing key, JWT secret, payment-processor key), tell the user: "I need a secret called ` + "`X`" + ` — run ` + "`berth_secret_set`" + ` to store it, then I'll reference it by name." Then pass it in ` + "`secrets`" + ` at deploy time.
- Reference inside your code via the env var: e.g. ` + "`process.env.DB_URL`" + ` (Node), ` + "`os.environ['DB_URL']`" + ` (Python), ` + "`os.Getenv(\"DB_URL\")`" + ` (Go).
`

const aiGuideLimits = `## Resource limits

### Container

| Phase | Memory | CPU | Notes |
|---|---|---|---|
| Build | unconstrained | as configured | Heavy compilation OK (npm install, Go builds, ML wheels) |
| Runtime — deploy | 512 MB default | 0.5 default | Override via ` + "`memory`" + `, ` + "`cpus`" + ` deploy params |
| Runtime — sandbox | 1 GB default | 0.5 default | Same overrides |

Admin can shift defaults globally via ` + "`container.default_memory`" + ` / ` + "`container.default_cpus`" + ` settings.

**Mismatch trap:** the build phase is unconstrained, but runtime is much tighter. Don't generate code that loads a multi-GB model into RAM at runtime when the deploy is at the 512 MB default. Either request more memory at deploy time or stream/page the data.

### Filesystem inside the container

| Path | Mode | Notes |
|---|---|---|
| ` + "`/app`" + ` | read-only on deploy, read-write on sandbox | Your code |
| ` + "`/data`" + ` | read-write | Persistent app state — see ` + "`storage`" + ` topic |
| ` + "`/tmp`" + ` | read-write tmpfs, 256 MB | Scratch only, lost on restart |
| everything else | runtime image, read-only in practice | Don't write here |

### Document store (` + "`/_data`" + `)

- 100 KB per document (raw JSON)
- 10,000 documents per collection
- 100 collections per deployment
- 50 MB total DB size per deployment

Hitting any of these returns a 4xx; design accordingly.

### Network quota

Egress bytes counted per period. Defaults from admin settings. Override per-deploy with ` + "`network_quota: \"5g\"`" + ` etc. When the quota is exhausted, the proxy serves 503 for outbound traffic until the period resets.
`

const aiGuideTTL = `## TTL — deployment expiry

The ` + "`ttl`" + ` parameter on ` + "`berth_deploy`" + ` and ` + "`berth_sandbox_create`" + ` controls when the deployment auto-destroys.

| Input | Meaning |
|---|---|
| ` + "`\"24h\"`" + ` | 24 hours |
| ` + "`\"7d\"`" + ` | 7 days (= 168h) |
| ` + "`\"168\"`" + ` | bare integer = hours |
| ` + "`\"0\"`" + ` | never expires |
| ` + "`\"\"`" + ` (omitted) | use the user's default |

Defaults: 72h for ` + "`berth_deploy`" + `, 4h for ` + "`berth_sandbox_create`" + `.

` + "`expiresAt`" + ` is recomputed each time you set a non-empty ` + "`ttl`" + `. To make a deployment permanent, pass ` + "`ttl: \"0\"`" + `.

When in doubt for production-style deploys: ask the user for an explicit TTL or default to ` + "`\"7d\"`" + `. Sandboxes should usually keep the short default — they're meant to be short-lived.
`

const aiGuideAccess = `## Access control modes

Set via ` + "`protect_mode`" + ` at deploy time, or change later with ` + "`berth_protect`" + `. Caddy enforces gating at the proxy layer before any request reaches the container.

### ` + "`public`" + ` (default)

No auth. Anyone with the URL can hit it.

### ` + "`basic_auth`" + ` — HTTP Basic

Set ` + "`protect_username`" + ` + ` + "`protect_password`" + ` (plaintext, hashed server-side with bcrypt). Browsers prompt; non-interactive callers send ` + "`Authorization: Basic <base64(user:pass)>`" + `.

### ` + "`api_key`" + ` — header-based

Pass ` + "`X-API-Key: <key>`" + ` on every request. Set ` + "`protect_api_key`" + ` to specify the key, or omit and an ` + "`sk_…`" + ` key is auto-generated and returned in the deploy response. Best for CI/webhooks/M2M.

### ` + "`user`" + ` — gated to OpenBerth accounts

Set ` + "`protect_users: [\"alice\", \"bob\"]`" + ` to restrict to specific users. Empty list = any authenticated OpenBerth user. Visitors must be logged in to OpenBerth — the proxy redirects them to ` + "`/login`" + ` if not.

### Choosing

| Use case | Mode |
|---|---|
| Public demo / portfolio | ` + "`public`" + ` |
| Quick sharing with a small team | ` + "`basic_auth`" + ` |
| API endpoint for CI / webhooks / scripts | ` + "`api_key`" + ` |
| Internal tool restricted to known coworkers | ` + "`user`" + ` |
`

const aiGuideFrameworks = `## Framework auto-detection + ` + "`.berth.json`" + ` overrides

The server walks the project tree at deploy time and picks the first matching language provider:

1. **Go** — ` + "`go.mod`" + ` present. Image: ` + "`golang:<go.mod's go-line version>`" + `. Runtime image: ` + "`debian:bookworm-slim`" + ` (binary is statically linked with ` + "`CGO_ENABLED=0`" + `).
2. **Python** — any of ` + "`requirements.txt`" + `, ` + "`pyproject.toml`" + `, ` + "`Pipfile`" + `, ` + "`setup.py`" + `, ` + "`setup.cfg`" + `, ` + "`app.py`" + `, ` + "`main.py`" + `, ` + "`manage.py`" + `. Image: ` + "`python:<version>-slim`" + `. Sub-frameworks: Django (` + "`manage.py`" + `), FastAPI (` + "`fastapi`" + ` in deps), Flask (` + "`flask`" + ` in deps), generic.
3. **Node** — ` + "`package.json`" + ` present. Image: ` + "`node:<version>-slim`" + `. Sub-frameworks (priority): Next.js, Nuxt, SvelteKit, Vite, CRA, Vue CLI, Angular, generic ` + "`npm start`" + `.
4. **Static** — ` + "`index.html`" + ` at root. Image: ` + "`caddy:2-alpine`" + `. No build, no app process — Caddy serves the directory.

### Version pinning

| Language | Sources, in priority order |
|---|---|
| Go | ` + "`go 1.X`" + ` line in ` + "`go.mod`" + ` |
| Python | ` + "`.python-version`" + ` → ` + "`pyproject.toml`" + ` ` + "`requires-python`" + ` → ` + "`runtime.txt`" + ` |
| Node | ` + "`.nvmrc`" + ` → ` + "`.node-version`" + ` → ` + "`package.json`" + ` ` + "`engines.node`" + ` |

Defaults if none specified: Go 1.22, Python 3.12, Node 20.

### ` + "`.berth.json`" + ` — escape hatch when detection is wrong

Drop a file at the project root:

` + "```json\n{\n  \"language\": \"node\",\n  \"build\": \"npm run build:prod\",\n  \"start\": \"node dist/server.js\",\n  \"install\": \"npm install --legacy-peer-deps\",\n  \"dev\": \"npm run dev:custom\"\n}\n```" + `

Only present fields take effect. ` + "`language`" + ` alone is enough if you want OpenBerth to pick a default image and you'll set build/start yourself. The CLI side of ` + "`.berth.json`" + ` (name, ttl, memory, port, secrets, etc.) coexists in the same file — both halves are read by their respective consumers.

There's no override for *image version* — set the language's native version file (` + "`go.mod`" + `, ` + "`.nvmrc`" + `, ` + "`.python-version`" + `) instead. Pinning belongs in the project's source-of-truth files.
`

const aiGuideSingleFile = `## Single-file deploys (standalone bridge only)

The standalone CLI/MCP bridge supports deploying **one source file** directly. The CLI auto-scaffolds a minimal project around your file, then uploads the temp directory as a normal multi-file deploy. Server-side MCP tools (` + "`berth_deploy`" + ` with inline ` + "`files`" + `) do **not** scaffold — you must provide the complete file map yourself.

### Supported extensions

| Ext | Output | Build pipeline |
|---|---|---|
| ` + "`.jsx`" + ` | React (JS) + Vite | ` + "`npm install && vite build`" + ` |
| ` + "`.tsx`" + ` | React (TS) + Vite | same |
| ` + "`.vue`" + ` | Vue 3 + Vite | same |
| ` + "`.svelte`" + ` | Svelte + Vite | same |
| ` + "`.html`" + ` | Static HTML | none — Caddy serves it |
| ` + "`.md`" + ` | Markdown rendered client-side via marked | none |
| ` + "`.ipynb`" + ` | Jupyter notebook pre-rendered to HTML | none |

### Dependency inference (JSX/TSX/Vue/Svelte)

The CLI scans your file's imports and adds them to a generated ` + "`package.json`" + `. Framework deps (React, Vue, Svelte, Vite plugins) are pinned; user-imported packages get ` + "`*`" + ` and npm picks latest. Tailwind is auto-added when class indicators are detected.

Limits of import scanning:
- Only static ` + "`import X from \"pkg\"`" + ` and ` + "`require(\"pkg\")`" + ` are detected.
- Dynamic imports (` + "`import(variable)`" + `) won't be picked up.
- TypeScript ` + "`import type`" + ` is treated like a runtime import (usually harmless).

### When to fall back to a directory

If you need:
- A custom build step
- A specific dependency version
- An ` + "`.env`" + `
- Multiple source files

…ship a directory and let framework detection do its thing. Single-file is for the smallest possible "I have one .jsx" path.
`

// aiGuideTopics maps the topic name (as accepted in the tool's `topic`
// argument) to its markdown content. This is the source-of-truth lookup
// for the berth_guide handler.
var aiGuideTopics = map[string]string{
	"workflow":    aiGuideWorkflow,
	"storage":     aiGuideStorage,
	"secrets":     aiGuideSecrets,
	"limits":      aiGuideLimits,
	"ttl":         aiGuideTTL,
	"access":      aiGuideAccess,
	"frameworks":  aiGuideFrameworks,
	"single-file": aiGuideSingleFile,
}

// lookupGuideTopic returns (content, true) when topic is a known
// section, or ("", false) otherwise. Used by the tool dispatcher to
// translate an arbitrary user-supplied topic into a 200 or 4xx-style
// MCP tool result.
func lookupGuideTopic(topic string) (string, bool) {
	v, ok := aiGuideTopics[topic]
	return v, ok
}

// aiGuideTopicNames is a stable ordered list used in the no-arg
// response and in the tool's input schema enum.
var aiGuideTopicNames = []string{
	"workflow", "storage", "secrets", "limits",
	"ttl", "access", "frameworks", "single-file",
}
