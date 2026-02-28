package main

import (
	"encoding/json"
	"fmt"
)

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
