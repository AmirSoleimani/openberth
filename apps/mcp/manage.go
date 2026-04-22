package main

import (
	"encoding/json"
	"fmt"
)

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

func (s *MCPServer) toolList(args json.RawMessage) *ToolResult {
	var params struct {
		All bool `json:"all"`
	}
	if len(args) > 0 {
		json.Unmarshal(args, &params)
	}
	// Default scope: caller's own deployments. `all: true` lists every
	// deployment on the server (read visibility is open).
	path := "/api/deployments?owner=me"
	if params.All {
		path = "/api/deployments"
	}
	body, err := s.apiGet(path)
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

// ── Quota ───────────────────────────────────────────────────────────

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

// ── Secrets ─────────────────────────────────────────────────────────

func (s *MCPServer) toolSecretSet(args json.RawMessage) *ToolResult {
	var params struct {
		Name string `json:"name"`
	}
	json.Unmarshal(args, &params)

	if params.Name == "" {
		return errorResult("Secret name required")
	}

	body, err := s.apiPost("/api/secrets", args)
	if err != nil {
		return errorResult("Secret set failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Secret set failed: " + errMsg)
	}

	msg, _ := resp["message"].(string)
	if msg == "" {
		msg = fmt.Sprintf("Secret '%s' stored.", params.Name)
	}
	return textResult(msg)
}

func (s *MCPServer) toolSecretList() *ToolResult {
	body, err := s.apiGet("/api/secrets")
	if err != nil {
		return errorResult("Secret list failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Secret list failed: " + errMsg)
	}

	secrets, ok := resp["secrets"].([]interface{})
	if !ok || len(secrets) == 0 {
		return textResult("No secrets stored.")
	}

	pretty, _ := json.MarshalIndent(secrets, "", "  ")
	return textResult(string(pretty))
}

func (s *MCPServer) toolSecretDelete(args json.RawMessage) *ToolResult {
	var params struct {
		Name   string `json:"name"`
		Global bool   `json:"global"`
	}
	json.Unmarshal(args, &params)

	if params.Name == "" {
		return errorResult("Secret name required")
	}

	path := "/api/secrets/" + params.Name
	if params.Global {
		path += "?global=true"
	}

	body, err := s.apiDelete(path)
	if err != nil {
		return errorResult("Secret delete failed: " + err.Error())
	}

	var resp map[string]interface{}
	json.Unmarshal(body, &resp)

	if errMsg, ok := resp["error"].(string); ok {
		return errorResult("Secret delete failed: " + errMsg)
	}

	return textResult(fmt.Sprintf("Secret '%s' deleted.", params.Name))
}
