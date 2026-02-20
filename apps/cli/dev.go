package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// scaffoldMapping holds info for single-file scaffold mode in dev.
type scaffoldMapping struct {
	sourceFile   string // absolute path to the original file (e.g. /home/user/App.jsx)
	scaffoldedAs string // relative path inside the sandbox (e.g. App.jsx)
	cleanup      func()
}

func cmdDev() {
	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Dev%s\n\n", cBold, cReset)

	client, err := NewAPIClient()
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}

	var sandboxID, sandboxURL string
	var scaffold *scaffoldMapping
	var projectDir string

	// --attach: reattach to an existing sandbox (falls back to project config)
	attachID := getFlag("attach", "")
	if attachID == "" {
		pCfg := loadProjectConfig(".")
		if pCfg.SandboxID != "" {
			attachID = pCfg.SandboxID
		}
	}
	if attachID != "" {
		sandboxID, sandboxURL, projectDir, scaffold = attachToSandbox(client, attachID)
	} else {
		sandboxID, sandboxURL, projectDir, scaffold = createNewSandbox(client)
	}

	if scaffold != nil {
		defer scaffold.cleanup()
	}

	// Set up file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fail("Failed to create file watcher: " + err.Error())
		os.Exit(1)
	}
	defer watcher.Close()

	// What to watch depends on mode
	if scaffold != nil {
		// Scaffold mode: watch the directory containing the original file
		watcher.Add(filepath.Dir(scaffold.sourceFile))
		fmt.Printf("  %s👀%s Watching %s%s%s (Ctrl+C to stop)\n\n",
			cCyan, cReset, cBold, filepath.Base(scaffold.sourceFile), cReset)
	} else {
		// Directory mode: watch recursively
		watchRecursive(watcher, projectDir, loadIgnorePatterns(projectDir))
		fmt.Printf("  %s👀%s Watching for file changes... (Ctrl+C to stop)\n\n", cCyan, cReset)
	}

	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Debounce timer
	var debounceTimer *time.Timer
	pendingChanges := map[string]string{} // path -> "write" or "delete"

	flushChanges := func() {
		if len(pendingChanges) == 0 {
			return
		}

		var changes []map[string]string

		if scaffold != nil {
			// Scaffold mode: only the source file matters, map it to scaffolded name
			for absPath, op := range pendingChanges {
				if absPath != scaffold.sourceFile {
					continue
				}
				change := map[string]string{
					"op":   op,
					"path": scaffold.scaffoldedAs,
				}
				if op == "write" {
					content, err := os.ReadFile(absPath)
					if err != nil {
						continue
					}
					change["content"] = string(content)
				}
				changes = append(changes, change)
			}
		} else {
			// Directory mode: push all changes with relative paths
			for absPath, op := range pendingChanges {
				relPath, _ := filepath.Rel(projectDir, absPath)
				change := map[string]string{
					"op":   op,
					"path": relPath,
				}
				if op == "write" {
					content, err := os.ReadFile(absPath)
					if err != nil {
						continue
					}
					change["content"] = string(content)
				}
				changes = append(changes, change)
			}
		}

		pendingChanges = map[string]string{}

		if len(changes) == 0 {
			return
		}

		pushBody := map[string]interface{}{
			"changes": changes,
		}
		pushResult, err := client.RequestJSON("POST", "/api/sandbox/"+sandboxID+"/push", pushBody)
		if err != nil {
			fmt.Printf("  %s✗%s Push failed: %s\n", cRed, cReset, err.Error())
		} else {
			updated, _ := pushResult["updated"].(float64)
			deleted, _ := pushResult["deleted"].(float64)
			depsInstalled, _ := pushResult["depsInstalled"].(bool)

			msg := fmt.Sprintf("Pushed %d file(s)", int(updated)+int(deleted))
			if depsInstalled {
				msg += " (deps reinstalled)"
			}
			fmt.Printf("  %s↑%s %s\n", cGreen, cReset, msg)
		}
	}

	for {
		select {
		case event, isOpen := <-watcher.Events:
			if !isOpen {
				return
			}

			if scaffold != nil {
				// Scaffold mode: only care about the source file
				if event.Name != scaffold.sourceFile {
					continue
				}
				if event.Has(fsnotify.Write) {
					pendingChanges[event.Name] = "write"
				}
			} else {
				// Directory mode
				relPath, _ := filepath.Rel(projectDir, event.Name)
				patterns := loadIgnorePatterns(projectDir)
				ignored, _ := shouldIgnore(relPath, false, patterns)
				if ignored {
					continue
				}
				if strings.HasPrefix(filepath.Base(event.Name), ".openberth") {
					continue
				}

				if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
					finfo, err := os.Stat(event.Name)
					if err == nil && finfo.IsDir() {
						watchRecursive(watcher, event.Name, patterns)
						continue
					}
					pendingChanges[event.Name] = "write"
				}
				if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					pendingChanges[event.Name] = "delete"
				}
			}

			// Reset debounce timer
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(300*time.Millisecond, flushChanges)

		case err, isOpen := <-watcher.Errors:
			if !isOpen {
				return
			}
			fmt.Printf("  %s⚠%s Watcher error: %s\n", cYellow, cReset, err.Error())

		case <-sigCh:
			fmt.Println()
			fmt.Println()
			fmt.Printf("  %s🏰 Sandbox still running%s\n", cBold, cReset)
			fmt.Printf("  %s›%s URL:      %s%s%s\n", cCyan, cReset, cCyan, sandboxURL, cReset)
			fmt.Printf("  %s›%s ID:       %s\n", cCyan, cReset, sandboxID)
			fmt.Printf("  %s›%s Reattach: %sberth dev --attach %s%s\n", cCyan, cReset, cDim, sandboxID, cReset)
			fmt.Printf("  %s›%s Promote:  %sberth promote %s%s\n", cCyan, cReset, cDim, sandboxID, cReset)
			fmt.Printf("  %s›%s Destroy:  %sberth destroy %s%s\n", cCyan, cReset, cDim, sandboxID, cReset)
			fmt.Println()
			return
		}
	}
}

// attachToSandbox reattaches the file watcher to an existing running sandbox.
func attachToSandbox(client *APIClient, id string) (sandboxID, sandboxURL, projectDir string, scaffold *scaffoldMapping) {
	spin("Connecting to sandbox " + id)
	status, err := client.Request("GET", "/api/deployments/"+id)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	containerStatus, _ := status["containerStatus"].(string)
	mode, _ := status["mode"].(string)
	if mode != "sandbox" {
		fail("Deployment " + id + " is not a sandbox. Use berth update instead.")
		os.Exit(1)
	}
	if containerStatus != "running" {
		fail(fmt.Sprintf("Sandbox is '%s', not running.", containerStatus))
		os.Exit(1)
	}

	sandboxID = id
	sandboxURL, _ = status["url"].(string)
	name, _ := status["name"].(string)
	fw, _ := status["framework"].(string)

	ok(fmt.Sprintf("Attached to %s%s%s (%s)", cBold, name, cReset, fw))
	fmt.Println()
	fmt.Printf("  %s⚡%s %sURL:%s %s%s%s\n", cGreen, cReset, cBold, cReset, cCyan, sandboxURL, cReset)
	fmt.Printf("  %s›%s ID: %s\n\n", cCyan, cReset, sandboxID)

	// Resolve target: could be a single file or directory
	dir := getFlag("dir", ".")
	target := ""
	for i := 1; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			i++
			continue
		}
		target = args[i]
		break
	}

	if target != "" && IsSingleFile(target) {
		// Single-file mode: watch the source file and map to scaffolded name
		absPath, _ := filepath.Abs(target)
		ext := strings.ToLower(filepath.Ext(absPath))
		scaffoldedName := "App" + ext
		if ext == ".html" {
			scaffoldedName = "index.html"
		}
		scaffold = &scaffoldMapping{
			sourceFile:   absPath,
			scaffoldedAs: scaffoldedName,
			cleanup:      func() {},
		}
		projectDir = filepath.Dir(absPath)
	} else {
		if target != "" {
			dir = target
		}
		projectDir, _ = filepath.Abs(dir)
	}

	if _, err := os.Stat(projectDir); err != nil {
		fail("Not found: " + projectDir)
		os.Exit(1)
	}

	return sandboxID, sandboxURL, projectDir, scaffold
}

// createNewSandbox creates a new sandbox and waits for it to be ready.
func createNewSandbox(client *APIClient) (sandboxID, sandboxURL, projectDir string, scaffold *scaffoldMapping) {
	name := getFlag("name", "")
	ttl := getFlag("ttl", "4h")
	envVars := getFlags("env")
	memory := getFlag("memory", "")
	port := getFlag("port", "")
	protectMode := getFlag("protect", "")
	protectUser := getFlag("username", "")
	protectPass := getFlag("password", "")
	protectKey := getFlag("api-key", "")
	protectUsers := getFlag("users", "")
	networkQuota := getFlag("network-quota", "")

	// Load project config for fallback defaults
	pCfgDir, _ := filepath.Abs(getFlag("dir", "."))
	pCfg := loadProjectConfig(pCfgDir)
	if name == "" {
		name = pCfg.Name
	}
	if ttl == "4h" && pCfg.TTL != "" {
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

	// Resolve target: single file or directory
	target := ""
	for i := 1; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			i++
			continue
		}
		target = args[i]
		break
	}

	if target != "" && IsSingleFile(target) {
		// Single-file scaffold mode
		absPath, _ := filepath.Abs(target)
		spin("Scaffolding " + filepath.Base(absPath))

		result, err := ScaffoldSingleFile(absPath)
		if err != nil {
			done()
			fail(err.Error())
			os.Exit(1)
		}
		done()

		ok(fmt.Sprintf("Auto-scaffolded: %s%s%s", cBold, result.Framework, cReset))
		projectDir = result.Dir

		ext := strings.ToLower(filepath.Ext(absPath))
		scaffoldedName := "App" + ext
		if ext == ".html" {
			scaffoldedName = "index.html"
		}

		scaffold = &scaffoldMapping{
			sourceFile:   absPath,
			scaffoldedAs: scaffoldedName,
			cleanup:      result.Cleanup,
		}

		if name == "" {
			name = strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
		}
	} else {
		// Directory mode
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

	// Read all project files
	spin("Reading project files")
	files, err := readProjectFiles(projectDir)
	if err != nil {
		done()
		fail("Failed to read project: " + err.Error())
		os.Exit(1)
	}
	done()
	ok(fmt.Sprintf("Read %d files", len(files)))

	// Create sandbox
	spin("Creating sandbox")
	body := map[string]interface{}{
		"files": files,
		"name":  name,
		"ttl":   ttl,
	}
	if memory != "" {
		body["memory"] = memory
	}
	if port != "" {
		body["port"] = port
	}
	if protectMode != "" {
		body["protect_mode"] = protectMode
	}
	if protectUser != "" {
		body["protect_username"] = protectUser
	}
	if protectPass != "" {
		body["protect_password"] = protectPass
	}
	if protectKey != "" {
		body["protect_api_key"] = protectKey
	}
	if protectUsers != "" {
		var userList []string
		for _, u := range strings.Split(protectUsers, ",") {
			if t := strings.TrimSpace(u); t != "" {
				userList = append(userList, t)
			}
		}
		body["protect_users"] = userList
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

	result, err := client.RequestJSON("POST", "/api/sandbox", body)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	sandboxID, _ = result["id"].(string)
	sandboxURL, _ = result["url"].(string)
	fw, _ := result["framework"].(string)

	ok(fmt.Sprintf("Framework: %s%s%s", cBold, fw, cReset))
	fmt.Println()
	fmt.Printf("  %s⚡%s %sURL:%s %s%s%s\n", cGreen, cReset, cBold, cReset, cCyan, sandboxURL, cReset)
	fmt.Printf("  %s›%s ID: %s\n\n", cCyan, cReset, sandboxID)

	// Write sandbox ID back to project config
	pCfg.SandboxID = sandboxID
	saveProjectConfig(pCfgDir, pCfg)

	// Phase 1: Wait for container to start
	spin("Starting container")
	containerUp := false
	for i := 0; i < 60; i++ {
		time.Sleep(2 * time.Second)
		status, err := client.Request("GET", "/api/deployments/"+sandboxID)
		if err != nil {
			continue
		}
		containerStatus, _ := status["containerStatus"].(string)
		if containerStatus == "running" {
			done()
			ok("Container started")
			containerUp = true
			break
		}
		if containerStatus == "failed" {
			done()
			fail("Sandbox failed to start. Run: berth logs " + sandboxID)
			os.Exit(1)
		}
	}
	if !containerUp {
		done()
		fail("Sandbox timed out. Run: berth logs " + sandboxID)
		os.Exit(1)
	}

	// Phase 2: Wait for dev server to be ready (HTTP probe)
	spin("Installing dependencies & starting dev server")
	probeClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	ready := false
	for i := 0; i < 90; i++ {
		resp, err := probeClient.Get(sandboxURL)
		if err == nil {
			resp.Body.Close()
			// 502 = Caddy can't reach upstream (dev server not listening yet)
			if resp.StatusCode != 502 {
				done()
				ok("Dev server is ready!")
				fmt.Println()
				ready = true
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if !ready {
		done()
		fmt.Println()
		info("Dev server not responding yet — it may still be installing dependencies.")
		info(fmt.Sprintf("Check logs: %sberth logs %s%s", cDim, sandboxID, cReset))
		fmt.Println()
	}

	return sandboxID, sandboxURL, projectDir, scaffold
}

// readProjectFiles walks the project directory and reads all non-ignored files into a map.
func readProjectFiles(dir string) (map[string]string, error) {
	patterns := loadIgnorePatterns(dir)
	files := map[string]string{}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(dir, path)
		if relPath == "." {
			return nil
		}

		ignored, skipDir := shouldIgnore(relPath, info.IsDir(), patterns)
		if ignored {
			if skipDir {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Skip binary files (simple heuristic: skip files > 1MB)
		if info.Size() > 1024*1024 {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		// Skip files that appear binary (contain null bytes in first 512 bytes)
		check := content
		if len(check) > 512 {
			check = check[:512]
		}
		for _, b := range check {
			if b == 0 {
				return nil
			}
		}

		files[relPath] = string(content)
		return nil
	})

	return files, err
}

// watchRecursive adds all subdirectories under dir to the watcher, respecting ignore patterns.
func watchRecursive(watcher *fsnotify.Watcher, dir string, patterns []string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(dir, path)
		if relPath != "." {
			ignored, skipDir := shouldIgnore(relPath, true, patterns)
			if ignored {
				if skipDir {
					return filepath.SkipDir
				}
				return nil
			}
		}

		watcher.Add(path)
		return nil
	})
}
