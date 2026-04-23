package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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

// createTarball creates a .tar.gz of the project directory, skipping ignored files.
func createTarball(projectDir, outputPath string) (int, error) {
	patterns := loadIgnorePatterns(projectDir)

	outFile, err := os.Create(outputPath)
	if err != nil {
		return 0, err
	}
	defer outFile.Close()

	gz := gzip.NewWriter(outFile)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	fileCount := 0
	err = filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		relPath, _ := filepath.Rel(projectDir, path)
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
			return nil // we only add files
		}

		// Add file to tar
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		io.Copy(tw, f)
		fileCount++
		return nil
	})

	return fileCount, err
}


func formatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}
