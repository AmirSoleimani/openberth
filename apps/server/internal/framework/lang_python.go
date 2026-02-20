package framework

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type PythonProvider struct{}

func (p *PythonProvider) Language() string { return "python" }

func (p *PythonProvider) Detect(projectDir string) *FrameworkInfo {
	if !isPythonProject(projectDir) {
		return nil
	}

	pyVer := parsePythonVersion(projectDir)
	image := "python:" + pyVer + "-slim"

	framework, startCmd, devCmd, env := detectPythonFramework(projectDir)

	return &FrameworkInfo{
		Framework: framework,
		Language:  "python",
		BuildCmd:  "",
		StartCmd:  startCmd,
		DevCmd:    devCmd,
		Port:      8000,
		Image:     image,
		RunImage:  "",
		CacheDir:  "venv",
		Env:       env,
	}
}

func (p *PythonProvider) BuildScript(fw *FrameworkInfo) string {
	buildStep := ""
	if fw.BuildCmd != "" {
		buildStep = fmt.Sprintf(`
echo "🔨 Running build..."
%s 2>&1
echo "✓ Build complete"
`, fw.BuildCmd)
	}

	return fmt.Sprintf(`#!/bin/sh
set -e
echo "🏰 [build] Python project"

cd /app

# Copy code to build volume
cp -r /app/code/. /app/ 2>&1
echo "✓ Code copied"

# Determine requirements file for hash comparison
req_file_hash() {
  for RF in requirements.txt pyproject.toml Pipfile.lock; do
    if [ -f "$1/$RF" ]; then md5sum "$1/$RF" 2>/dev/null | cut -d' ' -f1; return; fi
  done
}

install_deps() {
  if [ -f "requirements.txt" ]; then
    pip install -r requirements.txt --no-cache-dir 2>&1
  elif [ -f "pyproject.toml" ]; then
    pip install . --no-cache-dir 2>&1
  elif [ -f "Pipfile" ]; then
    pip install pipenv --no-cache-dir 2>&1 && pipenv install --deploy 2>&1
  elif [ -f "setup.py" ]; then
    pip install . --no-cache-dir 2>&1
  fi
  pip install gunicorn uvicorn --no-cache-dir 2>&1 || true
  echo "✓ Dependencies installed"
}

if [ -d "/old/venv" ]; then
  # ── Rebuild: copy cached venv from old volume ──
  OLD_HASH=$(req_file_hash /old)
  NEW_HASH=$(req_file_hash /app)

  echo "♻ Rebuild — copying cached venv..."
  (cd /old && tar cf - venv) | tar xf -
  export PATH="/app/venv/bin:$PATH"

  if [ -n "$OLD_HASH" ] && [ "$OLD_HASH" = "$NEW_HASH" ]; then
    echo "⚡ Dependencies unchanged — skipping install"
  else
    echo "📦 Dependencies changed — reinstalling..."
    install_deps
  fi
else
  # ── Fresh deploy ──
  echo "📦 Creating virtual environment..."
  python -m venv /app/venv 2>&1
  export PATH="/app/venv/bin:$PATH"

  echo "📦 Installing dependencies..."
  install_deps
fi
%s
rm -f /app/.openberth-build.sh /app/.openberth-run.sh
echo "🏰 [build] Complete"
`, buildStep)
}

func (p *PythonProvider) RunScript(fw *FrameworkInfo) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
echo "🏰 [run] Starting server..."
cd /app
export PATH="/app/venv/bin:$PATH"
exec %s
`, fw.StartCmd)
}

func (p *PythonProvider) CacheVolumes(userID string) []string {
	suffix := ""
	if userID != "" {
		suffix = "-" + userID
	}
	return []string{"-v=openberth-pip-cache" + suffix + ":/root/.cache/pip:rw"}
}

func (p *PythonProvider) RebuildCopyScript() string { return "" }

func (p *PythonProvider) SandboxEntrypoint(fw *FrameworkInfo, port int) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
cd /app

# Create venv if missing
if [ ! -d "/app/venv" ]; then
  echo "🏰 [sandbox] Creating virtual environment..."
  python -m venv /app/venv 2>&1
fi

# Activate venv
export PATH="/app/venv/bin:$PATH"

# Install dependencies
if [ -f requirements.txt ]; then
  echo "🏰 [sandbox] Installing from requirements.txt..."
  pip install -r requirements.txt 2>&1
elif [ -f pyproject.toml ]; then
  echo "🏰 [sandbox] Installing from pyproject.toml..."
  pip install . 2>&1
elif [ -f Pipfile ]; then
  echo "🏰 [sandbox] Installing from Pipfile..."
  pip install pipenv 2>&1 && pipenv install 2>&1
fi

echo "🏰 [sandbox] Starting dev server..."
while true; do
  %s || true
  echo "🏰 [sandbox] Dev server exited, restarting in 2s..."
  sleep 2
done
`, fw.DevCmd)
}

func (p *PythonProvider) SandboxEnv() map[string]string { return nil }

func (p *PythonProvider) StaticOnly() bool { return false }

// -- helpers --

func isPythonProject(dir string) bool {
	markers := []string{
		"requirements.txt", "pyproject.toml", "Pipfile",
		"setup.py", "setup.cfg", "app.py", "main.py", "manage.py",
	}
	for _, f := range markers {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

func parsePythonVersion(dir string) string {
	// 1. .python-version (pyenv)
	if data, err := os.ReadFile(filepath.Join(dir, ".python-version")); err == nil {
		ver := strings.TrimSpace(string(data))
		re := regexp.MustCompile(`^(\d+\.\d+)`)
		if m := re.FindStringSubmatch(ver); len(m) >= 2 {
			return m[1]
		}
	}

	// 2. pyproject.toml requires-python
	if data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml")); err == nil {
		re := regexp.MustCompile(`requires-python\s*=\s*"[><=]*\s*(\d+\.\d+)`)
		if m := re.FindStringSubmatch(string(data)); len(m) >= 2 {
			return m[1]
		}
	}

	// 3. runtime.txt (Heroku convention)
	if data, err := os.ReadFile(filepath.Join(dir, "runtime.txt")); err == nil {
		re := regexp.MustCompile(`python-(\d+\.\d+)`)
		if m := re.FindStringSubmatch(string(data)); len(m) >= 2 {
			return m[1]
		}
	}

	return DefaultPythonVersion
}

func detectPythonFramework(dir string) (framework string, startCmd string, devCmd string, env map[string]string) {
	env = map[string]string{"PYTHONUNBUFFERED": "1"}

	reqs := ""
	if data, err := os.ReadFile(filepath.Join(dir, "requirements.txt")); err == nil {
		reqs = strings.ToLower(string(data))
	}

	pyproject := ""
	if data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml")); err == nil {
		pyproject = strings.ToLower(string(data))
	}

	allDeps := reqs + "\n" + pyproject

	// Django
	if _, err := os.Stat(filepath.Join(dir, "manage.py")); err == nil {
		wsgiModule := findDjangoWSGI(dir)
		if wsgiModule != "" {
			return "django",
				"gunicorn " + wsgiModule + ".wsgi:application --bind 0.0.0.0:$PORT",
				"python manage.py runserver 0.0.0.0:$PORT",
				env
		}
		return "django",
			"python manage.py runserver 0.0.0.0:$PORT",
			"python manage.py runserver 0.0.0.0:$PORT",
			env
	}

	// FastAPI
	if strings.Contains(allDeps, "fastapi") {
		appFile := findPythonAppFile(dir, []string{"uvicorn"})
		return "fastapi",
			"uvicorn " + appFile + ":app --host 0.0.0.0 --port $PORT",
			"uvicorn " + appFile + ":app --host 0.0.0.0 --port $PORT --reload",
			env
	}

	// Flask
	if strings.Contains(allDeps, "flask") {
		appFile := findPythonAppFile(dir, []string{"flask"})
		return "flask",
			"gunicorn " + appFile + ":app --bind 0.0.0.0:$PORT",
			"flask --app " + appFile + " run --host 0.0.0.0 --port $PORT --debug",
			env
	}

	// Generic Python
	if _, err := os.Stat(filepath.Join(dir, "app.py")); err == nil {
		return "python", "python app.py", "python app.py", env
	}
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err == nil {
		return "python", "python main.py", "python main.py", env
	}

	return "python", "python app.py", "python app.py", env
}

func findDjangoWSGI(dir string) string {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			wsgi := filepath.Join(dir, e.Name(), "wsgi.py")
			if _, err := os.Stat(wsgi); err == nil {
				return e.Name()
			}
		}
	}
	return ""
}

func findPythonAppFile(dir string, _ []string) string {
	for _, name := range []string{"app", "main", "server"} {
		if _, err := os.Stat(filepath.Join(dir, name+".py")); err == nil {
			return name
		}
	}
	return "app"
}
