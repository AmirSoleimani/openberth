package framework

import (
	"os"
	"path/filepath"
)

type StaticProvider struct{}

func (p *StaticProvider) Language() string { return "static" }

func (p *StaticProvider) Detect(projectDir string) *FrameworkInfo {
	if _, err := os.Stat(filepath.Join(projectDir, "index.html")); err != nil {
		return nil
	}
	return &FrameworkInfo{
		Framework: "static",
		Language:  "static",
		Port:      80,
		Image:     ImageStatic,
	}
}

func (p *StaticProvider) BuildScript(_ *FrameworkInfo) string   { return "" }
func (p *StaticProvider) RunScript(_ *FrameworkInfo) string     { return "" }
func (p *StaticProvider) CacheVolumes(_ string) []string        { return nil }
func (p *StaticProvider) RebuildCopyScript() string             { return "" }
func (p *StaticProvider) SandboxEntrypoint(_ *FrameworkInfo, _ int) string { return "" }
func (p *StaticProvider) SandboxEnv() map[string]string         { return nil }
func (p *StaticProvider) StaticOnly() bool                      { return true }
