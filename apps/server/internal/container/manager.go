package container

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openberth/openberth/apps/server/internal/config"
	"github.com/openberth/openberth/apps/server/internal/framework"
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

// Create runs a two-phase deploy:
//
//	Phase 1 (build): gVisor, no memory limit
//	Phase 2 (run):   gVisor, tight limits
func (cm *ContainerManager) Create(opts CreateOpts) (*ContainerResult, error) {
	hostPort, err := cm.findPort()
	if err != nil {
		return nil, err
	}

	p := framework.GetProvider(opts.Language)
	if p != nil && p.StaticOnly() {
		return cm.createStatic(opts, hostPort)
	}

	volumeName := volumeForDeploy(opts.ID)

	log.Printf("[build] Phase 1: build %s (%s/%s)", opts.ID, opts.Language, opts.Framework)

	if _, err := execCmd("docker", "volume", "create", volumeName); err != nil {
		return nil, fmt.Errorf("create volume: %w", err)
	}

	if err := cm.runBuild(opts, volumeName, ""); err != nil {
		execCmd("docker", "volume", "rm", "-f", volumeName)
		return nil, err
	}

	log.Printf("[build] Phase 1 complete for %s", opts.ID)

	result, err := cm.startRuntime(opts, volumeName, hostPort)
	if err != nil {
		execCmd("docker", "volume", "rm", "-f", volumeName)
		return nil, err
	}

	return result, nil
}

// Rebuild does a blue-green deploy.
func (cm *ContainerManager) Rebuild(opts CreateOpts) (*ContainerResult, error) {
	p := framework.GetProvider(opts.Language)
	if p != nil && p.StaticOnly() {
		return cm.rebuildStatic(opts)
	}

	runnerName := "sc-" + opts.ID

	oldVolume := cm.currentVolume(opts.ID)
	if oldVolume == "" {
		return nil, fmt.Errorf("cannot find current volume for %s", opts.ID)
	}

	hostPort := cm.InspectPort(opts.ID)
	if hostPort == 0 {
		return nil, fmt.Errorf("cannot determine port for %s", opts.ID)
	}

	newVolume := volumeForDeploy(opts.ID)
	log.Printf("[rebuild] Blue-green for %s: %s -> %s", opts.ID, oldVolume, newVolume)

	if _, err := execCmd("docker", "volume", "create", newVolume); err != nil {
		return nil, fmt.Errorf("create volume: %w", err)
	}

	// Build container mounts old volume at /old:ro — providers copy cache directly
	if err := cm.runBuild(opts, newVolume, oldVolume); err != nil {
		execCmd("docker", "volume", "rm", "-f", newVolume)
		return nil, err
	}

	log.Printf("[rebuild] Swapping runtime for %s (port %d)", opts.ID, hostPort)
	execCmd("docker", "rm", "-f", runnerName)

	result, err := cm.startRuntime(opts, newVolume, hostPort)
	if err != nil {
		log.Printf("[rebuild] New runtime failed, rolling back: %v", err)
		cm.startRuntime(opts, oldVolume, hostPort)
		execCmd("docker", "volume", "rm", "-f", newVolume)
		return nil, fmt.Errorf("swap failed: %w", err)
	}

	execCmd("docker", "volume", "rm", "-f", oldVolume)
	log.Printf("[rebuild] Blue-green deploy complete for %s", opts.ID)
	return result, nil
}

// -- Internal helpers --

func (cm *ContainerManager) runBuild(opts CreateOpts, volumeName string, oldVolume string) error {
	p := framework.GetProvider(opts.Language)
	buildScript := p.BuildScript(fwInfoFromOpts(opts))
	buildScriptPath := filepath.Join(opts.CodeDir, ".openberth-build.sh")
	if err := os.WriteFile(buildScriptPath, []byte(buildScript), 0755); err != nil {
		return fmt.Errorf("write build script: %w", err)
	}

	builderName := fmt.Sprintf("sc-build-%s-%d", opts.ID, time.Now().UnixMilli())
	buildArgs := []string{
		"run", "--rm",
		"--name", builderName,
		"--label", "openberth=true",
		"--label", "openberth.phase=build",
	}

	if cm.gvisorReady {
		buildArgs = append(buildArgs, "--runtime=runsc")
	}

	buildArgs = append(buildArgs,
		"--cpus="+cm.cfg.Container.CPUs,
		fmt.Sprintf("--pids-limit=%d", cm.cfg.Container.PIDLimit*2),
		"--cap-drop=ALL",
	)
	if !cm.gvisorReady {
		buildArgs = append(buildArgs, "--security-opt=no-new-privileges")
	}

	buildArgs = append(buildArgs,
		"-v="+volumeName+":/app:rw",
		"-v="+opts.CodeDir+":/app/code:ro",
	)

	// Mount old volume read-only for rebuild (providers copy cache from /old)
	if oldVolume != "" {
		buildArgs = append(buildArgs, "-v="+oldVolume+":/old:ro")
	}

	// Language-specific per-user cache volumes
	buildArgs = append(buildArgs, p.CacheVolumes(opts.UserID)...)

	buildArgs = append(buildArgs,
		"-w=/app",
		fmt.Sprintf("-e=PORT=%d", opts.Port),
	)
	for k, v := range opts.FrameworkEnv {
		buildArgs = append(buildArgs, "-e="+k+"="+v)
	}
	for k, v := range opts.UserEnv {
		buildArgs = append(buildArgs, "-e="+k+"="+v)
	}

	buildArgs = append(buildArgs, opts.Image, "/bin/sh", "/app/code/.openberth-build.sh")

	buildOut, err := execCmdTimeout("docker", 10*time.Minute, buildArgs...)
	if err != nil {
		log.Printf("[build] FAILED for %s:\n%s", opts.ID, buildOut)
		return fmt.Errorf("build failed: %w\nOutput:\n%s", err, buildOut)
	}

	return nil
}

func (cm *ContainerManager) startRuntime(opts CreateOpts, volumeName string, hostPort int) (*ContainerResult, error) {
	log.Printf("[run] Starting runtime for %s on port %d (image=%s)", opts.ID, hostPort, opts.runtimeImage())

	p := framework.GetProvider(opts.Language)
	runScript := p.RunScript(fwInfoFromOpts(opts))
	runScriptPath := filepath.Join(opts.CodeDir, ".openberth-run.sh")
	if err := os.WriteFile(runScriptPath, []byte(runScript), 0755); err != nil {
		return nil, fmt.Errorf("write run script: %w", err)
	}

	containerName := "sc-" + opts.ID
	runArgs := []string{
		"run", "-d",
		"--name", containerName,
		"--restart=unless-stopped",
		"--label", "openberth=true",
		"--label", "openberth.id=" + opts.ID,
		"--label", "openberth.phase=run",
		"--label", "openberth.volume=" + volumeName,
	}

	if cm.gvisorReady {
		runArgs = append(runArgs, "--runtime=runsc")
	}

	memory := opts.Memory
	if memory == "" {
		memory = cm.cfg.Container.Memory
	}
	cpus := opts.CPUs
	if cpus == "" {
		cpus = cm.cfg.Container.CPUs
	}
	runArgs = append(runArgs,
		"--memory="+memory,
		"--cpus="+cpus,
		fmt.Sprintf("--pids-limit=%d", cm.cfg.Container.PIDLimit),
		"--cap-drop=ALL",
	)
	if cm.cfg.Container.DiskSize != "" {
		runArgs = append(runArgs, "--storage-opt", "size="+cm.cfg.Container.DiskSize)
	}
	if !cm.gvisorReady {
		runArgs = append(runArgs, "--security-opt=no-new-privileges")
	}
	persistDir := filepath.Join(cm.cfg.PersistDir, opts.ID)
	os.MkdirAll(persistDir, 0755)

	runArgs = append(runArgs,
		"-v="+volumeName+":/app:rw",
		"-v="+opts.CodeDir+":/app/code:ro",
		"-v="+persistDir+":/data:rw",
		"--tmpfs=/tmp:rw,exec,nosuid,size=256m",
		fmt.Sprintf("-p=127.0.0.1:%d:%d", hostPort, opts.Port),
		"-w=/app",
	)

	env := map[string]string{
		"PORT":     fmt.Sprintf("%d", opts.Port),
		"DATA_DIR": "/data",
	}
	for k, v := range opts.FrameworkEnv {
		env[k] = v
	}
	for k, v := range opts.UserEnv {
		env[k] = v
	}
	for k, v := range env {
		runArgs = append(runArgs, "-e="+k+"="+v)
	}

	// Use the runtime image (may differ from build image for compiled languages)
	runArgs = append(runArgs, opts.runtimeImage(), "/bin/sh", "/app/code/.openberth-run.sh")

	out, err := execCmd("docker", runArgs...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %w (output: %s)", err, out)
	}

	cid := strings.TrimSpace(out)
	if len(cid) > 12 {
		cid = cid[:12]
	}

	log.Printf("[run] Started %s (container=%s, port=%d, volume=%s)", opts.ID, cid, hostPort, volumeName)

	return &ContainerResult{
		ContainerID: cid,
		HostPort:    hostPort,
		Name:        containerName,
		GVisor:      cm.gvisorReady,
	}, nil
}

func (cm *ContainerManager) createStatic(opts CreateOpts, hostPort int) (*ContainerResult, error) {
	containerName := "sc-" + opts.ID

	args := []string{
		"run", "-d",
		"--name", containerName,
		"--restart=unless-stopped",
		"--label", "openberth=true",
		"--label", "openberth.id=" + opts.ID,
	}

	if cm.gvisorReady {
		args = append(args, "--runtime=runsc")
	}

	args = append(args,
		"--memory=128m",
		"--cpus=0.25",
		fmt.Sprintf("--pids-limit=%d", cm.cfg.Container.PIDLimit),
		"--cap-drop=ALL",
		"--cap-add=NET_BIND_SERVICE",
	)
	if cm.cfg.Container.DiskSize != "" {
		args = append(args, "--storage-opt", "size="+cm.cfg.Container.DiskSize)
	}
	if !cm.gvisorReady {
		args = append(args, "--security-opt=no-new-privileges")
	}
	args = append(args,
		"--read-only",
		"--tmpfs=/config:rw,noexec,nosuid,size=1m",
		"--tmpfs=/data:rw,noexec,nosuid,size=1m",
		"-v="+opts.CodeDir+":/srv:ro",
		fmt.Sprintf("-p=127.0.0.1:%d:8080", hostPort),
	)

	args = append(args, opts.Image, "caddy", "file-server", "--root", "/srv", "--listen", ":8080")

	out, err := execCmd("docker", args...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %w (output: %s)", err, out)
	}

	cid := strings.TrimSpace(out)
	if len(cid) > 12 {
		cid = cid[:12]
	}

	return &ContainerResult{
		ContainerID: cid,
		HostPort:    hostPort,
		Name:        containerName,
		GVisor:      cm.gvisorReady,
	}, nil
}

// rebuildStatic handles updates for static-only deployments.
// Static containers bind-mount the code directory, so files are already updated on disk.
// We just need to restart the container to pick up any Caddy config changes.
func (cm *ContainerManager) rebuildStatic(opts CreateOpts) (*ContainerResult, error) {
	hostPort := cm.InspectPort(opts.ID)
	if hostPort == 0 {
		return nil, fmt.Errorf("cannot determine port for %s", opts.ID)
	}

	log.Printf("[rebuild] Static rebuild for %s (restart on port %d)", opts.ID, hostPort)

	// Remove old container and recreate with same port
	execCmd("docker", "rm", "-f", "sc-"+opts.ID)

	return cm.createStatic(opts, hostPort)
}

// ── Sandbox ──────────────────────────────────────────────────────────

type SandboxOpts struct {
	ID           string
	UserID       string
	CodeDir      string // {DeploysDir}/{id} — mounted rw at /app
	Framework    string
	Language     string
	DevCmd       string // e.g. "npx vite dev --host 0.0.0.0 --port $PORT"
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
		"CHOKIDAR_USEPOLLING":  "true",
		"WATCHPACK_POLLING":    "true",
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

// createStaticSandbox serves static files with Caddy but with rw mount so pushes apply instantly.
func (cm *ContainerManager) createStaticSandbox(opts SandboxOpts, hostPort int) (*ContainerResult, error) {
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

	args = append(args,
		"--memory=128m",
		"--cpus=0.25",
		fmt.Sprintf("--pids-limit=%d", cm.cfg.Container.PIDLimit),
		"--cap-drop=ALL",
		"--cap-add=NET_BIND_SERVICE",
	)
	if cm.cfg.Container.DiskSize != "" {
		args = append(args, "--storage-opt", "size="+cm.cfg.Container.DiskSize)
	}
	if !cm.gvisorReady {
		args = append(args, "--security-opt=no-new-privileges")
	}
	args = append(args,
		"--tmpfs=/config:rw,noexec,nosuid,size=1m",
		"--tmpfs=/data:rw,noexec,nosuid,size=1m",
		"-v="+opts.CodeDir+":/srv:rw", // rw instead of ro for sandbox
		fmt.Sprintf("-p=127.0.0.1:%d:8080", hostPort),
	)

	args = append(args, "caddy:2-alpine", "caddy", "file-server", "--root", "/srv", "--listen", ":8080")

	out, err := execCmd("docker", args...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %w (output: %s)", err, out)
	}

	cid := strings.TrimSpace(out)
	if len(cid) > 12 {
		cid = cid[:12]
	}

	log.Printf("[sandbox] Started static sandbox %s (container=%s, port=%d)", opts.ID, cid, hostPort)

	return &ContainerResult{
		ContainerID: cid,
		HostPort:    hostPort,
		Name:        containerName,
		GVisor:      cm.gvisorReady,
	}, nil
}

// ExecInContainer runs a command inside a running container and returns the output.
func (cm *ContainerManager) ExecInContainer(deployID string, command string, timeout time.Duration) (string, int, error) {
	name := "sc-" + deployID
	out, err := execCmdTimeout("docker", timeout, "exec", name, "sh", "-c", command)
	exitCode := 0
	if err != nil {
		// Try to extract exit code from the error
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return out, exitCode, err
}

// -- Lifecycle --

func (cm *ContainerManager) Destroy(deployID string) {
	execCmd("docker", "rm", "-f", "sc-"+deployID)
	out, _ := execCmd("docker", "volume", "ls", "-q", "--filter", "name=sc-ws-"+deployID)
	for _, vol := range strings.Split(strings.TrimSpace(out), "\n") {
		if vol != "" {
			execCmd("docker", "volume", "rm", "-f", vol)
		}
	}
}

func (cm *ContainerManager) Logs(deployID string, tail int) string {
	name := "sc-" + deployID
	out, err := execCmd("docker", "logs", "--tail", fmt.Sprintf("%d", tail), name)
	if err != nil {
		return fmt.Sprintf("Error fetching logs: %v", err)
	}
	return out
}

func (cm *ContainerManager) Status(deployID string) string {
	name := "sc-" + deployID
	out, err := execCmd("docker", "inspect", "-f", "{{.State.Status}}", name)
	if err != nil {
		return "not_found"
	}
	return strings.TrimSpace(out)
}

func (cm *ContainerManager) Restart(deployID string) bool {
	name := "sc-" + deployID
	_, err := execCmd("docker", "restart", "-t", "5", name)
	return err == nil
}

// InspectPort reads the host port mapping from a running container.
func (cm *ContainerManager) InspectPort(deployID string) int {
	name := "sc-" + deployID
	out, err := execCmd("docker", "inspect", "-f",
		`{{range $p, $conf := .NetworkSettings.Ports}}{{range $conf}}{{.HostPort}}{{end}}{{end}}`,
		name)
	if err != nil {
		return 0
	}
	port := 0
	fmt.Sscanf(strings.TrimSpace(out), "%d", &port)
	return port
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
		Framework: opts.Framework,
		Language:  opts.Language,
		BuildCmd:  opts.BuildCmd,
		StartCmd:  opts.StartCmd,
		Port:      opts.Port,
		Image:     opts.Image,
		RunImage:  opts.RunImage,
		CacheDir:  opts.CacheDir,
		Env:       opts.FrameworkEnv,
	}
}

// fwInfoFromSandboxOpts reconstructs a FrameworkInfo from SandboxOpts for provider calls.
func fwInfoFromSandboxOpts(opts SandboxOpts) *framework.FrameworkInfo {
	return &framework.FrameworkInfo{
		Framework: opts.Framework,
		Language:  opts.Language,
		DevCmd:    opts.DevCmd,
		Port:      opts.Port,
		Image:     opts.Image,
		Env:       opts.FrameworkEnv,
	}
}
