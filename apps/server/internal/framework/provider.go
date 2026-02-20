package framework

// FrameworkInfo describes the detected language, framework, and build/run commands for a project.
type FrameworkInfo struct {
	Framework string            `json:"framework"`
	Language  string            `json:"language"` // "node", "go", "python"
	BuildCmd  string            `json:"buildCmd"`
	StartCmd  string            `json:"startCmd"`
	Port      int               `json:"port"`
	Image     string            `json:"image"`    // build image
	RunImage  string            `json:"runImage"` // runtime image (empty = same as Image)
	CacheDir  string            `json:"cacheDir"` // what to copy on rebuild (e.g. "node_modules", "target")
	Env       map[string]string `json:"env,omitempty"`
	DevCmd    string            `json:"devCmd,omitempty"` // dev server command (sandbox mode)
}

// LanguageProvider encapsulates all language-specific logic: detection, build/run
// scripts, caching, and sandbox configuration. Each language implements this
// interface in its own lang_*.go file.
type LanguageProvider interface {
	Language() string                                     // "go", "node", "python", "static"
	Detect(projectDir string) *FrameworkInfo              // nil = not this language
	BuildScript(fw *FrameworkInfo) string                 // shell script for build phase
	RunScript(fw *FrameworkInfo) string                   // shell script for runtime phase
	CacheVolumes(userID string) []string                  // Docker -v flags for build caching
	RebuildCopyScript() string                            // shell to copy cache between volumes
	SandboxEntrypoint(fw *FrameworkInfo, port int) string // dev server entrypoint script
	SandboxEnv() map[string]string                        // sandbox-specific env var overrides
	StaticOnly() bool                                     // true = skip build/run, serve files directly
}

// registry holds all registered language providers in detection priority order.
var registry []LanguageProvider

// Register adds a provider to the registry.
func Register(p LanguageProvider) { registry = append(registry, p) }

// GetProvider returns the provider for the given language string, or nil.
func GetProvider(language string) LanguageProvider {
	for _, p := range registry {
		if p.Language() == language {
			return p
		}
	}
	return nil
}

// DetectFramework iterates providers in registration order (first match wins).
func DetectFramework(projectDir string) *FrameworkInfo {
	for _, p := range registry {
		if info := p.Detect(projectDir); info != nil {
			return info
		}
	}
	return nil
}

func init() {
	Register(&GoProvider{})
	Register(&PythonProvider{})
	Register(&NodeProvider{})
	Register(&StaticProvider{}) // most generic — must be last
}
