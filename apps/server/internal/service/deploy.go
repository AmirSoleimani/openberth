package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openberth/openberth/apps/server/internal/framework"
	"github.com/openberth/openberth/apps/server/internal/store"
)

// ── Network quota resolution ────────────────────────────────────────

// ResolveNetworkQuota returns the effective network quota for a deployment.
// Priority: per-deploy override > admin setting > config file default.
// Returns "" if network quota is disabled (the default).
func (svc *Service) ResolveNetworkQuota(perDeployOverride string) string {
	enabled, _ := svc.Store.GetSetting("network.quota_enabled")
	if enabled != "true" {
		if perDeployOverride != "" {
			return perDeployOverride
		}
		return ""
	}
	if perDeployOverride != "" {
		return perDeployOverride
	}
	if defaultQuota, _ := svc.Store.GetSetting("network.default_quota"); defaultQuota != "" {
		return defaultQuota
	}
	return svc.Cfg.Container.NetworkQuota
}

// QuotaResetInterval returns the configured interval for periodic network quota
// resets. Reads "network.quota_reset_interval" setting (format: \d+(h|d)).
// Default: 30 days.
func (svc *Service) QuotaResetInterval() time.Duration {
	val, _ := svc.Store.GetSetting("network.quota_reset_interval")
	if val == "" {
		return 30 * 24 * time.Hour
	}
	re := regexp.MustCompile(`^(\d+)(h|d)$`)
	m := re.FindStringSubmatch(val)
	if m == nil {
		return 30 * 24 * time.Hour
	}
	n, _ := strconv.Atoi(m[1])
	if m[2] == "h" {
		return time.Duration(n) * time.Hour
	}
	return time.Duration(n) * 24 * time.Hour
}

// ── Deploy Code ─────────────────────────────────────────────────────

func (svc *Service) DeployCode(user *store.User, p CodeDeployParams) (*DeployResult, error) {
	if err := validateFiles(p.Files); err != nil {
		return nil, err
	}
	if err := svc.checkDeployLimit(user.ID, user.MaxDeployments); err != nil {
		return nil, err
	}

	id, name, subdomain, err := svc.generateDeployIdentity(p.Name, "app-", "")
	if err != nil {
		return nil, err
	}

	codeDir, err := svc.prepareDeployDir(id, p.Files)
	if err != nil {
		return nil, err
	}

	fw, err := detectFrameworkOrFail(codeDir)
	if err != nil {
		return nil, err
	}

	ttlHours := ParseTTL(p.TTL, user.DefaultTTLHours)
	port := resolvePort(p.Port, fw.Port)
	envVars := ensureEnv(p.Env)
	expiresAt := computeExpiry(ttlHours)
	resolvedQuota := svc.ResolveNetworkQuota(p.NetworkQuota)

	svc.Store.CreateDeployment(&store.Deployment{
		ID:           id,
		UserID:       user.ID,
		Name:         name,
		Title:        p.Title,
		Description:  p.Description,
		Subdomain:    subdomain,
		Framework:    fw.Framework,
		Status:       "building",
		TTLHours:     ttlHours,
		EnvJSON:      marshalEnv(envVars),
		ExpiresAt:    expiresAt,
		NetworkQuota: resolvedQuota,
		Memory:       p.Memory,
		CPUs:         p.CPUs,
	})

	aci, err := svc.setupAccessControl(id, codeDir, subdomain, p.ProtectMode, p.ProtectUsername, p.ProtectPassword, p.ProtectApiKey, p.ProtectUsers)
	if err != nil {
		return nil, err
	}

	svc.buildAndStart(buildStartParams{
		ID: id, UserID: user.ID, UserName: user.Name,
		CodeDir: codeDir, Subdomain: subdomain,
		Memory: p.Memory, CPUs: p.CPUs, NetworkQuota: resolvedQuota,
		LogPrefix: "deploy-code", FW: fwInfo(fw), Port: port,
		EnvVars: envVars, AC: aci,
	})

	var accessMode, apiKey string
	if aci != nil {
		accessMode = aci.Mode
		apiKey = aci.ResultKey
	}
	return &DeployResult{
		ID: id, Name: name, Title: p.Title, Description: p.Description,
		Subdomain: subdomain, Framework: fw.Framework, Language: fw.Language,
		Status: "building", URL: svc.deployURL(subdomain),
		ExpiresAt: expiresAt, AccessMode: accessMode, ApiKey: apiKey,
	}, nil
}

// ── Update Code ─────────────────────────────────────────────────────

func (svc *Service) UpdateCode(user *store.User, p CodeUpdateParams) (*UpdateResult, error) {
	if len(p.Files) == 0 {
		return nil, ErrBadRequest("No files provided.")
	}

	deploy, err := svc.Store.GetDeployment(p.DeployID)
	if err != nil || deploy == nil {
		return nil, ErrNotFound("Deployment not found.")
	}
	if deploy.UserID != user.ID && user.Role != "admin" {
		return nil, ErrForbidden("Not your deployment.")
	}
	if deploy.Status != "running" {
		return nil, ErrBadRequest(fmt.Sprintf("Cannot update deployment in '%s' state.", deploy.Status))
	}
	if deploy.Locked {
		return nil, ErrBadRequest("Deployment is locked. Unlock it first.")
	}

	codeDir := filepath.Join(svc.Cfg.DeploysDir, deploy.ID)

	// Clear existing files (keep .openberth hidden files)
	entries, _ := os.ReadDir(codeDir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".openberth") {
			os.RemoveAll(filepath.Join(codeDir, e.Name()))
		}
	}

	if err := pathTraversalGuard(codeDir, p.Files); err != nil {
		return nil, err
	}
	for relPath, content := range p.Files {
		clean := filepath.Clean(relPath)
		fullPath := filepath.Join(codeDir, clean)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		os.WriteFile(fullPath, []byte(content), 0644)
	}

	fw := framework.DetectWithOverrides(codeDir)
	if fw == nil {
		return nil, ErrBadRequest("Could not detect framework in updated code. Add a .berth.json with \"language\" and \"start\" fields.")
	}

	// Merge env: start with existing, override with any new values
	envVars := map[string]string{}
	if deploy.EnvJSON != "" && deploy.EnvJSON != "{}" {
		json.Unmarshal([]byte(deploy.EnvJSON), &envVars)
	}
	for k, v := range p.Env {
		envVars[k] = v
	}

	port := resolvePort(p.Port, fw.Port)

	// Preserve existing deployment settings unless explicitly overridden
	memory := coalesce(p.Memory, deploy.Memory)
	cpus := coalesce(p.CPUs, deploy.CPUs)
	quota := deploy.NetworkQuota
	if p.NetworkQuota != "" {
		quota = p.NetworkQuota
	}

	if b, err := json.Marshal(envVars); err == nil {
		svc.Store.UpdateDeploymentEnvJSON(deploy.ID, string(b))
	}
	svc.Store.UpdateDeploymentStatus(deploy.ID, "updating")

	svc.rebuildAndStart(deploy, user.Name, fwInfo(fw), port, envVars, memory, cpus, quota, "update-code")

	return &UpdateResult{
		ID:      deploy.ID,
		Status:  "updating",
		URL:     svc.deployURL(deploy.Subdomain),
		Message: "Code updated. Rebuilding...",
	}, nil
}
