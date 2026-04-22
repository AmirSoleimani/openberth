// Package types holds cross-cutting enum values used across store, service,
// and httphandler layers. It must have zero internal dependencies so any
// package can import it without creating cycles.
package types

// DeploymentStatus is the lifecycle state persisted in the deployments table.
type DeploymentStatus string

const (
	StatusBuilding  DeploymentStatus = "building"
	StatusRunning   DeploymentStatus = "running"
	StatusUpdating  DeploymentStatus = "updating"
	StatusFailed    DeploymentStatus = "failed"
	StatusDestroyed DeploymentStatus = "destroyed"
	StatusStopped   DeploymentStatus = "stopped"
	StatusPending   DeploymentStatus = "pending"
)

// AccessMode is the proxy-level access control applied to a deployment.
type AccessMode string

const (
	AccessPublic    AccessMode = "public"
	AccessBasicAuth AccessMode = "basic_auth"
	AccessAPIKey    AccessMode = "api_key"
	AccessUser      AccessMode = "user"
)

// Role is the authorization role stored on a user account.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// SecretScope distinguishes per-user secrets from globally-shared ones.
type SecretScope string

const (
	SecretScopeUser   SecretScope = "user"
	SecretScopeGlobal SecretScope = "global"
)

// DeployMode distinguishes a production deploy from an iterative sandbox.
type DeployMode string

const (
	ModeDeploy  DeployMode = "deploy"
	ModeSandbox DeployMode = "sandbox"
)
