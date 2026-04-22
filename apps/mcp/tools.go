package main

// Input schemas use the small DSL in schema.go. Two tools here have no
// server-side counterpart: berth_deploy_dir and berth_update_dir. They
// operate on a local filesystem path, which only this standalone MCP can
// see — it runs on the user's machine, the server-side MCP does not.

func (s *MCPServer) tools() []Tool {
	return []Tool{
		{
			Name:        "berth_deploy",
			Description: "Deploy a small set of AI-generated files to a live HTTPS URL. Provide files as a map of filepath to content. Best for 1-20 files generated in conversation.\n\nIMPORTANT: If the project already exists on the user's local filesystem (e.g., they said \"deploy my app\" or referenced a directory path), use berth_deploy_dir instead — it's faster and includes all project files automatically.\n\nFor iterative development where you'll make multiple changes, use berth_sandbox_create instead — it supports instant hot-reload without full rebuilds.\n\nFramework is auto-detected. If detection fails, include a .berth.json file with override fields (language, build, start, install, dev).",
			InputSchema: schema(map[string]any{
				"files":            prop("object", "Map of relative file paths to file contents. Max 20 files, 5MB total."),
				"name":             prop("string", "Subdomain name (optional, auto-generated if empty)"),
				"title":            prop("string", "Display title for the gallery (optional)"),
				"description":      prop("string", "Description shown in the gallery (optional)"),
				"env":              prop("object", "Environment variables as key-value pairs (optional)"),
				"secrets":          arrProp("string", "Names of stored secrets to inject as environment variables. Use berth_secret_list to see available secrets. Secret values are resolved server-side and never exposed."),
				"port":             prop("integer", "Port the app listens on (optional, auto-detected)"),
				"memory":           prop("string", "Memory limit, e.g. '512m', '1g' (optional, default 512m)"),
				"network_quota":    prop("string", "Network transfer quota, e.g. '5g', '10g' (optional, uses server default)"),
				"ttl":              prop("string", "Time to live: '24h', '7d', '0' for never (optional, default 72h)"),
				"protect_mode":     prop("string", "Access control mode: 'basic_auth', 'api_key', or 'user'. Set at deploy time so protection is active when the route goes live."),
				"protect_username": prop("string", "Username for basic_auth mode"),
				"protect_password": prop("string", "Password for basic_auth mode"),
				"protect_api_key":  prop("string", "Custom API key (optional, auto-generated if empty)"),
				"protect_users":    arrProp("string", "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access."),
			}, "files"),
		},
		{
			Name:        "berth_deploy_dir",
			Description: "Deploy a project directory from the local filesystem to a live HTTPS URL. Creates a tarball and uploads it. Respects .gitignore.\n\nPREFERRED over berth_deploy when:\n- The project already exists on disk (user said \"deploy this\", \"deploy my app\", referenced a path)\n- The project has more than 5 files\n- The project has dependencies (node_modules, venv, etc.) that shouldn't be sent inline\n\nFor iterative development where you'll make multiple changes, use berth_sandbox_create instead.",
			InputSchema: schema(map[string]any{
				"path":             prop("string", "Absolute or relative path to the project directory"),
				"name":             prop("string", "Subdomain name (optional, auto-generated from directory name)"),
				"env":              prop("object", "Environment variables as key-value pairs (optional)"),
				"secrets":          arrProp("string", "Names of stored secrets to inject as environment variables. Use berth_secret_list to see available secrets. Secret values are resolved server-side and never exposed."),
				"port":             prop("string", "Port the app listens on (optional, auto-detected)"),
				"memory":           prop("string", "Memory limit (optional)"),
				"network_quota":    prop("string", "Network transfer quota (optional)"),
				"ttl":              prop("string", "Time to live (optional)"),
				"protect_mode":     prop("string", "Access control mode: 'basic_auth', 'api_key', or 'user'."),
				"protect_username": prop("string", "Username for basic_auth mode"),
				"protect_password": prop("string", "Password for basic_auth mode"),
				"protect_api_key":  prop("string", "Custom API key (optional, auto-generated if empty)"),
				"protect_users":    arrProp("string", "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access."),
			}, "path"),
		},
		{
			Name:        "berth_update",
			Description: "Update an existing deployment with new inline files. Replaces all files and triggers a full rebuild.\n\nIMPORTANT: If the project exists on the user's filesystem, use berth_update_dir instead.\nFor iterative changes to a sandbox, use berth_sandbox_push — it's instant (no rebuild).",
			InputSchema: schema(map[string]any{
				"id":            prop("string", "Deployment ID to update"),
				"files":         prop("object", "Map of relative file paths to file contents (replaces all files)"),
				"env":           prop("object", "Environment variables (optional)"),
				"secrets":       arrProp("string", "Names of stored secrets to inject as environment variables. Use berth_secret_list to see available secrets. Secret values are resolved server-side and never exposed."),
				"port":          prop("integer", "Port override (optional)"),
				"memory":        prop("string", "Memory limit (optional)"),
				"network_quota": prop("string", "Network transfer quota (optional)"),
			}, "id", "files"),
		},
		{
			Name:        "berth_update_dir",
			Description: "Update an existing deployment from a local project directory. Creates a tarball and uploads it. Respects .gitignore.\n\nPREFERRED over berth_update when the project exists on disk.",
			InputSchema: schema(map[string]any{
				"id":            prop("string", "Deployment ID to update"),
				"path":          prop("string", "Absolute or relative path to the project directory"),
				"env":           prop("object", "Environment variables (optional)"),
				"secrets":       arrProp("string", "Names of stored secrets to inject as environment variables. Use berth_secret_list to see available secrets. Secret values are resolved server-side and never exposed."),
				"port":          prop("string", "Port override (optional)"),
				"memory":        prop("string", "Memory limit (optional)"),
				"network_quota": prop("string", "Network transfer quota (optional)"),
			}, "id", "path"),
		},
		{
			Name:        "berth_status",
			Description: "Get the status of a deployment (building, running, failed, etc.) and its URL.\n\nUse this after berth_deploy or berth_update to check if the build completed. Status values: 'building' (wait and check again), 'running' (ready to use), 'failed' (check berth_logs for errors).",
			InputSchema: schema(map[string]any{
				"id": prop("string", "Deployment ID"),
			}, "id"),
		},
		{
			Name:        "berth_source",
			Description: "Get the source code files of a deployment. Returns all text files and their contents. Use this to inspect what code is running in a deployment.\n\nRequires the deployment ID. Use berth_update to modify and redeploy.",
			InputSchema: schema(map[string]any{
				"id": prop("string", "Deployment ID"),
			}, "id"),
		},
		{
			Name:        "berth_logs",
			Description: "Get the container logs for a deployment. Useful for debugging build or runtime errors.\n\nALWAYS check logs when:\n- A deployment status is 'failed'\n- The deployed app shows errors or blank pages\n- You need to debug application behavior\n\nLogs include both build output and runtime output.",
			InputSchema: schema(map[string]any{
				"id":   prop("string", "Deployment ID"),
				"tail": prop("integer", "Number of log lines (default 100)"),
			}, "id"),
		},
		{
			Name:        "berth_list",
			Description: "List active deployments. By default returns the caller's own deployments. Pass `all: true` to list every deployment on the server (read visibility is open — each entry includes `ownerId`/`ownerName`). Mutation still requires ownership or admin.\n\nUse this to find existing deployments before creating new ones. If the user wants to update an existing app, find it here first rather than creating a duplicate.",
			InputSchema: schema(map[string]any{
				"all": prop("boolean", "If true, list every user's deployments. Default false = own only."),
			}),
		},
		{
			Name:        "berth_destroy",
			Description: "Destroy a deployment and free its resources. This permanently removes the deployment, its container, data, and URL.",
			InputSchema: schema(map[string]any{
				"id": prop("string", "Deployment ID to destroy"),
			}, "id"),
		},
		{
			Name:        "berth_protect",
			Description: "Set access control on a deployment. Modes: 'basic_auth' (browser password prompt), 'api_key' (header auth), 'user' (require OpenBerth login), 'public' (remove protection).\n\nFor basic_auth, provide username and password. For api_key, an API key is auto-generated if not provided — use it via the 'X-Api-Key' header. For 'user' mode, optionally provide a 'users' list to restrict access to specific usernames — if omitted, any authenticated user can access. Use 'public' to remove any existing protection.",
			InputSchema: schema(map[string]any{
				"id":       prop("string", "Deployment ID"),
				"mode":     prop("string", "Access mode: 'public', 'basic_auth', 'api_key', or 'user'"),
				"username": prop("string", "Username for basic_auth mode"),
				"password": prop("string", "Password for basic_auth mode"),
				"apiKey":   prop("string", "Custom API key (optional, auto-generated if empty)"),
				"users":    arrProp("string", "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access."),
			}, "id", "mode"),
		},
		{
			Name:        "berth_lock",
			Description: "Lock or unlock a deployment. A locked deployment keeps running and serving traffic, but rejects all code updates, metadata changes, access control changes, and destroy until unlocked.\n\nUse this to freeze a stable deployment and prevent accidental changes.",
			InputSchema: schema(map[string]any{
				"id":     prop("string", "Deployment ID"),
				"locked": prop("boolean", "true to lock, false to unlock"),
			}, "id", "locked"),
		},
		// ── Sandbox tools ────────────────────────────────────────────
		{
			Name:        "berth_sandbox_create",
			Description: "Create a live development sandbox with hot reload. File changes via berth_sandbox_push apply instantly — no full rebuild needed.\n\nUSE SANDBOX (not deploy) when:\n- The user wants to iterate on code (they'll make multiple changes)\n- You're building something step-by-step\n- You want to test changes quickly before finalizing\n\nSupports: static HTML, Node.js (Vite/Next.js/Nuxt/SvelteKit), Python (FastAPI/Flask/Django), Go.\n\nAfter creation, use berth_sandbox_push to update files instantly. When done iterating, use berth_sandbox_promote to create an optimized production deployment.\n\nFramework is auto-detected. If detection fails, include a .berth.json file with override fields (language, build, start, install, dev).",
			InputSchema: schema(map[string]any{
				"files":            prop("object", "Map of relative file paths to file contents. Max 100 files, 10MB total."),
				"name":             prop("string", "Subdomain name (optional, auto-generated if empty)"),
				"env":              prop("object", "Environment variables as key-value pairs (optional)"),
				"secrets":          arrProp("string", "Names of stored secrets to inject as environment variables. Use berth_secret_list to see available secrets. Secret values are resolved server-side and never exposed."),
				"port":             prop("integer", "Port the dev server listens on (optional, auto-detected)"),
				"memory":           prop("string", "Memory limit, e.g. '512m', '1g' (optional, default 1g)"),
				"network_quota":    prop("string", "Network transfer quota, e.g. '5g', '10g' (optional, uses server default)"),
				"ttl":              prop("string", "Time to live: '4h', '12h', '24h' (optional, default 4h)"),
				"protect_mode":     prop("string", "Access control mode: 'basic_auth', 'api_key', or 'user'. Set at create time so protection is active when the route goes live."),
				"protect_username": prop("string", "Username for basic_auth mode"),
				"protect_password": prop("string", "Password for basic_auth mode"),
				"protect_api_key":  prop("string", "Custom API key (optional, auto-generated if empty)"),
				"protect_users":    arrProp("string", "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access."),
			}, "files"),
		},
		{
			Name:        "berth_sandbox_push",
			Description: "Push file changes to a running sandbox. Changes apply instantly via HMR (Node/Vite) or trigger automatic rebuild (Go, Python). No full container rebuild needed.\n\nIf you modify dependency files (package.json, requirements.txt, go.mod), dependencies are automatically reinstalled.\n\nThis is the primary way to update sandbox code. Much faster than berth_update.",
			InputSchema: schema(map[string]any{
				"id": prop("string", "Sandbox ID"),
				"changes": objArrayProp(
					"Array of file changes to apply",
					map[string]any{
						"op":      prop("string", "Operation: 'write' or 'delete'"),
						"path":    prop("string", "Relative file path"),
						"content": prop("string", "File content (required for 'write', omit for 'delete')"),
					},
					"op", "path",
				),
			}, "id", "changes"),
		},
		{
			Name:        "berth_sandbox_install",
			Description: "Install or remove packages in a running sandbox. Supports npm (Node), pip (Python), and go get (Go).\n\nUse this when the user asks to add a library or dependency to their sandbox project.",
			InputSchema: schema(map[string]any{
				"id":        prop("string", "Sandbox ID"),
				"packages":  arrProp("string", "Package names to install or remove"),
				"uninstall": prop("boolean", "Set to true to uninstall packages (default: false)"),
			}, "id", "packages"),
		},
		{
			Name:        "berth_sandbox_exec",
			Description: "Run a shell command inside a running sandbox container. Useful for running scripts, checking file contents, debugging, or running tests.",
			InputSchema: schema(map[string]any{
				"id":      prop("string", "Sandbox ID"),
				"command": prop("string", "Shell command to execute"),
				"timeout": prop("integer", "Timeout in seconds (default 30, max 300)"),
			}, "id", "command"),
		},
		{
			Name:        "berth_sandbox_logs",
			Description: "Get logs from a running sandbox. Includes dev server output, build logs, and install logs.\n\nCheck these when the sandbox app shows errors or unexpected behavior.",
			InputSchema: schema(map[string]any{
				"id":   prop("string", "Sandbox ID"),
				"tail": prop("integer", "Number of log lines (default 100)"),
			}, "id"),
		},
		{
			Name:        "berth_sandbox_destroy",
			Description: "Destroy a sandbox and free its resources.",
			InputSchema: schema(map[string]any{
				"id": prop("string", "Sandbox ID to destroy"),
			}, "id"),
		},
		{
			Name:        "berth_sandbox_promote",
			Description: "Promote a sandbox to a production deployment. Stops the dev server, runs a full optimized build, and starts a production runtime. The URL stays the same.\n\nUse this when the user is happy with their sandbox and wants to make it permanent. The sandbox's short TTL is replaced with the deployment TTL.",
			InputSchema: schema(map[string]any{
				"id":            prop("string", "Sandbox ID to promote"),
				"ttl":           prop("string", "Time to live for the deployment: '24h', '7d', '0' for never (optional, default: user's default TTL)"),
				"memory":        prop("string", "Memory limit, e.g. '512m', '1g' (optional)"),
				"network_quota": prop("string", "Network transfer quota, e.g. '5g', '10g' (optional)"),
				"env":           prop("object", "Environment variables to set (optional, merged with existing)"),
				"secrets":       arrProp("string", "Names of stored secrets to inject as environment variables. Use berth_secret_list to see available secrets. Secret values are resolved server-side and never exposed."),
			}, "id"),
		},
		{
			Name:        "berth_update_quota",
			Description: "Update the network transfer quota on a deployment. Applies immediately on running containers without redeployment.\n\nUse this to increase, decrease, or remove the network quota. Set quota to '' (empty string) to remove the quota entirely.",
			InputSchema: schema(map[string]any{
				"id":    prop("string", "Deployment ID"),
				"quota": prop("string", "Network transfer quota, e.g. '1g', '5g', '10g', or '' to remove"),
			}, "id", "quota"),
		},
		// ── Secret tools ─────────────────────────────────────────────
		{
			Name:        "berth_secret_set",
			Description: "Store an encrypted secret for use in deployments. Add a description so the secret can be discovered later via berth_secret_list. The value is encrypted at rest and can never be read back. If the secret already exists, its value is updated and all deployments using it are automatically restarted.",
			InputSchema: schema(map[string]any{
				"name":        prop("string", "Secret name (used as the environment variable name)"),
				"value":       prop("string", "Secret value (encrypted at rest, never returned by the API)"),
				"description": prop("string", "Human-readable description of what this secret is for (optional)"),
				"global":      prop("boolean", "If true, the secret is available to all users. Default false."),
			}, "name", "value"),
		},
		{
			Name:        "berth_secret_list",
			Description: "List stored secrets with names and descriptions (values are never returned). Use this to discover available secrets before deploying. Pass secret names to the 'secrets' parameter of berth_deploy or berth_sandbox_create.",
			InputSchema: schema(map[string]any{}),
		},
		{
			Name:        "berth_secret_delete",
			Description: "Delete a stored secret by name.",
			InputSchema: schema(map[string]any{
				"name":   prop("string", "Secret name to delete"),
				"global": prop("boolean", "If true, delete a global secret. Default false."),
			}, "name"),
		},
	}
}
