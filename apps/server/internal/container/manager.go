package container

import (
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/config"
	"github.com/AmirSoleimani/openberth/apps/server/internal/framework"
)

type ContainerManager struct {
	cfg         *config.Config
	gvisorReady bool
}

type ContainerResult struct {
	ContainerID string
	HostPort    int
	Name        string
	GVisor      bool
}

func NewContainerManager(cfg *config.Config) *ContainerManager {
	cm := &ContainerManager{cfg: cfg}
	cm.gvisorReady = cm.checkGVisor()
	return cm
}

func (cm *ContainerManager) checkGVisor() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--runtime=runsc", "hello-world")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func (cm *ContainerManager) GVisorAvailable() bool {
	return cm.gvisorReady
}

func (cm *ContainerManager) findPort() (int, error) {
	for i := 0; i < 100; i++ {
		port := 10000 + rand.Intn(50000)
		out, _ := execCmd("ss", "-tlnp")
		if !strings.Contains(out, fmt.Sprintf(":%d ", port)) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("could not find available port")
}

type CreateOpts struct {
	ID           string
	UserID       string
	CodeDir      string
	Framework    string
	Language     string
	Port         int
	Image        string // build image
	RunImage     string // runtime image (empty = same as Image)
	BuildCmd     string
	StartCmd     string
	InstallCmd   string // custom install override from .berth.json
	CacheDir     string // what to preserve on rebuild (node_modules, target, venv)
	FrameworkEnv map[string]string
	UserEnv      map[string]string
	Memory       string
	CPUs         string
	NetworkQuota string // per-deploy override, e.g. "10g" (empty = use config default)
}

func (opts CreateOpts) runtimeImage() string {
	if opts.RunImage != "" {
		return opts.RunImage
	}
	return opts.Image
}

func volumeForDeploy(deployID string) string {
	return fmt.Sprintf("sc-ws-%s-%d", deployID, time.Now().UnixMilli())
}

func (cm *ContainerManager) currentVolume(deployID string) string {
	out, err := execCmd("docker", "inspect", "-f",
		`{{range .Mounts}}{{if eq .Destination "/app"}}{{.Name}}{{end}}{{end}}`,
		"sc-"+deployID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// -- Helpers --

func execCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func execCmdTimeout(name string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("timed out after %s", timeout)
	}
	return string(out), err
}

// fwInfoFromOpts reconstructs a FrameworkInfo from CreateOpts for provider calls.
func fwInfoFromOpts(opts CreateOpts) *framework.FrameworkInfo {
	return &framework.FrameworkInfo{
		Framework:  opts.Framework,
		Language:   opts.Language,
		BuildCmd:   opts.BuildCmd,
		StartCmd:   opts.StartCmd,
		InstallCmd: opts.InstallCmd,
		Port:       opts.Port,
		Image:      opts.Image,
		RunImage:   opts.RunImage,
		CacheDir:   opts.CacheDir,
		Env:        opts.FrameworkEnv,
	}
}

// fwInfoFromSandboxOpts reconstructs a FrameworkInfo from SandboxOpts for provider calls.
func fwInfoFromSandboxOpts(opts SandboxOpts) *framework.FrameworkInfo {
	return &framework.FrameworkInfo{
		Framework:  opts.Framework,
		Language:   opts.Language,
		DevCmd:     opts.DevCmd,
		InstallCmd: opts.InstallCmd,
		Port:       opts.Port,
		Image:      opts.Image,
		Env:        opts.FrameworkEnv,
	}
}
