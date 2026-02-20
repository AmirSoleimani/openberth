package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── Project config ─────────────────────────────────────────────────

const projectConfigFile = ".berth.json"

type ProjectConfig struct {
	Name         string `json:"name,omitempty"`
	TTL          string `json:"ttl,omitempty"`
	Memory       string `json:"memory,omitempty"`
	Port         string `json:"port,omitempty"`
	Protect      string `json:"protect,omitempty"`
	NetworkQuota string `json:"networkQuota,omitempty"`
	DeploymentID string `json:"deploymentId,omitempty"`
	URL          string `json:"url,omitempty"`
	SandboxID    string `json:"sandboxId,omitempty"`
}

func projectConfigPath(dir string) string {
	return filepath.Join(dir, projectConfigFile)
}

func loadProjectConfig(dir string) *ProjectConfig {
	cfg := &ProjectConfig{}
	data, err := os.ReadFile(projectConfigPath(dir))
	if err != nil {
		return cfg
	}
	json.Unmarshal(data, cfg)
	return cfg
}

func saveProjectConfig(dir string, cfg *ProjectConfig) error {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	data = append(data, '\n')
	return os.WriteFile(projectConfigPath(dir), data, 0644)
}

// resolveDeploymentID tries to find a deployment ID from:
// 1. First positional arg after command (if not a --flag)
// 2. Project config deploymentId
// Returns (id, source) where source is "arg" or "project".
func resolveDeploymentID(projectDir string) (string, string) {
	// Check positional args: args[0] is the command, args[1+] are arguments
	if len(os.Args) >= 3 {
		candidate := os.Args[2]
		if !strings.HasPrefix(candidate, "--") {
			return candidate, "arg"
		}
	}

	pCfg := loadProjectConfig(projectDir)
	if pCfg.DeploymentID != "" {
		return pCfg.DeploymentID, "project"
	}

	return "", ""
}

// resolveSandboxID tries to find a sandbox ID from:
// 1. First positional arg after command (if not a --flag)
// 2. Project config sandboxId
func resolveSandboxID(projectDir string) (string, string) {
	if len(os.Args) >= 3 {
		candidate := os.Args[2]
		if !strings.HasPrefix(candidate, "--") {
			return candidate, "arg"
		}
	}

	pCfg := loadProjectConfig(projectDir)
	if pCfg.SandboxID != "" {
		return pCfg.SandboxID, "project"
	}

	return "", ""
}

// ── Interactive prompts ────────────────────────────────────────────

var stdinScanner *bufio.Scanner

func getStdinScanner() *bufio.Scanner {
	if stdinScanner == nil {
		stdinScanner = bufio.NewScanner(os.Stdin)
	}
	return stdinScanner
}

// promptString shows a question and reads free text input with a default.
// Returns default immediately in non-interactive (--yes) mode.
func promptString(question, defaultVal string) string {
	if hasFlag("yes") {
		return defaultVal
	}

	if defaultVal != "" {
		fmt.Printf("  %s%s%s [%s]: ", cBold, question, cReset, defaultVal)
	} else {
		fmt.Printf("  %s%s%s: ", cBold, question, cReset)
	}

	scanner := getStdinScanner()
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			return input
		}
	}
	return defaultVal
}

// promptChoice shows numbered choices and returns the selected index (0-based).
// defaultIdx is 0-based.
func promptChoice(question string, choices []string, defaultIdx int) int {
	if hasFlag("yes") {
		return defaultIdx
	}

	fmt.Printf("  %s%s%s\n", cBold, question, cReset)
	for i, c := range choices {
		marker := "  "
		if i == defaultIdx {
			marker = "> "
		}
		fmt.Printf("  %s%s%d. %s%s\n", cDim, marker, i+1, c, cReset)
	}
	fmt.Printf("  Choice [%d]: ", defaultIdx+1)

	scanner := getStdinScanner()
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			// Parse number
			var choice int
			if _, err := fmt.Sscanf(input, "%d", &choice); err == nil {
				if choice >= 1 && choice <= len(choices) {
					return choice - 1
				}
			}
		}
	}
	return defaultIdx
}

// promptYesNo shows a y/n question and returns the answer.
func promptYesNo(question string, defaultYes bool) bool {
	if hasFlag("yes") {
		return defaultYes
	}

	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	fmt.Printf("  %s%s%s [%s]: ", cBold, question, cReset, hint)

	scanner := getStdinScanner()
	if scanner.Scan() {
		input := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if input == "y" || input == "yes" {
			return true
		}
		if input == "n" || input == "no" {
			return false
		}
	}
	return defaultYes
}
