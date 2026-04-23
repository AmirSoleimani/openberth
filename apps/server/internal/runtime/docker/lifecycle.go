package docker

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/runtime"
)

// Exec runs a shell command inside a running container and returns the
// combined stdout+stderr plus the exit code.
func (d *Driver) Exec(instanceID, command string, timeout time.Duration) (runtime.ExecResult, error) {
	name := "sc-" + instanceID
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
	return runtime.ExecResult{Output: out, ExitCode: exitCode}, err
}

// Destroy removes the container and any associated build volumes.
// The per-deployment network is also torn down; already-gone networks
// are silently ignored (best-effort).
func (d *Driver) Destroy(instanceID string) error {
	execCmd("docker", "rm", "-f", "sc-"+instanceID)
	out, _ := execCmd("docker", "volume", "ls", "-q", "--filter", "name=sc-ws-"+instanceID)
	for _, vol := range strings.Split(strings.TrimSpace(out), "\n") {
		if vol != "" {
			execCmd("docker", "volume", "rm", "-f", vol)
		}
	}
	d.removeNetwork(instanceID)
	return nil
}

// Logs returns the last `tail` lines from the container.
func (d *Driver) Logs(instanceID string, tail int) string {
	name := "sc-" + instanceID
	out, err := execCmd("docker", "logs", "--tail", fmt.Sprintf("%d", tail), name)
	if err != nil {
		return fmt.Sprintf("Error fetching logs: %v", err)
	}
	return out
}

// LogStream starts a streaming docker logs process and returns an
// io.ReadCloser. The caller must close the reader when done, which kills
// the underlying docker logs process.
func (d *Driver) LogStream(instanceID string, tail int) (io.ReadCloser, error) {
	name := "sc-" + instanceID
	cmd := exec.Command("docker", "logs", "--follow", "--tail", fmt.Sprintf("%d", tail), name)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Return a wrapper that kills the process when closed
	return &streamReader{ReadCloser: stdout, cmd: cmd}, nil
}

type streamReader struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (s *streamReader) Close() error {
	s.cmd.Process.Kill()
	s.cmd.Wait()
	return s.ReadCloser.Close()
}

// Status reports the docker-level state of the container, mapped into
// the runtime.Status vocabulary.
func (d *Driver) Status(instanceID string) runtime.Status {
	name := "sc-" + instanceID
	out, err := execCmd("docker", "inspect", "-f", "{{.State.Status}}", name)
	if err != nil {
		return runtime.StatusNotFound
	}
	return mapDockerState(strings.TrimSpace(out))
}

// mapDockerState converts Docker's state strings into the driver-neutral
// runtime.Status vocabulary. Unknown docker states collapse to Failed so
// callers treat them as "something's wrong" rather than silently OK.
func mapDockerState(s string) runtime.Status {
	switch s {
	case "running":
		return runtime.StatusRunning
	case "exited", "stopped":
		return runtime.StatusStopped
	case "restarting":
		return runtime.StatusRestarting
	case "":
		return runtime.StatusNotFound
	default:
		return runtime.StatusFailed
	}
}

// Restart performs a docker restart with a 5-second grace period.
func (d *Driver) Restart(instanceID string) bool {
	name := "sc-" + instanceID
	_, err := execCmd("docker", "restart", "-t", "5", name)
	return err == nil
}

// Port reads the host port mapping from a running container.
func (d *Driver) Port(instanceID string) int {
	name := "sc-" + instanceID
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
