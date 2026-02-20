package service

import (
	"github.com/openberth/openberth/apps/server/internal/proxy"
	"github.com/openberth/openberth/apps/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// ComputeAccessControl processes protect parameters and returns values ready for
// DB storage and proxy config. Used by both deploy-time protect and the protect command.
func ComputeAccessControl(mode, username, password, apiKey, users string) (accessUser, accessHash, accessUsers, resultKey string, err error) {
	switch mode {
	case "basic_auth":
		if username == "" || password == "" {
			return "", "", "", "", ErrBadRequest("Username and password required for basic_auth mode.")
		}
		hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if e != nil {
			return "", "", "", "", ErrInternal("Failed to hash password.")
		}
		accessUser = username
		accessHash = string(hash)
	case "api_key":
		key := apiKey
		if key == "" {
			key = "sk_" + RandomHex(24)
		}
		accessHash = key
		resultKey = key
	case "user":
		accessUsers = users
	case "public":
		// Clear all access fields
	default:
		return "", "", "", "", ErrBadRequest("Invalid mode. Use: public, basic_auth, api_key, or user.")
	}
	return
}

// AccessControlFromDeployment builds a proxy AccessControl from a deployment's stored fields.
func AccessControlFromDeployment(d *store.Deployment) *proxy.AccessControl {
	if d.AccessMode == "" || d.AccessMode == "public" {
		return nil
	}
	return &proxy.AccessControl{
		Mode:      d.AccessMode,
		Username:  d.AccessUser,
		Hash:      d.AccessHash,
		Subdomain: d.Subdomain,
	}
}
