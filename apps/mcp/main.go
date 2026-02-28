package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
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

// ── Main ────────────────────────────────────────────────────────────

func main() {
	server := NewMCPServer()
	server.Run()
}
