package docker

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/runtime"
)

// Stats returns an instantaneous resource snapshot for the running
// container backing instanceID. It does not stream — every call shells
// out to `docker stats --no-stream` once. Cheap (~50ms) but not free,
// so callers shouldn't poll faster than a few Hz.
//
// When the container isn't running the driver returns zero-valued
// LiveStats with no error: the caller can render "0% / 0 B" in the UI
// without special-casing build-in-progress or stopped instances.
func (d *Driver) Stats(instanceID string) (runtime.LiveStats, error) {
	name := "sc-" + instanceID

	// `docker stats --no-stream --format '{{json .}}'` prints one line per
	// container. We name a single container so the output is one line of
	// JSON.
	out, err := execCmd("docker", "stats", "--no-stream", "--format", "{{json .}}", name)
	if err != nil {
		// Container missing or stopped: return zeros, not an error.
		// `docker stats` returns non-zero for unknown containers.
		return runtime.LiveStats{}, nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return runtime.LiveStats{}, nil
	}

	var raw struct {
		CPUPerc  string `json:"CPUPerc"`
		MemUsage string `json:"MemUsage"`
		PIDs     string `json:"PIDs"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return runtime.LiveStats{}, fmt.Errorf("parse docker stats output: %w", err)
	}

	stats := runtime.LiveStats{
		CPUPercent: parseDockerPercent(raw.CPUPerc),
		PIDs:       parseDockerInt(raw.PIDs),
	}
	stats.MemoryBytes, stats.MemoryLimitBytes = parseDockerMemUsage(raw.MemUsage)

	// Build-volume size is fetched via a follow-up `du -sb /app` exec inside
	// the running container. Cheaper than `docker system df -v` (which
	// scans every volume on the host) and works regardless of host mount
	// access. Bounded timeout so a frozen container doesn't stall stats.
	if vol, err := d.containerDiskUsage(instanceID, "/app", 5*time.Second); err == nil {
		stats.BuildVolumeBytes = vol
	}

	return stats, nil
}

// containerDiskUsage runs `du -sb <path>` inside a container and parses
// the byte count. Returns (0, error) when the exec fails (e.g. container
// not running, du not installed).
func (d *Driver) containerDiskUsage(instanceID, path string, timeout time.Duration) (int64, error) {
	res, err := d.Exec(instanceID, "du -sb "+path+" 2>/dev/null | head -1", timeout)
	if err != nil || res.ExitCode != 0 {
		return 0, fmt.Errorf("du failed (exit %d): %v", res.ExitCode, err)
	}
	// Output shape: "12345\t/app". Take the first whitespace-separated field.
	line := strings.TrimSpace(res.Output)
	if line == "" {
		return 0, fmt.Errorf("empty du output")
	}
	field := strings.Fields(line)[0]
	return strconv.ParseInt(field, 10, 64)
}

// parseDockerPercent strips a trailing "%" and parses the float.
// "12.34%" → 12.34. Returns 0 on parse failure.
func parseDockerPercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseDockerInt parses a whitespace-trimmed int. Returns 0 on failure.
func parseDockerInt(s string) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return v
}

// parseDockerMemUsage splits "123.4MiB / 512MiB" into (used, limit) bytes.
// Either side may be "--" when the container is stopped — those return 0.
func parseDockerMemUsage(s string) (int64, int64) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return 0, 0
	}
	used := parseDockerSize(parts[0])
	limit := parseDockerSize(parts[1])
	return used, limit
}

// parseDockerSize converts a docker-formatted size ("12.34MiB", "5GB",
// "1.2kB") into bytes. Docker uses both binary (MiB) and decimal (MB)
// suffixes; we accept both and treat them as binary multiples (this
// matches docker's own internal bookkeeping where MiB is the truth and
// MB is just an alias in the output formatter).
func parseDockerSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "--" || s == "0" || s == "0B" {
		return 0
	}
	// Find the split between number and unit.
	end := 0
	for end < len(s) {
		c := s[end]
		if (c >= '0' && c <= '9') || c == '.' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0
	}
	num, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToUpper(strings.TrimSpace(s[end:]))
	var mult float64
	switch unit {
	case "B":
		mult = 1
	case "KB", "KIB", "K":
		mult = 1024
	case "MB", "MIB", "M":
		mult = 1024 * 1024
	case "GB", "GIB", "G":
		mult = 1024 * 1024 * 1024
	case "TB", "TIB", "T":
		mult = 1024 * 1024 * 1024 * 1024
	default:
		mult = 1
	}
	return int64(num * mult)
}
