// Package runtime defines the pluggable deploy-backend interface.
//
// A Runtime drives the lifecycle of one deployment instance — build, run,
// update, destroy — without committing to any particular technology. The
// reference implementation under runtime/docker targets the Docker engine,
// but drivers for Kubernetes, Firecracker microVMs, or other backends can
// be added in-tree by implementing this interface and calling Register in
// an init function.
//
// Drivers must honor a small POSIX contract so framework providers (which
// emit shell build/run scripts) stay runtime-agnostic:
//
//	/bin/sh          present
//	/app             rw  — build artifacts live here, becomes workdir
//	/app/code        ro  — source tree bind-mount
//	/data            rw  — persistent per-instance state
//	/tmp             rw tmpfs
//	$PORT, $DATA_DIR injected into the environment
//
// See docs/runtime-drivers.md for the full contract and guidance on
// implementing new drivers.
package runtime

import (
	"io"
	"time"
)

// Runtime drives one deployment backend. All methods are concurrency-safe
// with respect to different instanceIDs; callers must still coordinate
// concurrent operations against the same instance.
type Runtime interface {
	// Deploy performs a fresh build-and-run for an instance. It's the path
	// taken by `berth deploy` and the MCP berth_deploy tool.
	Deploy(opts DeployOpts) (*Result, error)

	// Rebuild updates an already-running instance. Drivers typically do a
	// blue-green swap so the endpoint stays reachable throughout.
	Rebuild(opts DeployOpts) (*Result, error)

	// RestartRuntime relaunches the runtime half of an instance without
	// redoing the build phase. Used for secret rotation and env refresh.
	RestartRuntime(opts DeployOpts) (*Result, error)

	// StartSandbox brings up a long-lived development instance with live
	// code mounting. Drivers without live-mount support should return an
	// error; callers gate on Capabilities().Sandbox.
	StartSandbox(opts SandboxOpts) (*Result, error)

	// Destroy removes the instance and any driver-owned artifacts (volumes,
	// snapshots, etc). Best-effort; missing instances are not an error.
	Destroy(instanceID string) error

	// Status reports the current lifecycle state.
	Status(instanceID string) Status

	// Restart kicks the instance. Returns true on apparent success.
	Restart(instanceID string) bool

	// Port returns the host port (or driver-equivalent) exposing the
	// instance's primary listener. Zero means unknown.
	Port(instanceID string) int

	// Exec runs a shell command inside the instance. Drivers without
	// exec support should return a clear error; callers gate on
	// Capabilities().Exec.
	Exec(instanceID, command string, timeout time.Duration) (ExecResult, error)

	// Logs returns up to tail lines of recent output.
	Logs(instanceID string, tail int) string

	// LogStream returns a streaming reader of live output. The caller must
	// Close the reader when done to release driver resources.
	LogStream(instanceID string, tail int) (io.ReadCloser, error)

	// Capabilities reports which optional features this driver supports.
	Capabilities() Capabilities
}

// DeployOpts bundles everything a driver needs to build and run an instance.
// It mirrors the former container.CreateOpts but lives in the runtime
// package so the interface has no Docker-specific leakage.
//
// BuildEnv vs UserEnv: BuildEnv is what the build phase sees (no resolved
// secret values — they don't belong in build.log or in postinstall scripts).
// UserEnv is the full runtime environment including resolved secrets. If
// BuildEnv is nil, callers that haven't been updated for the split get the
// legacy behavior (build sees everything).
type DeployOpts struct {
	ID           string
	UserID       string
	CodeDir      string // host path to the extracted source tree
	Framework    string
	Language     string
	Port         int
	Image        string // build image
	RunImage     string // runtime image (empty = same as Image)
	BuildCmd     string
	StartCmd     string
	InstallCmd   string // custom install override from .berth.json
	CacheDir     string // language cache path to preserve across rebuilds
	FrameworkEnv map[string]string
	BuildEnv     map[string]string // env visible only during the build phase (no resolved secrets)
	UserEnv      map[string]string // env visible at runtime (includes resolved secrets)
	Memory       string
	CPUs         string
	NetworkQuota string // per-deploy override; empty = driver/config default
}

// SandboxOpts is DeployOpts' sibling for dev-mode instances with live code.
type SandboxOpts struct {
	ID           string
	UserID       string
	CodeDir      string // {DeploysDir}/{id} — mounted rw so pushes apply instantly
	Framework    string
	Language     string
	DevCmd       string // e.g. "npx vite dev --host 0.0.0.0 --port $PORT"
	InstallCmd   string
	Port         int
	Image        string // e.g. node:20-slim
	FrameworkEnv map[string]string
	BuildEnv     map[string]string // build-time env (no resolved secret values)
	UserEnv      map[string]string // runtime env (includes resolved secrets)
	Memory       string
	NetworkQuota string
}

// Result is returned by Deploy / Rebuild / RestartRuntime / StartSandbox.
// Fields are driver-agnostic; Metadata carries any extras a driver needs
// to surface (e.g. container name, pod UID, VM ID) without expanding the
// shared shape.
type Result struct {
	InstanceID string            // driver's opaque ID for this instance
	Endpoint   Endpoint          // how to reach the instance
	Isolated   bool              // was secure isolation applied (gVisor/Kata/etc)?
	Metadata   map[string]string // driver-specific extras, optional
}

// Endpoint describes how the proxy should reach an instance. Today only
// Port is consumed by the Caddy layer (which hardcodes localhost); Host is
// reserved for drivers that route via service DNS or non-loopback IPs.
type Endpoint struct {
	Host string
	Port int
}

// Capabilities declares which optional features a driver supports.
// Service-layer callers that depend on a capability should gate on the
// relevant flag before invoking the corresponding method.
type Capabilities struct {
	Sandbox         bool // supports live bind-mount dev instances?
	SecureIsolation bool // has gVisor / Kata / equivalent available?
	Exec            bool // can run commands inside a running instance?
}

// Status is the lifecycle state of an instance as seen by the driver.
// These values mirror the Docker container states but are not tied to
// Docker semantics — every driver maps its own state machine into this
// common vocabulary.
type Status string

const (
	StatusRunning    Status = "running"
	StatusStopped    Status = "stopped"
	StatusRestarting Status = "restarting"
	StatusFailed     Status = "failed"
	StatusNotFound   Status = "not_found"
)

// ExecResult is the outcome of a shell command run via Runtime.Exec.
type ExecResult struct {
	Output   string
	ExitCode int
}
