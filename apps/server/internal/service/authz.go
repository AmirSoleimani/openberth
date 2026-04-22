package service

import (
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
	"github.com/AmirSoleimani/openberth/apps/server/internal/types"
)

// CanMutateDeploy returns true when the caller is allowed to mutate the
// given deployment. Either the caller owns it, or the caller is an admin.
// Used by destroy, update, lock, protect, promote, and other write paths.
func CanMutateDeploy(deploy *store.Deployment, user *store.User) bool {
	if deploy == nil || user == nil {
		return false
	}
	return deploy.UserID == user.ID || user.Role == string(types.RoleAdmin)
}

// IsAdmin reports whether the user holds the admin role.
func IsAdmin(user *store.User) bool {
	return user != nil && user.Role == string(types.RoleAdmin)
}
