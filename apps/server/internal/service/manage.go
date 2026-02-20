package service

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/openberth/openberth/apps/server/internal/proxy"
	"github.com/openberth/openberth/apps/server/internal/store"
)

// ── Parameter/Result types ──────────────────────────────────────────

type UpdateMetaParams struct {
	DeployID     string
	Title        *string
	Description  *string
	TTL          *string
	NetworkQuota *string
}

type UpdateMetaResult struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	TTLHours     int    `json:"ttlHours"`
	ExpiresAt    string `json:"expiresAt"`
	NetworkQuota string `json:"networkQuota"`
	Message      string `json:"message"`
}

type ProtectParams struct {
	DeployID string
	Mode     string // "public", "basic_auth", "api_key", "user"
	Username string
	Password string // plaintext, will be bcrypt-hashed
	ApiKey   string // if empty for api_key mode, auto-generate
	Users    string // comma-separated usernames for "user" mode
}

type ProtectResult struct {
	ID         string   `json:"id"`
	AccessMode string   `json:"accessMode"`
	Username   string   `json:"username,omitempty"`
	ApiKey     string   `json:"apiKey,omitempty"`
	Users      []string `json:"users,omitempty"`
	Message    string   `json:"message"`
}

type LockResult struct {
	ID      string `json:"id"`
	Locked  bool   `json:"locked"`
	Message string `json:"message"`
}

// ── Update Metadata ─────────────────────────────────────────────────

func (svc *Service) UpdateMeta(user *store.User, p UpdateMetaParams) (*UpdateMetaResult, error) {
	deploy, err := svc.Store.GetDeployment(p.DeployID)
	if err != nil || deploy == nil {
		return nil, ErrNotFound("Deployment not found.")
	}
	if deploy.UserID != user.ID && user.Role != "admin" {
		return nil, ErrForbidden("Not your deployment.")
	}
	if deploy.Locked {
		return nil, ErrBadRequest("Deployment is locked. Unlock it first.")
	}

	title := deploy.Title
	desc := deploy.Description
	if p.Title != nil {
		title = *p.Title
	}
	if p.Description != nil {
		desc = *p.Description
	}

	if p.Title != nil || p.Description != nil {
		if err := svc.Store.UpdateDeploymentMeta(deploy.ID, title, desc); err != nil {
			return nil, ErrInternal("Failed to update metadata.")
		}
	}

	ttlHours := deploy.TTLHours
	expiresAt := deploy.ExpiresAt
	if p.TTL != nil {
		ttlHours = ParseTTL(*p.TTL, 0)
		if ttlHours > 0 {
			expiresAt = time.Now().Add(time.Duration(ttlHours) * time.Hour).UTC().Format("2006-01-02 15:04:05")
		} else {
			expiresAt = ""
		}
		if err := svc.Store.UpdateDeploymentTTL(deploy.ID, ttlHours, expiresAt); err != nil {
			return nil, ErrInternal("Failed to update TTL.")
		}
	}

	networkQuota := deploy.NetworkQuota
	if p.NetworkQuota != nil {
		resolvedQuota := svc.ResolveNetworkQuota(*p.NetworkQuota)
		if err := svc.Store.UpdateDeploymentNetworkQuota(deploy.ID, resolvedQuota); err != nil {
			return nil, ErrInternal("Failed to update network quota.")
		}
		if svc.Bandwidth != nil {
			svc.Bandwidth.RecheckQuota(deploy.ID, deploy.Subdomain, resolvedQuota)
		}
		networkQuota = resolvedQuota
	}

	log.Printf("[update-meta] %s | user=%s", deploy.Subdomain, user.Name)

	return &UpdateMetaResult{
		ID:           deploy.ID,
		Title:        title,
		Description:  desc,
		TTLHours:     ttlHours,
		ExpiresAt:    expiresAt,
		NetworkQuota: networkQuota,
		Message:      "Metadata updated.",
	}, nil
}

// ── Protect Deployment ──────────────────────────────────────────────

func (svc *Service) ProtectDeployment(user *store.User, p ProtectParams) (*ProtectResult, error) {
	deploy, err := svc.Store.GetDeployment(p.DeployID)
	if err != nil || deploy == nil {
		return nil, ErrNotFound("Deployment not found.")
	}
	if deploy.UserID != user.ID && user.Role != "admin" {
		return nil, ErrForbidden("Not your deployment.")
	}
	if deploy.Status != "running" {
		return nil, ErrBadRequest(fmt.Sprintf("Cannot protect deployment in '%s' state.", deploy.Status))
	}
	if deploy.Locked {
		return nil, ErrBadRequest("Deployment is locked. Unlock it first.")
	}

	accessUser, accessHash, accessUsers, resultKey, acErr := ComputeAccessControl(p.Mode, p.Username, p.Password, p.ApiKey, p.Users)
	if acErr != nil {
		return nil, acErr
	}

	if err := svc.Store.UpdateDeploymentAccess(deploy.ID, p.Mode, accessUser, accessHash, accessUsers); err != nil {
		return nil, ErrInternal("Failed to update access control.")
	}

	ac := &proxy.AccessControl{Mode: p.Mode, Username: accessUser, Hash: accessHash, Subdomain: deploy.Subdomain}
	if p.Mode == "public" {
		ac = nil
	}
	svc.Proxy.AddRoute(deploy.Subdomain, deploy.Port, ac)

	msg := "Access control updated."
	switch p.Mode {
	case "basic_auth":
		msg = fmt.Sprintf("Basic auth enabled. Username: %s", accessUser)
	case "api_key":
		msg = fmt.Sprintf("API key protection enabled. Use header 'X-Api-Key: %s'.", resultKey)
	case "user":
		if accessUsers != "" {
			msg = fmt.Sprintf("User protection enabled. Restricted to: %s", accessUsers)
		} else {
			msg = "User protection enabled. Users must login to OpenBerth to access this deployment."
		}
	case "public":
		msg = "Protection removed. Deployment is now public."
	}

	log.Printf("[protect] %s | mode=%s | user=%s", deploy.Subdomain, p.Mode, user.Name)

	var userList []string
	if accessUsers != "" {
		userList = strings.Split(accessUsers, ",")
	}

	return &ProtectResult{
		ID:         deploy.ID,
		AccessMode: p.Mode,
		Username:   accessUser,
		ApiKey:     resultKey,
		Users:      userList,
		Message:    msg,
	}, nil
}

// ── Destroy Deployment ──────────────────────────────────────────────

func (svc *Service) DestroyDeployment(user *store.User, id string) error {
	deploy, _ := svc.Store.GetDeployment(id)
	if deploy == nil {
		return ErrNotFound("Not found.")
	}
	if deploy.UserID != user.ID && user.Role != "admin" {
		return ErrForbidden("Not your deployment.")
	}
	if deploy.Locked {
		return ErrBadRequest("Deployment is locked. Unlock it first.")
	}

	svc.DestroyFull(deploy)
	log.Printf("[destroy] %s | user=%s", deploy.Subdomain, user.Name)
	return nil
}

// ── Lock Deployment ─────────────────────────────────────────────────

func (svc *Service) LockDeployment(user *store.User, id string, locked bool) (*LockResult, error) {
	deploy, _ := svc.Store.GetDeployment(id)
	if deploy == nil {
		return nil, ErrNotFound("Not found.")
	}
	if deploy.UserID != user.ID && user.Role != "admin" {
		return nil, ErrForbidden("Not your deployment.")
	}
	if deploy.Status != "running" {
		return nil, ErrBadRequest(fmt.Sprintf("Cannot lock/unlock deployment in '%s' state.", deploy.Status))
	}

	if err := svc.Store.UpdateDeploymentLocked(id, locked); err != nil {
		return nil, ErrInternal("Failed to update lock state.")
	}

	action := "locked"
	if !locked {
		action = "unlocked"
	}
	log.Printf("[%s] %s | user=%s", action, deploy.Subdomain, user.Name)

	return &LockResult{
		ID:      id,
		Locked:  locked,
		Message: fmt.Sprintf("Deployment %s.", action),
	}, nil
}
