package framework

// Default versions (fallback when project files don't specify) and Docker images.

const (
	DefaultGoVersion     = "1.22"
	DefaultPythonVersion = "3.12"
	DefaultNodeVersion   = "20"
)

const (
	ImageStatic      = "caddy:2-alpine"
	ImageRuntimeSlim = "debian:bookworm-slim"
)
