package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type APIClient struct {
	server string
	key    string
	http   *http.Client
}

func NewAPIClient() (*APIClient, error) {
	cfg := loadCLIConfig()
	if cfg.Server == "" {
		return nil, fmt.Errorf("server not configured. Run: berth config set server https://your-domain.com")
	}
	if cfg.Key == "" {
		return nil, fmt.Errorf("API key not configured. Run: berth config set key YOUR_KEY")
	}
	return &APIClient{
		server: strings.TrimRight(cfg.Server, "/"),
		key:    cfg.Key,
		http:   &http.Client{},
	}, nil
}

// Request makes a JSON API request (GET, DELETE, etc.)
func (c *APIClient) Request(method, path string) (map[string]interface{}, error) {
	url := c.server + path
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response from server")
	}

	if resp.StatusCode >= 400 {
		if errMsg, ok := result["error"].(string); ok {
			return nil, fmt.Errorf("%s", errMsg)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return result, nil
}

// Upload sends a tarball + form fields via multipart POST.
func (c *APIClient) Upload(path, tarballPath string, fields map[string][]string) (map[string]interface{}, error) {
	url := c.server + path

	// Create a pipe for streaming multipart
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Write multipart in a goroutine
	go func() {
		defer pw.Close()
		defer writer.Close()

		// Write form fields
		for key, values := range fields {
			for _, v := range values {
				writer.WriteField(key, v)
			}
		}

		// Write file
		part, err := writer.CreateFormFile("tarball", filepath.Base(tarballPath))
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		f, err := os.Open(tarballPath)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		defer f.Close()
		io.Copy(part, f)
	}()

	req, err := http.NewRequest(http.MethodPost, url, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response from server")
	}

	if resp.StatusCode >= 400 {
		if errMsg, ok := result["error"].(string); ok {
			return nil, fmt.Errorf("%s", errMsg)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return result, nil
}

// DownloadToDir makes an authenticated GET, reads the server-suggested
// filename from Content-Disposition, and writes the body to <dir>/<name>.
// If the header is missing or unparseable, fallbackName is used instead.
// Returns the absolute path written and the byte count.
func (c *APIClient) DownloadToDir(path, dir, fallbackName string) (string, int64, error) {
	url := c.server + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		if json.Unmarshal(body, &result) == nil {
			if msg, ok := result["error"].(string); ok {
				return "", 0, fmt.Errorf("%s", msg)
			}
		}
		return "", 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	name := fallbackName
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if fn, ok := params["filename"]; ok && fn != "" {
				// Strip any path component the server emitted — the filename
				// attribute shouldn't contain one, but we never trust input.
				name = filepath.Base(fn)
			}
		}
	}
	if name == "" {
		name = "download"
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	out := filepath.Join(dir, name)
	f, err := os.Create(out)
	if err != nil {
		return "", 0, fmt.Errorf("create file: %w", err)
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("download: %w", err)
	}
	return out, n, nil
}

// Download makes an authenticated GET request and streams the response to a file.
func (c *APIClient) Download(path, outputFile string) (int64, error) {
	url := c.server + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		if json.Unmarshal(body, &result) == nil {
			if errMsg, ok := result["error"].(string); ok {
				return 0, fmt.Errorf("%s", errMsg)
			}
		}
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(outputFile)
	if err != nil {
		return 0, fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return 0, fmt.Errorf("download: %w", err)
	}
	return n, nil
}

// UploadFile sends a single file as multipart upload.
func (c *APIClient) UploadFile(apiPath, filePath, fieldName string) (map[string]interface{}, error) {
	url := c.server + apiPath

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer writer.Close()

		part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		f, err := os.Open(filePath)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		defer f.Close()
		io.Copy(part, f)
	}()

	req, err := http.NewRequest(http.MethodPost, url, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response from server")
	}

	if resp.StatusCode >= 400 {
		if errMsg, ok := result["error"].(string); ok {
			return nil, fmt.Errorf("%s", errMsg)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return result, nil
}

// RequestJSON makes a JSON POST request.
func (c *APIClient) RequestJSON(method, path string, body interface{}) (map[string]interface{}, error) {
	url := c.server + path

	var reader io.Reader
	if body != nil {
		pr, pw := io.Pipe()
		go func() {
			json.NewEncoder(pw).Encode(body)
			pw.Close()
		}()
		reader = pr
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response from server")
	}

	if resp.StatusCode >= 400 {
		if errMsg, ok := result["error"].(string); ok {
			return nil, fmt.Errorf("%s", errMsg)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return result, nil
}
