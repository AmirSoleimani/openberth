// Package sysstats samples host-level resource usage by reading the
// Linux procfs and a single statfs syscall. No external dependencies,
// no CGO. Linux-only — every OpenBerth host runs Linux because the
// runtime depends on Docker + gVisor.
package sysstats

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// HostStats is the snapshot returned by Sample. Values are absolute
// (bytes, seconds) where appropriate; CPU is a normalised percent.
type HostStats struct {
	CPUPercent     float64   `json:"cpuPercent"`     // 0..100, all cores combined
	CPUCores       int       `json:"cpuCores"`       // count of logical CPUs
	MemoryTotal    int64     `json:"memoryTotal"`    // bytes
	MemoryUsed     int64     `json:"memoryUsed"`     // total - available, bytes
	MemoryFree     int64     `json:"memoryFree"`     // available, bytes
	DiskTotal      int64     `json:"diskTotal"`      // bytes on the partition holding DataDir
	DiskUsed       int64     `json:"diskUsed"`       // bytes
	DiskFree       int64     `json:"diskFree"`       // bytes
	LoadAvg1       float64   `json:"loadAvg1"`       // 1-minute load
	LoadAvg5       float64   `json:"loadAvg5"`       // 5-minute load
	LoadAvg15      float64   `json:"loadAvg15"`      // 15-minute load
	UptimeSeconds  int64     `json:"uptimeSeconds"`  // host uptime
	SampledAt      time.Time `json:"sampledAt"`
}

// Sample reads /proc + statfs to produce a host snapshot. dataDir is the
// OpenBerth data directory; disk usage is reported for the partition
// that holds it (typically the same as `/var` or `/`).
//
// The CPU sample is taken across two reads of /proc/stat ~100ms apart
// to compute a delta. Total wall time is dominated by that sleep.
func Sample(dataDir string) (HostStats, error) {
	var s HostStats
	s.SampledAt = time.Now().UTC()

	cpu, cores, err := sampleCPU(100 * time.Millisecond)
	if err != nil {
		return s, fmt.Errorf("cpu: %w", err)
	}
	s.CPUPercent = cpu
	s.CPUCores = cores

	mem, err := readMemInfo()
	if err != nil {
		return s, fmt.Errorf("memory: %w", err)
	}
	s.MemoryTotal = mem.total
	s.MemoryFree = mem.available
	s.MemoryUsed = mem.total - mem.available

	disk, err := readDisk(dataDir)
	if err != nil {
		return s, fmt.Errorf("disk: %w", err)
	}
	s.DiskTotal = disk.total
	s.DiskUsed = disk.used
	s.DiskFree = disk.free

	if l1, l5, l15, err := readLoadAvg(); err == nil {
		s.LoadAvg1, s.LoadAvg5, s.LoadAvg15 = l1, l5, l15
	}
	if up, err := readUptime(); err == nil {
		s.UptimeSeconds = up
	}

	return s, nil
}

// ── CPU ────────────────────────────────────────────────────────────

// sampleCPU reads /proc/stat twice with `interval` between, computes
// the busy-vs-total delta, and returns busy% and the number of logical
// CPU cores seen.
func sampleCPU(interval time.Duration) (float64, int, error) {
	t1, _, err := readProcStat()
	if err != nil {
		return 0, 0, err
	}
	time.Sleep(interval)
	t2, cores, err := readProcStat()
	if err != nil {
		return 0, 0, err
	}
	totalDelta := t2.total - t1.total
	idleDelta := t2.idle - t1.idle
	if totalDelta <= 0 {
		return 0, cores, nil
	}
	pct := float64(totalDelta-idleDelta) * 100.0 / float64(totalDelta)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, cores, nil
}

type cpuTimes struct {
	total int64 // sum of every field on the "cpu" aggregate line
	idle  int64 // idle + iowait
}

// readProcStat parses /proc/stat:
//   cpu  user nice system idle iowait irq softirq steal guest guest_nice
//   cpu0 …   (one per core)
// We sum the aggregate-line numbers for total and idle, and count the
// "cpuN " lines for core count.
func readProcStat() (cpuTimes, int, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuTimes{}, 0, err
	}
	defer f.Close()

	var t cpuTimes
	cores := 0
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		if !strings.HasPrefix(line, "cpu") {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// "cpu" (length 3) is the aggregate; "cpu0", "cpu1", ... are per-core.
		if fields[0] == "cpu" {
			var sum int64
			for _, f := range fields[1:] {
				v, err := strconv.ParseInt(f, 10, 64)
				if err != nil {
					continue
				}
				sum += v
			}
			t.total = sum
			// idle = field 4, iowait = field 5 (1-indexed past the label).
			if len(fields) > 5 {
				idle, _ := strconv.ParseInt(fields[4], 10, 64)
				iowait, _ := strconv.ParseInt(fields[5], 10, 64)
				t.idle = idle + iowait
			}
		} else {
			cores++
		}
	}
	return t, cores, scan.Err()
}

// ── Memory ─────────────────────────────────────────────────────────

type memInfo struct {
	total, available int64 // bytes
}

// readMemInfo parses /proc/meminfo. Only MemTotal and MemAvailable
// matter for the snapshot — MemFree understates because it doesn't
// count reclaimable cache.
func readMemInfo() (memInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return memInfo{}, err
	}
	defer f.Close()

	var info memInfo
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Values are in KiB; the unit suffix is always " kB".
		v, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		bytes := v * 1024
		switch fields[0] {
		case "MemTotal:":
			info.total = bytes
		case "MemAvailable:":
			info.available = bytes
		}
	}
	if info.total == 0 {
		return info, fmt.Errorf("MemTotal missing from /proc/meminfo")
	}
	return info, nil
}

// ── Disk ───────────────────────────────────────────────────────────

type diskInfo struct {
	total, used, free int64
}

// readDisk uses statfs(2) on the data dir's partition. Reported sizes
// are based on the available-to-non-root figure (Bavail) so the user
// sees what their tenants can actually consume, not the reserved-root
// extra ~5%.
func readDisk(path string) (diskInfo, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return diskInfo{}, err
	}
	bsize := int64(st.Bsize)
	total := int64(st.Blocks) * bsize
	free := int64(st.Bavail) * bsize
	used := total - free
	if used < 0 {
		used = 0
	}
	return diskInfo{total: total, used: used, free: free}, nil
}

// ── Load average ───────────────────────────────────────────────────

func readLoadAvg() (float64, float64, float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("malformed /proc/loadavg")
	}
	l1, _ := strconv.ParseFloat(fields[0], 64)
	l5, _ := strconv.ParseFloat(fields[1], 64)
	l15, _ := strconv.ParseFloat(fields[2], 64)
	return l1, l5, l15, nil
}

// ── Uptime ─────────────────────────────────────────────────────────

func readUptime() (int64, error) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 1 {
		return 0, fmt.Errorf("malformed /proc/uptime")
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	return int64(v), nil
}
