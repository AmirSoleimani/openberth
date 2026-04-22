package docker

import (
	"fmt"
	"log"
	"strings"

	"github.com/AmirSoleimani/openberth/apps/server/internal/runtime"
)

// createStatic builds a read-only Caddy container to serve plain static
// assets. Used by languages flagged StaticOnly (e.g. pure HTML projects).
func (d *Driver) createStatic(opts runtime.DeployOpts, hostPort int) (*runtime.Result, error) {
	containerName := "sc-" + opts.ID

	if err := d.ensureNetwork(opts.ID); err != nil {
		return nil, err
	}

	args := []string{
		"run", "-d",
		"--name", containerName,
		"--restart=unless-stopped",
		"--label", "openberth=true",
		"--label", "openberth.id=" + opts.ID,
	}

	if d.gvisorReady {
		args = append(args, "--runtime=runsc")
	}

	args = append(args,
		"--memory=128m",
		"--cpus=0.25",
		fmt.Sprintf("--pids-limit=%d", d.cfg.Container.PIDLimit),
		"--cap-drop=ALL",
		"--cap-add=NET_BIND_SERVICE",
	)
	if d.cfg.Container.DiskSize != "" {
		args = append(args, "--storage-opt", "size="+d.cfg.Container.DiskSize)
	}
	if !d.gvisorReady {
		args = append(args, "--security-opt=no-new-privileges")
	}
	args = d.addNetworkArg(args, opts.ID)
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

	return d.makeResult(cid, containerName, hostPort), nil
}

// rebuildStatic handles updates for static-only deployments.
// Static containers bind-mount the code directory, so files are already
// updated on disk. We just need to restart the container to pick up any
// Caddy config changes.
func (d *Driver) rebuildStatic(opts runtime.DeployOpts) (*runtime.Result, error) {
	hostPort := d.Port(opts.ID)
	if hostPort == 0 {
		return nil, fmt.Errorf("cannot determine port for %s", opts.ID)
	}

	log.Printf("[rebuild] Static rebuild for %s (restart on port %d)", opts.ID, hostPort)

	// Remove old container and recreate with same port
	execCmd("docker", "rm", "-f", "sc-"+opts.ID)

	return d.createStatic(opts, hostPort)
}

// createStaticSandbox serves static files with Caddy but with rw mount so
// pushes apply instantly.
func (d *Driver) createStaticSandbox(opts runtime.SandboxOpts, hostPort int) (*runtime.Result, error) {
	containerName := "sc-" + opts.ID

	args := []string{
		"run", "-d",
		"--name", containerName,
		"--restart=unless-stopped",
		"--label", "openberth=true",
		"--label", "openberth.id=" + opts.ID,
		"--label", "openberth.phase=sandbox",
	}

	if d.gvisorReady {
		args = append(args, "--runtime=runsc")
	}

	args = append(args,
		"--memory=128m",
		"--cpus=0.25",
		fmt.Sprintf("--pids-limit=%d", d.cfg.Container.PIDLimit),
		"--cap-drop=ALL",
		"--cap-add=NET_BIND_SERVICE",
	)
	if d.cfg.Container.DiskSize != "" {
		args = append(args, "--storage-opt", "size="+d.cfg.Container.DiskSize)
	}
	if !d.gvisorReady {
		args = append(args, "--security-opt=no-new-privileges")
	}
	args = d.addNetworkArg(args, opts.ID)
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

	return d.makeResult(cid, containerName, hostPort), nil
}
