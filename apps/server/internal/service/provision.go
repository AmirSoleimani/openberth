package service

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/xid"

	"github.com/openberth/openberth/apps/server/internal/container"
	"github.com/openberth/openberth/apps/server/internal/framework"
	"github.com/openberth/openberth/apps/server/internal/proxy"
	"github.com/openberth/openberth/apps/server/internal/store"
)

// ── Validation helpers ──────────────────────────────────────────────

// validateFiles checks that a file map is non-empty, within limits, and total size is acceptable.
func validateFiles(files map[string]string) error {
	if len(files) == 0 {
		return ErrBadRequest("No files provided. Include at least one file in the 'files' object.")
	}
	if len(files) > 100 {
		return ErrBadRequest(fmt.Sprintf("Too many files (%d). Code deploy supports up to 100 files.", len(files)))
	}
	totalSize := 0
	for _, content := range files {
		totalSize += len(content)
	}
	if totalSize > 10*1024*1024 {
		return ErrBadRequest("Total file size exceeds 10MB. Use the tarball upload endpoint for larger projects.")
	}
	return nil
}

// checkDeployLimit verifies the user hasn't exceeded their deployment limit.
func (svc *Service) checkDeployLimit(userID string, max int) error {
	count, _ := svc.Store.CountActiveDeployments(userID)
	if count >= max {
		return ErrRateLimit(fmt.Sprintf("Deployment limit reached (%d). Destroy some first.", max))
	}
	return nil
}

// pathTraversalGuard checks that all file paths are safe relative to baseDir.
func pathTraversalGuard(baseDir string, files map[string]string) error {
	for relPath := range files {
		clean := filepath.Clean(relPath)
		fullPath := filepath.Join(baseDir, clean)
		if !strings.HasPrefix(fullPath, baseDir+string(filepath.Separator)) && fullPath != baseDir {
			return ErrBadRequest(fmt.Sprintf("Invalid file path: %s", relPath))
		}
	}
	return nil
}

// ── Identity helpers ────────────────────────────────────────────────

// generateDeployIdentity creates a unique ID, sanitized name, and subdomain.
// nameDefault is the prefix for auto-generated names (e.g. "app-").
// subdomainPrefix is prepended to the subdomain (e.g. "sandbox-" for sandboxes, "" for deploys).
func (svc *Service) generateDeployIdentity(name, nameDefault, subdomainPrefix string) (id, slug, subdomain string, err error) {
	id = xid.New().String()
	slug = SanitizeName(name)
	if slug == "" {
		slug = nameDefault + id[:6]
		subdomain = subdomainPrefix + slug + "-" + id[:4]
	} else {
		subdomain = subdomainPrefix + slug
	}

	if existing, _ := svc.Store.GetDeploymentBySubdomain(subdomain); existing != nil {
		return "", "", "", ErrConflict("Subdomain collision, try a different name.")
	}
	return id, slug, subdomain, nil
}

// prepareDeployDir creates the deploy directory and writes files to it.
func (svc *Service) prepareDeployDir(id string, files map[string]string) (string, error) {
	codeDir := filepath.Join(svc.Cfg.DeploysDir, id)
	os.MkdirAll(codeDir, 0755)

	if err := pathTraversalGuard(codeDir, files); err != nil {
		os.RemoveAll(codeDir)
		return "", err
	}

	for relPath, content := range files {
		clean := filepath.Clean(relPath)
		fullPath := filepath.Join(codeDir, clean)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			os.RemoveAll(codeDir)
			return "", ErrInternal("Failed to write file: " + err.Error())
		}
	}
	return codeDir, nil
}

// ── Framework/TTL/Env helpers ───────────────────────────────────────

// detectFrameworkOrFail detects the framework and returns an error if detection fails.
// On failure it cleans up codeDir.
func detectFrameworkOrFail(codeDir string) (*framework.FrameworkInfo, error) {
	fw := framework.DetectFramework(codeDir)
	if fw == nil {
		os.RemoveAll(codeDir)
		return nil, ErrBadRequest("Could not detect project type. Include package.json (Node), go.mod (Go), requirements.txt (Python), or index.html (static).")
	}
	return fw, nil
}

// resolvePort returns the user-provided port if valid, otherwise the framework default.
func resolvePort(userPort, fwPort int) int {
	if userPort > 0 && userPort < 65536 {
		return userPort
	}
	return fwPort
}

// computeExpiry calculates the expiry timestamp given TTL hours.
func computeExpiry(ttlHours int) string {
	if ttlHours > 0 {
		return time.Now().Add(time.Duration(ttlHours) * time.Hour).UTC().Format("2006-01-02 15:04:05")
	}
	return ""
}

// marshalEnv serializes env vars to JSON. Returns "{}" for empty/nil maps.
func marshalEnv(env map[string]string) string {
	if len(env) == 0 {
		return "{}"
	}
	if b, err := json.Marshal(env); err == nil {
		return string(b)
	}
	return "{}"
}

// ensureEnv returns env if non-nil, otherwise an empty map.
func ensureEnv(env map[string]string) map[string]string {
	if env == nil {
		return map[string]string{}
	}
	return env
}

// ── Access control helpers ──────────────────────────────────────────

// setupAccessControl processes protect parameters at deploy time.
// Returns the computed access control info and any error. On error, cleans up codeDir and DB record.
func (svc *Service) setupAccessControl(id, codeDir, subdomain, mode, username, password, apiKey, users string) (*accessControlInfo, error) {
	if mode == "" || mode == "public" {
		return nil, nil
	}
	accessUser, accessHash, accessUsers, resultKey, acErr := ComputeAccessControl(mode, username, password, apiKey, users)
	if acErr != nil {
		os.RemoveAll(codeDir)
		svc.Store.DeleteDeployment(id)
		return nil, acErr
	}
	svc.Store.UpdateDeploymentAccess(id, mode, accessUser, accessHash, accessUsers)
	return &accessControlInfo{
		Mode:      mode,
		Username:  accessUser,
		Hash:      accessHash,
		Subdomain: subdomain,
		ResultKey: resultKey,
	}, nil
}

// proxyACFromInfo converts accessControlInfo to a proxy.AccessControl pointer.
func proxyACFromInfo(aci *accessControlInfo) *proxy.AccessControl {
	if aci == nil {
		return nil
	}
	return &proxy.AccessControl{
		Mode:      aci.Mode,
		Username:  aci.Username,
		Hash:      aci.Hash,
		Subdomain: aci.Subdomain,
	}
}

// ── Async build helper ──────────────────────────────────────────────

// buildAndStart runs the container build and start in a goroutine.
// Used by DeployCode, DeployTarball, and CreateSandbox.
func (svc *Service) buildAndStart(p buildStartParams) {
	go func() {
		startTime := time.Now()
		result, err := svc.Container.Create(container.CreateOpts{
			ID:           p.ID,
			UserID:       p.UserID,
			CodeDir:      p.CodeDir,
			Framework:    p.FW.Framework,
			Language:     p.FW.Language,
			Port:         p.Port,
			Image:        p.FW.Image,
			RunImage:     p.FW.RunImage,
			BuildCmd:     p.FW.BuildCmd,
			StartCmd:     p.FW.StartCmd,
			CacheDir:     p.FW.CacheDir,
			FrameworkEnv: p.FW.Env,
			UserEnv:      p.EnvVars,
			Memory:       p.Memory,
			CPUs:         p.CPUs,
			NetworkQuota: p.NetworkQuota,
		})
		if err != nil {
			log.Printf("[%s] Build failed for %s: %v", p.LogPrefix, p.ID, err)
			svc.Store.UpdateDeploymentStatus(p.ID, "failed")
			svc.Container.Destroy(p.ID)
			return
		}
		svc.Proxy.AddRoute(p.Subdomain, result.HostPort, proxyACFromInfo(p.AC))
		svc.Store.UpdateDeploymentRunning(p.ID, result.ContainerID, result.HostPort)
		elapsed := time.Since(startTime).Seconds()
		log.Printf("[%s] %s | %s/%s | port %d | gVisor=%v | %.1fs | user=%s",
			p.LogPrefix, p.Subdomain, p.FW.Language, p.FW.Framework, result.HostPort, result.GVisor, elapsed, p.UserName)
	}()
}

// rebuildAndStart runs the container rebuild in a goroutine.
// Used by UpdateCode and UpdateTarball.
func (svc *Service) rebuildAndStart(deploy *store.Deployment, userName string, fw *frameworkInfo, port int, envVars map[string]string, memory, cpus, networkQuota, logPrefix string) {
	codeDir := filepath.Join(svc.Cfg.DeploysDir, deploy.ID)
	go func() {
		result, err := svc.Container.Rebuild(container.CreateOpts{
			ID:           deploy.ID,
			UserID:       deploy.UserID,
			CodeDir:      codeDir,
			Framework:    fw.Framework,
			Language:     fw.Language,
			Port:         port,
			Image:        fw.Image,
			RunImage:     fw.RunImage,
			BuildCmd:     fw.BuildCmd,
			StartCmd:     fw.StartCmd,
			CacheDir:     fw.CacheDir,
			FrameworkEnv: fw.Env,
			UserEnv:      envVars,
			Memory:       memory,
			CPUs:         cpus,
			NetworkQuota: networkQuota,
		})
		if err != nil {
			log.Printf("[%s] Rebuild failed for %s: %v", logPrefix, deploy.ID, err)
			svc.Store.UpdateDeploymentStatus(deploy.ID, "failed")
			return
		}
		svc.Store.UpdateDeploymentRunning(deploy.ID, result.ContainerID, result.HostPort)
		svc.Store.UpdateDeploymentNetworkQuota(deploy.ID, networkQuota)
		svc.Proxy.AddRoute(deploy.Subdomain, result.HostPort, AccessControlFromDeployment(deploy))
		log.Printf("[%s] %s rebuilt | user=%s", logPrefix, deploy.Subdomain, userName)
	}()
}

// ── Sandbox owner guard ─────────────────────────────────────────────

// sandboxOwnerGuard loads a sandbox deployment and verifies ownership and status.
func (svc *Service) sandboxOwnerGuard(sandboxID string, user *store.User) (*store.Deployment, error) {
	deploy, err := svc.Store.GetDeployment(sandboxID)
	if err != nil || deploy == nil {
		return nil, ErrNotFound("Sandbox not found.")
	}
	if deploy.UserID != user.ID && user.Role != "admin" {
		return nil, ErrForbidden("Not your sandbox.")
	}
	if deploy.Mode != "sandbox" {
		return nil, ErrBadRequest("Not a sandbox. Use berth_update for regular deployments.")
	}
	if deploy.Status != "running" {
		return nil, ErrBadRequest(fmt.Sprintf("Sandbox is '%s', not running.", deploy.Status))
	}
	if deploy.Locked {
		return nil, ErrBadRequest("Deployment is locked. Unlock it first.")
	}
	return deploy, nil
}

// ── Framework info conversion ───────────────────────────────────────

// fwInfo converts a framework.FrameworkInfo to our internal frameworkInfo.
func fwInfo(fw *framework.FrameworkInfo) *frameworkInfo {
	return &frameworkInfo{
		Framework: fw.Framework,
		Language:  fw.Language,
		Image:     fw.Image,
		RunImage:  fw.RunImage,
		BuildCmd:  fw.BuildCmd,
		StartCmd:  fw.StartCmd,
		CacheDir:  fw.CacheDir,
		Env:       fw.Env,
	}
}

// deployURL builds the full URL for a subdomain.
func (svc *Service) deployURL(subdomain string) string {
	scheme := "https"
	if svc.Cfg.Insecure {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s.%s", scheme, subdomain, svc.Cfg.Domain)
}
