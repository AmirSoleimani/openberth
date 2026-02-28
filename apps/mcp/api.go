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

// createTarball creates a .tar.gz from a directory, skipping common junk.
func createTarball(srcDir string, dest *os.File) (int, error) {
	gw := gzip.NewWriter(dest)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	skipDirs := map[string]bool{
		"node_modules": true, ".git": true, ".next": true, "dist": true,
		"build": true, "__pycache__": true, ".venv": true, "venv": true,
		"target": true, "vendor": true,
	}

	count := 0
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		rel, _ := filepath.Rel(srcDir, path)
		if rel == "." {
			return nil
		}

		// Skip junk directories
		if info.IsDir() && skipDirs[info.Name()] {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
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
