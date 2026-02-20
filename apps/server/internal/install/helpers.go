package install

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runCmd executes a shell command locally and returns its combined output.
func runCmd(command string) (string, error) {
	cmd := exec.Command("bash", "-c", command)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// writeFile writes content to a local file with the given permissions.
func writeFile(path, content string, mode os.FileMode) error {
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
