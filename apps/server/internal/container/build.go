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

// RecreateRuntime stops the current runtime and starts a new one with the same
// build volume but potentially different env vars. Skips the build phase entirely.
// Used for secret rotation where code hasn't changed.
func (cm *ContainerManager) RecreateRuntime(opts CreateOpts) (*ContainerResult, error) {
	p := framework.GetProvider(opts.Language)
	if p != nil && p.StaticOnly() {
		return cm.rebuildStatic(opts)
	}

	runnerName := "sc-" + opts.ID

	volume := cm.currentVolume(opts.ID)
	if volume == "" {
		return nil, fmt.Errorf("cannot find current volume for %s", opts.ID)
	}

	hostPort := cm.InspectPort(opts.ID)
	if hostPort == 0 {
		return nil, fmt.Errorf("cannot determine port for %s", opts.ID)
	}

	log.Printf("[restart] Restarting runtime for %s (port %d, same volume %s)", opts.ID, hostPort, volume)
	execCmd("docker", "rm", "-f", runnerName)

	result, err := cm.startRuntime(opts, volume, hostPort)
	if err != nil {
		return nil, fmt.Errorf("restart failed: %w", err)
	}

	log.Printf("[restart] Runtime restarted for %s", opts.ID)
	return result, nil
}

// runBuild executes the build phase: a short-lived container that produces
// the artifacts into a named volume. The volume becomes the /app for runtime.
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

// startRuntime starts the runtime container from a prebuilt volume.
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
