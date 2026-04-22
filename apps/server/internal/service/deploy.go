package service

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
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
	userEnv := ensureEnv(p.Env)
	// Validate secrets exist (but don't store resolved values)
	if len(p.Secrets) > 0 {
		if _, err := svc.resolveSecrets(user.ID, p.Secrets); err != nil {
			return nil, err
		}
	}
	// Resolve secrets JIT for container env
	buildEnvVars, envVars, err := svc.mergeEnvAndSecrets(user.ID, userEnv, p.Secrets)
	if err != nil {
		return nil, err
	}
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
		EnvJSON:      marshalEnv(userEnv),
		ExpiresAt:    expiresAt,
		NetworkQuota: resolvedQuota,
		Memory:       p.Memory,
		CPUs:         p.CPUs,
	})
	svc.Store.UpdateDeploymentSecrets(id, p.Secrets)

	aci, err := svc.setupAccessControl(id, codeDir, subdomain, p.ProtectMode, p.ProtectUsername, p.ProtectPassword, p.ProtectApiKey, p.ProtectUsers)
	if err != nil {
		return nil, err
	}

	svc.buildAndStart(buildStartParams{
		ID: id, UserID: user.ID, UserName: user.Name,
		CodeDir: codeDir, Subdomain: subdomain,
		Memory: p.Memory, CPUs: p.CPUs, NetworkQuota: resolvedQuota,
		LogPrefix: "deploy-code", FW: fwInfo(fw), Port: port,
		BuildEnvVars: buildEnvVars, EnvVars: envVars, AC: aci,
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

	deploy, err := svc.updateOwnerGuard(p.DeployID, user)
	if err != nil {
		return nil, err
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

	// Update stored secrets if provided
	if len(p.Secrets) > 0 {
		svc.Store.UpdateDeploymentSecrets(deploy.ID, p.Secrets)
		deploy.SecretsJSON = marshalSecrets(p.Secrets)
	}

	return svc.detectAndRebuild(deploy, user.Name, p.Env, p.Port, p.Memory, p.CPUs, p.NetworkQuota, "update-code")
}
