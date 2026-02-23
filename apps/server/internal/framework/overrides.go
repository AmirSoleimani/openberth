package framework

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// BerthConfig holds the server-side override fields from .berth.json.
// These coexist with CLI fields (name, ttl, memory, port, etc.) in the same file.
type BerthConfig struct {
	Language string `json:"language"` // "node", "python", "go", "static"
	Build    string `json:"build"`    // overrides FrameworkInfo.BuildCmd
	Start    string `json:"start"`    // overrides FrameworkInfo.StartCmd
	Install  string `json:"install"`  // overrides install step in provider build scripts
	Dev      string `json:"dev"`      // overrides FrameworkInfo.DevCmd (sandbox mode)
}

// ReadBerthConfig reads and parses .berth.json from codeDir.
// Returns nil if the file doesn't exist or has no server-side override fields.
func ReadBerthConfig(codeDir string) *BerthConfig {
	data, err := os.ReadFile(filepath.Join(codeDir, ".berth.json"))
	if err != nil {
		return nil
	}

	var cfg BerthConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}

	// Only return if at least one override field is set
	if cfg.Language == "" && cfg.Build == "" && cfg.Start == "" && cfg.Install == "" && cfg.Dev == "" {
		return nil
	}

	return &cfg
}

// ApplyOverrides patches non-empty override fields from cfg onto a detected FrameworkInfo.
func ApplyOverrides(fw *FrameworkInfo, cfg *BerthConfig) {
	if cfg == nil {
		return
	}
	if cfg.Build != "" {
		fw.BuildCmd = cfg.Build
	}
	if cfg.Start != "" {
		fw.StartCmd = cfg.Start
	}
	if cfg.Install != "" {
		fw.InstallCmd = cfg.Install
	}
	if cfg.Dev != "" {
		fw.DevCmd = cfg.Dev
	}
}

// DetectWithOverrides runs framework detection with .berth.json override support.
// Two modes:
//  1. Detection succeeds + overrides → patch specific FrameworkInfo fields
//  2. Detection fails + .berth.json has language + start → construct from DefaultsForLanguage + overrides
func DetectWithOverrides(codeDir string) *FrameworkInfo {
	cfg := ReadBerthConfig(codeDir)

	// If config forces a specific language, try that provider first
	if cfg != nil && cfg.Language != "" {
		p := GetProvider(cfg.Language)
		if p != nil {
			if fw := p.Detect(codeDir); fw != nil {
				ApplyOverrides(fw, cfg)
				return fw
			}
		}
		// Provider didn't detect anything, but we have language + start — use defaults
		if cfg.Start != "" {
			fw := DefaultsForLanguage(cfg.Language)
			if fw != nil {
				ApplyOverrides(fw, cfg)
				return fw
			}
		}
	}

	// Normal detection path
	fw := DetectFramework(codeDir)
	if fw != nil {
		ApplyOverrides(fw, cfg)
		return fw
	}

	// Detection failed — try fallback if config has language + start
	if cfg != nil && cfg.Language != "" && cfg.Start != "" {
		fw := DefaultsForLanguage(cfg.Language)
		if fw != nil {
			ApplyOverrides(fw, cfg)
			return fw
		}
	}

	return nil
}
