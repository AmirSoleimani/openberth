package mcphandler

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuideContentInSync diffs this package's guide.go against the
// standalone bridge's apps/mcp/guide.go. The two files are duplicated
// verbatim per the "shared logic by copy, not import" rule (CLAUDE.md)
// — modules can't import each other in this repo. Without this guard,
// updating only one side ships divergent guidance to AI clients.
//
// The comparison strips the `package` line (different packages on
// either side: mcphandler vs main) and hashes the rest. To resync
// after an intentional content change, copy this file's body to
// apps/mcp/guide.go (only the package line should differ) and re-run.
func TestGuideContentInSync(t *testing.T) {
	t.Helper()

	thisFile := mustReadGuide(t, "guide.go")
	standalone := mustReadGuide(t, filepath.Join("..", "..", "..", "..", "mcp", "guide.go"))

	if h1, h2 := hashStripped(thisFile), hashStripped(standalone); h1 != h2 {
		t.Fatalf(`guide.go drift detected between server-side and standalone MCP modules.

  server-side: apps/server/internal/httphandler/mcp/guide.go (sha256 sans package line: %s)
  standalone:  apps/mcp/guide.go                              (sha256 sans package line: %s)

The two files must contain byte-identical content except for their `+"`package`"+` line.
Resync by copying this file's body to apps/mcp/guide.go.`,
			h1, h2)
	}
}

func mustReadGuide(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// hashStripped returns the SHA-256 of the file content with the leading
// `package <name>` line removed, so the same canonical content gives the
// same hash whether it's in package `mcphandler` or `main`.
func hashStripped(content string) string {
	lines := strings.SplitN(content, "\n", 2)
	stripped := content
	if len(lines) == 2 && strings.HasPrefix(lines[0], "package ") {
		stripped = lines[1]
	}
	sum := sha256.Sum256([]byte(stripped))
	return hex.EncodeToString(sum[:])
}
