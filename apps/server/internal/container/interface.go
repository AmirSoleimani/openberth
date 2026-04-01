package container

import (
	"io"
	"time"
)

// Manager defines the interface for container lifecycle management.
// Implementations exist for Docker (default) and Kubernetes.
type Manager interface {
	GVisorAvailable() bool
	Create(opts CreateOpts) (*ContainerResult, error)
	Rebuild(opts CreateOpts) (*ContainerResult, error)
	RecreateRuntime(opts CreateOpts) (*ContainerResult, error)
	CreateSandbox(opts SandboxOpts) (*ContainerResult, error)
	ExecInContainer(deployID string, command string, timeout time.Duration) (string, int, error)
	Destroy(deployID string)
	Logs(deployID string, tail int) string
	LogStream(deployID string, tail int) (io.ReadCloser, error)
	Status(deployID string) string
	Restart(deployID string) bool
	InspectPort(deployID string) int
}
