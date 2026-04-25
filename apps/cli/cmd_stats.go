package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func cmdStats() {
	projectDir, _ := filepath.Abs(getFlag("dir", "."))
	id, _ := resolveDeploymentID(projectDir)
	if id == "" {
		fail("No deployment ID. Pass as argument or run 'berth init' + 'berth deploy' first.")
		os.Exit(1)
	}

	client, err := NewAPIClient()
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	result, err := client.Request("GET", "/api/deployments/"+id+"/stats")
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	if hasFlag("json") {
		printJSON(result)
		return
	}

	live, _ := result["live"].(map[string]any)
	storage, _ := result["storage"].(map[string]any)
	network, _ := result["network"].(map[string]any)

	cpuPct := numFloat(live["cpuPercent"])
	memBytes := numInt(live["memoryBytes"])
	memLimit := numInt(live["memoryLimitBytes"])
	pids := numInt(live["pids"])

	srcBytes := numInt(storage["sourceBytes"])
	persistBytes := numInt(storage["persistBytes"])
	buildBytes := numInt(storage["buildVolumeBytes"])
	totalBytes := numInt(storage["totalBytes"])

	usedNet := numInt(network["usedBytes"])
	quotaNet := numInt(network["quotaBytes"])
	remainingNet := numInt(network["remainingBytes"])
	periodStart, _ := network["periodStart"].(string)
	resetH := numInt(network["periodResetIntervalH"])

	fmt.Println()
	fmt.Printf("  %sDeployment:%s  %v\n", cBold, cReset, result["id"])
	fmt.Printf("  %sStatus:%s      %v\n", cBold, cReset, result["status"])
	fmt.Println()
	fmt.Printf("  %sLive%s\n", cBold, cReset)
	fmt.Printf("    CPU:        %s\n", colorPct(cpuPct, "%.2f%%", cpuPct))
	if memLimit > 0 {
		memPct := float64(memBytes) / float64(memLimit) * 100
		fmt.Printf("    Memory:     %s / %s (%s)\n", formatBytes(memBytes), formatBytes(memLimit), colorPct(memPct, "%.1f%%", memPct))
	} else {
		fmt.Printf("    Memory:     %s\n", formatBytes(memBytes))
	}
	if pids > 0 {
		fmt.Printf("    Processes:  %d\n", pids)
	}
	fmt.Println()
	fmt.Printf("  %sStorage%s     Total: %s\n", cBold, cReset, formatBytes(totalBytes))
	fmt.Printf("    Source:     %s\n", formatBytes(srcBytes))
	fmt.Printf("    Persist:    %s\n", formatBytes(persistBytes))
	fmt.Printf("    Build vol:  %s\n", formatBytes(buildBytes))
	fmt.Println()
	fmt.Printf("  %sNetwork%s     (period started %s, resets every %dh)\n", cBold, cReset, periodStart, resetH)
	if quotaNet > 0 {
		netPct := float64(usedNet) / float64(quotaNet) * 100
		fmt.Printf("    Used:       %s / %s (%s)\n", formatBytes(usedNet), formatBytes(quotaNet), colorPct(netPct, "%.1f%%", netPct))
		fmt.Printf("    Remaining:  %s\n", formatBytes(remainingNet))
	} else {
		fmt.Printf("    Used:       %s (no quota)\n", formatBytes(usedNet))
	}

	if recent, ok := network["recentPeriods"].([]any); ok && len(recent) > 0 {
		fmt.Printf("    History:\n")
		for _, p := range recent {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			ps, _ := pm["periodStart"].(string)
			fmt.Printf("      %s  %s\n", ps, formatBytes(numInt(pm["bytesOut"])))
		}
	}
	fmt.Println()
}

func cmdHostStats() {
	client, err := NewAPIClient()
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	result, err := client.Request("GET", "/api/admin/host-stats")
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	if hasFlag("json") {
		printJSON(result)
		return
	}

	cpuPct := numFloat(result["cpuPercent"])
	cpuCores := numInt(result["cpuCores"])
	memTotal := numInt(result["memoryTotal"])
	memUsed := numInt(result["memoryUsed"])
	memFree := numInt(result["memoryFree"])
	diskTotal := numInt(result["diskTotal"])
	diskUsed := numInt(result["diskUsed"])
	diskFree := numInt(result["diskFree"])
	load1 := numFloat(result["loadAvg1"])
	load5 := numFloat(result["loadAvg5"])
	load15 := numFloat(result["loadAvg15"])
	uptime := numInt(result["uptimeSeconds"])
	sampledAt, _ := result["sampledAt"].(string)

	fmt.Println()
	fmt.Printf("  %sHost Stats%s   sampled at %s\n", cBold, cReset, sampledAt)
	fmt.Println()
	fmt.Printf("  %sCPU%s         %s  (%d %s)\n", cBold, cReset, colorPct(cpuPct, "%.2f%%", cpuPct), cpuCores, plural(cpuCores, "core", "cores"))
	fmt.Println()
	if memTotal > 0 {
		memPct := float64(memUsed) / float64(memTotal) * 100
		fmt.Printf("  %sMemory%s      %s / %s (%s)\n", cBold, cReset, formatBytes(memUsed), formatBytes(memTotal), colorPct(memPct, "%.1f%%", memPct))
		fmt.Printf("              %s free\n", formatBytes(memFree))
	}
	fmt.Println()
	if diskTotal > 0 {
		diskPct := float64(diskUsed) / float64(diskTotal) * 100
		fmt.Printf("  %sDisk%s        %s / %s (%s)\n", cBold, cReset, formatBytes(diskUsed), formatBytes(diskTotal), colorPct(diskPct, "%.1f%%", diskPct))
		fmt.Printf("              %s free\n", formatBytes(diskFree))
	}
	fmt.Println()
	fmt.Printf("  %sLoad avg%s    %.2f  %.2f  %.2f  (1m / 5m / 15m)\n", cBold, cReset, load1, load5, load15)
	if cpuCores > 0 {
		fmt.Printf("              per-core: %.2f (1m)\n", load1/float64(cpuCores))
	}
	fmt.Println()
	fmt.Printf("  %sUptime%s      %s\n", cBold, cReset, formatUptime(uptime))
	fmt.Println()
}

// ── helpers ─────────────────────────────────────────────────────────

func numFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func numInt(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

func formatBytes(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	const unit = 1024.0
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(b)
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", b, units[0])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func formatUptime(secs int64) string {
	if secs <= 0 {
		return "—"
	}
	d := secs / 86400
	h := (secs % 86400) / 3600
	m := (secs % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func colorPct(pct float64, format string, args ...any) string {
	color := cGreen
	if pct >= 90 {
		color = cRed
	} else if pct >= 70 {
		color = cYellow
	}
	return color + fmt.Sprintf(format, args...) + cReset
}

func plural(n int64, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
