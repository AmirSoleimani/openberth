package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func cmdInit() {
	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Project Setup%s\n\n", cBold, cReset)

	projectDir, _ := filepath.Abs(getFlag("dir", "."))

	// Check for existing config
	existing := loadProjectConfig(projectDir)
	hasExisting := existing.DeploymentID != "" || existing.SandboxID != "" || existing.Name != ""

	if hasExisting {
		if !promptYesNo("Existing .berth.json found. Reinitialize?", true) {
			info("Cancelled")
			fmt.Println()
			return
		}
		fmt.Println()
	}

	// Detect framework for display
	framework := detectFrameworkHint(projectDir)
	if framework != "Unknown" {
		ok(fmt.Sprintf("Detected: %s%s%s", cBold, framework, cReset))
		fmt.Println()
	}

	// Name
	defaultName := filepath.Base(projectDir)
	if existing.Name != "" {
		defaultName = existing.Name
	}
	fmt.Printf("  %sWhat should your deployment be called?%s\n", cBold, cReset)
	fmt.Printf("  %sThis becomes the subdomain: <name>.yourdomain.com%s\n", cDim, cReset)
	name := promptString("Name", defaultName)
	fmt.Println()

	// TTL
	ttlChoices := []string{
		"24 hours",
		"3 days (default)",
		"7 days",
		"Forever (no expiration)",
	}
	ttlValues := []string{"24h", "72h", "7d", "0"}
	defaultTTL := 1 // 3 days
	if existing.TTL != "" {
		for i, v := range ttlValues {
			if v == existing.TTL {
				defaultTTL = i
				break
			}
		}
	}
	fmt.Printf("  %sHow long should deployments stay alive?%s\n", cBold, cReset)
	ttlIdx := promptChoice("", ttlChoices, defaultTTL)
	ttl := ttlValues[ttlIdx]
	fmt.Println()

	// Memory
	memChoices := []string{
		"256 MB  — lightweight static sites",
		"512 MB  — most apps (default)",
		"1 GB    — larger Node/Python apps",
		"2 GB    — heavy builds, large datasets",
	}
	memValues := []string{"256m", "512m", "1g", "2g"}
	defaultMem := 1 // 512m
	if existing.Memory != "" {
		for i, v := range memValues {
			if v == existing.Memory {
				defaultMem = i
				break
			}
		}
	}
	fmt.Printf("  %sHow much memory does your app need?%s\n", cBold, cReset)
	memIdx := promptChoice("", memChoices, defaultMem)
	memory := memValues[memIdx]
	fmt.Println()

	// Port
	defaultPort := existing.Port
	fmt.Printf("  %sDoes your app listen on a specific port?%s\n", cBold, cReset)
	fmt.Printf("  %sLeave blank for auto-detect (recommended)%s\n", cDim, cReset)
	port := promptString("Port", defaultPort)
	fmt.Println()

	// Protection
	protectChoices := []string{
		"Public — anyone can view (default)",
		"Password protected (basic auth)",
		"API key required",
		"OpenBerth users only",
	}
	protectValues := []string{"", "basic_auth", "api_key", "user"}
	defaultProtect := 0
	if existing.Protect != "" {
		for i, v := range protectValues {
			if v == existing.Protect {
				defaultProtect = i
				break
			}
		}
	}
	fmt.Printf("  %sShould your deployment be access-protected?%s\n", cBold, cReset)
	protectIdx := promptChoice("", protectChoices, defaultProtect)
	protect := protectValues[protectIdx]
	fmt.Println()

	// Build config — preserve existing deployment/sandbox IDs
	cfg := &ProjectConfig{
		Name:         name,
		TTL:          ttl,
		Memory:       memory,
		Port:         port,
		Protect:      protect,
		DeploymentID: existing.DeploymentID,
		URL:          existing.URL,
		SandboxID:    existing.SandboxID,
	}

	if err := saveProjectConfig(projectDir, cfg); err != nil {
		fail("Failed to save config: " + err.Error())
		os.Exit(1)
	}

	ok("Saved .berth.json")
	info("Tip: add .berth.json to .gitignore")
	info(fmt.Sprintf("Deploy with: %sberth deploy%s", cDim, cReset))
	fmt.Println()
}
