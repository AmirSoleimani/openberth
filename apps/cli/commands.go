package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ── Framework hint ─────────────────────────────────────────────────

func detectFrameworkHint(dir string) string {
	// Go
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "Go"
	}
	// Python
	for _, f := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "app.py", "main.py", "manage.py"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return "Python"
		}
	}
	// Node/Frontend
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			return "Static HTML"
		}
		return "Unknown"
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	json.Unmarshal(data, &pkg)

	deps := make(map[string]bool)
	for k := range pkg.Dependencies {
		deps[k] = true
	}
	for k := range pkg.DevDependencies {
		deps[k] = true
	}

	switch {
	case deps["next"]:
		return "Next.js"
	case deps["nuxt"]:
		return "Nuxt"
	case deps["@sveltejs/kit"]:
		return "SvelteKit"
	case deps["vite"]:
		return "Vite"
	case deps["react-scripts"]:
		return "Create React App"
	case deps["@vue/cli-service"]:
		return "Vue CLI"
	case deps["@angular/core"]:
		return "Angular"
	default:
		return "Node.js"
	}
}

// ── Commands ────────────────────────────────────────────────────────

func cmdDeploy() {
	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Deploy%s\n\n", cBold, cReset)

	name := getFlag("name", "")
	ttl := getFlag("ttl", "")
	envVars := getFlags("env")
	envFile := getFlag("env-file", "")
	memory := getFlag("memory", "")
	cpus := getFlag("cpus", "")
	port := getFlag("port", "")
	title := getFlag("title", "")
	description := getFlag("description", "")
	protectMode := getFlag("protect", "")
	protectUser := getFlag("username", "")
	protectPass := getFlag("password", "")
	protectKey := getFlag("api-key", "")
	protectUsers := getFlag("users", "")
	networkQuota := getFlag("network-quota", "")

	// Determine what we're deploying: single file or directory
	var projectDir string
	var cleanup func()

	target := getDeployTarget()

	if target != "" && IsSingleFile(target) {
		// Single file deploy — scaffold into a temp project
		absPath, _ := filepath.Abs(target)
		spin("Scaffolding " + filepath.Base(absPath))

		result, err := ScaffoldSingleFile(absPath)
		if err != nil {
			done()
			fail(err.Error())
			os.Exit(1)
		}
		done()
		cleanup = result.Cleanup
		projectDir = result.Dir

		ok(fmt.Sprintf("Auto-scaffolded: %s%s%s", cBold, result.Framework, cReset))

		if name == "" {
			name = strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
		}
	} else {
		// Directory deploy (existing behavior)
		dir := getFlag("dir", ".")
		if target != "" {
			dir = target
		}
		projectDir, _ = filepath.Abs(dir)

		if _, err := os.Stat(projectDir); err != nil {
			fail("Directory not found: " + projectDir)
			os.Exit(1)
		}

		framework := detectFrameworkHint(projectDir)
		ok(fmt.Sprintf("Detected: %s%s%s", cBold, framework, cReset))

		if name == "" {
			name = filepath.Base(projectDir)
		}
	}

	if cleanup != nil {
		defer cleanup()
	}

	// Load project config for fallback defaults
	pCfg := loadProjectConfig(projectDir)
	if name == filepath.Base(projectDir) && pCfg.Name != "" {
		name = pCfg.Name
	}
	if ttl == "" {
		ttl = pCfg.TTL
	}
	if memory == "" {
		memory = pCfg.Memory
	}
	if port == "" {
		port = pCfg.Port
	}
	if protectMode == "" {
		protectMode = pCfg.Protect
	}
	if networkQuota == "" {
		networkQuota = pCfg.NetworkQuota
	}

	// Load env vars from files
	// 1. Auto-load .env from project dir (if not a scaffolded single file)
	if cleanup == nil {
		autoEnv := filepath.Join(projectDir, ".env")
		if vars, err := parseEnvFile(autoEnv); err == nil && len(vars) > 0 {
			envVars = append(vars, envVars...) // explicit --env overrides .env
			ok(fmt.Sprintf("Loaded %d vars from .env", len(vars)))
		}
	}
	// 2. Explicit --env-file flag (highest priority)
	if envFile != "" {
		vars, err := parseEnvFile(envFile)
		if err != nil {
			fail("Cannot read env file: " + err.Error())
			os.Exit(1)
		}
		envVars = append(envVars, vars...) // --env-file overrides .env
		ok(fmt.Sprintf("Loaded %d vars from %s", len(vars), envFile))
	}

	// Create tarball
	spin("Compressing project")
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("openberth-%d.tar.gz", os.Getpid()))
	defer os.Remove(tmpFile)

	fileCount, err := createTarball(projectDir, tmpFile)
	if err != nil {
		done()
		fail("Failed to create archive: " + err.Error())
		os.Exit(1)
	}
	done()

	stat, _ := os.Stat(tmpFile)
	ok(fmt.Sprintf("Packed %d files (%s)", fileCount, formatSize(stat.Size())))

	// Check if this should be an auto-update
	isUpdate := pCfg.DeploymentID != "" && !hasFlag("new")

	// Upload
	client, err := NewAPIClient()
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	if isUpdate {
		// Auto-update existing deployment
		info(fmt.Sprintf("Updating deployment %s%s%s", cBold, pCfg.DeploymentID, cReset))
		spin("Pushing update")

		fields := map[string][]string{}
		if len(envVars) > 0 {
			fields["env"] = envVars
		}
		if memory != "" {
			fields["memory"] = []string{memory}
		}
		if cpus != "" {
			fields["cpus"] = []string{cpus}
		}
		if port != "" {
			fields["port"] = []string{port}
		}
		if networkQuota != "" {
			fields["network_quota"] = []string{networkQuota}
		}

		result, err := client.Upload(fmt.Sprintf("/api/deploy/%s/update", pCfg.DeploymentID), tmpFile, fields)
		if err != nil {
			done()
			// If 404, the deployment was destroyed externally — clear stale ID
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
				info("Previous deployment not found, creating new one instead...")
				pCfg.DeploymentID = ""
				pCfg.URL = ""
				saveProjectConfig(projectDir, pCfg)
				// Fall through to new deploy below
			} else {
				fail(err.Error())
				os.Exit(1)
			}
		} else {
			done()
			url, _ := result["url"].(string)
			msg, _ := result["message"].(string)
			ok(msg)
			fmt.Println()
			fmt.Printf("  %s⚡%s %s%s%s\n", cGreen, cReset, cCyan, url, cReset)
			warn("Building — may take a few minutes to become accessible.")
			fmt.Println()

			// Update URL in project config
			if url != "" {
				pCfg.URL = url
				saveProjectConfig(projectDir, pCfg)
			}
			return
		}
	}

	// New deployment
	spin("Uploading to server")

	fields := map[string][]string{"name": {name}}
	if ttl != "" {
		fields["ttl"] = []string{ttl}
	}
	if memory != "" {
		fields["memory"] = []string{memory}
	}
	if cpus != "" {
		fields["cpus"] = []string{cpus}
	}
	if port != "" {
		fields["port"] = []string{port}
	}
	if len(envVars) > 0 {
		fields["env"] = envVars
	}
	if title != "" {
		fields["title"] = []string{title}
	}
	if description != "" {
		fields["description"] = []string{description}
	}
	if protectMode != "" {
		fields["protect_mode"] = []string{protectMode}
	}
	if protectUser != "" {
		fields["protect_username"] = []string{protectUser}
	}
	if protectPass != "" {
		fields["protect_password"] = []string{protectPass}
	}
	if protectKey != "" {
		fields["protect_api_key"] = []string{protectKey}
	}
	if protectUsers != "" {
		fields["protect_users"] = []string{protectUsers}
	}
	if networkQuota != "" {
		fields["network_quota"] = []string{networkQuota}
	}

	result, err := client.Upload("/api/deploy", tmpFile, fields)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	fw, _ := result["framework"].(string)
	url, _ := result["url"].(string)
	id, _ := result["id"].(string)
	expiresAt, _ := result["expiresAt"].(string)

	ok(fmt.Sprintf("Framework: %s%s%s", cBold, fw, cReset))
	fmt.Println()
	fmt.Printf("  %s⚡%s %sLive at:%s %s%s%s\n", cGreen, cReset, cBold, cReset, cCyan, url, cReset)
	fmt.Println()

	if expiresAt != "" {
		info("Expires: " + expiresAt)
	}
	if am, _ := result["accessMode"].(string); am != "" {
		info(fmt.Sprintf("Access: %s%s%s", cYellow, am, cReset))
	}
	if ak, _ := result["apiKey"].(string); ak != "" {
		fmt.Printf("  %sAPI Key:%s %s%s%s\n", cBold, cReset, cCyan, ak, cReset)
	}
	info("ID: " + id)
	warn("Building — may take a few minutes to become accessible.")
	info(fmt.Sprintf("Status: %sberth status %s%s", cDim, id, cReset))
	info(fmt.Sprintf("Logs: %sberth logs %s%s", cDim, id, cReset))
	info(fmt.Sprintf("Destroy: %sberth destroy %s%s", cDim, id, cReset))
	fmt.Println()

	// Write back to project config
	pCfg.DeploymentID = id
	pCfg.URL = url
	saveProjectConfig(projectDir, pCfg)
}

// parseEnvFile reads a .env file and returns KEY=VALUE strings.
func parseEnvFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var vars []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Must contain = to be a valid env line
		if !strings.Contains(line, "=") {
			continue
		}
		// Strip optional "export " prefix
		line = strings.TrimPrefix(line, "export ")
		// Strip surrounding quotes from value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			// Remove surrounding quotes
			if len(val) >= 2 {
				if (val[0] == '"' && val[len(val)-1] == '"') ||
					(val[0] == '\'' && val[len(val)-1] == '\'') {
					val = val[1 : len(val)-1]
				}
			}
			vars = append(vars, key+"="+val)
		}
	}
	return vars, nil
}

// getDeployTarget returns the first positional arg after "deploy" that isn't a flag.
// e.g. "berth deploy App.jsx --name foo" → "App.jsx"
// e.g. "berth deploy --name foo" → ""
func getDeployTarget() string {
	// args[0] is "deploy", look for first non-flag arg after it
	for i := 1; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			// Skip --flag and its value
			i++
			continue
		}
		return args[i]
	}
	return ""
}

func cmdPull() {
	projectDir, _ := filepath.Abs(getFlag("dir", "."))
	id, source := resolveDeploymentID(projectDir)
	if id == "" {
		fail("No deployment ID. Pass as argument or run from a project with .berth.json.")
		os.Exit(1)
	}
	if source == "project" {
		info(fmt.Sprintf("Pulling source for %s%s%s from .berth.json", cBold, id, cReset))
	}

	output := getFlag("output", ".")
	output, _ = filepath.Abs(output)
	os.MkdirAll(output, 0755)

	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Pull%s\n\n", cBold, cReset)

	// Download tarball to temp file
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("openberth-pull-%s.tar.gz", id))
	defer os.Remove(tmpFile)

	spin("Downloading source code")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	size, err := client.Download("/api/deployments/"+id+"/source", tmpFile)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	// Extract to output dir
	spin("Extracting files")
	if err := extractTarball(tmpFile, output); err != nil {
		done()
		fail("Extract failed: " + err.Error())
		os.Exit(1)
	}
	done()

	ok(fmt.Sprintf("Source code downloaded to %s%s%s (%s)", cBold, output, cReset, formatSize(size)))
	fmt.Println()
}

func cmdPromote() {
	projectDir, _ := filepath.Abs(getFlag("dir", "."))
	id, source := resolveSandboxID(projectDir)
	if id == "" {
		fail("No sandbox ID. Pass as argument or run 'berth init' + 'berth dev' first.")
		os.Exit(1)
	}
	if source == "project" {
		info(fmt.Sprintf("Promoting sandbox %s%s%s from .berth.json", cBold, id, cReset))
	}
	ttl := getFlag("ttl", "")
	memory := getFlag("memory", "")
	cpus := getFlag("cpus", "")
	envVars := getFlags("env")
	networkQuota := getFlag("network-quota", "")

	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Promote%s\n\n", cBold, cReset)

	spin("Promoting sandbox to deployment")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	body := map[string]interface{}{}
	if ttl != "" {
		body["ttl"] = ttl
	}
	if memory != "" {
		body["memory"] = memory
	}
	if cpus != "" {
		body["cpus"] = cpus
	}
	if networkQuota != "" {
		body["network_quota"] = networkQuota
	}
	if len(envVars) > 0 {
		envMap := map[string]string{}
		for _, e := range envVars {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}
		body["env"] = envMap
	}

	result, err := client.RequestJSON("POST", "/api/sandbox/"+id+"/promote", body)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	fw, _ := result["framework"].(string)
	url, _ := result["url"].(string)
	resultID, _ := result["id"].(string)

	ok(fmt.Sprintf("Framework: %s%s%s", cBold, fw, cReset))
	fmt.Println()
	info("Building production deployment...")

	// Poll for build completion
	spin("Building")
	for i := 0; i < 180; i++ { // up to 6 minutes
		time.Sleep(2 * time.Second)
		status, err := client.Request("GET", "/api/deployments/"+resultID)
		if err != nil {
			continue
		}
		containerStatus, _ := status["containerStatus"].(string)
		if containerStatus == "running" {
			done()
			fmt.Println()
			fmt.Printf("  %s⚡%s %sLive at:%s %s%s%s\n", cGreen, cReset, cBold, cReset, cCyan, url, cReset)
			fmt.Println()
			info("ID: " + resultID)
			info(fmt.Sprintf("Logs: %sberth logs %s%s", cDim, resultID, cReset))
			fmt.Println()

			// Update project config: set deployment ID, clear sandbox ID
			pCfg := loadProjectConfig(projectDir)
			pCfg.DeploymentID = resultID
			pCfg.URL = url
			pCfg.SandboxID = ""
			saveProjectConfig(projectDir, pCfg)
			return
		}
		if containerStatus == "failed" {
			done()
			fail("Build failed. Run: berth logs " + resultID)
			os.Exit(1)
		}
	}
	done()
	fmt.Println()
	info("Build still in progress. Check status with:")
	info(fmt.Sprintf("  %sberth status %s%s", cDim, resultID, cReset))
	fmt.Println()
}

func cmdUpdate() {
	projectDir, _ := filepath.Abs(getFlag("dir", "."))
	id, source := resolveDeploymentID(projectDir)
	if id == "" {
		fail("No deployment ID. Pass as argument or run 'berth init' + 'berth deploy' first.")
		os.Exit(1)
	}
	if source == "project" {
		info(fmt.Sprintf("Updating %s%s%s from .berth.json", cBold, id, cReset))
	}
	envVars := getFlags("env")
	envFile := getFlag("env-file", "")
	memory := getFlag("memory", "")
	cpus := getFlag("cpus", "")
	port := getFlag("port", "")
	networkQuota := getFlag("network-quota", "")

	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Update%s\n\n", cBold, cReset)

	// Load env vars (same logic as deploy)
	autoEnv := filepath.Join(projectDir, ".env")
	if vars, err := parseEnvFile(autoEnv); err == nil && len(vars) > 0 {
		envVars = append(vars, envVars...)
		ok(fmt.Sprintf("Loaded %d vars from .env", len(vars)))
	}
	if envFile != "" {
		vars, err := parseEnvFile(envFile)
		if err != nil {
			fail("Cannot read env file: " + err.Error())
			os.Exit(1)
		}
		envVars = append(envVars, vars...)
		ok(fmt.Sprintf("Loaded %d vars from %s", len(vars), envFile))
	}

	spin("Compressing project")
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("openberth-update-%d.tar.gz", os.Getpid()))
	defer os.Remove(tmpFile)

	fileCount, err := createTarball(projectDir, tmpFile)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	stat, _ := os.Stat(tmpFile)
	ok(fmt.Sprintf("Packed %d files (%s)", fileCount, formatSize(stat.Size())))

	spin("Pushing update")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	fields := map[string][]string{}
	if len(envVars) > 0 {
		fields["env"] = envVars
	}
	if memory != "" {
		fields["memory"] = []string{memory}
	}
	if cpus != "" {
		fields["cpus"] = []string{cpus}
	}
	if port != "" {
		fields["port"] = []string{port}
	}
	if networkQuota != "" {
		fields["network_quota"] = []string{networkQuota}
	}

	result, err := client.Upload(fmt.Sprintf("/api/deploy/%s/update", id), tmpFile, fields)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	msg, _ := result["message"].(string)
	url, _ := result["url"].(string)
	ok(msg)
	fmt.Println()
	fmt.Printf("  %s⚡%s %s%s%s\n\n", cGreen, cReset, cCyan, url, cReset)
}

func cmdList() {
	client, err := NewAPIClient()
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	result, err := client.Request("GET", "/api/deployments")
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	deploys, _ := result["deployments"].([]interface{})
	fmt.Println()
	if len(deploys) == 0 {
		info("No active deployments.")
	} else {
		fmt.Printf("  %s%-12s %-20s %-12s %-10s %-6s %s%s\n", cDim, "ID", "NAME", "FRAMEWORK", "STATUS", "AGE", "URL", cReset)
		fmt.Printf("  %s%s%s\n", cDim, strings.Repeat("─", 96), cReset)
		for _, d := range deploys {
			dm, _ := d.(map[string]interface{})
			id, _ := dm["id"].(string)
			name, _ := dm["name"].(string)
			fw, _ := dm["framework"].(string)
			status, _ := dm["containerStatus"].(string)
			url, _ := dm["url"].(string)
			accessMode, _ := dm["accessMode"].(string)
			createdAt, _ := dm["createdAt"].(string)
			mode, _ := dm["mode"].(string)

			indicator := " "
			if mode == "sandbox" {
				indicator = cYellow + "⚙" + cReset
			}
			if accessMode != "" && accessMode != "public" {
				indicator = cYellow + "🔒" + cReset
			}

			statusColor := cYellow
			if status == "running" {
				statusColor = cGreen
			}
			fmt.Printf("  %-12s %-20s %-12s %s%-10s%s %-6s %s %s%s%s\n",
				id, truncate(name, 20), fw, statusColor, status, cReset, formatAge(createdAt), indicator, cCyan, url, cReset)
		}
	}
	fmt.Println()
}

func cmdStatus() {
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

	result, err := client.Request("GET", "/api/deployments/"+id)
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("  %sDeployment:%s  %v\n", cBold, cReset, result["id"])
	fmt.Printf("  %sName:%s       %v\n", cBold, cReset, result["name"])
	fmt.Printf("  %sFramework:%s  %v\n", cBold, cReset, result["framework"])
	fmt.Printf("  %sStatus:%s     %v\n", cBold, cReset, result["containerStatus"])
	fmt.Printf("  %sURL:%s        %s%v%s\n", cBold, cReset, cCyan, result["url"], cReset)
	fmt.Printf("  %sCreated:%s    %v\n", cBold, cReset, result["createdAt"])
	if exp, ok := result["expiresAt"].(string); ok && exp != "" {
		fmt.Printf("  %sExpires:%s    %v\n", cBold, cReset, exp)
	}
	if am, ok := result["accessMode"].(string); ok && am != "" && am != "public" {
		label := am
		if au, ok := result["accessUser"].(string); ok && au != "" {
			label = am + " (user: " + au + ")"
		}
		fmt.Printf("  %sAccess:%s     %s%s%s\n", cBold, cReset, cYellow, label, cReset)
	}
	fmt.Println()
}

func cmdLogs() {
	projectDir, _ := filepath.Abs(getFlag("dir", "."))
	id, _ := resolveDeploymentID(projectDir)
	if id == "" {
		fail("No deployment ID. Pass as argument or run 'berth init' + 'berth deploy' first.")
		os.Exit(1)
	}
	tail := getFlag("tail", "200")

	client, err := NewAPIClient()
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	result, err := client.Request("GET", fmt.Sprintf("/api/deployments/%s/logs?tail=%s", id, tail))
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	if logs, ok := result["logs"].(string); ok {
		fmt.Print(logs)
	}
}

func cmdDestroy() {
	client, err := NewAPIClient()
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	projectDir, _ := filepath.Abs(getFlag("dir", "."))

	if hasFlag("all") {
		spin("Destroying all deployments")
		result, err := client.Request("DELETE", "/api/deployments")
		if err != nil {
			done()
			fail(err.Error())
			os.Exit(1)
		}
		done()
		count, _ := result["destroyed"].(float64)
		ok(fmt.Sprintf("Destroyed %d deployment(s)", int(count)))

		// Clear project config IDs
		pCfg := loadProjectConfig(projectDir)
		if pCfg.DeploymentID != "" || pCfg.SandboxID != "" {
			pCfg.DeploymentID = ""
			pCfg.URL = ""
			pCfg.SandboxID = ""
			saveProjectConfig(projectDir, pCfg)
		}
	} else {
		id, _ := resolveDeploymentID(projectDir)
		if id == "" {
			fail("Usage: berth destroy <id> or berth destroy --all")
			os.Exit(1)
		}
		spin("Destroying " + id)
		_, err := client.Request("DELETE", "/api/deployments/"+id)
		if err != nil {
			done()
			fail(err.Error())
			os.Exit(1)
		}
		done()
		ok("Deployment " + id + " destroyed")

		// Clear matching ID from project config
		pCfg := loadProjectConfig(projectDir)
		if pCfg.DeploymentID == id {
			pCfg.DeploymentID = ""
			pCfg.URL = ""
			saveProjectConfig(projectDir, pCfg)
		}
		if pCfg.SandboxID == id {
			pCfg.SandboxID = ""
			saveProjectConfig(projectDir, pCfg)
		}
	}
}

func cmdProtect() {
	projectDir, _ := filepath.Abs(getFlag("dir", "."))
	id, _ := resolveDeploymentID(projectDir)
	if id == "" {
		fail("No deployment ID. Pass as argument or run 'berth init' + 'berth deploy' first.")
		os.Exit(1)
	}
	mode := getFlag("mode", "")
	if mode == "" {
		fail("--mode is required. Use: public, basic_auth, api_key, or user")
		os.Exit(1)
	}

	body := map[string]interface{}{"mode": mode}
	if u := getFlag("username", ""); u != "" {
		body["username"] = u
	}
	if p := getFlag("password", ""); p != "" {
		body["password"] = p
	}
	if k := getFlag("api-key", ""); k != "" {
		body["apiKey"] = k
	}
	if users := getFlag("users", ""); users != "" {
		var userList []string
		for _, u := range strings.Split(users, ",") {
			if t := strings.TrimSpace(u); t != "" {
				userList = append(userList, t)
			}
		}
		body["users"] = userList
	}

	spin("Updating access control")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	result, err := client.RequestJSON("POST", "/api/deployments/"+id+"/protect", body)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	msg, _ := result["message"].(string)
	ok(msg)

	if key, ok := result["apiKey"].(string); ok && key != "" {
		fmt.Println()
		fmt.Printf("  %sAPI Key:%s %s%s%s\n", cBold, cReset, cCyan, key, cReset)
	}
	fmt.Println()
}

func cmdLock(lock bool) {
	projectDir, _ := filepath.Abs(getFlag("dir", "."))
	id, _ := resolveDeploymentID(projectDir)
	if id == "" {
		if lock {
			fail("No deployment ID. Usage: berth lock <id>")
		} else {
			fail("No deployment ID. Usage: berth unlock <id>")
		}
		os.Exit(1)
	}

	action := "Locking"
	if !lock {
		action = "Unlocking"
	}
	spin(action + " deployment")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	result, err := client.RequestJSON("POST", "/api/deployments/"+id+"/lock", map[string]interface{}{"locked": lock})
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	msg, _ := result["message"].(string)
	ok(msg)
	fmt.Println()
}

func cmdQuota() {
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

	if hasFlag("remove") {
		spin("Removing network quota")
		body := map[string]interface{}{"network_quota": ""}
		result, err := client.RequestJSON("PATCH", "/api/deployments/"+id, body)
		if err != nil {
			done()
			fail(err.Error())
			os.Exit(1)
		}
		done()
		_ = result
		ok("Network quota removed")
	} else if val := getFlag("set", ""); val != "" {
		spin("Setting network quota to " + val)
		body := map[string]interface{}{"network_quota": val}
		result, err := client.RequestJSON("PATCH", "/api/deployments/"+id, body)
		if err != nil {
			done()
			fail(err.Error())
			os.Exit(1)
		}
		done()
		quota, _ := result["networkQuota"].(string)
		ok(fmt.Sprintf("Network quota set to %s", quota))
	} else {
		fail("Usage: berth quota <id> --set <value>  or  berth quota <id> --remove")
		os.Exit(1)
	}
	fmt.Println()
}

func cmdBackup() {
	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Backup%s\n\n", cBold, cReset)

	output := getFlag("output", fmt.Sprintf("openberth-backup-%s.tar.gz", time.Now().Format("2006-01-02")))

	spin("Downloading backup")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	size, err := client.Download("/api/admin/backup", output)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	ok(fmt.Sprintf("Backup saved: %s%s%s (%s)", cBold, output, cReset, formatSize(size)))
	fmt.Println()
}

func cmdRestore() {
	if len(os.Args) < 3 {
		fail("Usage: berth restore <backup-file.tar.gz>")
		os.Exit(1)
	}
	backupFile := os.Args[2]

	if _, err := os.Stat(backupFile); err != nil {
		fail("File not found: " + backupFile)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Restore%s\n\n", cBold, cReset)

	spin("Uploading backup and restoring")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	result, err := client.UploadFile("/api/admin/restore", backupFile, "backup")
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	msg, _ := result["message"].(string)
	users, _ := result["users"].(float64)
	deploys, _ := result["deployments"].(float64)
	rebuilding, _ := result["rebuilding"].(float64)

	ok("Backup restored successfully.")
	info(fmt.Sprintf("Users: %d", int(users)))
	info(fmt.Sprintf("Deployments: %d", int(deploys)))
	if rebuilding > 0 {
		info(fmt.Sprintf("Rebuilding: %d deployment(s) in background", int(rebuilding)))
		warn("TLS certificates may take a few minutes to provision — expect brief SSL errors until then.")
	}
	_ = msg
	fmt.Println()
}

func cmdVersion() {
	fmt.Printf("  %s⚓ OpenBerth%s\n", cBold, cReset)
	fmt.Printf("  CLI version:  %s\n", version)
	fmt.Printf("  Platform:     %s/%s\n", runtime.GOOS, runtime.GOARCH)

	// Try to get server version
	client, err := NewAPIClient()
	if err != nil {
		fmt.Printf("  Server:       %s(not configured)%s\n", cDim, cReset)
		fmt.Println()
		return
	}
	result, err := client.Request("GET", "/health")
	if err != nil {
		fmt.Printf("  Server:       %s(unreachable)%s\n", cDim, cReset)
		fmt.Println()
		return
	}
	if sv, ok := result["version"].(string); ok {
		fmt.Printf("  Server:       %s\n", sv)
	}
	if domain, ok := result["domain"].(string); ok {
		fmt.Printf("  Domain:       %s\n", domain)
	}
	fmt.Println()
}

func cmdConfig() {
	if len(os.Args) < 3 {
		printConfigHelp()
		return
	}

	action := os.Args[2]
	switch action {
	case "set":
		if len(os.Args) < 5 {
			fail("Usage: berth config set <key> <value>")
			os.Exit(1)
		}
		key, value := os.Args[3], os.Args[4]
		cfg := loadCLIConfig()
		switch key {
		case "server":
			cfg.Server = value
		case "key":
			cfg.Key = value
		default:
			fail("Unknown config key: " + key + ". Use 'server' or 'key'.")
			os.Exit(1)
		}
		saveCLIConfig(cfg)
		display := value
		if key == "key" && len(value) > 10 {
			display = value[:10] + "..."
		}
		ok(fmt.Sprintf("Set %s = %s", key, display))

	case "get":
		if len(os.Args) < 4 {
			fail("Usage: berth config get <key>")
			os.Exit(1)
		}
		cfg := loadCLIConfig()
		switch os.Args[3] {
		case "server":
			fmt.Println(cfg.Server)
		case "key":
			fmt.Println(cfg.Key)
		default:
			fmt.Println("(not set)")
		}

	case "show":
		cfg := loadCLIConfig()
		display := cfg.Key
		if len(display) > 10 {
			display = display[:10] + "..."
		}
		fmt.Printf("  server: %s\n", cfg.Server)
		fmt.Printf("  key:    %s\n", display)

	default:
		printConfigHelp()
	}
}

func printConfigHelp() {
	fmt.Println("Usage:")
	fmt.Println("  berth config set <key> <value>")
	fmt.Println("  berth config get <key>")
	fmt.Println("  berth config show")
}

func cmdLogin() {
	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Login%s\n\n", cBold, cReset)

	cfg := loadCLIConfig()
	if cfg.Server == "" {
		fail("Server not configured. Run: berth config set server https://your-domain.com")
		os.Exit(1)
	}
	server := strings.TrimRight(cfg.Server, "/")

	// Start local callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail("Failed to start local server: " + err.Error())
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	callbackURL := fmt.Sprintf("http://localhost:%d/callback", port)

	resultCh := make(chan loginResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><h2>Error</h2><p>No login code received.</p></body></html>`)
			resultCh <- loginResult{err: fmt.Errorf("no login code received")}
			return
		}

		// Exchange code for API key
		body := fmt.Sprintf(`{"code":"%s"}`, code)
		resp, err := http.Post(server+"/api/login/exchange", "application/json", strings.NewReader(body))
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><h2>Error</h2><p>Failed to exchange login code.</p></body></html>`)
			resultCh <- loginResult{err: fmt.Errorf("exchange failed: %w", err)}
			return
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		var result map[string]string
		if err := json.Unmarshal(respBody, &result); err != nil || result["apiKey"] == "" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><h2>Error</h2><p>Invalid response from server.</p></body></html>`)
			resultCh <- loginResult{err: fmt.Errorf("invalid exchange response")}
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body style="font-family:sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;"><div style="text-align:center;"><h2>Login successful!</h2><p>You can close this tab.</p></div></body></html>`)
		resultCh <- loginResult{apiKey: result["apiKey"], name: result["name"]}
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)

	// Open browser
	loginURL := fmt.Sprintf("%s/login?callback=%s", server, callbackURL)
	spin("Opening browser")
	if err := openBrowser(loginURL); err != nil {
		done()
		info("Could not open browser. Please visit:")
		fmt.Printf("  %s%s%s\n\n", cCyan, loginURL, cReset)
	} else {
		done()
		info(fmt.Sprintf("If browser didn't open: %s%s%s", cCyan, loginURL, cReset))
	}

	spin("Waiting for login")

	// Wait for result with timeout
	select {
	case result := <-resultCh:
		done()
		srv.Shutdown(context.Background())

		if result.err != nil {
			fail(result.err.Error())
			os.Exit(1)
		}

		// Save API key
		cfg.Key = result.apiKey
		saveCLIConfig(cfg)

		fmt.Println()
		ok(fmt.Sprintf("Logged in as %s%s%s", cBold, result.name, cReset))
		fmt.Println()

	case <-time.After(5 * time.Minute):
		done()
		srv.Shutdown(context.Background())
		fail("Login timed out (5 minutes). Please try again.")
		os.Exit(1)
	}
}

type loginResult struct {
	apiKey string
	name   string
	err    error
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("unsupported platform")
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func formatAge(createdAt string) string {
	t, err := time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		// Try RFC3339 as fallback
		t, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return "?"
		}
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
