package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	secrets := getFlags("secret")
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
	if len(pCfg.Secrets) > 0 && len(secrets) == 0 {
		secrets = pCfg.Secrets
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
		if len(secrets) > 0 {
			fields["secrets"] = secrets
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

			// Update URL in project config
			if url != "" {
				pCfg.URL = url
				saveProjectConfig(projectDir, pCfg)
			}

			if !hasFlag("no-wait") {
				fmt.Println()
				status := waitForBuild(client, pCfg.DeploymentID)
				switch status {
				case "running":
					deploySuccess(url)
				case "failed":
					fail("Build failed. Run: berth logs " + pCfg.DeploymentID)
					os.Exit(1)
				default:
					warn("Build still in progress.")
					info(fmt.Sprintf("Status: %sberth status %s%s", cDim, pCfg.DeploymentID, cReset))
					fmt.Println()
				}
			} else {
				fmt.Println()
				fmt.Printf("  %s⚡%s %s%s%s\n", cGreen, cReset, cCyan, url, cReset)
				warn("Building — may take a few minutes to become accessible.")
				fmt.Println()
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
	if len(secrets) > 0 {
		fields["secrets"] = secrets
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
	fmt.Printf("  %s›%s URL: %s%s%s\n", cCyan, cReset, cCyan, url, cReset)
	info("ID: " + id)

	if expiresAt != "" {
		info("Expires: " + expiresAt)
	}
	if am, _ := result["accessMode"].(string); am != "" {
		info(fmt.Sprintf("Access: %s%s%s", cYellow, am, cReset))
	}
	if ak, _ := result["apiKey"].(string); ak != "" {
		fmt.Printf("  %sAPI Key:%s %s%s%s\n", cBold, cReset, cCyan, ak, cReset)
	}

	if !hasFlag("no-wait") {
		fmt.Println()
		status := waitForBuild(client, id)
		switch status {
		case "running":
			deploySuccess(url)
		case "failed":
			fail("Build failed. Run: berth logs " + id)
			os.Exit(1)
		default:
			warn("Build still in progress.")
			info(fmt.Sprintf("Status: %sberth status %s%s", cDim, id, cReset))
			info(fmt.Sprintf("Logs: %sberth logs %s%s", cDim, id, cReset))
			fmt.Println()
		}
	} else {
		fmt.Println()
		warn("Building — may take a few minutes to become accessible.")
		info(fmt.Sprintf("Status: %sberth status %s%s", cDim, id, cReset))
		info(fmt.Sprintf("Logs: %sberth logs %s%s", cDim, id, cReset))
		info(fmt.Sprintf("Destroy: %sberth destroy %s%s", cDim, id, cReset))
		fmt.Println()
	}

	// Write back to project config — persist deploy parameters for next deploy
	pCfg.DeploymentID = id
	pCfg.URL = url
	if name != "" {
		pCfg.Name = name
	}
	if ttl != "" {
		pCfg.TTL = ttl
	}
	if memory != "" {
		pCfg.Memory = memory
	}
	if port != "" {
		pCfg.Port = port
	}
	if protectMode != "" {
		pCfg.Protect = protectMode
	}
	if networkQuota != "" {
		pCfg.NetworkQuota = networkQuota
	}
	if len(secrets) > 0 {
		pCfg.Secrets = secrets
	}
	pCfg.BerthVersion = version
	// Fetch server version from /health endpoint
	if health, err := client.Request("GET", "/health"); err == nil {
		if sv, ok := health["version"].(string); ok {
			pCfg.ServerVersion = sv
		}
	}
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

	// --output selects the directory in which to drop the archive. The
	// filename comes from the server's Content-Disposition header, so the
	// user's local file lands with the same shape they'd get from a browser
	// download. --format passes through to the server (zip default; tar.gz,
	// tgz, tar, or json also accepted by the endpoint).
	outputDir := getFlag("output", ".")
	outputDir, _ = filepath.Abs(outputDir)
	format := getFlag("format", "")

	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Pull%s\n\n", cBold, cReset)

	spin("Downloading source")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	path := "/api/deployments/" + id + "/source"
	if format != "" {
		path += "?format=" + format
	}
	fallback := fmt.Sprintf("openberth-%s-source.zip", id)
	if format == "tar.gz" || format == "tgz" || format == "tar" {
		fallback = fmt.Sprintf("openberth-%s-source.tar.gz", id)
	}

	savedPath, size, err := client.DownloadToDir(path, outputDir, fallback)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	ok(fmt.Sprintf("Saved %s%s%s (%s)", cBold, savedPath, cReset, formatSize(size)))
	info("Extract with `unzip` or `tar xzf` depending on the archive format.")
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
	secrets := getFlags("secret")
	networkQuota := getFlag("network-quota", "")

	// Load secrets from project config
	pCfg := loadProjectConfig(projectDir)
	if len(pCfg.Secrets) > 0 && len(secrets) == 0 {
		secrets = pCfg.Secrets
	}

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
	if len(secrets) > 0 {
		body["secrets"] = secrets
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

	status := waitForBuild(client, resultID)
	switch status {
	case "running":
		deploySuccess(url)
		// Update project config: set deployment ID, clear sandbox ID
		pCfg := loadProjectConfig(projectDir)
		pCfg.DeploymentID = resultID
		pCfg.URL = url
		pCfg.SandboxID = ""
		saveProjectConfig(projectDir, pCfg)
	case "failed":
		fail("Build failed. Run: berth logs " + resultID)
		os.Exit(1)
	default:
		warn("Build still in progress.")
		info(fmt.Sprintf("Status: %sberth status %s%s", cDim, resultID, cReset))
		fmt.Println()
	}
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
	secrets := getFlags("secret")
	envFile := getFlag("env-file", "")
	memory := getFlag("memory", "")
	cpus := getFlag("cpus", "")
	port := getFlag("port", "")
	networkQuota := getFlag("network-quota", "")

	// Load secrets from project config
	pCfg := loadProjectConfig(projectDir)
	if len(pCfg.Secrets) > 0 && len(secrets) == 0 {
		secrets = pCfg.Secrets
	}

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
	if len(secrets) > 0 {
		fields["secrets"] = secrets
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

	// Default: only the caller's own deployments (server scopes via ?owner=me).
	// --all shows every deployment on the server with an owner column.
	showAll := hasFlag("all")
	path := "/api/deployments?owner=me"
	if showAll {
		path = "/api/deployments"
	}

	result, err := client.Request("GET", path)
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	deploys, _ := result["deployments"].([]interface{})
	fmt.Println()
	if len(deploys) == 0 {
		info("No active deployments.")
		fmt.Println()
		return
	}

	if showAll {
		fmt.Printf("  %s%-12s %-20s %-14s %-12s %-10s %-6s %s%s\n", cDim, "ID", "NAME", "OWNER", "FRAMEWORK", "STATUS", "AGE", "URL", cReset)
		fmt.Printf("  %s%s%s\n", cDim, strings.Repeat("─", 112), cReset)
	} else {
		fmt.Printf("  %s%-12s %-20s %-12s %-10s %-6s %s%s\n", cDim, "ID", "NAME", "FRAMEWORK", "STATUS", "AGE", "URL", cReset)
		fmt.Printf("  %s%s%s\n", cDim, strings.Repeat("─", 96), cReset)
	}

	for _, d := range deploys {
		dm, _ := d.(map[string]interface{})
		id, _ := dm["id"].(string)
		name, _ := dm["name"].(string)
		fw, _ := dm["framework"].(string)
		// Read the DB-level status (building/running/updating/failed). The
		// list response no longer carries containerStatus — that's only
		// emitted by GET /api/deployments/{id}.
		status, _ := dm["status"].(string)
		url, _ := dm["url"].(string)
		accessMode, _ := dm["accessMode"].(string)
		createdAt, _ := dm["createdAt"].(string)
		mode, _ := dm["mode"].(string)
		ownerName, _ := dm["ownerName"].(string)

		indicator := " "
		if mode == "sandbox" {
			indicator = cYellow + "⚙" + cReset
		}
		if accessMode != "" && accessMode != "public" {
			indicator = cYellow + "🔒" + cReset
		}

		statusColor := cYellow
		switch status {
		case "running":
			statusColor = cGreen
		case "failed":
			statusColor = cRed
		}

		if showAll {
			fmt.Printf("  %-12s %-20s %-14s %-12s %s%-10s%s %-6s %s %s%s%s\n",
				id, truncate(name, 20), truncate(ownerName, 14), fw, statusColor, status, cReset, formatAge(createdAt), indicator, cCyan, url, cReset)
		} else {
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

	// Check for --follow or -f flag
	follow := hasFlag("follow")
	if !follow {
		for _, a := range args {
			if a == "-f" {
				follow = true
				break
			}
		}
	}

	if follow {
		streamLogs(id, tail)
		return
	}

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

func streamLogs(id, tail string) {
	client, err := NewAPIClient()
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	url := fmt.Sprintf("%s/api/deployments/%s/logs/stream?tail=%s", client.server, id, tail)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+client.key)

	resp, err := client.http.Do(req)
	if err != nil {
		fail("Connection failed: " + err.Error())
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fail(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		os.Exit(1)
	}

	// Read SSE stream — lines are "data: <content>\n\n"
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			fmt.Println(line[6:]) // strip "data: " prefix
		}
		// Skip empty lines (SSE separators)
	}

	if err := scanner.Err(); err != nil {
		// Connection closed — normal when container stops
		return
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

// ── Build wait ──────────────────────────────────────────────────────

// waitForBuild polls deployment status until running, failed, or timeout.
// Returns the final containerStatus string.
func waitForBuild(client *APIClient, id string) string {
	spin("Building")
	for i := 0; i < 180; i++ { // up to 6 minutes
		time.Sleep(2 * time.Second)
		status, err := client.Request("GET", "/api/deployments/"+id)
		if err != nil {
			continue
		}
		cs, _ := status["containerStatus"].(string)
		if cs == "running" || cs == "failed" {
			done()
			return cs
		}
	}
	done()
	return "timeout"
}

// deploySuccess handles the post-build success output: URL, QR, browser open.
func deploySuccess(url string) {
	fmt.Println()
	fmt.Printf("  %s✅%s %sLive at%s %s%s%s\n", cGreen, cReset, cBold, cReset, cCyan, url, cReset)

	if !hasFlag("no-qr") && isTerminal() {
		printQR(url)
	}

	fmt.Println()
	openBrowser(url)
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

