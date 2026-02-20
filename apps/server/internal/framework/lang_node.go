package framework

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type NodeProvider struct{}

func (p *NodeProvider) Language() string { return "node" }

func (p *NodeProvider) Detect(projectDir string) *FrameworkInfo {
	pkgPath := filepath.Join(projectDir, "package.json")
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Scripts         map[string]string `json:"scripts"`
		Engines         struct {
			Node string `json:"node"`
		} `json:"engines"`
	}

	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil
	}
	json.Unmarshal(data, &pkg)

	deps := make(map[string]bool)
	for k := range pkg.Dependencies {
		deps[k] = true
	}
	for k := range pkg.DevDependencies {
		deps[k] = true
	}

	nodeVer := parseNodeVersion(projectDir, pkg.Engines.Node)
	image := "node:" + nodeVer + "-slim"

	base := func(fw, build, start string, port int) *FrameworkInfo {
		return &FrameworkInfo{
			Framework: fw,
			Language:  "node",
			BuildCmd:  build,
			StartCmd:  start,
			Port:      port,
			Image:     image,
			CacheDir:  "node_modules",
		}
	}

	// Sub-framework priority: Next > Nuxt > SvelteKit > Vite > CRA > Vue CLI > Angular > generic
	switch {
	case deps["next"]:
		buildCmd := "npx next build"
		startCmd := "npx next start -H 0.0.0.0 -p $PORT"
		env := map[string]string{"HOSTNAME": "0.0.0.0", "PORT": "$PORT"}
		switch nextOutputMode(projectDir) {
		case "standalone":
			buildCmd = "npx next build && cp -r .next/static .next/standalone/.next/static 2>/dev/null; cp -r public .next/standalone/public 2>/dev/null; true"
			startCmd = "node .next/standalone/server.js"
		case "export":
			buildCmd = "npx next build && npm install --no-save serve"
			startCmd = "npx serve out -l $PORT"
		}
		info := base("nextjs", buildCmd, startCmd, 3000)
		info.DevCmd = "npx next dev -H 0.0.0.0 -p $PORT"
		info.Env = env
		return info

	case deps["nuxt"]:
		info := base("nuxt", "npx nuxi build", "node .output/server/index.mjs", 3000)
		info.Env = map[string]string{"NITRO_PORT": "$PORT", "NITRO_HOST": "0.0.0.0"}
		info.DevCmd = "npx nuxi dev --host 0.0.0.0 --port $PORT"
		return info

	case deps["@sveltejs/kit"]:
		info := base("sveltekit", "npx vite build", "node build/index.js", 3000)
		info.DevCmd = "npx vite dev --host 0.0.0.0 --port $PORT"
		return info

	case deps["vite"]:
		info := base("vite", "npx vite build", "npx vite preview --host 0.0.0.0 --port $PORT", 4173)
		info.DevCmd = "npx vite dev --host 0.0.0.0 --port $PORT"
		return info

	case deps["react-scripts"]:
		info := base("cra", "npx react-scripts build && npm install --no-save serve", "npx serve -s build -l $PORT", 3000)
		info.DevCmd = "npx react-scripts start"
		return info

	case deps["@vue/cli-service"]:
		info := base("vue-cli", "npx vue-cli-service build && npm install --no-save serve", "npx serve -s dist -l $PORT", 8080)
		info.DevCmd = "npx vue-cli-service serve --host 0.0.0.0 --port $PORT"
		return info

	case deps["@angular/cli"], deps["@angular/core"]:
		info := base("angular", "npx ng build && npm install --no-save serve", "npx serve -s dist/*/browser -l $PORT", 4200)
		info.DevCmd = "npx ng serve --host 0.0.0.0 --port $PORT"
		return info
	}

	// Generic Node with start script
	if pkg.Scripts["start"] != "" {
		buildCmd := ""
		if pkg.Scripts["build"] != "" {
			buildCmd = "npm run build"
		}
		info := base("node", buildCmd, "npm start", 3000)
		if pkg.Scripts["dev"] != "" {
			info.DevCmd = "npm run dev"
		}
		return info
	}

	return nil
}

func (p *NodeProvider) BuildScript(fw *FrameworkInfo) string {
	buildStep := ""
	if fw.BuildCmd != "" {
		buildStep = fmt.Sprintf(`
echo "🔨 Building project..."
NODE_ENV=production %s 2>&1
echo "✓ Build complete"
`, fw.BuildCmd)
	}

	// Patch Vite config to allow all hosts for frameworks that use Vite
	// (dev server needs server.allowedHosts, preview server needs preview.allowedHosts).
	// This runs after code copy so the patched config persists in the volume for runtime.
	vitePatch := ""
	if strings.Contains(fw.BuildCmd, "vite") || fw.Framework == "vite" || fw.Framework == "sveltekit" {
		vitePatch = "\n" + viteAllowHostsScript() + "\n"
	}

	return fmt.Sprintf(`#!/bin/sh
set -e
echo "🏰 [build] Node.js project"

cd /app

# Copy code to build volume
cp -r /app/code/. /app/ 2>&1
echo "✓ Code copied"
%s
# Determine lockfile for hash comparison
lock_file_hash() {
  for LF in package-lock.json yarn.lock pnpm-lock.yaml package.json; do
    if [ -f "$1/$LF" ]; then md5sum "$1/$LF" 2>/dev/null | cut -d' ' -f1; return; fi
  done
}

if [ -d "/old/node_modules" ]; then
  # ── Rebuild: copy cached node_modules from old volume ──
  OLD_HASH=$(lock_file_hash /old)
  NEW_HASH=$(lock_file_hash /app)

  echo "♻ Rebuild — copying cached node_modules..."
  (cd /old && tar cf - node_modules) | tar xf -

  if [ -n "$OLD_HASH" ] && [ "$OLD_HASH" = "$NEW_HASH" ]; then
    echo "⚡ Dependencies unchanged — skipping install"
  else
    echo "📦 Dependencies changed — reinstalling..."
    if [ -f "package-lock.json" ]; then
      npm install --prefer-offline 2>&1
    elif [ -f "yarn.lock" ]; then
      npx yarn install 2>&1
    elif [ -f "pnpm-lock.yaml" ]; then
      npx pnpm install 2>&1
    else
      npm install --prefer-offline 2>&1
    fi
    echo "✓ Dependencies installed ($(du -sh /app/node_modules 2>/dev/null | cut -f1))"
  fi
else
  # ── Fresh deploy ──
  echo "📦 Installing dependencies..."
  if [ -f "package-lock.json" ]; then
    npm ci --prefer-offline 2>&1 || npm install --prefer-offline 2>&1
  elif [ -f "yarn.lock" ]; then
    npx yarn install --frozen-lockfile 2>&1 || npx yarn install 2>&1
  elif [ -f "pnpm-lock.yaml" ]; then
    npx pnpm install --frozen-lockfile 2>&1 || npx pnpm install 2>&1
  else
    npm install --prefer-offline 2>&1
  fi
  echo "✓ Dependencies installed ($(du -sh /app/node_modules 2>/dev/null | cut -f1))"
fi
%s
rm -f /app/.openberth-build.sh /app/.openberth-run.sh
echo "🏰 [build] Complete"
`, vitePatch, buildStep)
}

func (p *NodeProvider) RunScript(fw *FrameworkInfo) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
echo "🏰 [run] Starting server..."
cd /app
exec %s
`, fw.StartCmd)
}

func (p *NodeProvider) CacheVolumes(userID string) []string {
	suffix := ""
	if userID != "" {
		suffix = "-" + userID
	}
	return []string{"-v=openberth-npm-cache" + suffix + ":/root/.npm:rw"}
}

func (p *NodeProvider) RebuildCopyScript() string { return "" }

func (p *NodeProvider) SandboxEntrypoint(fw *FrameworkInfo, port int) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
cd /app

# Install dependencies (first boot or package.json changed)
if [ -f package.json ]; then
  echo "🏰 [sandbox] Installing dependencies..."
  npm install 2>&1
fi

# Allow all hosts for Vite dev/preview servers (runs behind a reverse proxy)
%s

echo "🏰 [sandbox] Starting dev server..."
while true; do
  %s || true
  echo "🏰 [sandbox] Dev server exited, restarting in 2s..."
  sleep 2
done
`, viteAllowHostsScript(), fw.DevCmd)
}

// viteAllowHostsScript returns a shell snippet (node one-liner) that patches
// vite.config.* to set allowedHosts:true on both server and preview blocks.
// This is needed because OpenBerth runs behind a reverse proxy (Caddy) so the
// Host header doesn't match localhost. Vite 6+ rejects mismatched hosts by default.
func viteAllowHostsScript() string {
	return `node -e "
const fs=require('fs');
for(const f of['vite.config.js','vite.config.ts','vite.config.mjs','vite.config.mts']){
  if(!fs.existsSync(f))continue;
  let c=fs.readFileSync(f,'utf8');
  if(c.includes('allowedHosts'))break;
  let ps=false,pp=false;
  if(/server\s*:\s*\{/.test(c)){c=c.replace(/server\s*:\s*\{/,'server:{allowedHosts:true,');ps=true;}
  if(/preview\s*:\s*\{/.test(c)){c=c.replace(/preview\s*:\s*\{/,'preview:{allowedHosts:true,');pp=true;}
  let add='';
  if(!ps)add+='server:{allowedHosts:true},';
  if(!pp)add+='preview:{allowedHosts:true},';
  if(add){
    if(/defineConfig\s*\(\s*\{/.test(c))c=c.replace(/defineConfig\s*\(\s*\{/,'defineConfig({'+add);
    else if(/export\s+default\s*\{/.test(c))c=c.replace(/export\s+default\s*\{/,'export default{'+add);
    else if(/module\.exports\s*=\s*\{/.test(c))c=c.replace(/module\.exports\s*=\s*\{/,'module.exports={'+add);
  }
  fs.writeFileSync(f,c);break;
}" 2>/dev/null || true`
}

func (p *NodeProvider) SandboxEnv() map[string]string { return nil }

func (p *NodeProvider) StaticOnly() bool { return false }

// nextOutputMode returns the Next.js output mode: "standalone", "export", or "" (default).
func nextOutputMode(projectDir string) string {
	for _, name := range []string{"next.config.js", "next.config.mjs", "next.config.ts"} {
		data, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue
		}
		re := regexp.MustCompile(`output\s*:\s*['"](standalone|export)['"]`)
		if m := re.FindSubmatch(data); len(m) >= 2 {
			return string(m[1])
		}
	}
	return ""
}

// -- helpers --

func parseNodeVersion(dir string, enginesNode string) string {
	// 1. .nvmrc
	if data, err := os.ReadFile(filepath.Join(dir, ".nvmrc")); err == nil {
		ver := strings.TrimSpace(string(data))
		ver = strings.TrimPrefix(ver, "v")
		re := regexp.MustCompile(`^(\d+)`)
		if m := re.FindStringSubmatch(ver); len(m) >= 2 {
			return m[1]
		}
	}

	// 2. .node-version
	if data, err := os.ReadFile(filepath.Join(dir, ".node-version")); err == nil {
		ver := strings.TrimSpace(string(data))
		ver = strings.TrimPrefix(ver, "v")
		re := regexp.MustCompile(`^(\d+)`)
		if m := re.FindStringSubmatch(ver); len(m) >= 2 {
			return m[1]
		}
	}

	// 3. package.json engines.node
	if enginesNode != "" {
		re := regexp.MustCompile(`(\d+)`)
		if m := re.FindStringSubmatch(enginesNode); len(m) >= 2 {
			return m[1]
		}
	}

	return DefaultNodeVersion
}
