package mcphandler

// tools returns the list of MCP tool definitions.
func tools() []mcpTool {
	return []mcpTool{
		{
			Name:        "berth_deploy",
			Description: "Deploy a small set of AI-generated files to a live HTTPS URL. Provide files as a map of filepath to content. Best for 1-100 files generated in conversation.\n\nFor iterative development where you'll make multiple changes, use berth_sandbox_create instead — it supports instant hot-reload without full rebuilds.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"files":            map[string]interface{}{"type": "object", "description": "Map of relative file paths to file contents. Max 100 files, 10MB total."},
					"name":             map[string]interface{}{"type": "string", "description": "Subdomain name (optional, auto-generated if empty)"},
					"title":            map[string]interface{}{"type": "string", "description": "Display title for the gallery (optional)"},
					"description":      map[string]interface{}{"type": "string", "description": "Description shown in the gallery (optional)"},
					"env":              map[string]interface{}{"type": "object", "description": "Environment variables as key-value pairs (optional)"},
					"port":             map[string]interface{}{"type": "integer", "description": "Port the app listens on (optional, auto-detected)"},
					"memory":           map[string]interface{}{"type": "string", "description": "Memory limit, e.g. '512m', '1g' (optional, default 512m)"},
					"network_quota":    map[string]interface{}{"type": "string", "description": "Network transfer quota, e.g. '5g', '10g' (optional, uses server default)"},
					"ttl":              map[string]interface{}{"type": "string", "description": "Time to live: '24h', '7d', '0' for never (optional, default 72h)"},
					"protect_mode":     map[string]interface{}{"type": "string", "description": "Access control mode: 'basic_auth', 'api_key', or 'user'. Set at deploy time so protection is active when the route goes live."},
					"protect_username": map[string]interface{}{"type": "string", "description": "Username for basic_auth mode"},
					"protect_password": map[string]interface{}{"type": "string", "description": "Password for basic_auth mode"},
					"protect_api_key":  map[string]interface{}{"type": "string", "description": "Custom API key (optional, auto-generated if empty)"},
					"protect_users":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access."},
				},
				"required": []string{"files"},
			},
		},
		{
			Name:        "berth_update",
			Description: "Update an existing deployment with new inline files. Replaces all files and triggers a full rebuild.\n\nFor iterative changes to a sandbox, use berth_sandbox_push — it's instant (no rebuild).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":            map[string]interface{}{"type": "string", "description": "Deployment ID to update"},
					"files":         map[string]interface{}{"type": "object", "description": "Map of relative file paths to file contents (replaces all files). Max 100 files, 10MB total."},
					"env":           map[string]interface{}{"type": "object", "description": "Environment variables (optional)"},
					"port":          map[string]interface{}{"type": "integer", "description": "Port override (optional)"},
					"memory":        map[string]interface{}{"type": "string", "description": "Memory limit (optional)"},
					"network_quota": map[string]interface{}{"type": "string", "description": "Network transfer quota (optional)"},
				},
				"required": []string{"id", "files"},
			},
		},
		{
			Name:        "berth_status",
			Description: "Get the status of a deployment (building, running, failed, etc.) and its URL.\n\nUse this after berth_deploy or berth_update to check if the build completed. Status values: 'building' (wait and check again), 'running' (ready to use), 'failed' (check berth_logs for errors).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Deployment ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "berth_source",
			Description: "Get the source code files of a deployment. Returns all text files and their contents. Use this to inspect what code is running in a deployment.\n\nRequires the deployment ID. Use berth_update to modify and redeploy.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Deployment ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "berth_logs",
			Description: "Get the container logs for a deployment. Useful for debugging build or runtime errors.\n\nALWAYS check logs when:\n- A deployment status is 'failed'\n- The deployed app shows errors or blank pages\n- You need to debug application behavior\n\nLogs include both build output and runtime output.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":   map[string]interface{}{"type": "string", "description": "Deployment ID"},
					"tail": map[string]interface{}{"type": "integer", "description": "Number of log lines (default 100)"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "berth_list",
			Description: "List all active deployments with their IDs, URLs, and statuses.\n\nUse this to find existing deployments before creating new ones. If the user wants to update an existing app, find it here first rather than creating a duplicate.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "berth_destroy",
			Description: "Destroy a deployment and free its resources. This permanently removes the deployment, its container, data, and URL.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Deployment ID to destroy"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "berth_protect",
			Description: "Set access control on a deployment. Modes: 'basic_auth' (browser password prompt), 'api_key' (header auth), 'user' (require OpenBerth login), 'public' (remove protection).\n\nFor basic_auth, provide username and password. For api_key, an API key is auto-generated if not provided — use it via the 'X-Api-Key' header. For 'user' mode, optionally provide a 'users' list to restrict access to specific usernames — if omitted, any authenticated user can access. Use 'public' to remove any existing protection.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":       map[string]interface{}{"type": "string", "description": "Deployment ID"},
					"mode":     map[string]interface{}{"type": "string", "description": "Access mode: 'public', 'basic_auth', 'api_key', or 'user'"},
					"username": map[string]interface{}{"type": "string", "description": "Username for basic_auth mode"},
					"password": map[string]interface{}{"type": "string", "description": "Password for basic_auth mode"},
					"apiKey":   map[string]interface{}{"type": "string", "description": "Custom API key (optional, auto-generated if empty)"},
					"users":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access."},
				},
				"required": []string{"id", "mode"},
			},
		},
		{
			Name:        "berth_lock",
			Description: "Lock or unlock a deployment. A locked deployment keeps running and serving traffic, but rejects all code updates, metadata changes, access control changes, and destroy until unlocked.\n\nUse this to freeze a stable deployment and prevent accidental changes.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":     map[string]interface{}{"type": "string", "description": "Deployment ID"},
					"locked": map[string]interface{}{"type": "boolean", "description": "true to lock, false to unlock"},
				},
				"required": []string{"id", "locked"},
			},
		},
		// ── Sandbox tools ────────────────────────────────────────────
		{
			Name:        "berth_sandbox_create",
			Description: "Create a live development sandbox with hot reload. File changes via berth_sandbox_push apply instantly — no full rebuild needed.\n\nUSE SANDBOX (not deploy) when:\n- The user wants to iterate on code (they'll make multiple changes)\n- You're building something step-by-step\n- You want to test changes quickly before finalizing\n\nSupports: static HTML, Node.js (Vite/Next.js/Nuxt/SvelteKit), Python (FastAPI/Flask/Django), Go.\n\nAfter creation, use berth_sandbox_push to update files instantly. When done iterating, use berth_sandbox_promote to create an optimized production deployment.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"files":            map[string]interface{}{"type": "object", "description": "Map of relative file paths to file contents. Max 100 files, 10MB total."},
					"name":             map[string]interface{}{"type": "string", "description": "Subdomain name (optional, auto-generated if empty)"},
					"env":              map[string]interface{}{"type": "object", "description": "Environment variables as key-value pairs (optional)"},
					"port":             map[string]interface{}{"type": "integer", "description": "Port the dev server listens on (optional, auto-detected)"},
					"memory":           map[string]interface{}{"type": "string", "description": "Memory limit, e.g. '512m', '1g' (optional, default 1g)"},
					"network_quota":    map[string]interface{}{"type": "string", "description": "Network transfer quota, e.g. '5g', '10g' (optional, uses server default)"},
					"ttl":              map[string]interface{}{"type": "string", "description": "Time to live: '4h', '12h', '24h' (optional, default 4h)"},
					"protect_mode":     map[string]interface{}{"type": "string", "description": "Access control mode: 'basic_auth', 'api_key', or 'user'. Set at create time so protection is active when the route goes live."},
					"protect_username": map[string]interface{}{"type": "string", "description": "Username for basic_auth mode"},
					"protect_password": map[string]interface{}{"type": "string", "description": "Password for basic_auth mode"},
					"protect_api_key":  map[string]interface{}{"type": "string", "description": "Custom API key (optional, auto-generated if empty)"},
					"protect_users":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access."},
				},
				"required": []string{"files"},
			},
		},
		{
			Name:        "berth_sandbox_push",
			Description: "Push file changes to a running sandbox. Changes apply instantly via HMR (Node/Vite) or trigger automatic rebuild (Go, Python). No full container rebuild needed.\n\nIf you modify dependency files (package.json, requirements.txt, go.mod), dependencies are automatically reinstalled.\n\nThis is the primary way to update sandbox code. Much faster than berth_update.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Sandbox ID"},
					"changes": map[string]interface{}{
						"type":        "array",
						"description": "Array of file changes to apply",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"op":      map[string]interface{}{"type": "string", "description": "Operation: 'write' or 'delete'"},
								"path":    map[string]interface{}{"type": "string", "description": "Relative file path"},
								"content": map[string]interface{}{"type": "string", "description": "File content (required for 'write', omit for 'delete')"},
							},
							"required": []string{"op", "path"},
						},
					},
				},
				"required": []string{"id", "changes"},
			},
		},
		{
			Name:        "berth_sandbox_install",
			Description: "Install or remove packages in a running sandbox. Supports npm (Node), pip (Python), and go get (Go).\n\nUse this when the user asks to add a library or dependency to their sandbox project.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":        map[string]interface{}{"type": "string", "description": "Sandbox ID"},
					"packages":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Package names to install or remove"},
					"uninstall": map[string]interface{}{"type": "boolean", "description": "Set to true to uninstall packages (default: false)"},
				},
				"required": []string{"id", "packages"},
			},
		},
		{
			Name:        "berth_sandbox_exec",
			Description: "Run a shell command inside a running sandbox container. Useful for running scripts, checking file contents, debugging, or running tests.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":      map[string]interface{}{"type": "string", "description": "Sandbox ID"},
					"command": map[string]interface{}{"type": "string", "description": "Shell command to execute"},
					"timeout": map[string]interface{}{"type": "integer", "description": "Timeout in seconds (default 30, max 300)"},
				},
				"required": []string{"id", "command"},
			},
		},
		{
			Name:        "berth_sandbox_logs",
			Description: "Get logs from a running sandbox. Includes dev server output, build logs, and install logs.\n\nCheck these when the sandbox app shows errors or unexpected behavior.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":   map[string]interface{}{"type": "string", "description": "Sandbox ID"},
					"tail": map[string]interface{}{"type": "integer", "description": "Number of log lines (default 100)"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "berth_sandbox_destroy",
			Description: "Destroy a sandbox and free its resources.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Sandbox ID to destroy"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "berth_sandbox_promote",
			Description: "Promote a sandbox to a production deployment. Stops the dev server, runs a full optimized build, and starts a production runtime. The URL stays the same.\n\nUse this when the user is happy with their sandbox and wants to make it permanent. The sandbox's short TTL is replaced with the deployment TTL.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":            map[string]interface{}{"type": "string", "description": "Sandbox ID to promote"},
					"ttl":           map[string]interface{}{"type": "string", "description": "Time to live for the deployment: '24h', '7d', '0' for never (optional, default: user's default TTL)"},
					"memory":        map[string]interface{}{"type": "string", "description": "Memory limit, e.g. '512m', '1g' (optional)"},
					"network_quota": map[string]interface{}{"type": "string", "description": "Network transfer quota, e.g. '5g', '10g' (optional)"},
					"env":           map[string]interface{}{"type": "object", "description": "Environment variables to set (optional, merged with existing)"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "berth_update_quota",
			Description: "Update the network transfer quota on a deployment. Applies immediately on running containers without redeployment.\n\nUse this to increase, decrease, or remove the network quota. Set quota to '' (empty string) to remove the quota entirely.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":    map[string]interface{}{"type": "string", "description": "Deployment ID"},
					"quota": map[string]interface{}{"type": "string", "description": "Network transfer quota, e.g. '1g', '5g', '10g', or '' to remove"},
				},
				"required": []string{"id", "quota"},
			},
		},
	}
}
