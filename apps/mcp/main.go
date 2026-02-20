package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var version = "dev"

// ── MCP Protocol Types ──────────────────────────────────────────────

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ── Server ──────────────────────────────────────────────────────────

type MCPServer struct {
	apiURL string
	apiKey string
	http   *http.Client
}

func NewMCPServer() *MCPServer {
	url := os.Getenv("BERTH_SERVER")
	key := os.Getenv("BERTH_KEY")

	if url == "" || key == "" {
		fmt.Fprintf(os.Stderr, "BERTH_SERVER and BERTH_KEY env vars required\n")
		os.Exit(1)
	}

	return &MCPServer{
		apiURL: strings.TrimSuffix(url, "/"),
		apiKey: key,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (s *MCPServer) Run() {
	decoder := json.NewDecoder(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	var mu sync.Mutex

	writeResponse := func(resp *JSONRPCResponse) {
		mu.Lock()
		defer mu.Unlock()
		out, _ := json.Marshal(resp)
		writer.Write(out)
		writer.Write([]byte("\n"))
		writer.Flush()
		fmt.Fprintf(os.Stderr, "[berth-mcp] -> response (id=%v)\n", resp.ID)
	}

	for {
		var req JSONRPCRequest
		if err := decoder.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "[berth-mcp] decode error: %v\n", err)
			continue
		}

		fmt.Fprintf(os.Stderr, "[berth-mcp] <- %s\n", req.Method)

		// Handle tool calls concurrently so they don't block pings/other requests
		if req.Method == "tools/call" {
			go func(r JSONRPCRequest) {
				resp := s.handle(r)
				if resp != nil {
					writeResponse(resp)
				}
			}(req)
		} else {
			resp := s.handle(req)
			if resp != nil {
				writeResponse(resp)
			}
		}
	}
}

// isNotification returns true for JSON-RPC notifications (no id field).
func isNotification(req JSONRPCRequest) bool {
	return req.ID == nil
}

func (s *MCPServer) handle(req JSONRPCRequest) *JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "openberth",
					"version": version,
				},
			},
		}

	case "notifications/initialized", "notifications/cancelled":
		return nil // notifications, no response

	case "ping":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{},
		}

	case "resources/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"resources": []interface{}{},
			},
		}

	case "prompts/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"prompts": []interface{}{},
			},
		}

	case "tools/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": s.tools(),
			},
		}

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)

		result := s.callTool(params.Name, params.Arguments)
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}

	default:
		// Notifications (no id) must never get a response
		if isNotification(req) {
			return nil
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: "Method not found: " + req.Method},
		}
	}
}

// ── Tool Definitions ────────────────────────────────────────────────

func (s *MCPServer) tools() []Tool {
	return []Tool{
		{
			Name:        "berth_deploy",
			Description: "Deploy a small set of AI-generated files to a live HTTPS URL. Provide files as a map of filepath to content. Best for 1-20 files generated in conversation.\n\nIMPORTANT: If the project already exists on the user's local filesystem (e.g., they said \"deploy my app\" or referenced a directory path), use berth_deploy_dir instead — it's faster and includes all project files automatically.\n\nFor iterative development where you'll make multiple changes, use berth_sandbox_create instead — it supports instant hot-reload without full rebuilds.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"files": map[string]interface{}{
						"type":        "object",
						"description": "Map of relative file paths to file contents. Max 20 files, 5MB total.",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Subdomain name (optional, auto-generated if empty)",
					},
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Display title for the gallery (optional)",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Description shown in the gallery (optional)",
					},
					"env": map[string]interface{}{
						"type":        "object",
						"description": "Environment variables as key-value pairs (optional)",
					},
					"port": map[string]interface{}{
						"type":        "integer",
						"description": "Port the app listens on (optional, auto-detected)",
					},
					"memory": map[string]interface{}{
						"type":        "string",
						"description": "Memory limit, e.g. '512m', '1g' (optional, default 512m)",
					},
					"network_quota": map[string]interface{}{
						"type":        "string",
						"description": "Network transfer quota, e.g. '5g', '10g' (optional, uses server default)",
					},
					"ttl": map[string]interface{}{
						"type":        "string",
						"description": "Time to live: '24h', '7d', '0' for never (optional, default 72h)",
					},
					"protect_mode": map[string]interface{}{
						"type":        "string",
						"description": "Access control mode: 'basic_auth', 'api_key', or 'user'. Set at deploy time so protection is active when the route goes live.",
					},
					"protect_username": map[string]interface{}{
						"type":        "string",
						"description": "Username for basic_auth mode",
					},
					"protect_password": map[string]interface{}{
						"type":        "string",
						"description": "Password for basic_auth mode",
					},
					"protect_api_key": map[string]interface{}{
						"type":        "string",
						"description": "Custom API key (optional, auto-generated if empty)",
					},
					"protect_users": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access.",
					},
				},
				"required": []string{"files"},
			},
		},
		{
			Name:        "berth_deploy_dir",
			Description: "Deploy a project directory from the local filesystem to a live HTTPS URL. Creates a tarball and uploads it. Respects .gitignore.\n\nPREFERRED over berth_deploy when:\n- The project already exists on disk (user said \"deploy this\", \"deploy my app\", referenced a path)\n- The project has more than 5 files\n- The project has dependencies (node_modules, venv, etc.) that shouldn't be sent inline\n\nFor iterative development where you'll make multiple changes, use berth_sandbox_create instead.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute or relative path to the project directory",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Subdomain name (optional, auto-generated from directory name)",
					},
					"env": map[string]interface{}{
						"type":        "object",
						"description": "Environment variables as key-value pairs (optional)",
					},
					"port": map[string]interface{}{
						"type":        "string",
						"description": "Port the app listens on (optional, auto-detected)",
					},
					"memory": map[string]interface{}{
						"type":        "string",
						"description": "Memory limit (optional)",
					},
					"network_quota": map[string]interface{}{
						"type":        "string",
						"description": "Network transfer quota (optional)",
					},
					"ttl": map[string]interface{}{
						"type":        "string",
						"description": "Time to live (optional)",
					},
					"protect_mode": map[string]interface{}{
						"type":        "string",
						"description": "Access control mode: 'basic_auth', 'api_key', or 'user'.",
					},
					"protect_username": map[string]interface{}{
						"type":        "string",
						"description": "Username for basic_auth mode",
					},
					"protect_password": map[string]interface{}{
						"type":        "string",
						"description": "Password for basic_auth mode",
					},
					"protect_api_key": map[string]interface{}{
						"type":        "string",
						"description": "Custom API key (optional, auto-generated if empty)",
					},
					"protect_users": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "berth_update",
			Description: "Update an existing deployment with new inline files. Replaces all files and triggers a full rebuild.\n\nIMPORTANT: If the project exists on the user's filesystem, use berth_update_dir instead.\nFor iterative changes to a sandbox, use berth_sandbox_push — it's instant (no rebuild).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Deployment ID to update",
					},
					"files": map[string]interface{}{
						"type":        "object",
						"description": "Map of relative file paths to file contents (replaces all files)",
					},
					"env": map[string]interface{}{
						"type":        "object",
						"description": "Environment variables (optional)",
					},
					"port": map[string]interface{}{
						"type":        "integer",
						"description": "Port override (optional)",
					},
					"memory": map[string]interface{}{
						"type":        "string",
						"description": "Memory limit (optional)",
					},
					"network_quota": map[string]interface{}{
						"type":        "string",
						"description": "Network transfer quota (optional)",
					},
				},
				"required": []string{"id", "files"},
			},
		},
		{
			Name:        "berth_update_dir",
			Description: "Update an existing deployment from a local project directory. Creates a tarball and uploads it. Respects .gitignore.\n\nPREFERRED over berth_update when the project exists on disk.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Deployment ID to update",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute or relative path to the project directory",
					},
					"env": map[string]interface{}{
						"type":        "object",
						"description": "Environment variables (optional)",
					},
					"port": map[string]interface{}{
						"type":        "string",
						"description": "Port override (optional)",
					},
					"memory": map[string]interface{}{
						"type":        "string",
						"description": "Memory limit (optional)",
					},
					"network_quota": map[string]interface{}{
						"type":        "string",
						"description": "Network transfer quota (optional)",
					},
				},
				"required": []string{"id", "path"},
			},
		},
		{
			Name:        "berth_status",
			Description: "Get the status of a deployment (building, running, failed, etc.) and its URL.\n\nUse this after berth_deploy or berth_update to check if the build completed. Status values: 'building' (wait and check again), 'running' (ready to use), 'failed' (check berth_logs for errors).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Deployment ID",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Deployment ID",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Deployment ID",
					},
					"tail": map[string]interface{}{
						"type":        "integer",
						"description": "Number of log lines (default 100)",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Deployment ID to destroy",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Deployment ID",
					},
					"mode": map[string]interface{}{
						"type":        "string",
						"description": "Access mode: 'public', 'basic_auth', 'api_key', or 'user'",
					},
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username for basic_auth mode",
					},
					"password": map[string]interface{}{
						"type":        "string",
						"description": "Password for basic_auth mode",
					},
					"apiKey": map[string]interface{}{
						"type":        "string",
						"description": "Custom API key (optional, auto-generated if empty)",
					},
					"users": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access.",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Deployment ID",
					},
					"locked": map[string]interface{}{
						"type":        "boolean",
						"description": "true to lock, false to unlock",
					},
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
					"files": map[string]interface{}{
						"type":        "object",
						"description": "Map of relative file paths to file contents. Max 100 files, 10MB total.",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Subdomain name (optional, auto-generated if empty)",
					},
					"env": map[string]interface{}{
						"type":        "object",
						"description": "Environment variables as key-value pairs (optional)",
					},
					"port": map[string]interface{}{
						"type":        "integer",
						"description": "Port the dev server listens on (optional, auto-detected)",
					},
					"memory": map[string]interface{}{
						"type":        "string",
						"description": "Memory limit, e.g. '512m', '1g' (optional, default 1g)",
					},
					"network_quota": map[string]interface{}{
						"type":        "string",
						"description": "Network transfer quota, e.g. '5g', '10g' (optional, uses server default)",
					},
					"ttl": map[string]interface{}{
						"type":        "string",
						"description": "Time to live: '4h', '12h', '24h' (optional, default 4h)",
					},
					"protect_mode": map[string]interface{}{
						"type":        "string",
						"description": "Access control mode: 'basic_auth', 'api_key', or 'user'. Set at create time so protection is active when the route goes live.",
					},
					"protect_username": map[string]interface{}{
						"type":        "string",
						"description": "Username for basic_auth mode",
					},
					"protect_password": map[string]interface{}{
						"type":        "string",
						"description": "Password for basic_auth mode",
					},
					"protect_api_key": map[string]interface{}{
						"type":        "string",
						"description": "Custom API key (optional, auto-generated if empty)",
					},
					"protect_users": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Usernames allowed to access (for 'user' mode). If empty, any authenticated user can access.",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Sandbox ID",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Sandbox ID",
					},
					"packages": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Package names to install or remove",
					},
					"uninstall": map[string]interface{}{
						"type":        "boolean",
						"description": "Set to true to uninstall packages (default: false)",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Sandbox ID",
					},
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to execute",
					},
					"timeout": map[string]interface{}{
						"type":        "integer",
						"description": "Timeout in seconds (default 30, max 300)",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Sandbox ID",
					},
					"tail": map[string]interface{}{
						"type":        "integer",
						"description": "Number of log lines (default 100)",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Sandbox ID to destroy",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Sandbox ID to promote",
					},
					"ttl": map[string]interface{}{
						"type":        "string",
						"description": "Time to live for the deployment: '24h', '7d', '0' for never (optional, default: user's default TTL)",
					},
					"memory": map[string]interface{}{
						"type":        "string",
						"description": "Memory limit, e.g. '512m', '1g' (optional)",
					},
					"network_quota": map[string]interface{}{
						"type":        "string",
						"description": "Network transfer quota, e.g. '5g', '10g' (optional)",
					},
					"env": map[string]interface{}{
						"type":        "object",
						"description": "Environment variables to set (optional, merged with existing)",
					},
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
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Deployment ID",
					},
					"quota": map[string]interface{}{
						"type":        "string",
						"description": "Network transfer quota, e.g. '1g', '5g', '10g', or '' to remove",
					},
				},
				"required": []string{"id", "quota"},
			},
		},
	}
}

// ── Tool Execution ──────────────────────────────────────────────────

func (s *MCPServer) callTool(name string, args json.RawMessage) *ToolResult {
	switch name {
	case "berth_deploy":
		return s.toolDeploy(args)
	case "berth_deploy_dir":
		return s.toolDeployDir(args)
	case "berth_update":
		return s.toolUpdate(args)
	case "berth_update_dir":
		return s.toolUpdateDir(args)
	case "berth_status":
		return s.toolStatus(args)
	case "berth_source":
		return s.toolSource(args)
	case "berth_logs":
		return s.toolLogs(args)
	case "berth_list":
		return s.toolList()
	case "berth_destroy":
		return s.toolDestroy(args)
	case "berth_protect":
		return s.toolProtect(args)
	case "berth_lock":
		return s.toolLock(args)
	case "berth_sandbox_create":
		return s.toolSandboxCreate(args)
	case "berth_sandbox_push":
		return s.toolSandboxPush(args)
	case "berth_sandbox_install":
		return s.toolSandboxInstall(args)
	case "berth_sandbox_exec":
		return s.toolSandboxExec(args)
	case "berth_sandbox_logs":
		return s.toolSandboxLogs(args)
	case "berth_sandbox_destroy":
		return s.toolSandboxDestroy(args)
	case "berth_sandbox_promote":
		return s.toolSandboxPromote(args)
	case "berth_update_quota":
		return s.toolUpdateQuota(args)
	default:
		return errorResult("Unknown tool: " + name)
	}
}

func (s *MCPServer) toolDeploy(args json.RawMessage) *ToolResult {
	body, err := s.apiPost("/api/deploy/code", args)
	if err != nil {
		return errorResult("Deploy failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Deploy failed: " + errMsg)
	}

	url, _ := resp["url"].(string)
	id, _ := resp["id"].(string)
	fw, _ := resp["framework"].(string)

	text := fmt.Sprintf("Deployment started!\n\nURL: %s\nID: %s\nFramework: %s\nStatus: building\n\nIMPORTANT: The build takes 15-60 seconds. Call berth_status with id '%s' to check when it's ready. If status is 'failed', call berth_logs to see the error.", url, id, fw, id)

	return textResult(text)
}

func (s *MCPServer) toolDeployDir(args json.RawMessage) *ToolResult {
	var params struct {
		Path            string            `json:"path"`
		Name            string            `json:"name"`
		Env             map[string]string `json:"env"`
		Port            string            `json:"port"`
		Memory          string            `json:"memory"`
		NetworkQuota    string            `json:"network_quota"`
		TTL             string            `json:"ttl"`
		ProtectMode     string            `json:"protect_mode"`
		ProtectUsername  string            `json:"protect_username"`
		ProtectPassword string            `json:"protect_password"`
		ProtectApiKey   string            `json:"protect_api_key"`
		ProtectUsers    []string          `json:"protect_users"`
	}
	json.Unmarshal(args, &params)

	if params.Path == "" {
		return errorResult("Path required")
	}

	// Resolve path
	absPath, err := filepath.Abs(params.Path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error())
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return errorResult("Path not found: " + absPath)
	}
	if !info.IsDir() {
		return errorResult("Path is not a directory: " + absPath)
	}

	// Create tarball
	tmpFile, err := os.CreateTemp("", "berth-mcp-*.tar.gz")
	if err != nil {
		return errorResult("Failed to create temp file: " + err.Error())
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	fileCount, err := createTarball(absPath, tmpFile)
	if err != nil {
		return errorResult("Failed to create tarball: " + err.Error())
	}
	tmpFile.Close()

	// Build multipart upload
	fields := map[string]string{}
	if params.Name != "" {
		fields["name"] = params.Name
	} else {
		fields["name"] = filepath.Base(absPath)
	}
	if params.TTL != "" {
		fields["ttl"] = params.TTL
	}
	if params.Port != "" {
		fields["port"] = params.Port
	}
	if params.Memory != "" {
		fields["memory"] = params.Memory
	}
	if params.NetworkQuota != "" {
		fields["network_quota"] = params.NetworkQuota
	}
	if params.ProtectMode != "" {
		fields["protect_mode"] = params.ProtectMode
	}
	if params.ProtectUsername != "" {
		fields["protect_username"] = params.ProtectUsername
	}
	if params.ProtectPassword != "" {
		fields["protect_password"] = params.ProtectPassword
	}
	if params.ProtectApiKey != "" {
		fields["protect_api_key"] = params.ProtectApiKey
	}
	if len(params.ProtectUsers) > 0 {
		fields["protect_users"] = strings.Join(params.ProtectUsers, ",")
	}

	body, err := s.apiUpload("/api/deploy", tmpFile.Name(), fields, params.Env)
	if err != nil {
		return errorResult("Upload failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Deploy failed: " + errMsg)
	}

	url, _ := resp["url"].(string)
	id, _ := resp["id"].(string)
	fw, _ := resp["framework"].(string)

	text := fmt.Sprintf("Deployment started!\n\nURL: %s\nID: %s\nFramework: %s\nFiles: %d\nSource: %s", url, id, fw, fileCount, absPath)
	if am, ok := resp["accessMode"].(string); ok && am != "" {
		text += fmt.Sprintf("\nAccess: %s", am)
	}
	if ak, ok := resp["apiKey"].(string); ok && ak != "" {
		text += fmt.Sprintf("\nAPI Key: %s", ak)
	}
	text += fmt.Sprintf("\n\nIMPORTANT: The build takes 15-60 seconds. Call berth_status with id '%s' to check when it's ready. If status is 'failed', call berth_logs to see the error.", id)
	return textResult(text)
}

func (s *MCPServer) toolUpdate(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Deployment ID required")
	}

	body, err := s.apiPost("/api/deploy/"+params.ID+"/update/code", args)
	if err != nil {
		return errorResult("Update failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Update failed: " + errMsg)
	}

	url, _ := resp["url"].(string)

	return textResult(fmt.Sprintf("Code updated. Rebuilding...\n\nURL: %s\nID: %s\n\nIMPORTANT: The build takes 15-60 seconds. Call berth_status with id '%s' to check when it's ready. If status is 'failed', call berth_logs to see the error.", url, params.ID, params.ID))
}

func (s *MCPServer) toolUpdateDir(args json.RawMessage) *ToolResult {
	var params struct {
		ID     string            `json:"id"`
		Path   string            `json:"path"`
		Env    map[string]string `json:"env"`
		Port   string            `json:"port"`
		Memory       string            `json:"memory"`
		NetworkQuota string            `json:"network_quota"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Deployment ID required")
	}
	if params.Path == "" {
		return errorResult("Path required")
	}

	absPath, err := filepath.Abs(params.Path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error())
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return errorResult("Path not found: " + absPath)
	}
	if !info.IsDir() {
		return errorResult("Path is not a directory: " + absPath)
	}

	// Create tarball
	tmpFile, err := os.CreateTemp("", "berth-mcp-update-*.tar.gz")
	if err != nil {
		return errorResult("Failed to create temp file: " + err.Error())
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	fileCount, err := createTarball(absPath, tmpFile)
	if err != nil {
		return errorResult("Failed to create tarball: " + err.Error())
	}
	tmpFile.Close()

	fields := map[string]string{}
	if params.Port != "" {
		fields["port"] = params.Port
	}
	if params.Memory != "" {
		fields["memory"] = params.Memory
	}
	if params.NetworkQuota != "" {
		fields["network_quota"] = params.NetworkQuota
	}

	body, err := s.apiUpload("/api/deploy/"+params.ID+"/update", tmpFile.Name(), fields, params.Env)
	if err != nil {
		return errorResult("Upload failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Update failed: " + errMsg)
	}

	url, _ := resp["url"].(string)

	text := fmt.Sprintf("Code updated. Rebuilding...\n\nURL: %s\nID: %s\nFiles: %d\nSource: %s\n\nIMPORTANT: The build takes 15-60 seconds. Call berth_status with id '%s' to check when it's ready. If status is 'failed', call berth_logs to see the error.", url, params.ID, fileCount, absPath, params.ID)
	return textResult(text)
}

func (s *MCPServer) toolStatus(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	body, err := s.apiGet("/api/deployments/" + params.ID)
	if err != nil {
		return errorResult("Status check failed: " + err.Error() + "\n\nUse berth_list to find active deployments.")
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult(errMsg + "\n\nUse berth_list to find active deployments.")
	}

	pretty, _ := json.MarshalIndent(resp, "", "  ")
	text := string(pretty)

	// Add next-step hints based on status
	if status, ok := resp["status"].(string); ok {
		switch status {
		case "building":
			text += "\n\nStill building. Wait a few seconds and check again."
		case "failed":
			text += "\n\nBuild failed. Use berth_logs to see what went wrong."
		}
	}

	return textResult(text)
}

func (s *MCPServer) toolSource(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Deployment ID required")
	}

	body, err := s.apiGet("/api/deployments/" + params.ID + "/source?format=json")
	if err != nil {
		return errorResult("Source fetch failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult(errMsg)
	}

	pretty, _ := json.MarshalIndent(resp, "", "  ")
	text := string(pretty)
	text += "\n\nUse berth_update to modify and redeploy."
	return textResult(text)
}

func (s *MCPServer) toolLogs(args json.RawMessage) *ToolResult {
	var params struct {
		ID   string `json:"id"`
		Tail int    `json:"tail"`
	}
	json.Unmarshal(args, &params)

	tail := 100
	if params.Tail > 0 {
		tail = params.Tail
	}

	body, err := s.apiGet(fmt.Sprintf("/api/deployments/%s/logs?tail=%d", params.ID, tail))
	if err != nil {
		return errorResult("Log fetch failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult(errMsg)
	}

	logs, _ := resp["logs"].(string)
	if logs == "" {
		return textResult("No logs available yet.")
	}
	return textResult(logs)
}

func (s *MCPServer) toolList() *ToolResult {
	body, err := s.apiGet("/api/deployments")
	if err != nil {
		return errorResult("List failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	deploys, ok := resp["deployments"].([]interface{})
	if !ok || len(deploys) == 0 {
		return textResult("No active deployments.")
	}

	pretty, _ := json.MarshalIndent(deploys, "", "  ")
	return textResult(string(pretty))
}

func (s *MCPServer) toolDestroy(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Deployment ID required")
	}

	body, err := s.apiDelete("/api/deployments/" + params.ID)
	if err != nil {
		return errorResult("Destroy failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult(errMsg)
	}

	return textResult(fmt.Sprintf("Deployment %s destroyed.", params.ID))
}

// ── Protect ─────────────────────────────────────────────────────────

func (s *MCPServer) toolProtect(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Deployment ID required")
	}

	body, err := s.apiPost("/api/deployments/"+params.ID+"/protect", args)
	if err != nil {
		return errorResult("Protect failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Protect failed: " + errMsg)
	}

	msg, _ := resp["message"].(string)
	if msg == "" {
		msg = fmt.Sprintf("Protection updated for deployment %s.", params.ID)
	}
	return textResult(msg)
}

func (s *MCPServer) toolLock(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Deployment ID required")
	}

	body, err := s.apiPost("/api/deployments/"+params.ID+"/lock", args)
	if err != nil {
		return errorResult("Lock failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Lock failed: " + errMsg)
	}

	msg, _ := resp["message"].(string)
	if msg == "" {
		msg = fmt.Sprintf("Lock state updated for deployment %s.", params.ID)
	}
	return textResult(msg)
}

// ── Sandbox Tools ───────────────────────────────────────────────────

func (s *MCPServer) toolSandboxCreate(args json.RawMessage) *ToolResult {
	body, err := s.apiPost("/api/sandbox", args)
	if err != nil {
		return errorResult("Sandbox creation failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Sandbox creation failed: " + errMsg)
	}

	url, _ := resp["url"].(string)
	id, _ := resp["id"].(string)
	fw, _ := resp["framework"].(string)
	status, _ := resp["status"].(string)

	text := fmt.Sprintf("Sandbox created!\n\nURL: %s\nID: %s\nFramework: %s\nStatus: %s\n\nThe sandbox is starting with a dev server. Use berth_sandbox_push with id '%s' to update files instantly (no rebuild needed). When done iterating, use berth_sandbox_promote to create an optimized production deployment.", url, id, fw, status, id)
	return textResult(text)
}

func (s *MCPServer) toolSandboxPush(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	body, err := s.apiPost("/api/sandbox/"+params.ID+"/push", args)
	if err != nil {
		return errorResult("Push failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Push failed: " + errMsg)
	}

	msg := "Push complete."
	if updated, ok := resp["updated"].(float64); ok {
		msg = fmt.Sprintf("Push complete: %.0f files updated", updated)
		if deleted, ok := resp["deleted"].(float64); ok && deleted > 0 {
			msg += fmt.Sprintf(", %.0f deleted", deleted)
		}
		msg += "."
	}
	if depsInstalled, ok := resp["deps_installed"].(bool); ok && depsInstalled {
		msg += "\nDependencies reinstalled."
	}
	if installOutput, ok := resp["install_output"].(string); ok && installOutput != "" {
		msg += "\n\nInstall output:\n" + installOutput
	}
	return textResult(msg)
}

func (s *MCPServer) toolSandboxInstall(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	body, err := s.apiPost("/api/sandbox/"+params.ID+"/install", args)
	if err != nil {
		return errorResult("Install failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Install failed: " + errMsg)
	}

	msg, _ := resp["message"].(string)
	if msg == "" {
		msg = "Packages installed."
	}
	if output, ok := resp["output"].(string); ok && output != "" {
		msg += "\n\nOutput:\n" + output
	}
	return textResult(msg)
}

func (s *MCPServer) toolSandboxExec(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	body, err := s.apiPost("/api/sandbox/"+params.ID+"/exec", args)
	if err != nil {
		return errorResult("Exec failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Exec failed: " + errMsg)
	}

	output, _ := resp["output"].(string)
	if exitCode, ok := resp["exit_code"].(float64); ok && exitCode != 0 {
		output += fmt.Sprintf("\n\nExit code: %.0f", exitCode)
	}
	return textResult(output)
}

func (s *MCPServer) toolSandboxLogs(args json.RawMessage) *ToolResult {
	var params struct {
		ID   string `json:"id"`
		Tail int    `json:"tail"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	tail := 100
	if params.Tail > 0 {
		tail = params.Tail
	}

	body, err := s.apiGet(fmt.Sprintf("/api/sandbox/%s/logs?tail=%d", params.ID, tail))
	if err != nil {
		return errorResult("Log fetch failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult(errMsg)
	}

	logs, _ := resp["logs"].(string)
	if logs == "" {
		return textResult("No logs available yet.")
	}
	return textResult(logs)
}

func (s *MCPServer) toolSandboxDestroy(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	body, err := s.apiDelete("/api/sandbox/" + params.ID)
	if err != nil {
		return errorResult("Destroy failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult(errMsg)
	}

	return textResult(fmt.Sprintf("Sandbox %s destroyed.", params.ID))
}

func (s *MCPServer) toolSandboxPromote(args json.RawMessage) *ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	body, err := s.apiPost("/api/sandbox/"+params.ID+"/promote", args)
	if err != nil {
		return errorResult("Promote failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Promote failed: " + errMsg)
	}

	url, _ := resp["url"].(string)
	id, _ := resp["id"].(string)
	fw, _ := resp["framework"].(string)
	status, _ := resp["status"].(string)

	text := fmt.Sprintf("Promoting sandbox to production deployment...\n\nURL: %s\nID: %s\nFramework: %s\nStatus: %s\n\nIMPORTANT: The build takes 15-60 seconds. Call berth_status with id '%s' to check when it's ready.", url, id, fw, status, id)
	return textResult(text)
}

// ── Quota Tool ──────────────────────────────────────────────────────

func (s *MCPServer) toolUpdateQuota(args json.RawMessage) *ToolResult {
	var params struct {
		ID    string `json:"id"`
		Quota string `json:"quota"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Deployment ID required")
	}

	patchBody, _ := json.Marshal(map[string]string{"network_quota": params.Quota})
	body, err := s.apiPatch("/api/deployments/"+params.ID, json.RawMessage(patchBody))
	if err != nil {
		return errorResult("Update quota failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Update quota failed: " + errMsg)
	}

	quota, _ := resp["networkQuota"].(string)
	if quota == "" {
		return textResult(fmt.Sprintf("Network quota removed from deployment %s.", params.ID))
	}
	return textResult(fmt.Sprintf("Network quota set to %s for deployment %s.", quota, params.ID))
}

// ── HTTP helpers ────────────────────────────────────────────────────

func (s *MCPServer) apiPost(path string, body json.RawMessage) ([]byte, error) {
	req, _ := http.NewRequest("POST", s.apiURL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *MCPServer) apiUpload(path, tarballPath string, fields map[string]string, envVars map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add fields
	for k, v := range fields {
		writer.WriteField(k, v)
	}
	for k, v := range envVars {
		writer.WriteField("env", k+"="+v)
	}

	// Add tarball
	part, err := writer.CreateFormFile("tarball", "project.tar.gz")
	if err != nil {
		return nil, err
	}

	f, err := os.Open(tarballPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	io.Copy(part, f)
	writer.Close()

	req, _ := http.NewRequest("POST", s.apiURL+path, &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *MCPServer) apiPatch(path string, body json.RawMessage) ([]byte, error) {
	req, _ := http.NewRequest("PATCH", s.apiURL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *MCPServer) apiGet(path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", s.apiURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *MCPServer) apiDelete(path string) ([]byte, error) {
	req, _ := http.NewRequest("DELETE", s.apiURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// ── Result helpers ──────────────────────────────────────────────────

func textResult(text string) *ToolResult {
	return &ToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}

func errorResult(text string) *ToolResult {
	return &ToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
		IsError: true,
	}
}

// ── Tarball creation ────────────────────────────────────────────────

// createTarball creates a .tar.gz from a directory, skipping common junk.
func createTarball(srcDir string, dest *os.File) (int, error) {
	gw := gzip.NewWriter(dest)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	skipDirs := map[string]bool{
		"node_modules": true, ".git": true, ".next": true, "dist": true,
		"build": true, "__pycache__": true, ".venv": true, "venv": true,
		"target": true, "vendor": true,
	}

	count := 0
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		rel, _ := filepath.Rel(srcDir, path)
		if rel == "." {
			return nil
		}

		// Skip junk directories
		if info.IsDir() && skipDirs[info.Name()] {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
		}

		// Skip large files (>10MB)
		if info.Size() > 10*1024*1024 {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		io.Copy(tw, f)

		count++
		return nil
	})

	return count, err
}

// ── Main ────────────────────────────────────────────────────────────

func main() {
	server := NewMCPServer()
	server.Run()
}
