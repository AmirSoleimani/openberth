package service

import "io"

// ── Deploy/Sandbox result types ─────────────────────────────────────

// DeployResult is the common result type for deploy, sandbox create, and promote operations.
type DeployResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Subdomain   string `json:"subdomain"`
	Framework   string `json:"framework"`
	Language    string `json:"language,omitempty"`
	Status      string `json:"status"`
	URL         string `json:"url"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	AccessMode  string `json:"accessMode,omitempty"`
	ApiKey      string `json:"apiKey,omitempty"`
}

// UpdateResult is the result of an update operation.
type UpdateResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	URL     string `json:"url"`
	Message string `json:"message"`
}

// ── Code deploy params ──────────────────────────────────────────────

type CodeDeployParams struct {
	Files        map[string]string
	Name         string
	Title        string
	Description  string
	TTL          string
	Port         int
	Env          map[string]string
	Memory       string
	CPUs         string
	NetworkQuota string
	// Access control at deploy time
	ProtectMode     string
	ProtectUsername  string
	ProtectPassword string
	ProtectApiKey   string
	ProtectUsers    string
}

type CodeUpdateParams struct {
	DeployID     string
	Files        map[string]string
	Port         int
	Env          map[string]string
	Memory       string
	CPUs         string
	NetworkQuota string
}

// ── Tarball deploy params ───────────────────────────────────────────

type TarballDeployParams struct {
	File            io.Reader
	Name            string
	Title           string
	Description     string
	TTL             string
	Port            int
	EnvVars         map[string]string
	Memory          string
	CPUs            string
	NetworkQuota    string
	ProtectMode     string
	ProtectUsername  string
	ProtectPassword string
	ProtectApiKey   string
	ProtectUsers    string
}

type TarballUpdateParams struct {
	DeployID     string
	File         io.Reader
	Port         int
	EnvVars      map[string]string
	Memory       string
	CPUs         string
	NetworkQuota string
}

// ── Sandbox params/results ──────────────────────────────────────────

type SandboxCreateParams struct {
	Files        map[string]string
	Name         string
	TTL          string
	Port         int
	Env          map[string]string
	Memory       string
	NetworkQuota string
	Language     string // optional hint
	// Access control at create time
	ProtectMode     string
	ProtectUsername  string
	ProtectPassword string
	ProtectApiKey   string
	ProtectUsers    string
}

type PushChange struct {
	Op      string `json:"op"`      // "write" or "delete"
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

type PushParams struct {
	SandboxID string
	Changes   []PushChange
}

type PushResult struct {
	ID            string `json:"id"`
	Updated       int    `json:"updated"`
	Deleted       int    `json:"deleted"`
	DepsInstalled bool   `json:"depsInstalled,omitempty"`
	InstallOutput string `json:"installOutput,omitempty"`
}

type ExecParams struct {
	SandboxID string
	Command   string
	Timeout   int // seconds, default 30, max 300
}

type ExecResult struct {
	ID       string `json:"id"`
	Output   string `json:"output"`
	ExitCode int    `json:"exitCode"`
}

type InstallParams struct {
	SandboxID string
	Packages  []string
	Uninstall bool
}

type InstallResult struct {
	ID      string `json:"id"`
	Output  string `json:"output"`
	Message string `json:"message"`
}

type PromoteParams struct {
	SandboxID    string
	TTL          string
	Memory       string
	CPUs         string
	NetworkQuota string
	Env          map[string]string
}

// ── Build start params (internal) ───────────────────────────────────

// buildStartParams holds everything the async build goroutine needs.
type buildStartParams struct {
	ID           string
	UserID       string
	UserName     string
	CodeDir      string
	Subdomain    string
	Memory       string
	CPUs         string
	NetworkQuota string
	LogPrefix    string
	FW           *frameworkInfo
	Port         int
	EnvVars      map[string]string
	AC           *accessControlInfo
}

// frameworkInfo is a subset of framework.Detection used by the build goroutine.
type frameworkInfo struct {
	Framework string
	Language  string
	Image     string
	RunImage  string
	BuildCmd  string
	StartCmd  string
	CacheDir  string
	Env       map[string]string
}

// accessControlInfo holds computed access control for a deployment.
type accessControlInfo struct {
	Mode       string
	Username   string
	Hash       string
	Subdomain  string
	ResultKey  string
}
