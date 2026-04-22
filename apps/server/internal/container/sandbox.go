package container

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/framework"
)

type SandboxOpts struct {
	ID           string
	UserID       string
	CodeDir      string // {DeploysDir}/{id} — mounted rw at /app
	Framework    string
	Language     string
	DevCmd       string // e.g. "npx vite dev --host 0.0.0.0 --port $PORT"
	InstallCmd   string // custom install override from .berth.json
	Port         int    // container port
	Image        string // e.g. node:20-slim
	FrameworkEnv map[string]string
	UserEnv      map[string]string
	Memory       string
	NetworkQuota string // per-sandbox override
}

// CreateSandbox starts a long-lived container with a dev server and bind-mounted code.
func (cm *ContainerManager) CreateSandbox(opts SandboxOpts) (*ContainerResult, error) {
	hostPort, err := cm.findPort()
	if err != nil {
		return nil, err
	}

	p := framework.GetProvider(opts.Language)
	if p != nil && p.StaticOnly() {
		return cm.createStaticSandbox(opts, hostPort)
	}

	// Write the sandbox entrypoint script
	entrypoint := p.SandboxEntrypoint(fwInfoFromSandboxOpts(opts), opts.Port)
	entrypointPath := filepath.Join(opts.CodeDir, ".openberth-sandbox.sh")
	if err := os.WriteFile(entrypointPath, []byte(entrypoint), 0755); err != nil {
		return nil, fmt.Errorf("write sandbox entrypoint: %w", err)
	}

	containerName := "sc-" + opts.ID
	args := []string{
		"run", "-d",
		"--name", containerName,
		"--restart=unless-stopped",
		"--label", "openberth=true",
		"--label", "openberth.id=" + opts.ID,
		"--label", "openberth.phase=sandbox",
	}

	if cm.gvisorReady {
		args = append(args, "--runtime=runsc")
	}

	memory := opts.Memory
	if memory == "" {
		memory = "1g"
	}
	args = append(args,
		"--memory="+memory,
		"--cpus="+cm.cfg.Container.CPUs,
		fmt.Sprintf("--pids-limit=%d", cm.cfg.Container.PIDLimit*2),
		"--cap-drop=ALL",
	)
	if cm.cfg.Container.DiskSize != "" {
		args = append(args, "--storage-opt", "size="+cm.cfg.Container.DiskSize)
	}
	if !cm.gvisorReady {
		args = append(args, "--security-opt=no-new-privileges")
	}

	// Bind mount code dir rw (not a Docker volume — pushes apply instantly)
	persistDir := filepath.Join(cm.cfg.PersistDir, opts.ID)
	os.MkdirAll(persistDir, 0755)

	args = append(args,
		"-v="+opts.CodeDir+":/app:rw",
		"-v="+persistDir+":/data:rw",
		"--tmpfs=/tmp:rw,exec,nosuid,size=256m",
		fmt.Sprintf("-p=127.0.0.1:%d:%d", hostPort, opts.Port),
		"-w=/app",
	)

	// Language-specific cache volumes
	args = append(args, p.CacheVolumes(opts.UserID)...)

	// Environment
	env := map[string]string{
		"PORT":     fmt.Sprintf("%d", opts.Port),
		"DATA_DIR": "/data",
		"NODE_ENV": "development",
		// Enable polling for file watchers — Docker bind mounts don't
		// propagate inotify events from host writes (especially on macOS).
		"CHOKIDAR_USEPOLLING":     "true",
		"WATCHPACK_POLLING":       "true",
		"WATCHPACK_POLL_INTERVAL": "500",
	}
	for k, v := range opts.FrameworkEnv {
		env[k] = v
	}
	// Language-specific sandbox env overrides
	for k, v := range p.SandboxEnv() {
		env[k] = v
	}
	for k, v := range opts.UserEnv {
		env[k] = v
	}
	for k, v := range env {
		args = append(args, "-e="+k+"="+v)
	}

	args = append(args, opts.Image, "/bin/sh", "/app/.openberth-sandbox.sh")

	log.Printf("[sandbox] Starting sandbox for %s (%s/%s) on port %d", opts.ID, opts.Language, opts.Framework, hostPort)

	out, err := execCmd("docker", args...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %w (output: %s)", err, out)
	}

	cid := strings.TrimSpace(out)
	if len(cid) > 12 {
		cid = cid[:12]
	}

	// Verify container started
	time.Sleep(2 * time.Second)
	status := cm.Status(opts.ID)
	if status != "running" {
		logs := cm.Logs(opts.ID, 50)
		cm.Destroy(opts.ID)
		return nil, fmt.Errorf("sandbox container failed to start (status=%s). Logs:\n%s", status, logs)
	}

	log.Printf("[sandbox] Started %s (container=%s, port=%d)", opts.ID, cid, hostPort)

	return &ContainerResult{
		ContainerID: cid,
		HostPort:    hostPort,
		Name:        containerName,
		GVisor:      cm.gvisorReady,
	}, nil
}
