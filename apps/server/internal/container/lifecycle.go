package container

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

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

// Destroy removes the container and any associated volumes.
func (cm *ContainerManager) Destroy(deployID string) {
	execCmd("docker", "rm", "-f", "sc-"+deployID)
	out, _ := execCmd("docker", "volume", "ls", "-q", "--filter", "name=sc-ws-"+deployID)
	for _, vol := range strings.Split(strings.TrimSpace(out), "\n") {
		if vol != "" {
			execCmd("docker", "volume", "rm", "-f", vol)
		}
	}
}

// Logs returns the last `tail` lines from the container.
func (cm *ContainerManager) Logs(deployID string, tail int) string {
	name := "sc-" + deployID
	out, err := execCmd("docker", "logs", "--tail", fmt.Sprintf("%d", tail), name)
	if err != nil {
		return fmt.Sprintf("Error fetching logs: %v", err)
	}
	return out
}

// LogStream starts a streaming docker logs process and returns an io.ReadCloser.
// The caller must close the reader when done, which kills the process.
func (cm *ContainerManager) LogStream(deployID string, tail int) (io.ReadCloser, error) {
	name := "sc-" + deployID
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

// Status reports the docker-level state of the container.
func (cm *ContainerManager) Status(deployID string) string {
	name := "sc-" + deployID
	out, err := execCmd("docker", "inspect", "-f", "{{.State.Status}}", name)
	if err != nil {
		return "not_found"
	}
	return strings.TrimSpace(out)
}

// Restart performs a docker restart.
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
