package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type CLIConfig struct {
	Server string `json:"server"`
	Key    string `json:"key"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".berth.json")
}

func loadCLIConfig() *CLIConfig {
	cfg := &CLIConfig{}
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	json.Unmarshal(data, cfg)
	return cfg
}

func saveCLIConfig(cfg *CLIConfig) error {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(configPath(), data, 0600)
}
