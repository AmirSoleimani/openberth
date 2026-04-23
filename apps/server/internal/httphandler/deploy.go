package httphandler

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/service"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// ── Health ──────────────────────────────────────────────────────────

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, 200, map[string]interface{}{
		"status":  "ok",
		"version": h.version,
		"domain":  h.svc.Cfg.Domain,
		"gvisor":  h.svc.Runtime.Capabilities().SecureIsolation,
	})
}

func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, 200, map[string]string{
		"name":    "⚓ OpenBerth",
		"version": h.version,
		"docs":    "POST /api/deploy with a tarball to get started.",
		"health":  "/health",
	})
}

// ── Deploy (tarball) ────────────────────────────────────────────────

func (h *Handlers) Deploy(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	if err := r.ParseMultipartForm(200 << 20); err != nil {
		jsonErr(w, 400, "Failed to parse upload: "+err.Error())
		return
	}

	file, _, err := r.FormFile("tarball")
	if err != nil {
		jsonErr(w, 400, "No tarball uploaded.")
		return
	}
	defer file.Close()

	port := 0
	if portStr := r.FormValue("port"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p < 65536 {
			port = p
		}
	}

	result, svcErr := h.svc.DeployTarball(user, service.TarballDeployParams{
		File:            file,
		Name:            r.FormValue("name"),
		Title:           r.FormValue("title"),
		Description:     r.FormValue("description"),
		TTL:             r.FormValue("ttl"),
		Port:            port,
		EnvVars:         parseEnvVars(r),
		Secrets:         parseSecrets(r),
		Memory:          r.FormValue("memory"),
		CPUs:            r.FormValue("cpus"),
		NetworkQuota:    r.FormValue("network_quota"),
		ProtectMode:     r.FormValue("protect_mode"),
		ProtectUsername:  r.FormValue("protect_username"),
		ProtectPassword: r.FormValue("protect_password"),
		ProtectApiKey:   r.FormValue("protect_api_key"),
		ProtectUsers:    r.FormValue("protect_users"),
	})
	if svcErr != nil {
		writeErr(w, svcErr)
		return
	}

	resp := map[string]interface{}{
		"id":        result.ID,
		"name":      result.Name,
		"subdomain": result.Subdomain,
		"framework": result.Framework,
		"status":    result.Status,
		"url":       result.URL,
		"expiresAt": result.ExpiresAt,
	}
	if result.AccessMode != "" {
		resp["accessMode"] = result.AccessMode
	}
	if result.ApiKey != "" {
		resp["apiKey"] = result.ApiKey
	}
	jsonResp(w, 202, resp)
}

// ── Update (tarball) ────────────────────────────────────────────────

func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")

	if err := r.ParseMultipartForm(200 << 20); err != nil {
		jsonErr(w, 400, "Failed to parse upload.")
		return
	}
	file, _, err := r.FormFile("tarball")
	if err != nil {
		jsonErr(w, 400, "No tarball uploaded.")
		return
	}
	defer file.Close()

	port := 0
	if portStr := r.FormValue("port"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p < 65536 {
			port = p
		}
	}

	result, svcErr := h.svc.UpdateTarball(user, service.TarballUpdateParams{
		DeployID:     id,
		File:         file,
		Port:         port,
		EnvVars:      parseEnvVars(r),
		Secrets:      parseSecrets(r),
		Memory:       r.FormValue("memory"),
		CPUs:         r.FormValue("cpus"),
		NetworkQuota: r.FormValue("network_quota"),
	})
	if svcErr != nil {
		writeErr(w, svcErr)
		return
	}

	jsonResp(w, 200, result)
}

// ── DeployCode ──────────────────────────────────────────────────────

type CodeDeployRequest struct {
	Files           map[string]string `json:"files"`
	Name            string            `json:"name,omitempty"`
	Title           string            `json:"title,omitempty"`
	Description     string            `json:"description,omitempty"`
	TTL             string            `json:"ttl,omitempty"`
	Port            int               `json:"port,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Secrets         []string          `json:"secrets,omitempty"`
	Memory          string            `json:"memory,omitempty"`
	CPUs            string            `json:"cpus,omitempty"`
	NetworkQuota    string            `json:"network_quota,omitempty"`
	ProtectMode     string            `json:"protect_mode,omitempty"`
	ProtectUsername  string            `json:"protect_username,omitempty"`
	ProtectPassword string            `json:"protect_password,omitempty"`
	ProtectApiKey   string            `json:"protect_api_key,omitempty"`
	ProtectUsers    []string          `json:"protect_users,omitempty"`
}

func (h *Handlers) DeployCode(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	req, ok := decodeJSON[CodeDeployRequest](w, r)
	if !ok {
		return
	}

	result, err := h.svc.DeployCode(user, service.CodeDeployParams{
		Files:           req.Files,
		Name:            req.Name,
		Title:           req.Title,
		Description:     req.Description,
		TTL:             req.TTL,
		Port:            req.Port,
		Env:             req.Env,
		Secrets:         req.Secrets,
		Memory:          req.Memory,
		CPUs:            req.CPUs,
		NetworkQuota:    req.NetworkQuota,
		ProtectMode:     req.ProtectMode,
		ProtectUsername:  req.ProtectUsername,
		ProtectPassword: req.ProtectPassword,
		ProtectApiKey:   req.ProtectApiKey,
		ProtectUsers:    strings.Join(req.ProtectUsers, ","),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 202, result)
}

// ── UpdateCode ──────────────────────────────────────────────────────

func (h *Handlers) UpdateCode(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")

	req, ok := decodeJSON[CodeDeployRequest](w, r)
	if !ok {
		return
	}

	result, err := h.svc.UpdateCode(user, service.CodeUpdateParams{
		DeployID:     id,
		Files:        req.Files,
		Port:         req.Port,
		Env:          req.Env,
		Secrets:      req.Secrets,
		Memory:       req.Memory,
		CPUs:         req.CPUs,
		NetworkQuota: req.NetworkQuota,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 200, result)
}

// ── List ────────────────────────────────────────────────────────────

func (h *Handlers) ListDeployments(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	// `?owner=me` scopes to the caller; `?owner=<userID>` scopes to that user;
	// no param (or `?owner=all`) returns every deployment on the server.
	// Read visibility is open — mutation endpoints still enforce ownership.
	ownerFilter := ""
	switch q := r.URL.Query().Get("owner"); q {
	case "", "all":
		// no filter
	case "me":
		ownerFilter = user.ID
	default:
		ownerFilter = q
	}

	result, _ := h.svc.ListDeployments(user, ownerFilter)
	jsonResp(w, 200, map[string]interface{}{"deployments": result, "count": len(result)})
}

// ── Get ─────────────────────────────────────────────────────────────

func (h *Handlers) GetDeployment(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	info, err := h.svc.GetDeployment(user, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	jsonResp(w, 200, info)
}

// ── Logs ────────────────────────────────────────────────────────────

func (h *Handlers) GetLogs(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	result, err := h.svc.GetLogs(user, id, parseTail(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	jsonResp(w, 200, result)
}

// ── Stream Logs (SSE) ──────────────────────────────────────────────

// StreamLogs streams deployment logs via Server-Sent Events.
func (h *Handlers) StreamLogs(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	stream, err := h.svc.GetLogStream(user, id, parseTail(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer stream.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, 500, "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx/caddy buffering
	flusher.Flush()

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintf(w, "data: %s\n\n", line)
		flusher.Flush()

		// Check if client disconnected
		if r.Context().Err() != nil {
			return
		}
	}
}

// ── Destroy ─────────────────────────────────────────────────────────

func (h *Handlers) DestroyDeployment(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	if err := h.svc.DestroyDeployment(user, id); err != nil {
		writeErr(w, err)
		return
	}
	jsonResp(w, 200, map[string]string{"id": id, "status": "destroyed"})
}

// ── Protect ─────────────────────────────────────────────────────────

func (h *Handlers) ProtectDeployment(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	var req struct {
		Mode     string   `json:"mode"`
		Username string   `json:"username"`
		Password string   `json:"password"`
		ApiKey   string   `json:"apiKey"`
		Users    []string `json:"users"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return
	}

	result, err := h.svc.ProtectDeployment(user, service.ProtectParams{
		DeployID: id,
		Mode:     req.Mode,
		Username: req.Username,
		Password: req.Password,
		ApiKey:   req.ApiKey,
		Users:    strings.Join(req.Users, ","),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 200, result)
}

// ── Lock / Unlock ───────────────────────────────────────────────────

func (h *Handlers) LockDeployment(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	var req struct {
		Locked bool `json:"locked"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return
	}

	result, err := h.svc.LockDeployment(user, id, req.Locked)
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 200, result)
}

// ── UpdateMeta ──────────────────────────────────────────────────────

func (h *Handlers) UpdateMeta(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	var req struct {
		Title        *string `json:"title"`
		Description  *string `json:"description"`
		TTL          *string `json:"ttl"`
		NetworkQuota *string `json:"network_quota"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return
	}

	result, err := h.svc.UpdateMeta(user, service.UpdateMetaParams{
		DeployID:     id,
		Title:        req.Title,
		Description:  req.Description,
		TTL:          req.TTL,
		NetworkQuota: req.NetworkQuota,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 200, result)
}

// ── Source (download) ───────────────────────────────────────────────

func (h *Handlers) GetSource(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	deploy, _ := h.svc.Store.GetDeployment(id)
	if deploy == nil {
		jsonErr(w, 404, "Not found.")
		return
	}
	if !service.CanMutateDeploy(deploy, user) {
		jsonErr(w, 403, "Not your deployment.")
		return
	}

	srcDir := filepath.Join(h.svc.Cfg.DeploysDir, deploy.ID)
	if _, err := os.Stat(srcDir); err != nil {
		jsonErr(w, 404, "No source code available.")
		return
	}

	switch r.URL.Query().Get("format") {
	case "json":
		result, err := h.svc.GetSource(user, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		jsonResp(w, 200, result)
	case "tar.gz", "tgz", "tar":
		writeSourceTarGz(w, deploy, srcDir)
	default:
		// Default: zip. Native extraction in macOS Finder / Windows Explorer,
		// and every OS ships a zip unzipper. CLI pins ?format=tar.gz since its
		// extractor only understands tar.gz.
		writeSourceZip(w, deploy, srcDir)
	}
}

// sourceFileEntries walks srcDir and yields every regular file to visit,
// with its path relative to srcDir. Skips the .openberth/* hidden tree so
// internal artifacts don't end up in downloadable source archives.
func sourceFileEntries(srcDir string, visit func(relPath, absPath string, info os.FileInfo) error) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, path)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(rel, ".openberth") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		return visit(rel, path, info)
	})
}

// sourceArchiveBase returns the stem used in the Content-Disposition filename
// for source archives. Uses the human-readable deploy.Name when present;
// falls back to `ob-<id>-YYYY-MM-DD` for records missing a name (older rows
// from before the identity generator always populated it).
func sourceArchiveBase(deploy *store.Deployment) string {
	if deploy.Name != "" {
		return deploy.Name
	}
	return fmt.Sprintf("ob-%s-%s", deploy.ID, time.Now().UTC().Format("2006-01-02"))
}

func writeSourceTarGz(w http.ResponseWriter, deploy *store.Deployment, srcDir string) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-source.tar.gz"`, sourceArchiveBase(deploy)))
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	sourceFileEntries(srcDir, func(rel, abs string, info os.FileInfo) error {
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(abs)
		if err != nil {
			return nil
		}
		defer f.Close()
		io.Copy(tw, f)
		return nil
	})
}

func writeSourceZip(w http.ResponseWriter, deploy *store.Deployment, srcDir string) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-source.zip"`, sourceArchiveBase(deploy)))
	zw := zip.NewWriter(w)
	defer zw.Close()

	sourceFileEntries(srcDir, func(rel, abs string, info os.FileInfo) error {
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return nil
		}
		// Zip archives use forward slashes regardless of host OS.
		hdr.Name = filepath.ToSlash(rel)
		hdr.Method = zip.Deflate
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(abs)
		if err != nil {
			return nil
		}
		defer f.Close()
		io.Copy(fw, f)
		return nil
	})
}

