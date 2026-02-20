package mcphandler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/openberth/openberth/apps/server/internal/service"
	"github.com/openberth/openberth/apps/server/internal/store"
)

// MCPHandler implements the MCP Streamable HTTP transport for Claude UI.
// It delegates all business logic to the service layer.
type MCPHandler struct {
	svc      *service.Service
	auth     func(r *http.Request) *store.User
	version  string
	sessions sync.Map // sessionID -> userID
}

// NewMCPHandler creates a new MCP handler.
func NewMCPHandler(svc *service.Service, auth func(r *http.Request) *store.User, version string) *MCPHandler {
	return &MCPHandler{svc: svc, auth: auth, version: version}
}

// ── JSON-RPC types ───────────────────────────────────────────────────

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *mcpError   `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ── HTTP Handler ─────────────────────────────────────────────────────

func (m *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.handlePost(w, r)
	case http.MethodDelete:
		w.WriteHeader(200)
	default:
		http.NotFound(w, r)
	}
}

func (m *MCPHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	user := m.auth(r)
	if user == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, m.svc.Cfg.BaseURL))
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
		return
	}

	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON-RPC request"})
		return
	}

	resp := m.dispatch(req, user)
	if resp == nil {
		w.WriteHeader(202)
		return
	}

	if req.Method == "initialize" {
		sessionID := "mcp_" + service.RandomHex(16)
		m.sessions.Store(sessionID, user.ID)
		w.Header().Set("Mcp-Session-Id", sessionID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(resp)
}

// ── Dispatch ─────────────────────────────────────────────────────────

func (m *MCPHandler) dispatch(req mcpRequest, user *store.User) *mcpResponse {
	switch req.Method {
	case "initialize":
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "openberth",
					"version": m.version,
				},
			},
		}

	case "notifications/initialized", "notifications/cancelled":
		return nil

	case "ping":
		return &mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}

	case "tools/list":
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"tools": tools()},
		}

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		result := m.callTool(params.Name, params.Arguments, user)
		return &mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result}

	case "resources/list":
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"resources": []interface{}{}},
		}

	case "prompts/list":
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"prompts": []interface{}{}},
		}

	default:
		if req.ID == nil {
			return nil
		}
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcpError{Code: -32601, Message: "Method not found: " + req.Method},
		}
	}
}

// ── Tool Execution ───────────────────────────────────────────────────

func (m *MCPHandler) callTool(name string, args json.RawMessage, user *store.User) *mcpToolResult {
	switch name {
	case "berth_deploy":
		return m.toolDeploy(args, user)
	case "berth_update":
		return m.toolUpdate(args, user)
	case "berth_status":
		return m.toolStatus(args, user)
	case "berth_source":
		return m.toolSource(args, user)
	case "berth_logs":
		return m.toolLogs(args, user)
	case "berth_list":
		return m.toolList(user)
	case "berth_destroy":
		return m.toolDestroy(args, user)
	case "berth_protect":
		return m.toolProtect(args, user)
	case "berth_lock":
		return m.toolLock(args, user)
	case "berth_sandbox_create":
		return m.toolSandboxCreate(args, user)
	case "berth_sandbox_push":
		return m.toolSandboxPush(args, user)
	case "berth_sandbox_install":
		return m.toolSandboxInstall(args, user)
	case "berth_sandbox_exec":
		return m.toolSandboxExec(args, user)
	case "berth_sandbox_logs":
		return m.toolSandboxLogs(args, user)
	case "berth_sandbox_destroy":
		return m.toolSandboxDestroy(args, user)
	case "berth_sandbox_promote":
		return m.toolSandboxPromote(args, user)
	case "berth_update_quota":
		return m.toolUpdateQuota(args, user)
	default:
		return errorResult("Unknown tool: " + name)
	}
}

func (m *MCPHandler) toolDeploy(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		Files           map[string]string `json:"files"`
		Name            string            `json:"name"`
		Title           string            `json:"title"`
		Description     string            `json:"description"`
		Env             map[string]string `json:"env"`
		Port            int               `json:"port"`
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

	result, err := m.svc.DeployCode(user, service.CodeDeployParams{
		Files:           params.Files,
		Name:            params.Name,
		Title:           params.Title,
		Description:     params.Description,
		TTL:             params.TTL,
		Port:            params.Port,
		Env:             params.Env,
		Memory:          params.Memory,
		NetworkQuota:    params.NetworkQuota,
		ProtectMode:     params.ProtectMode,
		ProtectUsername:  params.ProtectUsername,
		ProtectPassword: params.ProtectPassword,
		ProtectApiKey:   params.ProtectApiKey,
		ProtectUsers:    strings.Join(params.ProtectUsers, ","),
	})
	if err != nil {
		return errorResult(err.Error())
	}

	text := fmt.Sprintf("Deployment started!\n\nURL: %s\nID: %s\nFramework: %s\nStatus: building", result.URL, result.ID, result.Framework)
	if result.AccessMode != "" {
		text += fmt.Sprintf("\nAccess: %s", result.AccessMode)
	}
	if result.ApiKey != "" {
		text += fmt.Sprintf("\nAPI Key: %s", result.ApiKey)
	}
	text += fmt.Sprintf("\n\nIMPORTANT: The build takes 15-60 seconds. Call berth_status with id '%s' to check when it's ready. If status is 'failed', call berth_logs to see the error.", result.ID)
	return textResult(text)
}

func (m *MCPHandler) toolUpdate(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID           string            `json:"id"`
		Files        map[string]string `json:"files"`
		Env          map[string]string `json:"env"`
		Port         int               `json:"port"`
		Memory       string            `json:"memory"`
		NetworkQuota string            `json:"network_quota"`
	}
	json.Unmarshal(args, &params)

	result, err := m.svc.UpdateCode(user, service.CodeUpdateParams{
		DeployID:     params.ID,
		Files:        params.Files,
		Port:         params.Port,
		Env:          params.Env,
		Memory:       params.Memory,
		NetworkQuota: params.NetworkQuota,
	})
	if err != nil {
		return errorResult(err.Error())
	}

	return textResult(fmt.Sprintf("Code updated. Rebuilding...\n\nURL: %s\nID: %s\n\nIMPORTANT: The build takes 15-60 seconds. Call berth_status with id '%s' to check when it's ready. If status is 'failed', call berth_logs to see the error.", result.URL, params.ID, params.ID))
}

func (m *MCPHandler) toolStatus(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	info, err := m.svc.GetDeployment(user, params.ID)
	if err != nil {
		return errorResult(err.Error() + "\n\nUse berth_list to find active deployments.")
	}

	pretty, _ := json.MarshalIndent(info, "", "  ")
	text := string(pretty)

	// Add next-step hints based on status
	switch info.Status {
	case "building":
		text += "\n\nStill building. Wait a few seconds and check again."
	case "failed":
		text += "\n\nBuild failed. Use berth_logs to see what went wrong."
	}

	return textResult(text)
}

func (m *MCPHandler) toolSource(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	result, err := m.svc.GetSource(user, params.ID)
	if err != nil {
		return errorResult(err.Error())
	}

	pretty, _ := json.MarshalIndent(result, "", "  ")
	text := string(pretty)
	text += "\n\nUse berth_update to modify and redeploy."
	return textResult(text)
}

func (m *MCPHandler) toolLogs(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID   string `json:"id"`
		Tail int    `json:"tail"`
	}
	json.Unmarshal(args, &params)

	tail := 100
	if params.Tail > 0 {
		tail = params.Tail
	}

	result, err := m.svc.GetLogs(user, params.ID, tail)
	if err != nil {
		return errorResult(err.Error())
	}

	if result.Logs == "" {
		return textResult("No logs available yet.")
	}
	return textResult(result.Logs)
}

func (m *MCPHandler) toolList(user *store.User) *mcpToolResult {
	result, _ := m.svc.ListDeployments(user)
	if len(result) == 0 {
		return textResult("No active deployments.")
	}

	pretty, _ := json.MarshalIndent(result, "", "  ")
	return textResult(string(pretty))
}

func (m *MCPHandler) toolDestroy(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Deployment ID required")
	}

	if err := m.svc.DestroyDeployment(user, params.ID); err != nil {
		return errorResult(err.Error())
	}

	return textResult(fmt.Sprintf("Deployment %s destroyed.", params.ID))
}

func (m *MCPHandler) toolProtect(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID       string   `json:"id"`
		Mode     string   `json:"mode"`
		Username string   `json:"username"`
		Password string   `json:"password"`
		ApiKey   string   `json:"apiKey"`
		Users    []string `json:"users"`
	}
	json.Unmarshal(args, &params)

	result, err := m.svc.ProtectDeployment(user, service.ProtectParams{
		DeployID: params.ID,
		Mode:     params.Mode,
		Username: params.Username,
		Password: params.Password,
		ApiKey:   params.ApiKey,
		Users:    strings.Join(params.Users, ","),
	})
	if err != nil {
		return errorResult(err.Error())
	}

	return textResult(result.Message)
}

func (m *MCPHandler) toolLock(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID     string `json:"id"`
		Locked bool   `json:"locked"`
	}
	json.Unmarshal(args, &params)

	result, err := m.svc.LockDeployment(user, params.ID, params.Locked)
	if err != nil {
		return errorResult(err.Error())
	}

	return textResult(result.Message)
}

// ── Sandbox Tool Handlers ────────────────────────────────────────────

func (m *MCPHandler) toolSandboxCreate(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		Files           map[string]string `json:"files"`
		Name            string            `json:"name"`
		Env             map[string]string `json:"env"`
		Port            int               `json:"port"`
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

	result, err := m.svc.CreateSandbox(user, service.SandboxCreateParams{
		Files:           params.Files,
		Name:            params.Name,
		TTL:             params.TTL,
		Port:            params.Port,
		Env:             params.Env,
		Memory:          params.Memory,
		NetworkQuota:    params.NetworkQuota,
		ProtectMode:     params.ProtectMode,
		ProtectUsername:  params.ProtectUsername,
		ProtectPassword: params.ProtectPassword,
		ProtectApiKey:   params.ProtectApiKey,
		ProtectUsers:    strings.Join(params.ProtectUsers, ","),
	})
	if err != nil {
		return errorResult(err.Error())
	}

	text := fmt.Sprintf("Sandbox created!\n\nURL: %s\nID: %s\nFramework: %s\nStatus: %s", result.URL, result.ID, result.Framework, result.Status)
	if result.AccessMode != "" {
		text += fmt.Sprintf("\nAccess: %s", result.AccessMode)
	}
	if result.ApiKey != "" {
		text += fmt.Sprintf("\nAPI Key: %s", result.ApiKey)
	}
	text += fmt.Sprintf("\n\nThe sandbox is starting with a dev server. Use berth_sandbox_push with id '%s' to update files instantly (no rebuild needed). When done iterating, use berth_sandbox_promote to create an optimized production deployment.", result.ID)
	return textResult(text)
}

func (m *MCPHandler) toolSandboxPush(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID      string               `json:"id"`
		Changes []service.PushChange `json:"changes"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	result, err := m.svc.SandboxPush(user, service.PushParams{
		SandboxID: params.ID,
		Changes:   params.Changes,
	})
	if err != nil {
		return errorResult(err.Error())
	}

	msg := fmt.Sprintf("Push complete: %d files updated, %d deleted.", result.Updated, result.Deleted)
	if result.DepsInstalled {
		msg += "\nDependencies reinstalled."
	}
	if result.InstallOutput != "" {
		msg += "\n\nInstall output:\n" + result.InstallOutput
	}
	return textResult(msg)
}

func (m *MCPHandler) toolSandboxInstall(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID        string   `json:"id"`
		Packages  []string `json:"packages"`
		Uninstall bool     `json:"uninstall"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	result, err := m.svc.SandboxInstall(user, service.InstallParams{
		SandboxID: params.ID,
		Packages:  params.Packages,
		Uninstall: params.Uninstall,
	})
	if err != nil {
		return errorResult(err.Error())
	}

	msg := result.Message
	if result.Output != "" {
		msg += "\n\nOutput:\n" + result.Output
	}
	return textResult(msg)
}

func (m *MCPHandler) toolSandboxExec(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID      string `json:"id"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	result, err := m.svc.SandboxExec(user, service.ExecParams{
		SandboxID: params.ID,
		Command:   params.Command,
		Timeout:   params.Timeout,
	})
	if err != nil {
		return errorResult(err.Error())
	}

	msg := result.Output
	if result.ExitCode != 0 {
		msg += fmt.Sprintf("\n\nExit code: %d", result.ExitCode)
	}
	return textResult(msg)
}

func (m *MCPHandler) toolSandboxLogs(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID   string `json:"id"`
		Tail int    `json:"tail"`
	}
	json.Unmarshal(args, &params)

	tail := 100
	if params.Tail > 0 {
		tail = params.Tail
	}

	result, err := m.svc.GetLogs(user, params.ID, tail)
	if err != nil {
		return errorResult(err.Error())
	}

	if result.Logs == "" {
		return textResult("No logs available yet.")
	}
	return textResult(result.Logs)
}

func (m *MCPHandler) toolSandboxPromote(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID           string            `json:"id"`
		TTL          string            `json:"ttl"`
		Memory       string            `json:"memory"`
		NetworkQuota string            `json:"network_quota"`
		Env          map[string]string `json:"env"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	result, err := m.svc.PromoteSandbox(user, service.PromoteParams{
		SandboxID:    params.ID,
		TTL:          params.TTL,
		Memory:       params.Memory,
		NetworkQuota: params.NetworkQuota,
		Env:          params.Env,
	})
	if err != nil {
		return errorResult(err.Error())
	}

	return textResult(fmt.Sprintf("Promoting sandbox to production deployment...\n\nURL: %s\nID: %s\nFramework: %s\nStatus: %s\n\nIMPORTANT: The build takes 15-60 seconds. Call berth_status with id '%s' to check when it's ready. If status is 'failed', call berth_logs to see the error.", result.URL, result.ID, result.Framework, result.Status, result.ID))
}

func (m *MCPHandler) toolSandboxDestroy(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Sandbox ID required")
	}

	if err := m.svc.DestroyDeployment(user, params.ID); err != nil {
		return errorResult(err.Error())
	}

	return textResult(fmt.Sprintf("Sandbox %s destroyed.", params.ID))
}

// ── Quota Tool Handler ───────────────────────────────────────────────

func (m *MCPHandler) toolUpdateQuota(args json.RawMessage, user *store.User) *mcpToolResult {
	var params struct {
		ID    string `json:"id"`
		Quota string `json:"quota"`
	}
	json.Unmarshal(args, &params)

	if params.ID == "" {
		return errorResult("Deployment ID required")
	}

	result, err := m.svc.UpdateMeta(user, service.UpdateMetaParams{
		DeployID:     params.ID,
		NetworkQuota: &params.Quota,
	})
	if err != nil {
		return errorResult(err.Error())
	}

	msg := fmt.Sprintf("Network quota updated for deployment %s.", params.ID)
	if result.NetworkQuota == "" {
		msg = fmt.Sprintf("Network quota removed from deployment %s.", params.ID)
	} else {
		msg = fmt.Sprintf("Network quota set to %s for deployment %s.", result.NetworkQuota, params.ID)
	}
	return textResult(msg)
}

// ── Helpers ──────────────────────────────────────────────────────────

func textResult(text string) *mcpToolResult {
	return &mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: text}},
	}
}

func errorResult(text string) *mcpToolResult {
	return &mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: text}},
		IsError: true,
	}
}
