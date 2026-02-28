package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
		Path           string            `json:"path"`
		Name           string            `json:"name"`
		Env            map[string]string `json:"env"`
		Port           string            `json:"port"`
		Memory         string            `json:"memory"`
		NetworkQuota   string            `json:"network_quota"`
		TTL            string            `json:"ttl"`
		ProtectMode    string            `json:"protect_mode"`
		ProtectUsername string            `json:"protect_username"`
		ProtectPassword string            `json:"protect_password"`
		ProtectApiKey  string            `json:"protect_api_key"`
		ProtectUsers   []string          `json:"protect_users"`
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
		ID           string            `json:"id"`
		Path         string            `json:"path"`
		Env          map[string]string `json:"env"`
		Port         string            `json:"port"`
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
