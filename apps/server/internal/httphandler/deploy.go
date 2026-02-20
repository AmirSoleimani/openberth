package httphandler

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/openberth/openberth/apps/server/internal/service"
)

// ── Health ──────────────────────────────────────────────────────────

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, 200, map[string]interface{}{
		"status":  "ok",
		"version": h.version,
		"domain":  h.svc.Cfg.Domain,
		"gvisor":  h.svc.Container.GVisorAvailable(),
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

// ── Gallery ─────────────────────────────────────────────────────────

func (h *Handlers) Gallery(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}
	items, err := h.svc.ListGallery()
	if err != nil {
		writeErr(w, err)
		return
	}
	resp := map[string]interface{}{"deployments": items}
	resp["userId"] = user.ID
	resp["userRole"] = user.Role
	resp["userName"] = user.DisplayName
	if resp["userName"] == "" {
		resp["userName"] = user.Name
	}
	resp["hasPassword"] = user.PasswordHash != ""
	jsonResp(w, 200, resp)
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

	result, _ := h.svc.ListDeployments(user)
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
	if deploy.UserID != user.ID && user.Role != "admin" {
		jsonErr(w, 403, "Not your deployment.")
		return
	}

	srcDir := filepath.Join(h.svc.Cfg.DeploysDir, deploy.ID)
	if _, err := os.Stat(srcDir); err != nil {
		jsonErr(w, 404, "No source code available.")
		return
	}

	if r.URL.Query().Get("format") == "json" {
		result, err := h.svc.GetSource(user, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		jsonResp(w, 200, result)
		return
	}

	// Stream tarball
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-source.tar.gz"`, deploy.Name))
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
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
		return nil
	})

	tw.Close()
	gw.Close()
}

// decodeJSONBody decodes a JSON request body into the given pointer.
func decodeJSONBody(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}
