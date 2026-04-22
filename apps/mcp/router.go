package main

import "encoding/json"

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
		return s.toolList(args)
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
	case "berth_secret_set":
		return s.toolSecretSet(args)
	case "berth_secret_list":
		return s.toolSecretList()
	case "berth_secret_delete":
		return s.toolSecretDelete(args)
	default:
		return errorResult("Unknown tool: " + name)
	}
}
