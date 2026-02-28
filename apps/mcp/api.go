package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ── HTTP helpers ────────────────────────────────────────────────────

func (s *MCPServer) apiPost(path string, body json.RawMessage) ([]byte, error) {
	req, _ := http.NewRequest("POST", s.apiURL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *MCPServer) apiUpload(path, tarballPath string, fields map[string]string, envVars map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add fields
	for k, v := range fields {
		writer.WriteField(k, v)
	}
	for k, v := range envVars {
		writer.WriteField("env", k+"="+v)
	}

	// Add tarball
	part, err := writer.CreateFormFile("tarball", "project.tar.gz")
	if err != nil {
		return nil, err
	}

	f, err := os.Open(tarballPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	io.Copy(part, f)
	writer.Close()

	req, _ := http.NewRequest("POST", s.apiURL+path, &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *MCPServer) apiPatch(path string, body json.RawMessage) ([]byte, error) {
	req, _ := http.NewRequest("PATCH", s.apiURL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *MCPServer) apiGet(path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", s.apiURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *MCPServer) apiDelete(path string) ([]byte, error) {
	req, _ := http.NewRequest("DELETE", s.apiURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// ── Result helpers ──────────────────────────────────────────────────

func textResult(text string) *ToolResult {
	return &ToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}

func errorResult(text string) *ToolResult {
	return &ToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
		IsError: true,
	}
}

// ── Tarball creation ────────────────────────────────────────────────

var defaultIgnores = []string{
	"node_modules/",
	".git/",
	".next/",
	".nuxt/",
	".output/",
	"dist/",
	"build/",
	".cache/",
	"venv/",
	"__pycache__/",
	".env.local",
	".openberth-entry.sh",
	".openberth-sandbox.sh",
}

// loadIgnorePatterns reads .gitignore and .berthignore from dir and merges
// them with defaultIgnores.
func loadIgnorePatterns(dir string) []string {
	patterns := append([]string{}, defaultIgnores...)
	for _, name := range []string{".gitignore", ".berthignore"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					patterns = append(patterns, line)
				}
			}
		}
	}
	return patterns
}

// shouldIgnore checks whether a relative path should be ignored according to patterns.
// Returns (ignored, skipDir) — skipDir is true when a directory itself should be skipped entirely.
func shouldIgnore(relPath string, isDir bool, patterns []string) (bool, bool) {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}

		// Directory pattern (ends with /)
		if strings.HasSuffix(pattern, "/") {
			dirName := strings.TrimSuffix(pattern, "/")
			if isDir && (relPath == dirName || strings.HasPrefix(relPath, dirName+"/")) {
				return true, true
			}
			if !isDir && strings.Contains(relPath, dirName+"/") {
				return true, false
			}
			continue
		}

		// Exact match or glob-like match
		matched, _ := filepath.Match(pattern, filepath.Base(relPath))
		if matched || relPath == pattern || strings.HasPrefix(relPath, pattern+"/") {
			return true, isDir
		}
	}
	return false, false
}

// createTarball creates a .tar.gz from a directory, respecting .gitignore and .berthignore.
func createTarball(srcDir string, dest *os.File) (int, error) {
	patterns := loadIgnorePatterns(srcDir)

	gw := gzip.NewWriter(dest)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	count := 0
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		rel, _ := filepath.Rel(srcDir, path)
		if rel == "." {
			return nil
		}

		ignored, skipDir := shouldIgnore(rel, info.IsDir(), patterns)
		if ignored {
			if skipDir {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil // we only add files
		}

		// Skip large files (>10MB)
		if info.Size() > 10*1024*1024 {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		io.Copy(tw, f)

		count++
		return nil
	})

	return count, err
}
