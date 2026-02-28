package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

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
				"instructions": "OpenBerth deploys code to live HTTPS URLs.\n\nDecision guide:\n1. ITERATIVE DEVELOPMENT (building step-by-step, multiple changes expected):\n   → berth_sandbox_create → berth_sandbox_push (instant updates) → berth_sandbox_promote (when done)\n2. ONE-SHOT DEPLOY (final code, no iteration):\n   → berth_deploy\n\nRules:\n- Call berth_list before creating new deployments to avoid duplicates.\n- After berth_deploy or berth_update, call berth_status to check build progress (builds take 15-60s). If 'failed', call berth_logs.\n- Prefer berth_sandbox_push over berth_update for active development — push is instant, update triggers a full rebuild.\n- When the user references a local directory or existing project, ALWAYS use berth_deploy_dir (not berth_deploy). It's faster, handles any project size, and respects .gitignore. Only use berth_deploy for code generated in conversation.\n- Similarly, prefer berth_update_dir over berth_update for local projects.\n- Framework is auto-detected. If wrong or unsupported, include a .berth.json with \"language\" and \"start\" fields. Override fields: language, build, start, install, dev.",
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
