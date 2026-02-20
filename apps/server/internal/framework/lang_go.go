package framework

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type GoProvider struct{}

func (p *GoProvider) Language() string { return "go" }

func (p *GoProvider) Detect(projectDir string) *FrameworkInfo {
	if _, err := os.Stat(filepath.Join(projectDir, "go.mod")); err != nil {
		return nil
	}

	goVer := parseGoVersion(projectDir)
	image := "golang:" + goVer

	moduleName := parseGoModuleName(projectDir)
	binaryName := "server"
	if moduleName != "" {
		parts := strings.Split(moduleName, "/")
		binaryName = parts[len(parts)-1]
	}

	return &FrameworkInfo{
		Framework: "go",
		Language:  "go",
		BuildCmd:  "go build -o /app/bin/" + binaryName + " .",
		StartCmd:  "/app/bin/" + binaryName,
		DevCmd:    "go build -o /tmp/bin/server . && /tmp/bin/server",
		Port:      8080,
		Image:     image,
		RunImage:  ImageRuntimeSlim,
		CacheDir:  "",
		Env:       map[string]string{"GIN_MODE": "release"},
	}
}

func (p *GoProvider) BuildScript(fw *FrameworkInfo) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
echo "🏰 [build] Go project"

cd /app

# Copy code to build volume
cp -r /app/code/. /app/ 2>&1
echo "✓ Code copied"

# Check dependencies (modules are in shared cache volume)
OLD_HASH=""
NEW_HASH=""
if [ -f "/old/go.sum" ]; then
  OLD_HASH=$(md5sum "/old/go.sum" 2>/dev/null | cut -d' ' -f1)
fi
if [ -f "go.sum" ]; then
  NEW_HASH=$(md5sum "go.sum" 2>/dev/null | cut -d' ' -f1)
fi

if [ -n "$OLD_HASH" ] && [ "$OLD_HASH" = "$NEW_HASH" ]; then
  echo "⚡ Dependencies unchanged"
else
  echo "📦 Downloading modules..."
  go mod download 2>&1
  echo "✓ Modules downloaded"
fi

echo "🔨 Building..."
mkdir -p /app/bin
CGO_ENABLED=0 %s 2>&1
echo "✓ Binary built ($(du -sh /app/bin/ 2>/dev/null | cut -f1))"

rm -f /app/.openberth-build.sh /app/.openberth-run.sh
echo "🏰 [build] Complete"
`, fw.BuildCmd)
}

func (p *GoProvider) RunScript(fw *FrameworkInfo) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
echo "🏰 [run] Starting server..."
cd /app
exec %s
`, fw.StartCmd)
}

func (p *GoProvider) CacheVolumes(userID string) []string {
	suffix := ""
	if userID != "" {
		suffix = "-" + userID
	}
	return []string{
		"-v=openberth-go-mod" + suffix + ":/go/pkg/mod:rw",
		"-v=openberth-go-build" + suffix + ":/root/.cache/go-build:rw",
	}
}

func (p *GoProvider) RebuildCopyScript() string { return "" }

func (p *GoProvider) SandboxEntrypoint(fw *FrameworkInfo, port int) string {
	return `#!/bin/sh
set -e
cd /app

echo "🏰 [sandbox] Downloading Go modules..."
go mod download 2>&1

echo "🏰 [sandbox] Building..."
mkdir -p /tmp/bin
go build -o /tmp/bin/server . 2>&1

echo "🏰 [sandbox] Starting server..."
/tmp/bin/server &
SERVER_PID=$!

# Poll for source changes — rebuild when .go files are newer than binary
while true; do
  sleep 1
  if [ -n "$(find /app -name '*.go' -newer /tmp/bin/server 2>/dev/null | head -1)" ]; then
    echo "🏰 [sandbox] Changes detected, rebuilding..."
    if go build -o /tmp/bin/server . 2>&1; then
      kill $SERVER_PID 2>/dev/null && wait $SERVER_PID 2>/dev/null || true
      /tmp/bin/server &
      SERVER_PID=$!
      echo "🏰 [sandbox] Restarted."
    else
      echo "🏰 [sandbox] Build failed, keeping old binary."
    fi
  fi
done
`
}

func (p *GoProvider) SandboxEnv() map[string]string {
	return map[string]string{"GIN_MODE": "debug"}
}

func (p *GoProvider) StaticOnly() bool { return false }

// -- helpers --

func parseGoVersion(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return DefaultGoVersion
	}

	re := regexp.MustCompile(`(?m)^go\s+(1\.\d+)`)
	m := re.FindStringSubmatch(string(data))
	if len(m) >= 2 {
		return m[1]
	}
	return DefaultGoVersion
}

func parseGoModuleName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`(?m)^module\s+(\S+)`)
	m := re.FindStringSubmatch(string(data))
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}
