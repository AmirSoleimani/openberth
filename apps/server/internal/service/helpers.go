package service

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SanitizeName cleans a user-provided name for use as a subdomain component.
func SanitizeName(name string) string {
	if name == "" {
		return ""
	}
	re := regexp.MustCompile(`[^a-z0-9-]`)
	s := re.ReplaceAllString(strings.ToLower(name), "-")
	s = regexp.MustCompile(`-+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// ParseTTL parses a TTL string like "24h", "7d", "0" into hours.
func ParseTTL(ttl string, defaultHours int) int {
	if ttl == "" {
		return defaultHours
	}
	if ttl == "0" {
		return 0
	}
	re := regexp.MustCompile(`^(\d+)(h|d)?$`)
	m := re.FindStringSubmatch(ttl)
	if m == nil {
		return defaultHours
	}
	n, _ := strconv.Atoi(m[1])
	if m[2] == "d" {
		return n * 24
	}
	return n
}

// RandomHex returns a random hex string of n bytes (2n hex characters).
func RandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ParseSize converts a human-readable size string (e.g. "5g", "500m") to bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "0" {
		return 0, nil
	}
	var multiplier int64 = 1
	switch {
	case strings.HasSuffix(s, "t"):
		multiplier = 1 << 40
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "g"):
		multiplier = 1 << 30
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "m"):
		multiplier = 1 << 20
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "k"):
		multiplier = 1 << 10
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return n * multiplier, nil
}

// coalesce returns the first non-empty string.
func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// validPkgName matches safe package name characters for npm, pip, and go.
var validPkgName = regexp.MustCompile(`^[a-zA-Z0-9@_./:=<>^~-]+$`)

// detectProjectLang guesses the primary language of a project directory.
func detectProjectLang(codeDir string) string {
	if _, err := os.Stat(filepath.Join(codeDir, "go.mod")); err == nil {
		return "go"
	}
	pythonMarkers := []string{"requirements.txt", "pyproject.toml", "Pipfile", "manage.py", "setup.py"}
	for _, f := range pythonMarkers {
		if _, err := os.Stat(filepath.Join(codeDir, f)); err == nil {
			return "python"
		}
	}
	if _, err := os.Stat(filepath.Join(codeDir, "package.json")); err == nil {
		return "node"
	}
	return "node"
}

// detectInstallCmd returns the appropriate install command for a project directory.
func detectInstallCmd(codeDir string) string {
	lang := detectProjectLang(codeDir)
	switch lang {
	case "go":
		return "cd /app && go mod download 2>&1"
	case "python":
		if _, err := os.Stat(filepath.Join(codeDir, "requirements.txt")); err == nil {
			return "cd /app && /app/venv/bin/pip install -r requirements.txt 2>&1"
		}
		if _, err := os.Stat(filepath.Join(codeDir, "pyproject.toml")); err == nil {
			return "cd /app && /app/venv/bin/pip install . 2>&1"
		}
		if _, err := os.Stat(filepath.Join(codeDir, "Pipfile")); err == nil {
			return "cd /app && /app/venv/bin/pip install pipenv && /app/venv/bin/pipenv install 2>&1"
		}
		return "cd /app && /app/venv/bin/pip install -r requirements.txt 2>&1"
	default:
		return "cd /app && npm install 2>&1"
	}
}

// CurrentPeriodStart returns the start date of the current billing period
// based on the reset interval duration.
func CurrentPeriodStart(interval time.Duration) string {
	now := time.Now().UTC()
	days := int(interval.Hours() / 24)
	switch {
	case days <= 7:
		// Weekly: ISO week start (Monday)
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7 // Sunday = 7
		}
		monday := now.AddDate(0, 0, -(weekday - 1))
		return monday.Format("2006-01-02")
	case days <= 31:
		// Monthly: first of current month
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	default:
		// Quarterly: first of current quarter
		q := (int(now.Month()) - 1) / 3
		return time.Date(now.Year(), time.Month(q*3+1), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	}
}
