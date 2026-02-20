package service

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/openberth/openberth/apps/server/internal/store"
)

// ── Result types ────────────────────────────────────────────────────

type DeployInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
	Subdomain       string `json:"subdomain"`
	Framework       string `json:"framework"`
	Status          string `json:"status"`
	ContainerStatus string `json:"containerStatus"`
	URL             string `json:"url"`
	Port            int    `json:"port"`
	CreatedAt       string `json:"createdAt"`
	ExpiresAt       string `json:"expiresAt"`
	AccessMode      string `json:"accessMode"`
	AccessUser      string `json:"accessUser,omitempty"`
	Mode            string `json:"mode"`
	NetworkQuota    string `json:"networkQuota,omitempty"`
	QuotaRemaining  *int64 `json:"quotaRemaining,omitempty"`
	Locked          bool   `json:"locked"`
}

type LogsResult struct {
	ID   string `json:"id"`
	Logs string `json:"logs"`
}

type GalleryItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Subdomain    string `json:"subdomain"`
	Framework    string `json:"framework"`
	URL          string `json:"url"`
	CreatedAt    string `json:"createdAt"`
	OwnerName    string `json:"ownerName"`
	UserID       string `json:"userId"`
	AccessMode   string `json:"accessMode"`
	AccessUser   string `json:"accessUser,omitempty"`
	AccessUsers  string `json:"accessUsers,omitempty"`
	TTLHours     int    `json:"ttlHours"`
	ExpiresAt    string `json:"expiresAt"`
	Mode         string `json:"mode"`
	NetworkQuota string `json:"networkQuota,omitempty"`
	Locked       bool   `json:"locked"`
	Status       string `json:"status"`
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
	if deploy.UserID != user.ID && user.Role != "admin" {
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
		ContainerStatus: svc.Container.Status(deploy.ID),
		URL:             svc.deployURL(deploy.Subdomain),
		Port:            deploy.Port,
		CreatedAt:       deploy.CreatedAt,
		ExpiresAt:       deploy.ExpiresAt,
		AccessMode:      deploy.AccessMode,
		AccessUser:      deploy.AccessUser,
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
	if deploy.UserID != user.ID && user.Role != "admin" {
		return nil, ErrForbidden("Not your deployment.")
	}

	return &LogsResult{
		ID:   deploy.ID,
		Logs: svc.Container.Logs(deploy.ID, tail),
	}, nil
}

// ── List Deployments ────────────────────────────────────────────────

func (svc *Service) ListDeployments(user *store.User) ([]DeployInfo, error) {
	userID := user.ID
	if user.Role == "admin" {
		userID = ""
	}

	deploys, _ := svc.Store.ListDeployments(userID)
	result := make([]DeployInfo, 0, len(deploys))
	for _, d := range deploys {
		cs := d.Status
		if d.Status == "running" {
			cs = svc.Container.Status(d.ID)
		}
		result = append(result, DeployInfo{
			ID:              d.ID,
			Name:            d.Name,
			Title:           d.Title,
			Description:     d.Description,
			Subdomain:       d.Subdomain,
			Framework:       d.Framework,
			Status:          d.Status,
			ContainerStatus: cs,
			URL:             svc.deployURL(d.Subdomain),
			CreatedAt:       d.CreatedAt,
			ExpiresAt:       d.ExpiresAt,
			AccessMode:      d.AccessMode,
			Mode:            d.Mode,
		})
	}

	return result, nil
}

// ── List Gallery ────────────────────────────────────────────────────

func (svc *Service) ListGallery() ([]GalleryItem, error) {
	deploys, err := svc.Store.ListPublicDeployments()
	if err != nil {
		return nil, ErrInternal("Failed to list deployments.")
	}

	items := make([]GalleryItem, 0, len(deploys))
	for _, d := range deploys {
		items = append(items, GalleryItem{
			ID:           d.ID,
			Name:         d.Name,
			Title:        d.Title,
			Description:  d.Description,
			Subdomain:    d.Subdomain,
			Framework:    d.Framework,
			URL:          svc.deployURL(d.Subdomain),
			CreatedAt:    d.CreatedAt,
			OwnerName:    d.OwnerName,
			UserID:       d.UserID,
			AccessMode:   d.AccessMode,
			AccessUser:   d.AccessUser,
			AccessUsers:  d.AccessUsers,
			TTLHours:     d.TTLHours,
			ExpiresAt:    d.ExpiresAt,
			Mode:         d.Mode,
			NetworkQuota: d.NetworkQuota,
			Locked:       d.Locked,
			Status:       d.Status,
		})
	}
	return items, nil
}

// ── Get Source ───────────────────────────────────────────────────────

func (svc *Service) GetSource(user *store.User, id string) (*SourceResult, error) {
	deploy, _ := svc.Store.GetDeployment(id)
	if deploy == nil {
		return nil, ErrNotFound("Not found.")
	}
	if deploy.UserID != user.ID && user.Role != "admin" {
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
