package service

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/framework"
	"github.com/AmirSoleimani/openberth/apps/server/internal/runtime"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// ── Create Sandbox ──────────────────────────────────────────────────

func (svc *Service) CreateSandbox(user *store.User, p SandboxCreateParams) (*DeployResult, error) {
	if err := validateFiles(p.Files); err != nil {
		return nil, err
	}
	if err := svc.checkDeployLimit(user.ID, user.MaxDeployments); err != nil {
		return nil, err
	}

	id, name, subdomain, err := svc.generateDeployIdentity(p.Name, "sandbox-", "sandbox-")
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

	if fw.Language == "node" && fw.DevCmd == "" {
		os.RemoveAll(codeDir)
		return nil, ErrBadRequest("No dev command detected for this Node.js project. Add a \"dev\" script to package.json or use berth_deploy instead.")
	}

	ttlHours := ParseTTL(p.TTL, 4)
	port := resolvePort(p.Port, fw.Port)
	userEnv := ensureEnv(p.Env)
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
		Subdomain:    subdomain,
		Framework:    fw.Framework,
		Status:       "building",
		TTLHours:     ttlHours,
		EnvJSON:      marshalEnv(userEnv),
		ExpiresAt:    expiresAt,
		Mode:         "sandbox",
		NetworkQuota: resolvedQuota,
		Memory:       p.Memory,
	})
	svc.Store.UpdateDeploymentSecrets(id, p.Secrets)

	aci, err := svc.setupAccessControl(id, codeDir, subdomain, p.ProtectMode, p.ProtectUsername, p.ProtectPassword, p.ProtectApiKey, p.ProtectUsers)
	if err != nil {
		return nil, err
	}

	devCmd := fw.DevCmd
	if fw.Framework == "static" {
		devCmd = ""
	}

	go func() {
		startTime := time.Now()
		result, err := svc.Runtime.StartSandbox(runtime.SandboxOpts{
			ID:           id,
			UserID:       user.ID,
			CodeDir:      codeDir,
			Framework:    fw.Framework,
			Language:     fw.Language,
			DevCmd:       devCmd,
			InstallCmd:   fw.InstallCmd,
			Port:         port,
			Image:        fw.Image,
			FrameworkEnv: fw.Env,
			BuildEnv:     buildEnvVars,
			UserEnv:      envVars,
			Memory:       svc.ResolveMemory(p.Memory),
			NetworkQuota: resolvedQuota,
		})
		if err != nil {
			log.Printf("[sandbox] Failed for %s: %v", id, err)
			svc.Store.UpdateDeploymentStatus(id, "failed")
			svc.Runtime.Destroy(id)
			return
		}

		svc.Proxy.AddRoute(subdomain, result.Endpoint.Port, proxyACFromInfo(aci))
		svc.Store.UpdateDeploymentRunning(id, result.InstanceID, result.Endpoint.Port)

		elapsed := time.Since(startTime).Seconds()
		log.Printf("[sandbox] %s | %s/%s | port %d | %.1fs | user=%s",
			subdomain, fw.Language, fw.Framework, result.Endpoint.Port, elapsed, user.Name)
	}()

	var accessMode, apiKey string
	if aci != nil {
		accessMode = aci.Mode
		apiKey = aci.ResultKey
	}
	return &DeployResult{
		ID: id, Name: name, Subdomain: subdomain,
		Framework: fw.Framework, Language: fw.Language,
		Status: "building", URL: svc.deployURL(subdomain),
		ExpiresAt: expiresAt, AccessMode: accessMode, ApiKey: apiKey,
	}, nil
}

// ── Sandbox Push ────────────────────────────────────────────────────

func (svc *Service) SandboxPush(user *store.User, p PushParams) (*PushResult, error) {
	deploy, err := svc.sandboxOwnerGuard(p.SandboxID, user)
	if err != nil {
		return nil, err
	}
	if len(p.Changes) == 0 {
		return nil, ErrBadRequest("No changes provided.")
	}

	codeDir := filepath.Join(svc.Cfg.DeploysDir, deploy.ID)
	updated := 0
	deleted := 0
	depFileChanged := false
	depFiles := map[string]bool{
		"package.json": true, "package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
		"requirements.txt": true, "pyproject.toml": true, "Pipfile": true, "Pipfile.lock": true,
		"go.mod": true, "go.sum": true,
	}

	for _, change := range p.Changes {
		clean := filepath.Clean(change.Path)
		fullPath := filepath.Join(codeDir, clean)
		if !strings.HasPrefix(fullPath, codeDir+string(filepath.Separator)) && fullPath != codeDir {
			return nil, ErrBadRequest(fmt.Sprintf("Invalid file path: %s", change.Path))
		}

		switch change.Op {
		case "write":
			os.MkdirAll(filepath.Dir(fullPath), 0755)
			if err := os.WriteFile(fullPath, []byte(change.Content), 0644); err != nil {
				return nil, ErrInternal("Failed to write file: " + err.Error())
			}
			updated++
		case "delete":
			os.Remove(fullPath)
			deleted++
		default:
			return nil, ErrBadRequest(fmt.Sprintf("Invalid op '%s'. Use 'write' or 'delete'.", change.Op))
		}

		if depFiles[filepath.Base(clean)] {
			depFileChanged = true
		}
	}

	// Touch changed files inside the container to trigger inotify events.
	var touchPaths []string
	for _, change := range p.Changes {
		if change.Op == "write" {
			touchPaths = append(touchPaths, shellQuote("/app/"+filepath.Clean(change.Path)))
		}
	}
	if len(touchPaths) > 0 {
		touchCmd := "touch " + strings.Join(touchPaths, " ")
		svc.Runtime.Exec(deploy.ID, touchCmd, 5*time.Second)
	}

	// If dependency file changed, run install synchronously
	var depsInstalled bool
	var installOutput string
	if depFileChanged {
		installCmd := detectInstallCmd(codeDir)
		log.Printf("[sandbox-push] Dependency file changed for %s, running %s", deploy.ID, installCmd)
		res, err := svc.Runtime.Exec(deploy.ID, installCmd, 2*time.Minute)
		installOutput = res.Output
		if err != nil {
			log.Printf("[sandbox-push] install failed for %s: %v", deploy.ID, err)
			installOutput = res.Output + "\nInstall failed: " + err.Error()
		} else {
			depsInstalled = true
		}
	}

	log.Printf("[sandbox-push] %s | %d written, %d deleted, deps=%v | user=%s",
		deploy.Subdomain, updated, deleted, depsInstalled, user.Name)

	return &PushResult{
		ID:            deploy.ID,
		Updated:       updated,
		Deleted:       deleted,
		DepsInstalled: depsInstalled,
		InstallOutput: installOutput,
	}, nil
}

// ── Sandbox Exec ────────────────────────────────────────────────────

func (svc *Service) SandboxExec(user *store.User, p ExecParams) (*ExecResult, error) {
	deploy, err := svc.sandboxOwnerGuard(p.SandboxID, user)
	if err != nil {
		return nil, err
	}
	if p.Command == "" {
		return nil, ErrBadRequest("No command provided.")
	}

	timeout := 30
	if p.Timeout > 0 {
		timeout = p.Timeout
	}
	if timeout > 300 {
		timeout = 300
	}

	res, _ := svc.Runtime.Exec(deploy.ID, p.Command, time.Duration(timeout)*time.Second)

	return &ExecResult{
		ID:       deploy.ID,
		Output:   res.Output,
		ExitCode: res.ExitCode,
	}, nil
}

// ── Sandbox Install ─────────────────────────────────────────────────

func (svc *Service) SandboxInstall(user *store.User, p InstallParams) (*InstallResult, error) {
	deploy, err := svc.sandboxOwnerGuard(p.SandboxID, user)
	if err != nil {
		return nil, err
	}
	if len(p.Packages) == 0 {
		return nil, ErrBadRequest("No packages specified.")
	}

	for _, pkg := range p.Packages {
		if !validPkgName.MatchString(pkg) {
			return nil, ErrBadRequest(fmt.Sprintf("Invalid package name: %s", pkg))
		}
	}

	pkgList := strings.Join(p.Packages, " ")
	codeDir := filepath.Join(svc.Cfg.DeploysDir, deploy.ID)
	lang := detectProjectLang(codeDir)

	var cmd string
	var action string
	if p.Uninstall {
		action = "uninstall"
		switch lang {
		case "python":
			cmd = "cd /app && /app/venv/bin/pip uninstall -y " + pkgList + " 2>&1"
		case "go":
			cmd = "cd /app && go mod tidy 2>&1"
		default:
			cmd = "cd /app && npm uninstall " + pkgList + " 2>&1"
		}
	} else {
		action = "install"
		switch lang {
		case "python":
			cmd = "cd /app && /app/venv/bin/pip install " + pkgList + " 2>&1"
		case "go":
			cmd = "cd /app && go get " + pkgList + " 2>&1"
		default:
			cmd = "cd /app && npm install " + pkgList + " 2>&1"
		}
	}

	res, err := svc.Runtime.Exec(deploy.ID, cmd, 2*time.Minute)
	if err != nil {
		return &InstallResult{
			ID:      deploy.ID,
			Output:  res.Output,
			Message: fmt.Sprintf("Package %s failed: %s", action, err.Error()),
		}, nil
	}

	log.Printf("[sandbox-install] %s | %s %s | user=%s", deploy.Subdomain, action, pkgList, user.Name)

	return &InstallResult{
		ID:      deploy.ID,
		Output:  res.Output,
		Message: fmt.Sprintf("Successfully %sed: %s", action, pkgList),
	}, nil
}

// ── Promote Sandbox ─────────────────────────────────────────────────

func (svc *Service) PromoteSandbox(user *store.User, p PromoteParams) (*DeployResult, error) {
	deploy, err := svc.Store.GetDeployment(p.SandboxID)
	if err != nil || deploy == nil {
		return nil, ErrNotFound("Sandbox not found.")
	}
	if !CanMutateDeploy(deploy, user) {
		return nil, ErrForbidden("Not your sandbox.")
	}
	if deploy.Mode != "sandbox" {
		return nil, ErrBadRequest("Not a sandbox. Only sandboxes can be promoted.")
	}
	if deploy.Status != "running" {
		return nil, ErrBadRequest(fmt.Sprintf("Sandbox is '%s', not running.", deploy.Status))
	}
	if deploy.Locked {
		return nil, ErrBadRequest("Deployment is locked. Unlock it first.")
	}

	codeDir := filepath.Join(svc.Cfg.DeploysDir, deploy.ID)

	fw := framework.DetectWithOverrides(codeDir)
	if fw == nil {
		return nil, ErrInternal("Could not detect framework from sandbox code. Add a .berth.json with \"language\" and \"start\" fields.")
	}

	if fw.Framework != "static" && fw.BuildCmd == "" && fw.StartCmd == "" {
		return nil, ErrBadRequest("Framework has no build or start command. Cannot promote.")
	}

	oldSubdomain := deploy.Subdomain
	newSubdomain := strings.TrimPrefix(oldSubdomain, "sandbox-")

	if newSubdomain != oldSubdomain {
		if existing, _ := svc.Store.GetDeploymentBySubdomain(newSubdomain); existing != nil {
			newSubdomain = oldSubdomain
		}
	}

	svc.Runtime.Destroy(deploy.ID)
	svc.Proxy.RemoveRoute(oldSubdomain)

	svc.Store.UpdateDeploymentStatus(deploy.ID, "building")
	svc.Store.UpdateDeploymentMode(deploy.ID, "deploy")
	if newSubdomain != oldSubdomain {
		svc.Store.UpdateDeploymentSubdomain(deploy.ID, newSubdomain)
	}

	ttlHours := ParseTTL(p.TTL, user.DefaultTTLHours)
	expiresAt := computeExpiry(ttlHours)
	svc.Store.UpdateDeploymentTTL(deploy.ID, ttlHours, expiresAt)

	// Merge secrets: start with existing sandbox secrets, override with promote params
	secretNames := parseSecretsJSON(deploy.SecretsJSON)
	if len(p.Secrets) > 0 {
		secretNames = p.Secrets
		svc.Store.UpdateDeploymentSecrets(deploy.ID, p.Secrets)
	}
	// Store only user-supplied env (merge existing + new)
	userEnv := map[string]string{}
	if deploy.EnvJSON != "" && deploy.EnvJSON != "{}" {
		json.Unmarshal([]byte(deploy.EnvJSON), &userEnv)
	}
	for k, v := range ensureEnv(p.Env) {
		userEnv[k] = v
	}
	svc.Store.UpdateDeploymentEnvJSON(deploy.ID, marshalEnv(userEnv))
	// Resolve secrets JIT for container
	buildEnvVars, envVars, err := svc.mergeEnvAndSecrets(user.ID, userEnv, secretNames)
	if err != nil {
		return nil, err
	}
	resolvedQuota := svc.ResolveNetworkQuota(p.NetworkQuota)
	svc.Store.UpdateDeploymentNetworkQuota(deploy.ID, resolvedQuota)

	// Preserve access control from the sandbox
	var aci *accessControlInfo
	if deploy.AccessMode != "" && deploy.AccessMode != "public" {
		aci = &accessControlInfo{
			Mode:      deploy.AccessMode,
			Username:  deploy.AccessUser,
			Hash:      deploy.AccessHash,
			Subdomain: newSubdomain,
		}
	}

	svc.buildAndStart(buildStartParams{
		ID: deploy.ID, UserID: user.ID, UserName: user.Name,
		CodeDir: codeDir, Subdomain: newSubdomain,
		Memory: p.Memory, CPUs: p.CPUs, NetworkQuota: resolvedQuota,
		LogPrefix: "promote", FW: fwInfo(fw), Port: fw.Port,
		BuildEnvVars: buildEnvVars, EnvVars: envVars, AC: aci,
	})

	return &DeployResult{
		ID: deploy.ID, Name: deploy.Name, Subdomain: newSubdomain,
		Framework: fw.Framework, Status: "building",
		URL: svc.deployURL(newSubdomain),
	}, nil
}
