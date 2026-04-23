package service

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// ── Result types ────────────────────────────────────────────────────

// DeployInfo is the response shape for both list and single-deployment reads.
// ContainerStatus and QuotaRemaining are populated only by the single-get path
// (GetDeployment); list responses leave them zero so the list stays cheap.
type DeployInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
	Subdomain       string `json:"subdomain"`
	Framework       string `json:"framework"`
	Status          string `json:"status"`
	ContainerStatus string `json:"containerStatus,omitempty"`
	URL             string `json:"url"`
	Port            int    `json:"port,omitempty"`
	CreatedAt       string `json:"createdAt"`
	ExpiresAt       string `json:"expiresAt"`
	TTLHours        int    `json:"ttlHours,omitempty"`
	OwnerID         string `json:"ownerId"`
	OwnerName       string `json:"ownerName,omitempty"`
	AccessMode      string `json:"accessMode"`
	AccessUser      string `json:"accessUser,omitempty"`
	AccessUsers     string `json:"accessUsers,omitempty"`
	Mode            string `json:"mode"`
	NetworkQuota    string `json:"networkQuota,omitempty"`
	QuotaRemaining  *int64 `json:"quotaRemaining,omitempty"`
	Locked          bool   `json:"locked"`
}

type LogsResult struct {
	ID   string `json:"id"`
	Logs string `json:"logs"`
}

type SourceResult struct {
	Files map[string]string `json:"files"`
}

// ── Get Deployment ──────────────────────────────────────────────────

func (svc *Service) GetDeployment(user *store.User, id string) (*DeployInfo, error) {
	deploy, _ := svc.Store.GetDeployment(id)
	if deploy == nil {
		return nil, ErrNotFound("Not found.")
	}
	if !CanMutateDeploy(deploy, user) {
		return nil, ErrForbidden("Not your deployment.")
	}

	info := &DeployInfo{
		ID:              deploy.ID,
		Name:            deploy.Name,
		Title:           deploy.Title,
		Description:     deploy.Description,
		Subdomain:       deploy.Subdomain,
		Framework:       deploy.Framework,
		Status:          deploy.Status,
		ContainerStatus: string(svc.Runtime.Status(deploy.ID)),
		URL:             svc.deployURL(deploy.Subdomain),
		Port:            deploy.Port,
		CreatedAt:       deploy.CreatedAt,
		ExpiresAt:       deploy.ExpiresAt,
		TTLHours:        deploy.TTLHours,
		OwnerID:         deploy.UserID,
		OwnerName:       deploy.OwnerName,
		AccessMode:      deploy.AccessMode,
		AccessUser:      deploy.AccessUser,
		AccessUsers:     deploy.AccessUsers,
		Mode:            deploy.Mode,
		NetworkQuota:    deploy.NetworkQuota,
		Locked:          deploy.Locked,
	}
	if deploy.NetworkQuota != "" && deploy.Status == "running" {
		quotaBytes, err := ParseSize(deploy.NetworkQuota)
		if err == nil && quotaBytes > 0 {
			period := CurrentPeriodStart(svc.QuotaResetInterval())
			used, _ := svc.Store.GetBandwidth(deploy.ID, period)
			remaining := quotaBytes - used
			if remaining < 0 {
				remaining = 0
			}
			info.QuotaRemaining = &remaining
		}
	}
	return info, nil
}

// ── Get Logs ────────────────────────────────────────────────────────

func (svc *Service) GetLogs(user *store.User, id string, tail int) (*LogsResult, error) {
	deploy, _ := svc.Store.GetDeployment(id)
	if deploy == nil {
		return nil, ErrNotFound("Not found.")
	}
	if !CanMutateDeploy(deploy, user) {
		return nil, ErrForbidden("Not your deployment.")
	}

	return &LogsResult{
		ID:   deploy.ID,
		Logs: svc.Runtime.Logs(deploy.ID, tail),
	}, nil
}

// ── Get Log Stream ─────────────────────────────────────────────────

// GetLogStream returns a streaming reader for deployment logs.
// The caller must close the reader when done.
func (svc *Service) GetLogStream(user *store.User, id string, tail int) (io.ReadCloser, error) {
	deploy, _ := svc.Store.GetDeployment(id)
	if deploy == nil {
		return nil, ErrNotFound("Not found.")
	}
	if !CanMutateDeploy(deploy, user) {
		return nil, ErrForbidden("Not your deployment.")
	}
	return svc.Runtime.LogStream(deploy.ID, tail)
}

// ── List Deployments ────────────────────────────────────────────────

// ListDeployments returns deployments visible to the caller.
// Read visibility is open: any authenticated user can list every deployment.
// ownerFilter: "" returns all deployments; any other value filters by user ID.
// ContainerStatus and QuotaRemaining are omitted from list rows to keep the
// response cheap at scale — callers fetch them via GetDeployment when needed.
func (svc *Service) ListDeployments(user *store.User, ownerFilter string) ([]DeployInfo, error) {
	deploys, _ := svc.Store.ListDeployments(ownerFilter)
	result := make([]DeployInfo, 0, len(deploys))
	for _, d := range deploys {
		result = append(result, DeployInfo{
			ID:           d.ID,
			Name:         d.Name,
			Title:        d.Title,
			Description:  d.Description,
			Subdomain:    d.Subdomain,
			Framework:    d.Framework,
			Status:       d.Status,
			URL:          svc.deployURL(d.Subdomain),
			CreatedAt:    d.CreatedAt,
			ExpiresAt:    d.ExpiresAt,
			TTLHours:     d.TTLHours,
			OwnerID:      d.UserID,
			OwnerName:    d.OwnerName,
			AccessMode:   d.AccessMode,
			AccessUser:   d.AccessUser,
			AccessUsers:  d.AccessUsers,
			Mode:         d.Mode,
			NetworkQuota: d.NetworkQuota,
			Locked:       d.Locked,
		})
	}
	return result, nil
}

// ── Get Source ───────────────────────────────────────────────────────

// sourceAllowBasenames is the set of filenames that always return their
// real contents, even if they would otherwise match sourceDenyBasenames
// or sourceDenyGlobs. Sample / example / template env files are
// intentionally readable — they're shipped as documentation.
var sourceAllowBasenames = map[string]bool{
	".env.example":    true,
	".env.sample":     true,
	".env.template":   true,
	".env.dist":       true,
	"env.example":     true,
	"env.sample":      true,
	"env.template":    true,
	"env.dist":        true,
}

// sourceDenyBasenames matches exact basenames of files whose content must
// not be returned via GetSource — they commonly hold secrets, credentials,
// or private keys.
var sourceDenyBasenames = map[string]bool{
	".env":                 true,
	".env.local":           true,
	".env.development":     true,
	".env.production":      true,
	".env.staging":         true,
	".env.test":            true,
	".env.development.local": true,
	".env.production.local":  true,
	".env.test.local":        true,
	"secrets.json":         true,
	"secrets.yaml":         true,
	"secrets.yml":          true,
	"credentials":          true,
	"kubeconfig":           true,
	"terraform.tfstate":    true,
	"terraform.tfstate.backup": true,
	"gcloud-service-key.json":  true,
	".netrc":               true,
	".pgpass":              true,
	"id_rsa":               true,
	"id_dsa":               true,
	"id_ecdsa":             true,
	"id_ed25519":           true,
}

// sourceDenyGlobs are filepath.Match patterns applied to the basename when
// no exact match is found. Captures extensions (*.pem, *.key, *.kubeconfig)
// and prefixed families (firebase-admin*.json, service-account*.json).
var sourceDenyGlobs = []string{
	"*.pem", "*.key", "*.crt", "*.p12", "*.pfx",
	"*.kubeconfig",
	"firebase-admin*.json",
	"service-account*.json",
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*",
}

// sourceDenyPathPrefixes match against the path relative to the deploy
// root — these are directory-qualified secrets (.aws/credentials etc.).
var sourceDenyPathPrefixes = []string{
	".aws/credentials",
	".aws/config",
	".kube/config",
	".ssh/",
	".gnupg/",
}

// redactedSourceFile is the placeholder content returned in place of a
// file whose basename/path matches a deny pattern. Kept short and
// actionable so client tools / UIs surface it clearly.
func redactedSourceFile(rel, reason string) string {
	return "[redacted by openberth: " + rel + " matches sensitive-file pattern '" + reason + "']"
}

// sourceMatchDeny returns the matched pattern name when rel is sensitive,
// or "" when it's safe to include. The allow list wins over the deny list.
func sourceMatchDeny(rel string) string {
	base := filepath.Base(rel)
	if sourceAllowBasenames[base] {
		return ""
	}
	for _, p := range sourceDenyPathPrefixes {
		if strings.HasPrefix(rel, p) {
			return p
		}
	}
	if sourceDenyBasenames[base] {
		return base
	}
	for _, glob := range sourceDenyGlobs {
		if ok, _ := filepath.Match(glob, base); ok {
			return glob
		}
	}
	return ""
}

func (svc *Service) GetSource(user *store.User, id string) (*SourceResult, error) {
	deploy, _ := svc.Store.GetDeployment(id)
	if deploy == nil {
		return nil, ErrNotFound("Not found.")
	}
	if !CanMutateDeploy(deploy, user) {
		return nil, ErrForbidden("Not your deployment.")
	}

	srcDir := filepath.Join(svc.Cfg.DeploysDir, deploy.ID)
	if _, err := os.Stat(srcDir); err != nil {
		return nil, ErrNotFound("No source code available.")
	}

	files := map[string]string{}
	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, path)
		if strings.HasPrefix(rel, ".openberth") {
			return nil
		}

		// Redact sensitive files before reading their content so an oversized
		// secret file (e.g. a multi-MB PEM) doesn't even get buffered.
		if match := sourceMatchDeny(rel); match != "" {
			files[rel] = redactedSourceFile(rel, match)
			return nil
		}

		if info.Size() > 5*1024*1024 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		check := data
		if len(check) > 512 {
			check = check[:512]
		}
		for _, b := range check {
			if b == 0 {
				return nil
			}
		}
		files[rel] = string(data)
		return nil
	})

	return &SourceResult{Files: files}, nil
}
